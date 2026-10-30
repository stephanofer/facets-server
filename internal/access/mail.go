package access

import (
	"bytes"
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/mail"
)

type Sender interface {
	Send(context.Context, mail.Message) (string, error)
}
type mailIntent struct {
	Kind, Ref, Principal, Email, Secret string
	Expiry                              time.Time
}

func (s *Service) enqueueMail(ctx context.Context, tx pgx.Tx, v mailIntent) error {
	id := uuid.New().String()
	encrypted := s.crypto.seal("delivery:"+id, []byte(v.Secret))
	if err := accessdb.New(tx).InsertDelivery(ctx, accessdb.InsertDeliveryParams{ID: id, Kind: v.Kind, ResourceRef: v.Ref,
		PrincipalID: nullable(v.Principal), TargetEmail: v.Email, Ciphertext: encrypted, ExpiresAt: instant(v.Expiry)}); err != nil {
		return err
	}
	_, err := s.store.Enqueue(ctx, tx, execution.Job{Queue: "notifications", Kind: "access.mail", DedupKey: "access.mail:" + id,
		Payload: []byte(id), MaxAttempts: 3, ExternalEffect: true})
	return err
}
func (s *Service) securityNotice(ctx context.Context, tx pgx.Tx, p accessdb.Principal, event string) error {
	if err := s.audit(ctx, tx, p.ID, "identity:"+p.ID, event, p.ID); err != nil {
		return err
	}
	return s.enqueueMail(ctx, tx, mailIntent{Kind: "security", Ref: p.ID, Principal: p.ID, Email: p.Email, Expiry: time.Now().Add(24 * time.Hour)})
}

// No arbitrary-recipient handler is registered. Delivery secrets are encrypted
// separately from the verifier and erased after acceptance, cancellation or TTL.
func (s *Service) MailHandler(sender Sender) execution.Handler {
	check := func(ctx context.Context, payload []byte) error {
		_, secret, err := s.delivery(ctx, string(payload))
		clear(secret)
		return err
	}
	return execution.Handler{Check: check, Execute: func(ctx context.Context, _ string, payload []byte) (string, error) {
		d, secret, err := s.delivery(ctx, string(payload))
		if err != nil {
			return "", err
		}
		defer clear(secret)
		if sender == nil {
			return "", execution.ErrPermanent
		}
		message := mail.Message{To: d.TargetEmail, Subject: "Facets access confirmation", Body: "Your Facets confirmation code: " + string(secret) + "\nUse it only in the action you requested. Do not share it."}
		if d.Kind == "invitation" {
			message.Subject = "Facets space invitation"
			message.Body = "You were invited to a Facets space. Sign in with this verified email and review the invitation before accepting.\nInvitation code: " + string(secret) + "\nJoining grants access to accounts shared with the entire space, not private accounts."
		}
		if d.Kind == "security" {
			message.Subject = "Facets security notice"
			message.Body = "Security settings for your Facets identity changed. If you did not authorize this action, secure your email and revoke your Facets sessions."
		}
		result, err := sender.Send(ctx, message)
		if err != nil {
			return "", err
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.store.DB.Timeout)
		defer cancel()
		if err := accessdb.New(s.store.DB.Pool).ClearDelivery(cleanup, d.ID); err != nil {
			return "", errors.Join(execution.ErrUncertain, err)
		}
		return result, nil
	}}
}

func (s *Service) delivery(ctx context.Context, id string) (accessdb.AccessDelivery, []byte, error) {
	if _, err := uuid.Parse(id); err != nil {
		return accessdb.AccessDelivery{}, nil, execution.ErrPermanent
	}
	var d accessdb.AccessDelivery
	var secret []byte
	err := s.store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		q := accessdb.New(tx)
		var err error
		d, err = q.GetDelivery(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return execution.ErrObsolete
		}
		if err != nil {
			return err
		}
		if len(d.Ciphertext) == 0 || !time.Now().Before(d.ExpiresAt.Time) {
			return execution.ErrObsolete
		}
		secret, err = s.crypto.open("delivery:"+id, d.Ciphertext)
		if err != nil {
			return err
		}
		if d.Kind == "invitation" {
			recipient, lookupErr := q.FindPrincipal(ctx, accessdb.FindPrincipalParams{Realm: "customer", Email: d.TargetEmail})
			if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
				return lookupErr
			}
			if lookupErr == nil && (recipient.State == "suspended" || !recipient.Notifications) {
				return execution.ErrObsolete
			}
			i, err := q.FindInvitation(ctx, verifier(string(secret)))
			if err != nil {
				return obsolete(err)
			}
			if i.ID != d.ResourceRef || i.State != "pending" || !time.Now().Before(i.ExpiresAt.Time) {
				return execution.ErrObsolete
			}
			inviter, err := q.GetMember(ctx, accessdb.GetMemberParams{SpaceID: i.SpaceID, ID: i.Inviter})
			if err != nil {
				return obsolete(err)
			}
			p, err := q.ReadPrincipal(ctx, inviter.PrincipalID)
			if err != nil {
				return err
			}
			if p.State != "active" || !inviteRoleAllowed(inviter.Role, i.Role) {
				return execution.ErrObsolete
			}
			if err := s.capability(ctx, tx, i.SpaceID, "invite", 0); err != nil {
				if errors.Is(err, ErrCapacity) {
					return execution.ErrObsolete
				}
				return err
			}
			return nil
		}
		p, err := q.ReadPrincipal(ctx, d.PrincipalID.String)
		if err != nil {
			return obsolete(err)
		}
		if p.State == "suspended" {
			return execution.ErrObsolete
		}
		if d.Kind == "security" {
			return nil
		}
		t, err := q.FindToken(ctx, accessdb.FindTokenParams{Verifier: verifier(string(secret)), Purpose: d.Kind})
		if err != nil {
			return obsolete(err)
		}
		if t.ID != d.ResourceRef || t.ConsumedAt.Valid || t.InvalidatedAt.Valid || t.TargetEmail != d.TargetEmail || !time.Now().Before(t.ExpiresAt.Time) {
			return execution.ErrObsolete
		}
		if d.Kind != "email" && p.Email != d.TargetEmail {
			return execution.ErrObsolete
		}
		if !bytes.Equal(t.Verifier, verifier(string(secret))) {
			return execution.ErrObsolete
		}
		return nil
	})
	if err != nil {
		clear(secret)
		if errors.Is(err, execution.ErrObsolete) && d.ID != "" {
			cleanup, cancel := context.WithTimeout(ctx, s.store.DB.Timeout)
			defer cancel()
			if clearErr := accessdb.New(s.store.DB.Pool).ClearDelivery(cleanup, d.ID); clearErr != nil {
				return d, nil, errors.Join(err, clearErr)
			}
		}
		return d, nil, err
	}
	return d, secret, nil
}
func obsolete(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return execution.ErrObsolete
	}
	return err
}
