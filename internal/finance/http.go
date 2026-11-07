package finance

import (
	"context"
	"log/slog"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/httpapi"
)

type routes struct {
	api     huma.API
	service *Service
	log     *slog.Logger
}
type response[T any] struct{ Body T }
type financialReference struct {
	ID string `json:"id"`
}
type reference = financialReference
type AccountPathInput struct {
	access.AuthorizationInput
	access.SpacePathInput
	ID string `path:"account_id" format:"uuid"`
}
type AccountActionInput struct {
	AccountPathInput
	access.ActionKeyInput
}
type FinancialVersionConfirmation struct {
	Version int64 `json:"version" minimum:"1"`
	Confirm bool  `json:"confirm"`
}
type accountSharingBody struct {
	FinancialVersionConfirmation
	Sharing Sharing `json:"sharing"`
}
type accountResponsibilityBody struct {
	FinancialVersionConfirmation
	MemberID string `json:"member_id" format:"uuid"`
}

func (r *routes) register[I, O any](method, path, id, summary string, handler func(context.Context, *I) (O, error)) {
	huma.Register(r.api, huma.Operation{Method: method, Path: path, OperationID: id, Summary: summary, Tags: []string{"Finance"}, MaxBodyBytes: 32 << 10, Security: []map[string][]string{{"customerSession": {}}}, Responses: httpapi.ErrorResponses(r.api, 400, 401, 403, 404, 409, 413, 422, 429, 500, 503, 504)}, func(ctx context.Context, in *I) (*response[O], error) {
		v, err := handler(ctx, in)
		if err != nil {
			return nil, access.HTTPProblem(ctx, r.log, err)
		}
		return &response[O]{Body: v}, nil
	})
}
func ref(id string, err error) (reference, error) { return reference{ID: id}, err }
func command(in AccountActionInput) access.SpaceCommand {
	return access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}
}

func RegisterHTTP(api huma.API, s *Service, log *slog.Logger) {
	r := &routes{api: api, service: s, log: log}
	r.organization()
	r.operations()
	base := "/v1/spaces/{space_id}/accounts"
	r.register("GET", base+"/proposal", "propose-account-visibility", "Propose visibility without sharing existing accounts", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
	}) (AccountProposal, error) {
		return s.AccountProposal(ctx, in.Authorization, in.SpaceID)
	})
	r.register("POST", base, "create-financial-account", "Create financial position with opening reference", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		access.ActionKeyInput
		Body AccountCreate
	}) (reference, error) {
		return ref(s.CreateAccount(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.Body))
	})
	r.register("GET", base, "list-financial-accounts", "List authorized financial positions", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		access.PagingInput
	}) (access.Page[AccountView], error) {
		return s.Accounts(ctx, in.Authorization, in.SpaceID, in.Cursor, in.Limit)
	})
	r.register("GET", base+"/{account_id}", "get-financial-account", "Read authorized financial position", func(ctx context.Context, in *AccountPathInput) (AccountView, error) {
		return s.Account(ctx, in.Authorization, in.SpaceID, in.ID)
	})
	r.register("PUT", base+"/{account_id}", "edit-financial-account", "Configure or archive a financial account without rewriting its reference", func(ctx context.Context, in *struct {
		AccountActionInput
		Body AccountEdit
	}) (reference, error) {
		return ref(s.EditAccount(ctx, command(in.AccountActionInput), in.ID, in.Body))
	})
	r.register("GET", base+"/{account_id}/sharing", "get-account-sharing", "Read account access configuration as responsible member", func(ctx context.Context, in *AccountPathInput) (Sharing, error) {
		return s.AccountSharing(ctx, in.Authorization, in.SpaceID, in.ID)
	})
	r.register("PUT", base+"/{account_id}/sharing", "share-financial-account", "Replace account access configuration", func(ctx context.Context, in *struct {
		AccountActionInput
		Body accountSharingBody
	}) (reference, error) {
		if !in.Body.Confirm {
			return ref("", access.ErrInvalid)
		}
		return ref(s.ShareAccount(ctx, command(in.AccountActionInput), in.ID, in.Body.Sharing, in.Body.Version))
	})
	r.register("POST", base+"/{account_id}/responsibility", "transfer-account-responsibility", "Transfer responsibility to an authorized current member", func(ctx context.Context, in *struct {
		AccountActionInput
		Body accountResponsibilityBody
	}) (reference, error) {
		if !in.Body.Confirm {
			return ref("", access.ErrInvalid)
		}
		return ref(s.TransferAccount(ctx, command(in.AccountActionInput), in.ID, in.Body.MemberID, in.Body.Version))
	})
	r.register("GET", base+"/recoverable", "list-recoverable-accounts", "Discover own former responsibilities without financial content", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		access.PagingInput
	}) (access.Page[RecoveryReference], error) {
		return s.RecoverableAccounts(ctx, in.Authorization, in.SpaceID, in.Cursor, in.Limit)
	})
	r.register("POST", base+"/{account_id}/recovery", "recover-financial-account", "Explicitly recover own former account responsibility", func(ctx context.Context, in *struct {
		AccountActionInput
		Body FinancialVersionConfirmation
	}) (reference, error) {
		if !in.Body.Confirm {
			return ref("", access.ErrInvalid)
		}
		return ref(s.RecoverAccount(ctx, command(in.AccountActionInput), in.ID, in.Body.Version))
	})
}
