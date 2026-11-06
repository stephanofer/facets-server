package access

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/httpapi"
	"github.com/stephanofer/facets-server/internal/telemetry"
)

type response[T any] struct{ Body T }
type acknowledgement struct {
	Status string `json:"status"`
}
type reference struct {
	ID string `json:"id"`
}
type AuthorizationInput struct {
	Authorization string `header:"Authorization" maxLength:"200"`
}
type ActionKeyInput struct {
	Key string `header:"Idempotency-Key" required:"true" pattern:"^[A-Za-z0-9._:-]{1,128}$" minLength:"1" maxLength:"128"`
}
type SpacePathInput struct {
	SpaceID string `path:"space_id" format:"uuid"`
}
type PagingInput struct {
	Cursor string `query:"cursor" maxLength:"36"`
	Limit  int32  `query:"limit" default:"50" minimum:"1" maximum:"100"`
}
type emptyInput struct{ AuthorizationInput }
type emailBody struct {
	Email string `json:"email" minLength:"3" maxLength:"254"`
}
type tokenBody struct {
	Token string `json:"token" minLength:"43" maxLength:"43"`
}
type passwordBody struct {
	Password string `json:"password" minLength:"15" maxLength:"1024"`
}
type proofBody struct {
	Password string `json:"password" minLength:"1" maxLength:"1024"`
	Code     string `json:"code,omitempty" maxLength:"100"`
}
type factorBody struct {
	Code string `json:"code" minLength:"6" maxLength:"100"`
}
type confirmBody struct {
	Confirm bool `json:"confirm"`
}
type originKey struct{}

func origin(ctx context.Context) string { v, _ := ctx.Value(originKey{}).(string); return v }

type routes struct {
	api     huma.API
	service *Service
	log     *slog.Logger
}

func (r *routes) register[I, O any](method, path, id string, handler func(context.Context, *I) (O, error)) {
	operation := huma.Operation{OperationID: id, Method: method, Path: path, Summary: id, MaxBodyBytes: 16 << 10,
		Responses: httpapi.ErrorResponses(r.api, 400, 401, 403, 404, 409, 413, 422, 429, 500, 503, 504)}
	if path != "/v1/auth/register" && path != "/v1/auth/login" && path != "/v1/auth/verify" && path != "/v1/auth/verification" && path != "/v1/auth/recovery" && path != "/v1/auth/password-reset" && path != "/v1/internal/auth/login" {
		scheme := "customerSession"
		if len(path) > 12 && path[:12] == "/v1/internal" {
			scheme = "staffSession"
		}
		operation.Security = []map[string][]string{{scheme: {}}}
	}
	huma.Register(r.api, operation, func(ctx context.Context, in *I) (*response[O], error) {
		out, err := handler(ctx, in)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &response[O]{Body: out}, nil
	})
}
func (r *routes) problem(ctx context.Context, err error) error {
	var status int
	var code string
	switch {
	case errors.Is(err, ErrInvalid):
		status, code = 422, "invalid_request"
	case errors.Is(err, ErrUnauthorized):
		status, code = 401, "authentication_required"
	case errors.Is(err, ErrForbidden):
		status, code = 403, "access_denied"
	case errors.Is(err, ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, ErrLimited):
		status, code = 429, "attempt_limit"
	case errors.Is(err, ErrCapacity):
		status, code = 403, "space_capability_unavailable"
	case errors.Is(err, ErrReauthenticate):
		status, code = 403, "reauthentication_required"
	case errors.Is(err, ErrResponsibility):
		status, code = 409, "responsibility_unresolved"
	default:
		if p, ok := errors.AsType[*pgconn.PgError](err); ok && p.Code == "23505" {
			status, code = 409, "resource_conflict"
		} else {
			p := httpapi.Translate(ctx, err)
			status, code = p.Status, p.Code
		}
	}
	if status >= 500 {
		telemetry.LogError(ctx, r.log, "access request failed", err)
	}
	return &httpapi.Problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Code: code, RequestID: httpapi.RequestID(ctx)}
}
func ack(err error) (acknowledgement, error)      { return acknowledgement{Status: "acknowledged"}, err }
func ref(id string, err error) (reference, error) { return reference{ID: id}, err }

