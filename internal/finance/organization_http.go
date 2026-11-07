package finance

import (
	"context"

	"github.com/stephanofer/facets-server/internal/access"
)

type CatalogPathInput struct {
	access.AuthorizationInput
	access.SpacePathInput
	ID string `path:"entry_id" format:"uuid"`
}
type CatalogActionInput struct {
	CatalogPathInput
	access.ActionKeyInput
}
type catalogConfirmation struct {
	Confirm bool `json:"confirm"`
}
type catalogCreateBody struct {
	Kind  string       `json:"kind" enum:"category,merchant,counterparty,tag"`
	Value CatalogValue `json:"value"`
}
type catalogEditBody struct {
	Value    CatalogValue `json:"value"`
	Archived bool         `json:"archived"`
	Version  int64        `json:"version" minimum:"1"`
}

func (r *routes) organization() {
	s := r.service
	base := "/v1/spaces/{space_id}/catalog"
	r.register("POST", base+"/defaults", "initialize-financial-catalog", "Initialize missing default categories without replacing edits", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		access.ActionKeyInput
		Body catalogConfirmation
	}) (reference, error) {
		if !in.Body.Confirm {
			return ref("", access.ErrInvalid)
		}
		return ref(s.InitializeCatalog(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}))
	})
	r.register("POST", base, "create-catalog-entry", "Create a category, merchant, counterparty or tag", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		access.ActionKeyInput
		Body catalogCreateBody
	}) (reference, error) {
		return ref(s.CreateCatalog(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.Body.Kind, in.Body.Value))
	})
	r.register("GET", base, "list-catalog-entries", "List space catalog including preserved archived identities", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		access.PagingInput
		Kind string `query:"kind" required:"true" enum:"category,merchant,counterparty,tag"`
	}) (access.Page[CatalogView], error) {
		return s.Catalog(ctx, in.Authorization, in.SpaceID, in.Kind, in.Cursor, in.Limit)
	})
	r.register("GET", base+"/{entry_id}", "get-catalog-entry", "Read original catalog identity and consolidation target", func(ctx context.Context, in *CatalogPathInput) (CatalogView, error) {
		return s.CatalogEntry(ctx, in.Authorization, in.SpaceID, in.ID)
	})
	r.register("PUT", base+"/{entry_id}", "edit-catalog-entry", "Edit or archive a catalog entry, preserving references", func(ctx context.Context, in *struct {
		CatalogActionInput
		Body catalogEditBody
	}) (reference, error) {
		return ref(s.EditCatalog(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.Body.Value, in.Body.Archived, in.Body.Version))
	})
	r.register("POST", base+"/{entry_id}/merge", "merge-catalog-entries", "Consolidate identities and reparent subcategories without deleting history", func(ctx context.Context, in *struct {
		CatalogActionInput
		Body CatalogMerge
	}) (reference, error) {
		return ref(s.MergeCatalog(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.Body))
	})
	exchange := "/v1/spaces/{space_id}/exchange-references"
	r.register("POST", exchange, "create-exchange-reference", "Record an immutable manual exchange reference or correction", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		access.ActionKeyInput
		Body ExchangeCreate
	}) (reference, error) {
		return ref(s.CreateExchange(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.Body))
	})
	r.register("GET", exchange, "list-exchange-references", "List historical or current valuation references", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		access.PagingInput
		From    string `query:"from_currency" maxLength:"3"`
		To      string `query:"to_currency" maxLength:"3"`
		Context string `query:"context" enum:"historical,current"`
	}) (access.Page[ExchangeView], error) {
		return s.Exchanges(ctx, in.Authorization, in.SpaceID, in.Cursor, in.From, in.To, in.Context, in.Limit)
	})
	r.register("GET", exchange+"/{entry_id}", "get-exchange-reference", "Read unchanged exchange provenance", func(ctx context.Context, in *CatalogPathInput) (ExchangeView, error) {
		return s.Exchange(ctx, in.Authorization, in.SpaceID, in.ID)
	})
	r.register("POST", "/v1/spaces/{space_id}/exchange-conversions", "convert-informative-amount", "Convert an informative amount without changing any position", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		Body ExchangeConversion
	}) (ConversionView, error) {
		return s.Convert(ctx, in.Authorization, in.SpaceID, in.Body)
	})
}
