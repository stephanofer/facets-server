package finance

import (
	"context"
	"github.com/stephanofer/facets-server/internal/access"
)

type PlanningResourcePath struct {
	access.AuthorizationInput
	access.SpacePathInput
	ID string `path:"resource_id" format:"uuid"`
}

func (r *routes) planningAccess() {
	base := "/v1/spaces/{space_id}/planning-resources"
	r.register("GET", base+"/recoverable", "list-recoverable-planning-resources", "List only the current identity's conserved accountless responsibility references", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		access.PagingInput
	}) (access.Page[RecoveryReference], error) {
		return r.service.RecoverablePlanning(ctx, in.Authorization, in.SpaceID, in.Cursor, in.Limit)
	})
	r.register("GET", base+"/{resource_id}/access", "get-planning-access", "Read accountless consultation and responsibility configuration as its responsible member", func(ctx context.Context, in *PlanningResourcePath) (PlanningAccessView, error) {
		return r.service.PlanningAccess(ctx, in.Authorization, in.SpaceID, in.ID)
	})
	r.register("PUT", base+"/{resource_id}/access", "edit-planning-access", "Explicitly share accountless planning or transfer responsibility without granting account access", func(ctx context.Context, in *struct {
		PlanningResourcePath
		access.ActionKeyInput
		Body PlanningAccessEdit
	}) (reference, error) {
		return ref(r.service.EditPlanningAccess(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.Body))
	})
	r.register("POST", base+"/{resource_id}/recover", "recover-planning-resource", "Recover the same identity's ended responsibility explicitly; do not restore other membership grants", func(ctx context.Context, in *struct {
		PlanningResourcePath
		access.ActionKeyInput
		Body FinancialVersionConfirmation
	}) (reference, error) {
		return ref(r.service.RecoverPlanningResource(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.Body))
	})
}