// RegisterHTTP is called during composition for both the live API and OpenAPI
// generation. It installs no alternative anonymous or permissive implementation.
func RegisterHTTP(api huma.API, s *Service, log *slog.Logger) {
	r := &routes{api: api, service: s, log: log}
	api.OpenAPI().Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"customerSession": {Type: "http", Scheme: "bearer", Description: "Opaque customer session. Never an internal credential."},
		"staffSession":    {Type: "http", Scheme: "bearer", Description: "Separate MFA-authenticated internal session."},
	}
	api.UseMiddleware(func(c huma.Context, next func(huma.Context)) {
		host, _, err := net.SplitHostPort(c.RemoteAddr())
		if err != nil {
			host = c.RemoteAddr()
		}
		if host == "" {
			host = "unknown"
		}
		ctx := context.WithValue(c.Context(), originKey{}, host)
		ctx = context.WithValue(ctx, auditContextKey{}, httpapi.RequestID(ctx))
		next(huma.WithContext(c, ctx))
	})
	r.register("GET", "/v1/spaces/{space_id}/departure", "preview-departure", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
	}) (DeparturePreview, error) {
		return s.Departure(ctx, in.Authorization, in.SpaceID)
	})
	r.register("POST", "/v1/invitations/preview", "preview-invitation", func(ctx context.Context, in *struct {
		AuthorizationInput
		Body tokenBody
	}) (InvitationPreview, error) {
		return s.PreviewInvitation(ctx, in.Authorization, in.Body.Token)
	})
	r.register("POST", "/v1/spaces/{space_id}/members/{member_id}/role", "change-member-role", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		ActionKeyInput
		ID   string `path:"member_id" format:"uuid"`
		Body struct {
			Role    string `json:"role" enum:"admin,member,reader"`
			Confirm bool   `json:"confirm"`
		}
	}) (reference, error) {
		if !in.Body.Confirm {
			return ref("", ErrInvalid)
		}
		return ref(s.ChangeRole(ctx, SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.Body.Role))
	})
	r.register("POST", "/v1/auth/register", "register", func(ctx context.Context, in *struct{ Body Registration }) (acknowledgement, error) {
		return ack(s.Register(ctx, in.Body, origin(ctx)))
	})
	r.register("POST", "/v1/auth/verification", "request-verification", func(ctx context.Context, in *struct{ Body emailBody }) (acknowledgement, error) {
		return ack(s.RequestToken(ctx, in.Body.Email, "verify", origin(ctx)))
	})
	r.register("POST", "/v1/auth/verify", "verify-email", func(ctx context.Context, in *struct{ Body tokenBody }) (acknowledgement, error) {
		return ack(s.Verify(ctx, in.Body.Token, origin(ctx)))
	})
	r.register("POST", "/v1/auth/recovery", "request-recovery", func(ctx context.Context, in *struct{ Body emailBody }) (acknowledgement, error) {
		return ack(s.RequestToken(ctx, in.Body.Email, "recover", origin(ctx)))
	})
	r.register("POST", "/v1/auth/password-reset", "reset-password", func(ctx context.Context, in *struct {
		Body struct {
			Token    string `json:"token" minLength:"43" maxLength:"43"`
			Password string `json:"password" minLength:"15" maxLength:"1024"`
			Code     string `json:"code,omitempty" maxLength:"100"`
		}
	}) (acknowledgement, error) {
		return ack(s.ResetPassword(ctx, ResetPassword{Token: in.Body.Token, Password: in.Body.Password, Code: in.Body.Code}, origin(ctx)))
	})
	for _, realm := range []string{"customer", "staff"} {
		base := "/v1"
		if realm == "staff" {
			base = "/v1/internal"
		}
		r.register("POST", base+"/auth/login", realm+"-login", func(ctx context.Context, in *struct{ Body Credentials }) (SessionResult, error) {
			return s.Login(ctx, in.Body, realm, origin(ctx))
		})
		r.register("POST", base+"/auth/reauthenticate", realm+"-reauthenticate", func(ctx context.Context, in *struct {
			AuthorizationInput
			Body proofBody
		}) (SessionResult, error) {
			return s.Reauthenticate(ctx, in.Authorization, in.Body.Password, in.Body.Code, realm, origin(ctx))
		})
		r.register("POST", base+"/auth/logout", realm+"-logout", func(ctx context.Context, in *emptyInput) (acknowledgement, error) {
			return ack(s.RevokeSession(ctx, in.Authorization, realm, "", false))
		})
		r.register("GET", base+"/me", realm+"-profile", func(ctx context.Context, in *emptyInput) (Profile, error) {
			return s.Profile(ctx, in.Authorization, realm)
		})
		r.register("GET", base+"/me/sessions", realm+"-sessions", func(ctx context.Context, in *emptyInput) ([]SessionView, error) {
			return s.Sessions(ctx, in.Authorization, realm)
		})
		r.register("POST", base+"/me/sessions/revoke", realm+"-revoke-session", func(ctx context.Context, in *struct {
			AuthorizationInput
			Body struct {
				ID  string `json:"id" maxLength:"36"`
				All bool   `json:"all"`
			}
		}) (acknowledgement, error) {
			return ack(s.RevokeSession(ctx, in.Authorization, realm, in.Body.ID, in.Body.All))
		})
		r.register("POST", base+"/me/password", realm+"-change-password", func(ctx context.Context, in *struct {
			AuthorizationInput
			Body passwordBody
		}) (acknowledgement, error) {
			return ack(s.ChangePassword(ctx, in.Authorization, in.Body.Password, realm))
		})
		r.register("POST", base+"/me/mfa/setup", realm+"-setup-mfa", func(ctx context.Context, in *emptyInput) (struct {
			URI string `json:"uri"`
		}, error) {
			uri, err := s.BeginMFA(ctx, in.Authorization, realm)
			return struct {
				URI string `json:"uri"`
			}{URI: uri}, err
		})
		r.register("POST", base+"/me/mfa/confirm", realm+"-confirm-mfa", func(ctx context.Context, in *struct {
			AuthorizationInput
			Body factorBody
		}) (struct {
			Codes []string `json:"recovery_codes"`
		}, error) {
			codes, err := s.ConfirmMFA(ctx, in.Authorization, in.Body.Code, realm, origin(ctx))
			return struct {
				Codes []string `json:"recovery_codes"`
			}{Codes: codes}, err
		})
		r.register("POST", base+"/me/mfa/recovery-codes", realm+"-regenerate-recovery", func(ctx context.Context, in *struct {
			AuthorizationInput
			Body confirmBody
		}) (struct {
			Codes []string `json:"recovery_codes"`
		}, error) {
			if !in.Body.Confirm {
				return struct {
					Codes []string `json:"recovery_codes"`
				}{}, ErrInvalid
			}
			codes, err := s.RegenerateRecoveryCodes(ctx, in.Authorization, realm)
			return struct {
				Codes []string `json:"recovery_codes"`
			}{Codes: codes}, err
		})
	}
	r.register("POST", "/v1/me/mfa/disable", "disable-mfa", func(ctx context.Context, in *struct {
		AuthorizationInput
		Body confirmBody
	}) (acknowledgement, error) {
		if !in.Body.Confirm {
			return ack(ErrInvalid)
		}
		return ack(s.DisableMFA(ctx, in.Authorization))
	})
	r.register("PUT", "/v1/me/preferences", "preferences", func(ctx context.Context, in *struct {
		AuthorizationInput
		Body Preferences
	}) (acknowledgement, error) {
		return ack(s.Preferences(ctx, in.Authorization, in.Body))
	})
	r.register("POST", "/v1/me/email", "change-email-request", func(ctx context.Context, in *struct {
		AuthorizationInput
		Body emailBody
	}) (acknowledgement, error) {
		return ack(s.ChangeEmailRequest(ctx, in.Authorization, in.Body.Email))
	})
	r.register("POST", "/v1/me/email/confirm", "confirm-email-change", func(ctx context.Context, in *struct {
		AuthorizationInput
		Body tokenBody
	}) (acknowledgement, error) {
		return ack(s.ConfirmEmail(ctx, in.Authorization, in.Body.Token, origin(ctx)))
	})
	r.register("GET", "/v1/spaces", "list-spaces", func(ctx context.Context, in *struct {
		AuthorizationInput
		PagingInput
	}) (Page[SpaceView], error) {
		return s.Spaces(ctx, in.Authorization, in.Cursor, in.Limit)
	})
	r.register("POST", "/v1/spaces", "create-space", func(ctx context.Context, in *struct {
		AuthorizationInput
		ActionKeyInput
		Body SpaceSettings
	}) (reference, error) {
		return ref(s.CreateSpace(ctx, in.Authorization, in.Key, in.Body))
	})
	r.register("GET", "/v1/spaces/{space_id}", "get-space", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
	}) (SpaceView, error) {
		return s.Space(ctx, in.Authorization, in.SpaceID)
	})
	r.register("PUT", "/v1/spaces/{space_id}", "edit-space", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		ActionKeyInput
		Body struct {
			Settings SpaceSettings `json:"settings"`
			Version  int64         `json:"version" minimum:"1"`
		}
	}) (reference, error) {
		return ref(s.EditSpace(ctx, SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.Body.Settings, in.Body.Version))
	})
	r.register("GET", "/v1/spaces/{space_id}/members", "list-members", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		PagingInput
	}) (Page[MemberView], error) {
		return s.members(ctx, in.Authorization, in.SpaceID, in.Cursor, in.Limit)
	})
	r.register("GET", "/v1/spaces/{space_id}/access", "space-capabilities", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
	}) (AccessView, error) {
		return s.accessView(ctx, in.Authorization, in.SpaceID)
	})
	r.register("GET", "/v1/spaces/{space_id}/invitations", "list-invitations", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		PagingInput
	}) (Page[InvitationView], error) {
		return s.invitations(ctx, in.Authorization, in.SpaceID, in.Cursor, in.Limit)
	})
	r.register("POST", "/v1/spaces/{space_id}/invitations", "invite-member", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		ActionKeyInput
		Body Invite
	}) (reference, error) {
		return ref(s.Invite(ctx, SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.Body))
	})
	r.register("POST", "/v1/invitations/decision", "decide-invitation", func(ctx context.Context, in *struct {
		AuthorizationInput
		ActionKeyInput
		Body InvitationDecision
	}) (reference, error) {
		return ref(s.DecideInvitation(ctx, in.Authorization, in.Key, in.Body))
	})
	r.register("POST", "/v1/spaces/{space_id}/invitations/{invitation_id}/resend", "resend-invitation", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		ActionKeyInput
		ID string `path:"invitation_id" format:"uuid"`
	}) (reference, error) {
		return ref(s.ResendInvitation(ctx, SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID))
	})
	r.register("POST", "/v1/spaces/{space_id}/invitations/{invitation_id}/cancel", "cancel-invitation", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		ActionKeyInput
		ID string `path:"invitation_id" format:"uuid"`
	}) (reference, error) {
		return ref(s.CancelInvitation(ctx, SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID))
	})
	r.register("POST", "/v1/spaces/{space_id}/members/{member_id}/remove", "remove-member", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		ActionKeyInput
		ID   string `path:"member_id" format:"uuid"`
		Body confirmBody
	}) (reference, error) {
		if !in.Body.Confirm {
			return ref("", ErrInvalid)
		}
		return ref(s.RemoveMember(ctx, SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID))
	})
	r.register("POST", "/v1/spaces/{space_id}/ownership", "transfer-ownership", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		ActionKeyInput
		Body struct {
			Member  string `json:"member_id" format:"uuid"`
			Confirm bool   `json:"confirm"`
		}
	}) (reference, error) {
		if !in.Body.Confirm {
			return ref("", ErrInvalid)
		}
		return ref(s.TransferOwnership(ctx, SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.Body.Member))
	})
	r.register("POST", "/v1/spaces/{space_id}/billing/claim", "claim-billing-responsibility", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		ActionKeyInput
		Body confirmBody
	}) (reference, error) {
		if !in.Body.Confirm {
			return ref("", ErrInvalid)
		}
		return ref(s.ClaimBilling(ctx, SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}))
	})
}

