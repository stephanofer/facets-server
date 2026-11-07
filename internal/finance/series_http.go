package finance

import (
	"context"

	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/calendar"
)

type SeriesPathInput struct {
	access.AuthorizationInput
	access.SpacePathInput
	ID string `path:"series_id" format:"uuid"`
}

func (r *routes) series() {
	base := "/v1/spaces/{space_id}/series"
	r.register("POST", base+"/preview", "preview-recurring-series", "Preview all nominal calendar slots without materializing or paying anything", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		Body SeriesPreview
	}) ([]calendar.Occurrence, error) {
		return r.service.PreviewSeries(ctx, in.Authorization, in.SpaceID, in.Body)
	})
	r.register("POST", base, "create-recurring-series", "Configure a manual recurring expectation, private by default without an account, without creating movements", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		access.ActionKeyInput
		Body SeriesCreate
	}) (reference, error) {
		return ref(r.service.CreateSeries(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.Body))
	})
	r.register("GET", base, "list-recurring-series", "List authorized series before pagination", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		access.PagingInput
	}) (access.Page[SeriesView], error) {
		return r.service.ListSeries(ctx, in.Authorization, in.SpaceID, in.Cursor, in.Limit)
	})
	r.register("GET", base+"/{series_id}", "get-recurring-series", "Read current version and durable materialization progress", func(ctx context.Context, in *SeriesPathInput) (SeriesView, error) {
		return r.service.Series(ctx, in.Authorization, in.SpaceID, in.ID)
	})
	r.register("PUT", base+"/{series_id}", "edit-recurring-series", "Version future conditions, pause, resume, end or cancel tracking; preserve existing dues", func(ctx context.Context, in *struct {
		SeriesPathInput
		access.ActionKeyInput
		Body SeriesEdit
	}) (reference, error) {
		return ref(r.service.EditSeries(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.Body))
	})
	r.register("POST", base+"/{series_id}/generate", "generate-recurring-occurrences", "Recoverably materialize a bounded horizon using the common due and application engine", func(ctx context.Context, in *struct {
		SeriesPathInput
		access.ActionKeyInput
		Body SeriesGenerate
	}) (reference, error) {
		return ref(r.service.GenerateSeries(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.Body))
	})
	r.register("GET", base+"/{series_id}/versions", "list-recurring-series-versions", "Read immutable conditions and their generation boundaries with current authorization", func(ctx context.Context, in *struct {
		SeriesPathInput
		Cursor string `query:"cursor" maxLength:"19"`
		Limit  int32  `query:"limit" default:"50" minimum:"1" maximum:"100"`
	}) (access.Page[SeriesVersionView], error) {
		return r.service.SeriesHistory(ctx, in.Authorization, in.SpaceID, in.ID, in.Cursor, in.Limit)
	})
}
