package app

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"

	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/config"
	"github.com/stephanofer/facets-server/internal/database"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/telemetry"
)

// ProvisionStaff reads secrets from a restricted stdin stream, not flags or logs.
// This creates an identity with no administrative or financial faculties.
func ProvisionStaff(ctx context.Context, c config.Config, operator string, in io.Reader, out io.Writer) error {
	if operator == "" || c.MigrationURL == "" {
		return errors.New("privileged provisioning credentials and operator required")
	}
	data, err := io.ReadAll(io.LimitReader(in, 16385))
	if err != nil {
		return err
	}
	defer clear(data)
	if len(data) > 16384 {
		return errors.New("provisioning input too large")
	}
	var v struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Secret   string `json:"totp_secret"`
		Code     string `json:"code"`
	}
	if err := json.Unmarshal(data, &v, json.RejectUnknownMembers(true)); err != nil {
		return errors.New("invalid provisioning input")
	}
	signals := telemetry.New(slog.New(slog.DiscardHandler))
	defer func() { _ = signals.Shutdown(context.WithoutCancel(ctx)) }()
	db, err := database.Open(ctx, database.Options{URL: c.MigrationURL, MaxConns: c.PoolMax, Timeout: c.SQLTimeout, Tracer: signals.Tracer()})
	if err != nil {
		return err
	}
	defer db.Pool.Close()
	if err := db.Ready(ctx); err != nil {
		return err
	}
	service, err := access.New(&execution.Store{DB: db, QueueLimit: c.QueueLimit}, c.Access)
	if err != nil {
		return err
	}
	codes, err := service.ProvisionStaff(ctx, access.Credentials{Email: v.Email, Password: v.Password, Code: v.Code}, v.Secret, operator)
	if err != nil {
		return err
	}
	return json.MarshalWrite(out, struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}{RecoveryCodes: codes})
}
