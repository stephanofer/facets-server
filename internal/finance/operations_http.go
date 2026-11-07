package finance

import (
	"context"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/finance/financedb"
)

type OperationPathInput struct {
	access.AuthorizationInput
	access.SpacePathInput
	ID string `path:"operation_id" format:"uuid"`
}

type ConsistencyCheckConfirmation struct {
	Confirm bool `json:"confirm"`
}

type AccountHistoryEntry struct {
	OperationID string `json:"operation_id"`
	Kind        string `json:"kind"`
	Date        string `json:"date"`
	EffectKind  string `json:"effect_kind"`
	AmountMinor string `json:"amount_minor"`
	Currency    string `json:"currency"`
	InTracking  bool   `json:"in_tracking"`
}

// AccountHistory deliberately returns only this account's effects. A relation
// to a private counter-end never authorizes its identifiers or shared metadata.
func (s *Service) AccountHistory(ctx context.Context, authorization, space, account, cursor string, limit int32) (access.Page[AccountHistoryEntry], error) {
	out := access.Page[AccountHistoryEntry]{Items: []AccountHistoryEntry{}}
	if limit < 1 || limit > 100 {
		return out, access.ErrInvalid
	}
	id, ordinal := "", int32(-1)
	if cursor != "" {
		var part string
		var ok bool
		id, part, ok = strings.Cut(cursor, ":")
		parsed, err := strconv.ParseInt(part, 10, 32)
		if !ok || err != nil || parsed < 0 || parsed > 3 || strconv.FormatInt(parsed, 10) != part {
			return out, access.ErrInvalid
		}
		if _, err := uuid.Parse(id); err != nil {
			return out, access.ErrInvalid
		}
		ordinal = int32(parsed)
	}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, a access.Actor, m accessdb.Membership) error {
		account, err := s.AuthorizeAccount(ctx, tx, a, space, account, "read")
		if err != nil {
			return err
		}
		rows, err := financedb.New(tx).AccountOperationHistory(ctx, financedb.AccountOperationHistoryParams{SpaceID: space, AccountID: account.ID, MemberID: m.ID, Cursor: id, CursorOrdinal: ordinal, PageSize: limit + 1})
		if err != nil {
			return err
		}
		out.Items, out.NextCursor = []AccountHistoryEntry{}, ""
		if len(rows) > int(limit) {
			rows = rows[:limit]
			last := rows[len(rows)-1]
			out.NextCursor = last.ID + ":" + strconv.FormatInt(int64(last.Ordinal), 10)
		}
		for _, row := range rows {
			out.Items = append(out.Items, AccountHistoryEntry{OperationID: row.ID, Kind: row.Kind, Date: row.EffectiveDate.Time.Format(time.DateOnly), EffectKind: row.EffectKind, AmountMinor: strconv.FormatInt(row.AmountMinor, 10), Currency: account.Currency, InTracking: row.InTracking})
		}
		return nil
	})
	return out, err
}

