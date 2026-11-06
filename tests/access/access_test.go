//go:build integration

package access_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
	"uuid"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp/totp"
	"github.com/pressly/goose/v3"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/app"
	"github.com/stephanofer/facets-server/internal/database"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/httpapi"
	"github.com/stephanofer/facets-server/internal/mail"
	"github.com/stephanofer/facets-server/migrations"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type fixture struct {
	sender   access.Sender
	db       *database.DB
	service  *access.Service
	policy   access.Policy
	store    *execution.Store
	worker   execution.Worker
	server   *httptest.Server
	mu       sync.Mutex
	messages map[string][]string
}
type user struct{ Email, Password, Token, ID, SpaceID, MemberID string }

func setup(t *testing.T) *fixture {
	t.Helper()
	data, err := os.ReadFile("../../environment.json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		PostgresImage string `json:"postgresImage"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	container, err := postgres.Run(t.Context(), env.PostgresImage, postgres.WithDatabase("access_test"), postgres.WithUsername("owner"), postgres.WithPassword(rand.Text()), postgres.BasicWaitStrategies())
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := container.ConnectionString(t.Context(), "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	// Exercise upgrade from the published block-2 schema with existing receipts.
	// The execution suite separately exercises an empty database through version 2.
	files, err := migrations.Files()
	if err != nil {
		t.Fatal(err)
	}
	first, err := files.Open("00001_execution.sql")
	if err != nil {
		t.Fatal(err)
	}
	firstSQL, err := io.ReadAll(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	legacy, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := legacy.Close(); err != nil {
			t.Error(err)
		}
	})
	provider, err := goose.NewProvider(goose.DialectPostgres, legacy, fstest.MapFS{"00001_execution.sql": &fstest.MapFile{Data: firstSQL}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecContext(t.Context(), `INSERT INTO action_receipts(actor,scope,action,action_key,fingerprint,result_ref) VALUES ('legacy-actor','legacy-scope','legacy-action','legacy-key',decode(repeat('00',32),'hex'),'legacy-result')`); err != nil {
		t.Fatal(err)
	}
	if err := app.Migrate(t.Context(), dsn); err != nil {
		t.Fatal(err)
	}
	if err := app.Migrate(t.Context(), dsn); err != nil {
		t.Fatal("migration reapplication", err)
	}
	db, err := database.Open(t.Context(), database.Options{URL: dsn, MaxConns: 12, Timeout: 5 * time.Second, Tracer: tracenoop.NewTracerProvider().Tracer("test")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	var legacyCount int
	if err := db.Pool.QueryRow(t.Context(), `SELECT count(*) FROM action_receipts WHERE action_key='legacy-key' AND result_ref='legacy-result'`).Scan(&legacyCount); err != nil || legacyCount != 1 {
		t.Fatal("upgrade altered existing receipts", err)
	}
	// All application commands use the restricted role; the owner pool is kept
	// exclusively for fixture setup, expiry controls and privileged maintenance.
	password := rand.Text()
	_, err = db.Pool.Exec(t.Context(), `CREATE ROLE access_app LOGIN PASSWORD '`+password+`';
 REVOKE CREATE ON SCHEMA public FROM PUBLIC;
 GRANT USAGE ON SCHEMA public TO access_app;
 GRANT SELECT,INSERT,UPDATE ON action_receipts,jobs,principals,recovery_codes,sessions,identity_tokens,access_limits,spaces,memberships,space_access,space_capabilities,invitations,access_deliveries TO access_app;
 GRANT SELECT,INSERT ON technical_audit TO access_app;
 GRANT USAGE ON SEQUENCE technical_audit_id_seq TO access_app;
 GRANT SELECT ON goose_db_version TO access_app`)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("access_app", password)
	runtimeDB, err := database.Open(t.Context(), database.Options{URL: u.String(), MaxConns: 12, Timeout: 5 * time.Second, Tracer: tracenoop.NewTracerProvider().Tracer("test")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtimeDB.Pool.Close)
	if err := runtimeDB.CheckRuntimeRole(t.Context()); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	p, err := access.LoadPolicy(func(k string) string {
		if k == "FACETS_ACCESS_KEY" {
			return hex.EncodeToString(key)
		}
		return ""
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	p.IdentityAttempts, p.OriginAttempts = 100, 10000
	p.Password.Concurrency = 8
	f := &fixture{db: db, policy: p, store: &execution.Store{DB: runtimeDB, QueueLimit: 1000}, messages: map[string][]string{}}
	f.service, err = access.New(f.store, p)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	api, err := httpapi.New(httpapi.Options{Ping: db.Ready, Log: log, Tracer: tracenoop.NewTracerProvider().Tracer("test"), Meter: noop.NewMeterProvider().Meter("test"), Timeout: 10 * time.Second, MaxBody: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	access.RegisterHTTP(api.Huma, f.service, log)
	f.server = httptest.NewServer(api.Handler)
	t.Cleanup(f.server.Close)
	ses := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("unsigned mail delivery")
		}
		var v struct {
			Destination struct {
				To []string `json:"ToAddresses"`
			} `json:"Destination"`
			Content struct {
				Simple struct {
					Body struct {
						Text struct {
							Data string `json:"Data"`
						} `json:"Text"`
					} `json:"Body"`
				} `json:"Simple"`
			} `json:"Content"`
		}
		if err := json.UnmarshalRead(r.Body, &v); err != nil {
			t.Error("invalid SES request", err)
			http.Error(w, "Invalid", 400)
			return
		}
		f.mu.Lock()
		for _, to := range v.Destination.To {
			f.messages[to] = append(f.messages[to], v.Content.Simple.Body.Text.Data)
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"MessageId":"synthetic-acceptance"}`)
	}))
	t.Cleanup(ses.Close)
	client := sesv2.NewFromConfig(aws.Config{Region: "us-east-1", HTTPClient: ses.Client(), Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "synthetic", SecretAccessKey: "synthetic", Source: "test"}, nil
	})}, func(o *sesv2.Options) { o.BaseEndpoint = new(ses.URL) })
	sender, err := mail.NewSES(client, mail.Options{From: "no-reply@example.test", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	f.sender = sender
	f.worker = execution.Worker{Store: f.store, Queue: "notifications", Concurrency: 2, Timeout: 2 * time.Second, Lease: 10 * time.Second, Poll: time.Second, Handlers: map[string]execution.Handler{"access.mail": f.service.MailHandler(sender)}, Log: log}
	return f
}

func (f *fixture) reset(t *testing.T) {
	t.Helper()
	_, err := f.db.Pool.Exec(t.Context(), `TRUNCATE access_deliveries,invitations,space_capabilities,space_access,memberships,spaces,access_limits,identity_tokens,sessions,recovery_codes,principals,jobs,technical_audit,action_receipts RESTART IDENTITY`)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.messages = map[string][]string{}
	f.mu.Unlock()
}
func (f *fixture) request[T any](t *testing.T, method, path, token, key string, body any, want int) T {
	t.Helper()
	data := []byte{}
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequestWithContext(t.Context(), method, f.server.URL+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	result, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		var p httpapi.Problem
		_ = json.Unmarshal(result, &p)
		t.Fatalf("%s %s: status=%d code=%s want=%d", method, path, res.StatusCode, p.Code, want)
	}
	var out T
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatal("decode response", err)
	}
	return out
}
func (f *fixture) drain(t *testing.T) {
	t.Helper()
	for range 100 {
		worked, err := f.worker.Once(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			return
		}
	}
	t.Fatal("unbounded mail drain")
}
func (f *fixture) code(t *testing.T, email string) string {
	t.Helper()
	f.drain(t)
	f.mu.Lock()
	defer f.mu.Unlock()
	messages := f.messages[email]
	pattern := regexp.MustCompile(`[A-Za-z0-9_-]{43}`)
	for i := len(messages) - 1; i >= 0; i-- {
		if code := pattern.FindString(messages[i]); code != "" {
			return code
		}
	}
	t.Fatal("confirmation mail not delivered")
	return ""
}
func (f *fixture) newUser(t *testing.T, email string) user {
	t.Helper()
	u := user{Email: email, Password: "Synthetic unique phrase " + uuid.New().String()}
	f.request[map[string]any](t, "POST", "/v1/auth/register", "", "", access.Registration{Email: email, Password: u.Password, Name: "Synthetic", Currency: "PEN", Zone: "America/Lima", Locale: "es-PE"}, 200)
	f.request[map[string]any](t, "POST", "/v1/auth/verify", "", "", map[string]any{"token": f.code(t, email)}, 200)
	session := f.request[access.SessionResult](t, "POST", "/v1/auth/login", "", "", access.Credentials{Email: email, Password: u.Password, Device: "test"}, 200)
	u.Token = session.Token
	p := f.request[access.Profile](t, "GET", "/v1/me", u.Token, "", nil, 200)
	u.ID = p.ID
	spaces := f.request[access.Page[access.SpaceView]](t, "GET", "/v1/spaces", u.Token, "", nil, 200)
	if len(spaces.Items) != 1 {
		t.Fatalf("personal space count = %d; want 1", len(spaces.Items))
	}
	u.SpaceID, u.MemberID = spaces.Items[0].ID, spaces.Items[0].MembershipID
	return u
}
func (f *fixture) invite(t *testing.T, owner, recipient user, role string) string {
	t.Helper()
	f.request[map[string]any](t, "POST", "/v1/spaces/"+owner.SpaceID+"/invitations", owner.Token, uuid.New().String(), access.Invite{Email: recipient.Email, Role: role}, 200)
	return f.code(t, recipient.Email)
}
func (f *fixture) join(t *testing.T, owner, recipient user, role string) string {
	t.Helper()
	token := f.invite(t, owner, recipient, role)
	result := f.request[struct {
		ID string `json:"id"`
	}](t, "POST", "/v1/invitations/decision", recipient.Token, uuid.New().String(), access.InvitationDecision{Token: token, Accept: true, AcknowledgeSharedAccounts: true}, 200)
	return result.ID
}
func (f *fixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.Pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAccess(t *testing.T) {
	f := setup(t)
	t.Run("verified registration and transactional personal space", func(t *testing.T) {
		f.reset(t)
		v := access.Registration{Email: "owner@example.test", Password: "Synthetic phrase for concurrent registration", Name: "Owner", Currency: "PEN", Zone: "America/Lima", Locale: "es-PE"}
		var wg sync.WaitGroup
		for range 6 {
			wg.Go(func() {
				if err := f.service.Register(t.Context(), v, "127.0.0.1"); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if f.count(t, `SELECT count(*) FROM principals`) != 1 || f.count(t, `SELECT count(*) FROM jobs`) != 1 {
			t.Fatal("registration was not idempotent")
		}
		f.request[httpapi.Problem](t, "POST", "/v1/auth/login", "", "", access.Credentials{Email: v.Email, Password: v.Password, Device: "test"}, 401)
		code := f.code(t, v.Email)
		for range 6 {
			wg.Go(func() {
				if err := f.service.Verify(t.Context(), code, "127.0.0.1"); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if f.count(t, `SELECT count(*) FROM spaces`) != 1 || f.count(t, `SELECT count(*) FROM memberships`) != 1 {
			t.Fatal("personal space duplicated")
		}
		if f.count(t, `SELECT count(*) FROM space_capabilities WHERE capability='members' AND used=1`) != 1 {
			t.Fatal("personal admission not charged once")
		}
		if f.count(t, `SELECT count(*) FROM identity_tokens WHERE consumed_at IS NOT NULL`) != 1 {
			t.Fatal("verification token not consumed")
		}
	})
	t.Run("session revocation expiry realms and absence of enumeration", func(t *testing.T) {
		f.reset(t)
		u := f.newUser(t, "session@example.test")
		other := f.request[access.SessionResult](t, "POST", "/v1/auth/login", "", "", access.Credentials{Email: u.Email, Password: u.Password, Device: "another device"}, 200)
		sessions := f.request[[]access.SessionView](t, "GET", "/v1/me/sessions", u.Token, "", nil, 200)
		if len(sessions) != 2 {
			t.Fatal("device sessions missing")
		}
		unknown := f.request[httpapi.Problem](t, "POST", "/v1/auth/login", "", "", access.Credentials{Email: "absent@example.test", Password: "Not a valid password", Device: "test"}, 401)
		wrong := f.request[httpapi.Problem](t, "POST", "/v1/auth/login", "", "", access.Credentials{Email: u.Email, Password: "Not a valid password", Device: "test"}, 401)
		if unknown.Code != wrong.Code {
			t.Fatal("enumerating credential response")
		}
		f.request[httpapi.Problem](t, "GET", "/v1/internal/me", u.Token, "", nil, 401)
		f.request[map[string]any](t, "POST", "/v1/auth/logout", u.Token, "", nil, 200)
		f.request[httpapi.Problem](t, "GET", "/v1/me", u.Token, "", nil, 401)
		f.request[access.Profile](t, "GET", "/v1/me", other.Token, "", nil, 200)
		if _, err := f.db.Pool.Exec(t.Context(), `UPDATE sessions SET seen_at=clock_timestamp()-interval '2 days' WHERE id=$1`, other.SessionID); err != nil {
			t.Fatal(err)
		}
		f.request[httpapi.Problem](t, "GET", "/v1/me", other.Token, "", nil, 401)
		if f.count(t, `SELECT count(*) FROM sessions WHERE encode(verifier,'hex')=$1`, hex.EncodeToString([]byte(other.Token))) != 0 {
			t.Fatal("raw session persisted")
		}
	})
	t.Run("MFA replay recovery and password reset cannot bypass factors", func(t *testing.T) {
		f.reset(t)
		u := f.newUser(t, "mfa@example.test")
		setup := f.request[struct {
			URI string `json:"uri"`
		}](t, "POST", "/v1/me/mfa/setup", u.Token, "", nil, 200)
		uri, err := url.Parse(setup.URI)
		if err != nil {
			t.Fatal(err)
		}
		secret := uri.Query().Get("secret")
		code, err := totp.GenerateCode(secret, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		enabled := f.request[struct {
			Codes []string `json:"recovery_codes"`
		}](t, "POST", "/v1/me/mfa/confirm", u.Token, "", map[string]any{"code": code}, 200)
		if len(enabled.Codes) != 10 {
			t.Fatal("recovery codes missing")
		}
		f.request[httpapi.Problem](t, "GET", "/v1/me", u.Token, "", nil, 401)
		f.request[httpapi.Problem](t, "POST", "/v1/auth/login", "", "", access.Credentials{Email: u.Email, Password: u.Password, Code: code, Device: "test"}, 401)
		session := f.request[access.SessionResult](t, "POST", "/v1/auth/login", "", "", access.Credentials{Email: u.Email, Password: u.Password, Code: enabled.Codes[0], Device: "recovered"}, 200)
		f.request[httpapi.Problem](t, "POST", "/v1/auth/login", "", "", access.Credentials{Email: u.Email, Password: u.Password, Code: enabled.Codes[0], Device: "duplicate"}, 401)
		f.request[map[string]any](t, "POST", "/v1/auth/recovery", "", "", map[string]any{"email": u.Email}, 200)
		recovery := f.code(t, u.Email)
		newPassword := "A different synthetic recovered phrase" //nolint:gosec // Synthetic recovery fixture in a disposable database.
		f.request[httpapi.Problem](t, "POST", "/v1/auth/password-reset", "", "", map[string]any{"token": recovery, "password": newPassword}, 401)
		f.request[map[string]any](t, "POST", "/v1/auth/password-reset", "", "", map[string]any{"token": recovery, "password": newPassword, "code": enabled.Codes[1]}, 200)
		f.request[httpapi.Problem](t, "GET", "/v1/me", session.Token, "", nil, 401)
		f.request[httpapi.Problem](t, "POST", "/v1/auth/login", "", "", access.Credentials{Email: u.Email, Password: newPassword, Device: "test"}, 401)
		restored := f.request[access.SessionResult](t, "POST", "/v1/auth/login", "", "", access.Credentials{Email: u.Email, Password: newPassword, Code: enabled.Codes[2], Device: "test"}, 200)
		p := f.request[access.Profile](t, "GET", "/v1/me", restored.Token, "", nil, 200)
		if !p.MFA {
			t.Fatal("password recovery disabled MFA")
		}
		if f.count(t, `SELECT count(*) FROM principals WHERE mfa_secret IS NOT NULL AND position(convert_to($1,'UTF8') in mfa_secret)>0`, secret) != 0 {
			t.Fatal("plaintext MFA secret in database")
		}
		f.request[map[string]any](t, "POST", "/v1/me/mfa/disable", restored.Token, "", map[string]any{"confirm": true}, 200)
		f.request[httpapi.Problem](t, "GET", "/v1/me", restored.Token, "", nil, 401)
		f.request[access.SessionResult](t, "POST", "/v1/auth/login", "", "", access.Credentials{Email: u.Email, Password: newPassword, Device: "after disable"}, 200)
	})
	t.Run("space contexts versions receipts and role isolation", func(t *testing.T) {
		f.reset(t)
		owner := f.newUser(t, "owner@example.test")
		reader := f.newUser(t, "reader@example.test")
		outsider := f.newUser(t, "outsider@example.test")
		f.join(t, owner, reader, "reader")
		spaces := f.request[access.Page[access.SpaceView]](t, "GET", "/v1/spaces?limit=1", reader.Token, "", nil, 200)
		if len(spaces.Items) != 1 || spaces.NextCursor == "" {
			t.Fatal("cursor paging missing")
		}
		second := f.request[access.Page[access.SpaceView]](t, "GET", "/v1/spaces?limit=1&cursor="+spaces.NextCursor, reader.Token, "", nil, 200)
		if len(second.Items) != 1 || second.Items[0].ID == spaces.Items[0].ID {
			t.Fatal("unstable cursor")
		}
		f.request[httpapi.Problem](t, "GET", "/v1/spaces/"+owner.SpaceID, outsider.Token, "", nil, 404)
		f.request[map[string]any](t, "POST", "/v1/spaces/"+owner.SpaceID+"/select", reader.Token, "", nil, 200)
		another := f.request[access.SessionResult](t, "POST", "/v1/auth/login", "", "", access.Credentials{Email: reader.Email, Password: reader.Password, Device: "other"}, 200)
		p := f.request[access.Profile](t, "GET", "/v1/me", another.Token, "", nil, 200)
		if p.ActiveSpace != "" {
			t.Fatal("context leaked across devices")
		}
		v := f.request[access.SpaceView](t, "GET", "/v1/spaces/"+owner.SpaceID, owner.Token, "", nil, 200)
		v.Settings.Name = "Edited space"
		edit := map[string]any{"settings": v.Settings, "version": v.Version}
		key := uuid.New().String()
		f.request[map[string]any](t, "PUT", "/v1/spaces/"+owner.SpaceID, reader.Token, key, edit, 403)
		f.request[map[string]any](t, "PUT", "/v1/spaces/"+owner.SpaceID, owner.Token, key, edit, 200)
		f.request[map[string]any](t, "PUT", "/v1/spaces/"+owner.SpaceID, owner.Token, key, edit, 200)
		f.request[httpapi.Problem](t, "PUT", "/v1/spaces/"+owner.SpaceID, owner.Token, uuid.New().String(), edit, 409)
		v.Settings.Name = "Different"
		edit["settings"] = v.Settings
		conflict := f.request[httpapi.Problem](t, "PUT", "/v1/spaces/"+owner.SpaceID, owner.Token, key, edit, 409)
		if conflict.Code != "idempotency_conflict" {
			t.Fatal("content mismatch not distinguished")
		}
	})
	t.Run("concurrent invitations respect per-space admission and commercial replacement", func(t *testing.T) {
		f.reset(t)
		owner := f.newUser(t, "owner@example.test")
		a := f.newUser(t, "a@example.test")
		b := f.newUser(t, "b@example.test")
		ta, tb := f.invite(t, owner, a, "member"), f.invite(t, owner, b, "member")
		err := f.db.Transaction(t.Context(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			return f.service.ReplaceEntitlements(ctx, tx, access.EntitlementChange{SpaceID: owner.SpaceID, Source: "commercial", Profile: "synthetic-verified-rights", SourceRef: "test-provider:event-1", State: "active", Operator: "test-provider", ExpectedVersion: 1, ValidFrom: time.Now().Add(-time.Minute), ValidUntil: time.Now().Add(time.Hour), Grants: []access.CapabilityGrant{{Key: "read", Ceiling: 1}, {Key: "manage", Ceiling: 1}, {Key: "invite", Ceiling: 1}, {Key: "members", Ceiling: 2}}})
		})
		if err != nil {
			t.Fatal(err)
		}
		other, err := access.New(f.store, f.policy)
		if err != nil {
			t.Fatal(err)
		}
		var accepted atomic.Int64
		var wg sync.WaitGroup
		for _, v := range []struct {
			u       user
			token   string
			service *access.Service
		}{{u: a, token: ta, service: f.service}, {u: b, token: tb, service: other}} {
			wg.Go(func() {
				_, err := v.service.DecideInvitation(t.Context(), "Bearer "+v.u.Token, uuid.New().String(), access.InvitationDecision{Token: v.token, Accept: true, AcknowledgeSharedAccounts: true})
				if err == nil {
					accepted.Add(1)
				} else if !errors.Is(err, access.ErrCapacity) {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if accepted.Load() != 1 || f.count(t, `SELECT count(*) FROM memberships WHERE space_id=$1 AND ended_at IS NULL`, owner.SpaceID) != 2 {
			t.Fatal("concurrent admission exceeded limit")
		}
		if f.count(t, `SELECT used FROM space_capabilities WHERE space_id=$1 AND capability='members'`, owner.SpaceID) != 2 {
			t.Fatal("member consumption mismatch")
		}
		if f.count(t, `SELECT count(*) FROM spaces WHERE personal_for IS NOT NULL`) != 3 {
			t.Fatal("invitation replaced personal spaces")
		}
		if f.count(t, `SELECT count(*) FROM space_access WHERE source='development'`) != 2 {
			t.Fatal("benefits leaked to other spaces")
		}
	})
	t.Run("membership removal ownership and billing remain independent", func(t *testing.T) {
		f.reset(t)
		owner := f.newUser(t, "owner@example.test")
		next := f.newUser(t, "next@example.test")
		member := f.join(t, owner, next, "member")
		f.request[map[string]any](t, "POST", "/v1/spaces/"+owner.SpaceID+"/ownership", owner.Token, uuid.New().String(), map[string]any{"member_id": member, "confirm": true}, 200)
		before := f.request[access.AccessView](t, "GET", "/v1/spaces/"+owner.SpaceID+"/access", next.Token, "", nil, 200)
		if before.BillingMember != owner.MemberID {
			t.Fatal("ownership silently changed billing")
		}
		f.request[httpapi.Problem](t, "POST", "/v1/spaces/"+owner.SpaceID+"/members/"+owner.MemberID+"/remove", owner.Token, uuid.New().String(), map[string]any{"confirm": true}, 409)
		f.request[map[string]any](t, "POST", "/v1/spaces/"+owner.SpaceID+"/billing/claim", next.Token, uuid.New().String(), map[string]any{"confirm": true}, 200)
		f.request[map[string]any](t, "POST", "/v1/spaces/"+owner.SpaceID+"/members/"+owner.MemberID+"/remove", owner.Token, uuid.New().String(), map[string]any{"confirm": true}, 200)
		f.request[httpapi.Problem](t, "GET", "/v1/spaces/"+owner.SpaceID, owner.Token, "", nil, 404)
		f.request[access.SpaceView](t, "GET", "/v1/spaces/"+next.SpaceID, next.Token, "", nil, 200)
		owner2 := next
		owner2.SpaceID = owner.SpaceID
		rejoined := f.join(t, owner2, owner, "reader")
		if rejoined == owner.MemberID {
			t.Fatal("old membership and grants resurrected")
		}
	})
	t.Run("separate internal identity requires MFA and grants no customer membership", func(t *testing.T) {
		f.reset(t)
		customer := f.newUser(t, "staff@example.test")
		key, err := totp.Generate(totp.GenerateOpts{Issuer: "Synthetic staff", AccountName: customer.Email})
		if err != nil {
			t.Fatal(err)
		}
		code, err := totp.GenerateCode(key.Secret(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		codes, err := f.service.ProvisionStaff(t.Context(), access.Credentials{Email: customer.Email, Password: customer.Password, Code: code}, key.Secret(), "test-operator")
		if err != nil {
			t.Fatal(err)
		}
		f.request[httpapi.Problem](t, "POST", "/v1/internal/auth/register", "", "", nil, 404)
		f.request[httpapi.Problem](t, "POST", "/v1/internal/auth/login", "", "", access.Credentials{Email: customer.Email, Password: customer.Password, Device: "test"}, 401)
		staff := f.request[access.SessionResult](t, "POST", "/v1/internal/auth/login", "", "", access.Credentials{Email: customer.Email, Password: customer.Password, Code: codes[0], Device: "test"}, 200)
		f.request[access.Profile](t, "GET", "/v1/internal/me", staff.Token, "", nil, 200)
		f.request[httpapi.Problem](t, "GET", "/v1/me", staff.Token, "", nil, 401)
		f.request[httpapi.Problem](t, "GET", "/v1/internal/me", customer.Token, "", nil, 401)
		f.request[httpapi.Problem](t, "GET", "/v1/spaces/"+customer.SpaceID, staff.Token, "", nil, 401)
		if f.count(t, `SELECT count(*) FROM principals WHERE realm='staff' AND mfa_secret IS NOT NULL`) != 1 {
			t.Fatal("staff MFA not persisted")
		}
	})
}
