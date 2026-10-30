//go:build integration

package access_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/httpapi"
)

func TestAccessLifecycle(t *testing.T) {
	f := setup(t)
	t.Run("replayed commands retain current action-specific authorization", func(t *testing.T) {
		f.reset(t)
		owner := f.newUser(t, "owner@example.test")
		admin := f.newUser(t, "admin@example.test")
		recipient := f.newUser(t, "recipient@example.test")
		adminMember := f.join(t, owner, admin, "admin")
		command := access.SpaceCommand{Authorization: "Bearer " + admin.Token, SpaceID: owner.SpaceID, Key: uuid.New().String()}
		joined := f.join(t, owner, recipient, "member")
		if _, err := f.service.RemoveMember(t.Context(), command, joined); err != nil {
			t.Fatal(err)
		}
		f.request[map[string]any](t, "POST", "/v1/spaces/"+owner.SpaceID+"/members/"+adminMember+"/role", owner.Token, uuid.New().String(), map[string]any{"role": "reader", "confirm": true}, 200)
		if _, err := f.service.RemoveMember(t.Context(), command, joined); !errors.Is(err, access.ErrForbidden) {
			t.Fatal("replayed removal bypassed current management role", err)
		}
		if f.count(t, `SELECT count(*) FROM action_receipts WHERE action_key=$1`, command.Key) != 1 {
			t.Fatal("denied replay changed original receipt")
		}
	})
	t.Run("resend invalidates previous delivery and cancellation prevents sending", func(t *testing.T) {
		f.reset(t)
		owner := f.newUser(t, "owner@example.test")
		recipient := f.newUser(t, "recipient@example.test")
		invitation := f.request[struct {
			ID string `json:"id"`
		}](t, "POST", "/v1/spaces/"+owner.SpaceID+"/invitations", owner.Token, uuid.New().String(), access.Invite{Email: recipient.Email, Role: "member"}, 200)
		// Keep the old delivery queued, replacing its token before any external send.
		if _, err := f.db.Pool.Exec(t.Context(), `UPDATE invitations SET sent_at=clock_timestamp()-interval '2 minutes' WHERE id=$1`, invitation.ID); err != nil {
			t.Fatal(err)
		}
		f.request[map[string]any](t, "POST", "/v1/spaces/"+owner.SpaceID+"/invitations/"+invitation.ID+"/resend", owner.Token, uuid.New().String(), nil, 200)
		token := f.code(t, recipient.Email)
		if f.count(t, `SELECT count(*) FROM jobs WHERE state='cancelled'`) != 1 {
			t.Fatal("replaced delivery sent")
		}
		preview := f.request[access.InvitationPreview](t, "POST", "/v1/invitations/preview", recipient.Token, "", map[string]any{"token": token}, 200)
		if preview.SpaceID != owner.SpaceID || preview.Role != "member" || !preview.GrantsSpaceSharedAccounts || preview.SharesExistingPrivateAccounts {
			t.Fatal("invitation did not explain the correct scope")
		}
		f.request[httpapi.Problem](t, "POST", "/v1/invitations/decision", recipient.Token, uuid.New().String(), access.InvitationDecision{Token: token, Accept: true}, 422)
		f.request[map[string]any](t, "POST", "/v1/spaces/"+owner.SpaceID+"/invitations/"+invitation.ID+"/cancel", owner.Token, uuid.New().String(), nil, 200)
		f.request[httpapi.Problem](t, "POST", "/v1/invitations/decision", recipient.Token, uuid.New().String(), access.InvitationDecision{Token: token, Accept: true, AcknowledgeSharedAccounts: true}, 422)
		if f.count(t, `SELECT count(*) FROM memberships WHERE principal_id=$1 AND ended_at IS NULL`, recipient.ID) != 1 {
			t.Fatal("cancelled invitation partially joined")
		}
	})
	t.Run("inviter role is revalidated and replay cannot resurrect an old membership", func(t *testing.T) {
		f.reset(t)
		owner := f.newUser(t, "owner@example.test")
		admin := f.newUser(t, "admin@example.test")
		recipient := f.newUser(t, "recipient@example.test")
		adminMember := f.join(t, owner, admin, "admin")
		admin.SpaceID = owner.SpaceID
		invitationToken := f.invite(t, admin, recipient, "member")
		f.request[map[string]any](t, "POST", "/v1/spaces/"+owner.SpaceID+"/members/"+adminMember+"/role", owner.Token, uuid.New().String(), map[string]any{"role": "member", "confirm": true}, 200)
		f.request[httpapi.Problem](t, "POST", "/v1/invitations/decision", recipient.Token, uuid.New().String(), access.InvitationDecision{Token: invitationToken, Accept: true, AcknowledgeSharedAccounts: true}, 403)
		if f.count(t, `SELECT used FROM space_capabilities WHERE space_id=$1 AND capability='members'`, owner.SpaceID) != 2 {
			t.Fatal("denied acceptance consumed a slot")
		}
		pending := f.request[access.Page[access.InvitationView]](t, "GET", "/v1/spaces/"+owner.SpaceID+"/invitations", owner.Token, "", nil, 200)
		for _, i := range pending.Items {
			if i.Email == recipient.Email && i.State == "pending" {
				f.request[map[string]any](t, "POST", "/v1/spaces/"+owner.SpaceID+"/invitations/"+i.ID+"/cancel", owner.Token, uuid.New().String(), nil, 200)
			}
		}
		token := f.invite(t, owner, recipient, "member")
		decision := access.InvitationDecision{Token: token, Accept: true, AcknowledgeSharedAccounts: true}
		action := uuid.New().String()
		joined := f.request[struct {
			ID string `json:"id"`
		}](t, "POST", "/v1/invitations/decision", recipient.Token, action, decision, 200)
		f.request[map[string]any](t, "POST", "/v1/invitations/decision", recipient.Token, action, decision, 200)
		f.request[access.InvitationPreview](t, "POST", "/v1/invitations/preview", recipient.Token, "", map[string]any{"token": token}, 200)
		f.request[map[string]any](t, "POST", "/v1/spaces/"+owner.SpaceID+"/members/"+joined.ID+"/remove", owner.Token, uuid.New().String(), map[string]any{"confirm": true}, 200)
		f.request[httpapi.Problem](t, "POST", "/v1/invitations/decision", recipient.Token, action, decision, 404)
		f.request[httpapi.Problem](t, "POST", "/v1/invitations/preview", recipient.Token, "", map[string]any{"token": token}, 404)
		newMember := f.join(t, owner, recipient, "reader")
		if newMember == joined.ID {
			t.Fatal("membership episode reused")
		}
		f.request[httpapi.Problem](t, "POST", "/v1/invitations/decision", recipient.Token, action, decision, 404)
		f.request[httpapi.Problem](t, "POST", "/v1/invitations/decision", recipient.Token, uuid.New().String(), decision, 404)
		f.request[httpapi.Problem](t, "POST", "/v1/invitations/preview", recipient.Token, "", map[string]any{"token": token}, 404)
	})
	t.Run("revocation commits before an already started command gets authorized", func(t *testing.T) {
		f.reset(t)
		owner := f.newUser(t, "owner@example.test")
		admin := f.newUser(t, "admin@example.test")
		member := f.join(t, owner, admin, "admin")
		hold, err := f.db.Pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = hold.Rollback(context.WithoutCancel(t.Context())) }()
		if _, err := hold.Exec(t.Context(), `SELECT id FROM principals WHERE id=$1 FOR UPDATE`, admin.ID); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := f.service.Invite(t.Context(), access.SpaceCommand{Authorization: "Bearer " + admin.Token, SpaceID: owner.SpaceID, Key: uuid.New().String()}, access.Invite{Email: "not-joined@example.test", Role: "member"})
			done <- err
		}()
		blocked := false
		for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
			if f.count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`) > 0 {
				blocked = true
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if !blocked {
			t.Fatal("command never waited for authorization")
		}
		f.request[map[string]any](t, "POST", "/v1/spaces/"+owner.SpaceID+"/members/"+member+"/remove", owner.Token, uuid.New().String(), map[string]any{"confirm": true}, 200)
		if err := hold.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if !errors.Is(err, access.ErrNotFound) {
				t.Fatal("command survived revocation", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("command did not stop")
		}
		if f.count(t, `SELECT count(*) FROM invitations WHERE email='not-joined@example.test'`) != 0 {
			t.Fatal("revoked command wrote effects")
		}
	})
	t.Run("artifact scope is invalidated without granting private account access", func(t *testing.T) {
		f.reset(t)
		owner := f.newUser(t, "owner@example.test")
		member := f.newUser(t, "member@example.test")
		membership := f.join(t, owner, member, "member")
		var snapshot access.ScopeSnapshot
		err := f.db.Transaction(t.Context(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			a, err := f.service.AuthenticateCustomer(ctx, tx, "Bearer "+member.Token)
			if err != nil {
				return err
			}
			snapshot, err = f.service.CaptureScope(ctx, tx, a, owner.SpaceID)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		f.request[map[string]any](t, "POST", "/v1/spaces/"+owner.SpaceID+"/members/"+membership+"/remove", owner.Token, uuid.New().String(), map[string]any{"confirm": true}, 200)
		f.join(t, owner, member, "member")
		err = f.db.Transaction(t.Context(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			a, err := f.service.AuthenticateCustomer(ctx, tx, "Bearer "+member.Token)
			if err != nil {
				return err
			}
			return f.service.RevalidateScope(ctx, tx, a, snapshot)
		})
		if !errors.Is(err, access.ErrNotFound) {
			t.Fatal("old derived scope became readable again", err)
		}
	})
	t.Run("expired and absent capabilities fail closed without affecting other spaces", func(t *testing.T) {
		f.reset(t)
		u := f.newUser(t, "owner@example.test")
		settings := access.SpaceSettings{Name: "Additional", Purpose: "family", Currency: "USD", Zone: "UTC", Locale: "en", PeriodDay: 31, NewAccountVisibility: "private"}
		extra := f.request[struct {
			ID string `json:"id"`
		}](t, "POST", "/v1/spaces", u.Token, uuid.New().String(), settings, 200)
		if _, err := f.db.Pool.Exec(t.Context(), `UPDATE space_access SET valid_from=clock_timestamp()-interval '2 days',valid_until=clock_timestamp()-interval '1 day' WHERE space_id=$1`, extra.ID); err != nil {
			t.Fatal(err)
		}
		f.request[httpapi.Problem](t, "GET", "/v1/spaces/"+extra.ID, u.Token, "", nil, 403)
		f.request[access.SpaceView](t, "GET", "/v1/spaces/"+u.SpaceID, u.Token, "", nil, 200)
		if _, err := f.db.Pool.Exec(t.Context(), `DELETE FROM space_capabilities WHERE space_id=$1 AND capability='invite'`, u.SpaceID); err != nil {
			t.Fatal(err)
		}
		f.request[httpapi.Problem](t, "POST", "/v1/spaces/"+u.SpaceID+"/invitations", u.Token, uuid.New().String(), access.Invite{Email: "recipient@example.test", Role: "member"}, 403)
		if f.count(t, `SELECT count(*) FROM invitations`) != 0 {
			t.Fatal("missing capability admitted invitation")
		}
	})
}

func TestIdentityLifecycle(t *testing.T) {
	f := setup(t)
	t.Run("reauthentication rotates tokens and credential changes revoke sessions", func(t *testing.T) {
		f.reset(t)
		u := f.newUser(t, "owner@example.test")
		if _, err := f.db.Pool.Exec(t.Context(), `UPDATE sessions SET reauthenticated_at=clock_timestamp()-interval '10 minutes' WHERE principal_id=$1`, u.ID); err != nil {
			t.Fatal(err)
		}
		newPassword := "A different long synthetic changed phrase"
		denied := f.request[httpapi.Problem](t, "POST", "/v1/me/password", u.Token, "", map[string]any{"password": newPassword}, 403)
		if denied.Code != "reauthentication_required" {
			t.Fatal("missing recent-authentication reason")
		}
		rotated := f.request[access.SessionResult](t, "POST", "/v1/auth/reauthenticate", u.Token, "", map[string]any{"password": u.Password}, 200)
		f.request[httpapi.Problem](t, "GET", "/v1/me", u.Token, "", nil, 401)
		f.request[map[string]any](t, "POST", "/v1/me/password", rotated.Token, "", map[string]any{"password": newPassword}, 200)
		f.request[httpapi.Problem](t, "GET", "/v1/me", rotated.Token, "", nil, 401)
		f.request[httpapi.Problem](t, "POST", "/v1/auth/login", "", "", access.Credentials{Email: u.Email, Password: u.Password, Device: "test"}, 401)
		f.request[access.SessionResult](t, "POST", "/v1/auth/login", "", "", access.Credentials{Email: u.Email, Password: newPassword, Device: "test"}, 200)
	})
	t.Run("email change proves new mailbox and preserves unrelated identity", func(t *testing.T) {
		f.reset(t)
		u := f.newUser(t, "before@example.test")
		other := f.newUser(t, "other@example.test")
		f.request[map[string]any](t, "POST", "/v1/me/email", u.Token, "", map[string]any{"email": "after@example.test"}, 200)
		token := f.code(t, "after@example.test")
		f.request[httpapi.Problem](t, "POST", "/v1/me/email/confirm", other.Token, "", map[string]any{"token": token}, 401)
		f.request[map[string]any](t, "POST", "/v1/me/email/confirm", u.Token, "", map[string]any{"token": token}, 200)
		f.request[httpapi.Problem](t, "GET", "/v1/me", u.Token, "", nil, 401)
		session := f.request[access.SessionResult](t, "POST", "/v1/auth/login", "", "", access.Credentials{Email: "after@example.test", Password: u.Password, Device: "test"}, 200)
		profile := f.request[access.Profile](t, "GET", "/v1/me", session.Token, "", nil, 200)
		if profile.ID != u.ID {
			t.Fatal("mail change replaced identity")
		}
		f.request[access.Profile](t, "GET", "/v1/me", other.Token, "", nil, 200)
	})
	t.Run("preferences use versions and session revocation is ownership scoped", func(t *testing.T) {
		f.reset(t)
		u := f.newUser(t, "owner@example.test")
		other := f.newUser(t, "other@example.test")
		p := f.request[access.Profile](t, "GET", "/v1/me", u.Token, "", nil, 200)
		prefs := access.Preferences{Name: "Updated", Locale: "en-GB", Zone: "Europe/London", Notifications: false, Version: p.Version}
		f.request[map[string]any](t, "PUT", "/v1/me/preferences", u.Token, "", prefs, 200)
		f.request[httpapi.Problem](t, "PUT", "/v1/me/preferences", u.Token, "", prefs, 409)
		p = f.request[access.Profile](t, "GET", "/v1/me", u.Token, "", nil, 200)
		if p.Name != "Updated" || p.Notifications {
			t.Fatal("preferences not persisted")
		}
		sessions := f.request[[]access.SessionView](t, "GET", "/v1/me/sessions", other.Token, "", nil, 200)
		f.request[map[string]any](t, "POST", "/v1/me/sessions/revoke", u.Token, "", map[string]any{"id": sessions[0].ID, "all": false}, 200)
		f.request[access.Profile](t, "GET", "/v1/me", other.Token, "", nil, 200)
	})
	t.Run("durable limiter is shared across service instances and expires without suspension", func(t *testing.T) {
		f.reset(t)
		u := f.newUser(t, "limited@example.test")
		if _, err := f.db.Pool.Exec(t.Context(), `TRUNCATE access_limits`); err != nil {
			t.Fatal(err)
		}
		p := f.policy
		p.IdentityAttempts = 2
		one, err := access.New(f.store, p)
		if err != nil {
			t.Fatal(err)
		}
		two, err := access.New(f.store, p)
		if err != nil {
			t.Fatal(err)
		}
		wrong := access.Credentials{Email: u.Email, Password: "invalid synthetic phrase", Device: "test"}
		for _, s := range []*access.Service{one, two} {
			if _, err := s.Login(t.Context(), wrong, "customer", "independent-origin"); !errors.Is(err, access.ErrUnauthorized) {
				t.Fatal(err)
			}
		}
		if _, err := one.Login(t.Context(), wrong, "customer", "independent-origin"); !errors.Is(err, access.ErrLimited) {
			t.Fatal("failure counters rolled back", err)
		}
		if _, err := f.db.Pool.Exec(t.Context(), `UPDATE access_limits SET expires_at=clock_timestamp()-interval '1 second'`); err != nil {
			t.Fatal(err)
		}
		if _, err := two.Login(t.Context(), access.Credentials{Email: u.Email, Password: u.Password, Device: "test"}, "customer", "independent-origin"); err != nil {
			t.Fatal("transient limiter suspended identity", err)
		}
	})
	t.Run("failed mail admission rolls back registration and expired secrets are scrubbed", func(t *testing.T) {
		f.reset(t)
		small, err := access.New(&execution.Store{DB: f.db, QueueLimit: 1}, f.policy)
		if err != nil {
			t.Fatal(err)
		}
		v := access.Registration{Email: "first@example.test", Password: "A synthetic pending registration phrase", Name: "Test", Currency: "PEN", Zone: "America/Lima", Locale: "es-PE"}
		if err := small.Register(t.Context(), v, "test"); err != nil {
			t.Fatal(err)
		}
		v.Email = "failed@example.test"
		if err := small.Register(t.Context(), v, "test"); !errors.Is(err, execution.ErrQueueFull) {
			t.Fatal(err)
		}
		if f.count(t, `SELECT count(*) FROM principals WHERE email='failed@example.test'`) != 0 {
			t.Fatal("failed registration partially persisted")
		}
		if _, err := f.db.Pool.Exec(t.Context(), `UPDATE identity_tokens SET expires_at=clock_timestamp()-interval '1 second'; UPDATE access_deliveries SET expires_at=clock_timestamp()-interval '1 second'`); err != nil {
			t.Fatal(err)
		}
		if err := access.MaintainSecrets(t.Context(), f.db); err != nil {
			t.Fatal(err)
		}
		if f.count(t, `SELECT count(*) FROM access_deliveries WHERE octet_length(ciphertext)>0 OR target_email<>''`) != 0 {
			t.Fatal("expired delivery secret retained")
		}
		if f.count(t, `SELECT count(*) FROM identity_tokens WHERE target_email<>''`) != 0 {
			t.Fatal("expired token recipient retained")
		}
		f.drain(t)
		if f.count(t, `SELECT count(*) FROM jobs WHERE state='cancelled'`) != 1 {
			t.Fatal("expired confirmation sent")
		}
	})
	t.Run("invalid bodies never echo supplied credentials", func(t *testing.T) {
		f.reset(t)
		p := f.request[httpapi.Problem](t, "POST", "/v1/auth/register", "", "", map[string]any{"email": "not-email", "password": "very-private-secret", "name": "x"}, 422)
		if strings.Contains(p.Error(), "private-secret") || p.RequestID == "" {
			t.Fatal("unsafe validation response")
		}
	})
}
