package finance

import (
	"context"

	"github.com/stephanofer/facets-server/internal/access"
)

type DuePathInput struct {
	access.AuthorizationInput
	access.SpacePathInput
	ID string `path:"due_id" format:"uuid"`
}

func (r *routes) dues() {
	base := "/v1/spaces/{space_id}"
	r.register("GET", base+"/operations/{operation_id}/effects/{effect}/application-capacity", "get-movement-application-capacity", "Read current movement capacity without reserving it; restricted allocations require current access", func(ctx context.Context, in *struct {
		OperationPathInput
		Effect int32 `path:"effect" minimum:"0" maximum:"3"`
	}) (MovementCapacityView, error) {
		return r.service.MovementCapacity(ctx, in.Authorization, in.SpaceID, in.ID, in.Effect)
	})
	r.register("POST", base+"/payment-operations", "create-payment-operation", "Create a real movement and apply it across expectations in one atomic command", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		access.ActionKeyInput
		Body PaymentOperationCreate
	}) (reference, error) {
		return ref(r.service.CreatePaymentOperation(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.Body))
	})
	r.register("POST", base+"/dues", "create-due-item", "Create an expectation, private by default without an account, without changing any financial position", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		access.ActionKeyInput
		Body DueCreate
	}) (reference, error) {
		return ref(r.service.CreateDue(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.Body))
	})
	r.register("GET", base+"/dues", "list-due-items", "List expectations authorized before pagination; until is exclusive", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		From   string `query:"from" format:"date"`
		Until  string `query:"until" format:"date"`
		Cursor string `query:"cursor" maxLength:"47"`
		Limit  int32  `query:"limit" default:"50" minimum:"1" maximum:"100"`
		DueFilters
	}) (access.Page[DueView], error) {
		return r.service.FilteredDues(ctx, in.Authorization, in.SpaceID, in.From, in.Until, in.Cursor, in.Limit, in.DueFilters)
	})
	r.register("GET", base+"/dues/{due_id}", "get-due-item", "Read exact fulfillment and explicitly uncertain remaining amounts", func(ctx context.Context, in *DuePathInput) (DueView, error) {
		return r.service.Due(ctx, in.Authorization, in.SpaceID, in.ID)
	})
	r.register("PUT", base+"/dues/{due_id}", "edit-due-item", "Confirm amount, renegotiate a date or explicitly omit or cancel an expectation", func(ctx context.Context, in *struct {
		DuePathInput
		access.ActionKeyInput
		Body DueEdit
	}) (reference, error) {
		return ref(r.service.EditDue(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.Body))
	})
	r.register("POST", base+"/payment-applications", "apply-payment", "Distribute an existing effective movement atomically across expectations", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		access.ActionKeyInput
		Body PaymentApplicationCreate
	}) (reference, error) {
		return ref(r.service.ApplyPayment(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.Body))
	})
	r.register("GET", base+"/dues/{due_id}/applications", "list-due-applications", "Read active applications with current access to all sensitive relations", func(ctx context.Context, in *DuePathInput) ([]PaymentApplicationView, error) {
		return r.service.DueApplications(ctx, in.Authorization, in.SpaceID, in.ID)
	})
	r.register("POST", base+"/dues/{due_id}/applications/{application_id}/unlink", "unlink-payment", "Release an application without deleting evidence or reversing the movement", func(ctx context.Context, in *struct {
		DuePathInput
		access.ActionKeyInput
		ApplicationID string `path:"application_id" format:"uuid"`
		Body          PaymentUnlink
	}) (reference, error) {
		return ref(r.service.UnlinkPayment(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.ApplicationID, in.Body))
	})
}