func (s *Service) members(ctx context.Context, authorization, id, cursor string, limit int32) (Page[MemberView], error) {
	result := Page[MemberView]{Items: []MemberView{}}
	if err := validateCursor(cursor, limit); err != nil {
		return result, err
	}
	err := s.withActor(ctx, authorization, "customer", false, func(ctx context.Context, tx pgx.Tx, a Actor) error {
		if _, err := s.AuthorizeSpace(ctx, tx, a, id, "read"); err != nil {
			return err
		}
		rows, err := accessdb.New(tx).ListMembers(ctx, accessdb.ListMembersParams{SpaceID: id, ID: cursor, Limit: limit + 1})
		if err != nil {
			return err
		}
		for i, v := range rows {
			if i == int(limit) {
				result.NextCursor = result.Items[i-1].ID
				break
			}
			result.Items = append(result.Items, MemberView{ID: v.ID, Role: v.Role, Name: v.DisplayName})
		}
		return nil
	})
	return result, err
}
func (s *Service) invitations(ctx context.Context, authorization, id, cursor string, limit int32) (Page[InvitationView], error) {
	result := Page[InvitationView]{Items: []InvitationView{}}
	if err := validateCursor(cursor, limit); err != nil {
		return result, err
	}
	err := s.withActor(ctx, authorization, "customer", false, func(ctx context.Context, tx pgx.Tx, a Actor) error {
		if _, err := s.AuthorizeSpace(ctx, tx, a, id, "invite"); err != nil {
			return err
		}
		rows, err := accessdb.New(tx).ListInvitations(ctx, accessdb.ListInvitationsParams{SpaceID: id, ID: cursor, Limit: limit + 1})
		if err != nil {
			return err
		}
		for i, v := range rows {
			if i == int(limit) {
				result.NextCursor = result.Items[i-1].ID
				break
			}
			result.Items = append(result.Items, InvitationView{ID: v.ID, Email: v.Email, Role: v.Role, State: v.State, ExpiresAt: v.ExpiresAt.Time, Generation: v.Generation})
		}
		return nil
	})
	return result, err
}
func (s *Service) accessView(ctx context.Context, authorization, id string) (AccessView, error) {
	result := AccessView{Capabilities: []CapabilityView{}}
	err := s.withActor(ctx, authorization, "customer", false, func(ctx context.Context, tx pgx.Tx, a Actor) error {
		if _, err := s.AuthorizeSpace(ctx, tx, a, id, "read"); err != nil {
			return err
		}
		q := accessdb.New(tx)
		v, err := q.GetSpaceAccess(ctx, id)
		if err != nil {
			return err
		}
		result.Source, result.Profile, result.State = v.Source, v.Profile, v.State
		result.ValidFrom, result.ValidUntil, result.BillingMember, result.Version = v.ValidFrom.Time, v.ValidUntil.Time, v.BillingMember, v.Version
		caps, err := q.ListCapabilities(ctx, id)
		if err != nil {
			return err
		}
		for _, c := range caps {
			result.Capabilities = append(result.Capabilities, CapabilityView{Key: c.Capability, Ceiling: strconv.FormatInt(c.Ceiling, 10), Used: strconv.FormatInt(c.Used, 10)})
		}
		return nil
	})
	return result, err
}