func (r *routes) operations() {
	base := "/v1/spaces/{space_id}"
	r.register("GET", base+"/operations", "search-financial-operations", "Search authorized current operations with stable date and identity pagination", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		OperationFilter
		Cursor string `query:"cursor" maxLength:"47"`
		Limit  int32  `query:"limit" default:"50" minimum:"1" maximum:"100"`
	}) (access.Page[OperationSummary], error) {
		return r.service.SearchOperations(ctx, in.Authorization, in.SpaceID, in.OperationFilter, in.Cursor, in.Limit)
	})
	r.register("GET", base+"/operation-suggestions", "suggest-operation-selections", "Suggest recent manual selections without applying them or detecting recurrence", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		Kind       string `query:"kind" enum:"income,expense,refund" default:"expense"`
		MerchantID string `query:"merchant_id" format:"uuid"`
		Limit      int32  `query:"limit" default:"10" minimum:"1" maximum:"20"`
	}) ([]RecentSelection, error) {
		return r.service.OperationSuggestions(ctx, in.Authorization, in.SpaceID, in.Kind, in.MerchantID, in.Limit)
	})
	r.register("GET", base+"/financial-summary", "query-financial-summary", "Query current positions and period economic and cash readings in original currencies", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		FinancialQuery
	}) (FinancialSummary, error) {
		return r.service.FinancialSummary(ctx, in.Authorization, in.SpaceID, in.FinancialQuery)
	})
	r.register("POST", base+"/operations", "create-financial-operation", "Confirm an atomic income, expense, transfer or refund", func(ctx context.Context, in *struct {
		access.AuthorizationInput
		access.SpacePathInput
		access.ActionKeyInput
		Body OperationCreate
	}) (reference, error) {
		return ref(r.service.CreateOperation(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.Body))
	})
	r.register("GET", base+"/operations/{operation_id}", "get-financial-operation", "Read an operation with access to all its accounts", func(ctx context.Context, in *OperationPathInput) (OperationView, error) {
		return r.service.Operation(ctx, in.Authorization, in.SpaceID, in.ID)
	})
	r.register("POST", base+"/operations/{operation_id}/correction-preview", "preview-operation-correction", "Review financial consequences without changing the operation", func(ctx context.Context, in *struct {
		OperationPathInput
		Body OperationCorrection
	}) (OperationConsequences, error) {
		return r.service.PreviewOperationCorrection(ctx, in.Authorization, in.SpaceID, in.ID, in.Body)
	})
	r.register("PUT", base+"/operations/{operation_id}", "correct-financial-operation", "Confirm an atomic replacement revision with optimistic concurrency", func(ctx context.Context, in *struct {
		OperationPathInput
		access.ActionKeyInput
		Body OperationCorrection
	}) (reference, error) {
		return ref(r.service.CorrectOperation(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.Body))
	})
	r.register("POST", base+"/operations/{operation_id}/void", "void-financial-operation", "Void every effect together while preserving the recorded evidence", func(ctx context.Context, in *struct {
		OperationPathInput
		access.ActionKeyInput
		Body OperationVoid
	}) (reference, error) {
		return ref(r.service.VoidOperation(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.Body))
	})
	r.register("GET", base+"/operations/{operation_id}/revisions", "list-operation-revisions", "List revision evidence authorized before pagination", func(ctx context.Context, in *struct {
		OperationPathInput
		Cursor string `query:"cursor" maxLength:"19"`
		Limit  int32  `query:"limit" default:"50" minimum:"1" maximum:"100"`
	}) (access.Page[OperationRevisionSummary], error) {
		return r.service.OperationRevisions(ctx, in.Authorization, in.SpaceID, in.ID, in.Cursor, in.Limit)
	})
	r.register("GET", base+"/operations/{operation_id}/revisions/{revision}", "get-operation-revision", "Read a retained revision under current permissions on its own accounts", func(ctx context.Context, in *struct {
		OperationPathInput
		Revision int64 `path:"revision" minimum:"1"`
	}) (OperationView, error) {
		return r.service.OperationRevision(ctx, in.Authorization, in.SpaceID, in.ID, in.Revision)
	})
	r.register("GET", base+"/accounts/{account_id}/history", "list-account-operation-history", "Read only the authorized account effects, without counter-end metadata", func(ctx context.Context, in *struct {
		AccountPathInput
		Cursor string `query:"cursor" maxLength:"40"`
		Limit  int32  `query:"limit" default:"50" minimum:"1" maximum:"100"`
	}) (access.Page[AccountHistoryEntry], error) {
		return r.service.AccountHistory(ctx, in.Authorization, in.SpaceID, in.ID, in.Cursor, in.Limit)
	})
	r.register("POST", base+"/accounts/{account_id}/reconciliation-preview", "preview-account-reconciliation", "Compare observed and historical calculated balances without writing", func(ctx context.Context, in *struct {
		AccountPathInput
		Body ReconciliationObservation
	}) (ReconciliationPreview, error) {
		return r.service.PreviewReconciliation(ctx, in.Authorization, in.SpaceID, in.ID, in.Body)
	})
	r.register("POST", base+"/accounts/{account_id}/reconciliations", "reconcile-account", "Compare an observed balance and optionally confirm a traceable adjustment", func(ctx context.Context, in *struct {
		AccountPathInput
		access.ActionKeyInput
		Body ReconciliationCreate
	}) (reference, error) {
		return ref(r.service.ReconcileAccount(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.Body))
	})
	r.register("GET", base+"/accounts/{account_id}/reconciliations", "list-account-reconciliations", "Read retained reconciliation evidence and historical review warnings", func(ctx context.Context, in *struct {
		AccountPathInput
		Cursor string `query:"cursor" maxLength:"36"`
		Limit  int32  `query:"limit" default:"50" minimum:"1" maximum:"100"`
	}) (access.Page[ReconciliationView], error) {
		return r.service.Reconciliations(ctx, in.Authorization, in.SpaceID, in.ID, in.Cursor, in.Limit)
	})
	r.register("POST", base+"/accounts/{account_id}/consistency-checks", "check-account-consistency", "Record a reconstruction check without silently repairing discrepancies", func(ctx context.Context, in *struct {
		AccountPathInput
		access.ActionKeyInput
		Body ConsistencyCheckConfirmation
	}) (reference, error) {
		return ref(r.service.CheckAccountConsistency(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.Body.Confirm))
	})
	r.register("GET", base+"/accounts/{account_id}/consistency-checks", "list-account-consistency-checks", "Read consistency checks and recorded discrepancies under current account access", func(ctx context.Context, in *struct {
		AccountPathInput
		Cursor string `query:"cursor" maxLength:"36"`
		Limit  int32  `query:"limit" default:"50" minimum:"1" maximum:"100"`
	}) (access.Page[ConsistencyView], error) {
		return r.service.ConsistencyChecks(ctx, in.Authorization, in.SpaceID, in.ID, in.Cursor, in.Limit)
	})
}
