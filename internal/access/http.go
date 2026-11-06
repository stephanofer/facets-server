package access

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

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
	// Security schemes document authentication; keep header parsing and validation at runtime.
	Authorization string `header:"Authorization" maxLength:"200" hidden:"true"`
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
type changeMemberRoleBody struct {
	Role    string `json:"role" enum:"admin,member,reader"`
	Confirm bool   `json:"confirm"`
}
type resetPasswordBody struct {
	Token    string `json:"token" minLength:"43" maxLength:"43"`
	Password string `json:"password" minLength:"15" maxLength:"1024"`
	Code     string `json:"code,omitempty" maxLength:"100"`
}
type revokeSessionBody struct {
	ID  string `json:"id" maxLength:"36"`
	All bool   `json:"all"`
}
type editSpaceBody struct {
	Settings SpaceSettings `json:"settings"`
	Version  int64         `json:"version" minimum:"1"`
}
type transferOwnershipBody struct {
	Member  string `json:"member_id" format:"uuid"`
	Confirm bool   `json:"confirm"`
}
type mfaSetup struct {
	URI string `json:"uri"`
}
type recoveryCodes struct {
	Codes []string `json:"recovery_codes"`
}
type originKey struct{}

func origin(ctx context.Context) string { v, _ := ctx.Value(originKey{}).(string); return v }

type routes struct {
	api     huma.API
	service *Service
	log     *slog.Logger
}

func (r *routes) register[I, O any](method, path, id, summary string, handler func(context.Context, *I) (O, error)) {
	operation := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{operationTag(path)}, MaxBodyBytes: 16 << 10,
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

func operationTag(path string) string {
	switch {
	case strings.HasPrefix(path, "/v1/internal/"):
		return "Internal"
	case strings.HasPrefix(path, "/v1/auth/"):
		return "Auth"
	case path == "/v1/me" || strings.HasPrefix(path, "/v1/me/"):
		return "Account"
	case strings.HasPrefix(path, "/v1/invitations/") || strings.HasPrefix(path, "/v1/spaces/{space_id}/invitations"):
		return "Invitations"
	case path == "/v1/spaces" || strings.HasPrefix(path, "/v1/spaces/"):
		return "Spaces"
	default:
		return ""
	}
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
	r.register("GET", "/v1/spaces/{space_id}/departure", "preview-departure", "Preview space departure", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
	}) (DeparturePreview, error) {
		return s.Departure(ctx, in.Authorization, in.SpaceID)
	})
	r.register("POST", "/v1/invitations/preview", "preview-invitation", "Preview invitation", func(ctx context.Context, in *struct {
		AuthorizationInput
		Body tokenBody
	}) (InvitationPreview, error) {
		return s.PreviewInvitation(ctx, in.Authorization, in.Body.Token)
	})
	r.register("POST", "/v1/spaces/{space_id}/members/{member_id}/role", "change-member-role", "Change space member role", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		ActionKeyInput
		ID   string `path:"member_id" format:"uuid"`
		Body changeMemberRoleBody
	}) (reference, error) {
		if !in.Body.Confirm {
			return ref("", ErrInvalid)
		}
		return ref(s.ChangeRole(ctx, SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.Body.Role))
	})
	r.register("POST", "/v1/auth/register", "register", "Register customer", func(ctx context.Context, in *struct{ Body Registration }) (acknowledgement, error) {
		return ack(s.Register(ctx, in.Body, origin(ctx)))
	})
	r.register("POST", "/v1/auth/verification", "request-verification", "Request email verification", func(ctx context.Context, in *struct{ Body emailBody }) (acknowledgement, error) {
		return ack(s.RequestToken(ctx, in.Body.Email, "verify", origin(ctx)))
	})
	r.register("POST", "/v1/auth/verify", "verify-email", "Verify customer email", func(ctx context.Context, in *struct{ Body tokenBody }) (acknowledgement, error) {
		return ack(s.Verify(ctx, in.Body.Token, origin(ctx)))
	})
	r.register("POST", "/v1/auth/recovery", "request-recovery", "Request password recovery", func(ctx context.Context, in *struct{ Body emailBody }) (acknowledgement, error) {
		return ack(s.RequestToken(ctx, in.Body.Email, "recover", origin(ctx)))
	})
	r.register("POST", "/v1/auth/password-reset", "reset-password", "Reset customer password", func(ctx context.Context, in *struct {
		Body resetPasswordBody
	}) (acknowledgement, error) {
		return ack(s.ResetPassword(ctx, ResetPassword{Token: in.Body.Token, Password: in.Body.Password, Code: in.Body.Code}, origin(ctx)))
	})
	for _, realm := range []string{"customer", "staff"} {
		base := "/v1"
		if realm == "staff" {
			base = "/v1/internal"
		}
		r.register("POST", base+"/auth/login", realm+"-login", "Sign in "+realm, func(ctx context.Context, in *struct{ Body Credentials }) (SessionResult, error) {
			return s.Login(ctx, in.Body, realm, origin(ctx))
		})
		r.register("POST", base+"/auth/reauthenticate", realm+"-reauthenticate", "Reauthenticate "+realm, func(ctx context.Context, in *struct {
			AuthorizationInput
			Body proofBody
		}) (SessionResult, error) {
			return s.Reauthenticate(ctx, in.Authorization, in.Body.Password, in.Body.Code, realm, origin(ctx))
		})
		r.register("POST", base+"/auth/logout", realm+"-logout", "Sign out "+realm, func(ctx context.Context, in *emptyInput) (acknowledgement, error) {
			return ack(s.RevokeSession(ctx, in.Authorization, realm, "", false))
		})
		r.register("GET", base+"/me", realm+"-profile", "Get "+realm+" profile", func(ctx context.Context, in *emptyInput) (Profile, error) {
			return s.Profile(ctx, in.Authorization, realm)
		})
		r.register("GET", base+"/me/sessions", realm+"-sessions", "List "+realm+" sessions", func(ctx context.Context, in *emptyInput) ([]SessionView, error) {
			return s.Sessions(ctx, in.Authorization, realm)
		})
		r.register("POST", base+"/me/sessions/revoke", realm+"-revoke-session", "Revoke "+realm+" sessions", func(ctx context.Context, in *struct {
			AuthorizationInput
			Body revokeSessionBody
		}) (acknowledgement, error) {
			return ack(s.RevokeSession(ctx, in.Authorization, realm, in.Body.ID, in.Body.All))
		})
		r.register("POST", base+"/me/password", realm+"-change-password", "Change "+realm+" password", func(ctx context.Context, in *struct {
			AuthorizationInput
			Body passwordBody
		}) (acknowledgement, error) {
			return ack(s.ChangePassword(ctx, in.Authorization, in.Body.Password, realm))
		})
		r.register("POST", base+"/me/mfa/setup", realm+"-setup-mfa", "Set up "+realm+" MFA", func(ctx context.Context, in *emptyInput) (mfaSetup, error) {
			uri, err := s.BeginMFA(ctx, in.Authorization, realm)
			return mfaSetup{URI: uri}, err
		})
		r.register("POST", base+"/me/mfa/confirm", realm+"-confirm-mfa", "Confirm "+realm+" MFA setup", func(ctx context.Context, in *struct {
			AuthorizationInput
			Body factorBody
		}) (recoveryCodes, error) {
			codes, err := s.ConfirmMFA(ctx, in.Authorization, in.Body.Code, realm, origin(ctx))
			return recoveryCodes{Codes: codes}, err
		})
		r.register("POST", base+"/me/mfa/recovery-codes", realm+"-regenerate-recovery", "Regenerate "+realm+" MFA recovery codes", func(ctx context.Context, in *struct {
			AuthorizationInput
			Body confirmBody
		}) (recoveryCodes, error) {
			if !in.Body.Confirm {
				return recoveryCodes{}, ErrInvalid
			}
			codes, err := s.RegenerateRecoveryCodes(ctx, in.Authorization, realm)
			return recoveryCodes{Codes: codes}, err
		})
	}
	r.register("POST", "/v1/me/mfa/disable", "disable-mfa", "Disable customer MFA", func(ctx context.Context, in *struct {
		AuthorizationInput
		Body confirmBody
	}) (acknowledgement, error) {
		if !in.Body.Confirm {
			return ack(ErrInvalid)
		}
		return ack(s.DisableMFA(ctx, in.Authorization))
	})
	r.register("PUT", "/v1/me/preferences", "preferences", "Update account preferences", func(ctx context.Context, in *struct {
		AuthorizationInput
		Body Preferences
	}) (acknowledgement, error) {
		return ack(s.Preferences(ctx, in.Authorization, in.Body))
	})
	r.register("POST", "/v1/me/email", "change-email-request", "Request account email change", func(ctx context.Context, in *struct {
		AuthorizationInput
		Body emailBody
	}) (acknowledgement, error) {
		return ack(s.ChangeEmailRequest(ctx, in.Authorization, in.Body.Email))
	})
	r.register("POST", "/v1/me/email/confirm", "confirm-email-change", "Confirm account email change", func(ctx context.Context, in *struct {
		AuthorizationInput
		Body tokenBody
	}) (acknowledgement, error) {
		return ack(s.ConfirmEmail(ctx, in.Authorization, in.Body.Token, origin(ctx)))
	})
	r.register("GET", "/v1/spaces", "list-spaces", "List accessible spaces", func(ctx context.Context, in *struct {
		AuthorizationInput
		PagingInput
	}) (Page[SpaceView], error) {
		return s.Spaces(ctx, in.Authorization, in.Cursor, in.Limit)
	})
	r.register("POST", "/v1/spaces", "create-space", "Create space", func(ctx context.Context, in *struct {
		AuthorizationInput
		ActionKeyInput
		Body SpaceSettings
	}) (reference, error) {
		return ref(s.CreateSpace(ctx, in.Authorization, in.Key, in.Body))
	})
	r.register("GET", "/v1/spaces/{space_id}", "get-space", "Get space", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
	}) (SpaceView, error) {
		return s.Space(ctx, in.Authorization, in.SpaceID)
	})
	r.register("PUT", "/v1/spaces/{space_id}", "edit-space", "Update space settings", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		ActionKeyInput
		Body editSpaceBody
	}) (reference, error) {
		return ref(s.EditSpace(ctx, SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.Body.Settings, in.Body.Version))
	})
	r.register("GET", "/v1/spaces/{space_id}/members", "list-members", "List space members", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		PagingInput
	}) (Page[MemberView], error) {
		return s.members(ctx, in.Authorization, in.SpaceID, in.Cursor, in.Limit)
	})
	r.register("GET", "/v1/spaces/{space_id}/access", "space-capabilities", "Get space capabilities and commercial status", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
	}) (AccessView, error) {
		return s.accessView(ctx, in.Authorization, in.SpaceID)
	})
	r.register("GET", "/v1/spaces/{space_id}/invitations", "list-invitations", "List space invitations", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		PagingInput
	}) (Page[InvitationView], error) {
		return s.invitations(ctx, in.Authorization, in.SpaceID, in.Cursor, in.Limit)
	})
	r.register("POST", "/v1/spaces/{space_id}/invitations", "invite-member", "Invite space member", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		ActionKeyInput
		Body Invite
	}) (reference, error) {
		return ref(s.Invite(ctx, SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.Body))
	})
	r.register("POST", "/v1/invitations/decision", "decide-invitation", "Accept or decline invitation", func(ctx context.Context, in *struct {
		AuthorizationInput
		ActionKeyInput
		Body InvitationDecision
	}) (reference, error) {
		return ref(s.DecideInvitation(ctx, in.Authorization, in.Key, in.Body))
	})
	r.register("POST", "/v1/spaces/{space_id}/invitations/{invitation_id}/resend", "resend-invitation", "Resend space invitation", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		ActionKeyInput
		ID string `path:"invitation_id" format:"uuid"`
	}) (reference, error) {
		return ref(s.ResendInvitation(ctx, SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID))
	})
	r.register("POST", "/v1/spaces/{space_id}/invitations/{invitation_id}/cancel", "cancel-invitation", "Cancel space invitation", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		ActionKeyInput
		ID string `path:"invitation_id" format:"uuid"`
	}) (reference, error) {
		return ref(s.CancelInvitation(ctx, SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID))
	})
	r.register("POST", "/v1/spaces/{space_id}/members/{member_id}/remove", "remove-member", "Remove space member", func(ctx context.Context, in *struct {
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
	r.register("POST", "/v1/spaces/{space_id}/ownership", "transfer-ownership", "Transfer space ownership", func(ctx context.Context, in *struct {
		AuthorizationInput
		SpacePathInput
		ActionKeyInput
		Body transferOwnershipBody
	}) (reference, error) {
		if !in.Body.Confirm {
			return ref("", ErrInvalid)
		}
		return ref(s.TransferOwnership(ctx, SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.Body.Member))
	})
	r.register("POST", "/v1/spaces/{space_id}/billing/claim", "claim-billing-responsibility", "Claim space billing responsibility", func(ctx context.Context, in *struct {
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
