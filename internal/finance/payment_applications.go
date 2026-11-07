package finance

import (
	"context"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance/financedb"
)

type DueAllocation struct {
	DueID         string `json:"due_id" format:"uuid"`
	Version       int64  `json:"version" minimum:"1"`
	MovementMinor string `json:"movement_minor" pattern:"^[1-9][0-9]{0,18}$"`
	DueMinor      string `json:"due_minor" pattern:"^[1-9][0-9]{0,18}$"`
}

type PaymentApplicationCreate struct {
	OperationID              string          `json:"operation_id" format:"uuid"`
	Revision                 int64           `json:"revision" minimum:"1"`
	Effect                   int32           `json:"effect" minimum:"0" maximum:"3"`
	Allocations              []DueAllocation `json:"allocations" minItems:"1" maxItems:"100"`
	Confirm                  bool            `json:"confirm"`
	ConfirmAccessRestriction bool            `json:"confirm_access_restriction"`
}

func (v PaymentApplicationCreate) validate() error {
	if _, err := uuid.Parse(v.OperationID); err != nil || v.Revision < 1 || v.Effect < 0 || v.Effect > 3 || !v.Confirm {
		return access.ErrInvalid
	}
	return validateAllocations(v.Allocations)
}

func validateAllocations(allocations []DueAllocation) error {
	if len(allocations) < 1 || len(allocations) > 100 {
		return access.ErrInvalid
	}
	seen := make(map[string]bool, len(allocations))
	for _, a := range allocations {
		if _, err := uuid.Parse(a.DueID); err != nil || a.Version < 1 || seen[a.DueID] {
			return access.ErrInvalid
		}
		seen[a.DueID] = true
		for _, value := range []string{a.MovementMinor, a.DueMinor} {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n <= 0 || strconv.FormatInt(n, 10) != value {
				return access.ErrInvalid
			}
		}
	}
	return nil
}

type PaymentApplicationView struct {
	ID             string `json:"id"`
	OperationID    string `json:"operation_id"`
	SourceRevision int64  `json:"source_revision"`
	Effect         int32  `json:"effect"`
	MovementMinor  string `json:"movement_minor"`
	DueMinor       string `json:"due_minor"`
}

// applicableEffect identifies what actually satisfies a commitment. An internal
// transfer or reconciliation adjustment is not a recurring payment or income.
func applicableEffect(e PositionEffectView, direction string) bool {
	return e.InTracking && (direction == "payment" && e.Kind == "purchase" ||
		direction == "receipt" && (e.Kind == "income" || e.Kind == "refund"))
}

func (s *Service) ApplyPayment(ctx context.Context, c access.SpaceCommand, v PaymentApplicationCreate) (string, error) {
	if err := v.validate(); err != nil {
		return "", err
	}
	c.Kind, c.Capability, c.Content = "payment.apply", "finance.write", v
	return s.access.RunSpaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership, _ string) error {
		if m.Role == "reader" {
			return access.ErrForbidden
		}
		if _, err := readOperation(ctx, tx, c.SpaceID, v.OperationID, m.ID, 0); err != nil {
			return err
		}
		if err := authorizeAppliedDues(ctx, tx, c.SpaceID, v.OperationID, m.ID); err != nil {
			return err
		}
		for _, a := range v.Allocations {
			if _, err := readDue(ctx, tx, c.SpaceID, a.DueID, m.ID); err != nil {
				return err
			}
		}
		return nil
	}, func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership) (string, error) {
		if err := s.applyPayment(ctx, tx, actor, m, c.SpaceID, v); err != nil {
			return "", err
		}
		return v.OperationID, audit(ctx, tx, actor, c.SpaceID, c.Kind, v.OperationID)
	})
}

func (s *Service) applyPayment(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership, space string, v PaymentApplicationCreate) error {
	allocations := slices.Clone(v.Allocations)
	slices.SortFunc(allocations, func(a, b DueAllocation) int { return strings.Compare(a.DueID, b.DueID) })
	q := financedb.New(tx)
	if _, err := q.LockApplicationOperation(ctx, financedb.LockApplicationOperationParams{SpaceID: space, ID: v.OperationID}); err != nil {
		return absent(err)
	}
	count, err := q.OperationApplicationCount(ctx, financedb.OperationApplicationCountParams{SpaceID: space, OperationID: v.OperationID})
	if err != nil {
		return err
	}
	if count+int64(len(allocations)) > 100 {
		return access.ErrInvalid
	}
	op, err := readOperation(ctx, tx, space, v.OperationID, m.ID, 0)
	if err != nil {
		return err
	}
	if op.Revision != v.Revision {
		return execution.ErrVersion
	}
	if op.Voided || int(v.Effect) >= len(op.Effects) {
		return access.ErrInvalid
	}
	for _, id := range operationEffectAccountIDs(op.Effects) {
		if _, err := s.AuthorizeAccount(ctx, tx, actor, space, id, "operate"); err != nil {
			return err
		}
	}
	effect := op.Effects[v.Effect]
	capacity, ok := new(big.Int).SetString(effect.AmountMinor, 10)
	if !ok {
		return access.ErrInvalid
	}
	capacity.Abs(capacity)
	used, err := q.MovementAppliedMinor(ctx, financedb.MovementAppliedMinorParams{SpaceID: space, OperationID: v.OperationID, EffectOrdinal: v.Effect})
	if err != nil {
		return err
	}
	total, ok := new(big.Int).SetString(used, 10)
	if !ok {
		return access.ErrInvalid
	}
	for _, a := range allocations {
		n, _ := new(big.Int).SetString(a.MovementMinor, 10)
		total.Add(total, n)
	}
	if total.Cmp(capacity) > 0 {
		return access.ErrInvalid
	}
	for _, a := range allocations {
		if _, err := q.LockDueItem(ctx, financedb.LockDueItemParams{SpaceID: space, ID: a.DueID}); err != nil {
			return absent(err)
		}
		row, err := readDue(ctx, tx, space, a.DueID, m.ID)
		if err != nil {
			return err
		}
		d := row.DueItem
		if d.AccountID.String != effect.AccountID && !v.ConfirmAccessRestriction {
			return access.ErrInvalid
		}
		if d.Version != a.Version {
			return execution.ErrVersion
		}
		if d.State != "open" || !applicableEffect(effect, d.Direction) || row.Currency == effect.Currency && a.MovementMinor != a.DueMinor {
			return access.ErrInvalid
		}
		if err := s.authorizePlanningWrite(ctx, tx, actor, m, space, d.AccountID.String, d.ResourceID.String); err != nil {
			return err
		}
		count, err := q.DueApplicationCount(ctx, financedb.DueApplicationCountParams{SpaceID: space, DueID: d.ID})
		if err != nil {
			return err
		}
		applied, ok := new(big.Int).SetString(row.AppliedMinor, 10)
		if !ok {
			return access.ErrInvalid
		}
		n, _ := new(big.Int).SetString(a.DueMinor, 10)
		if count >= 100 || !applied.Add(applied, n).IsInt64() {
			return access.ErrInvalid
		}
		movement, _ := strconv.ParseInt(a.MovementMinor, 10, 64)
		due, _ := strconv.ParseInt(a.DueMinor, 10, 64)
		if err := q.InsertPaymentApplication(ctx, financedb.InsertPaymentApplicationParams{ID: uuid.New().String(), SpaceID: space, DueID: d.ID, OperationID: v.OperationID, SourceRevision: v.Revision, EffectOrdinal: v.Effect, MovementMinor: movement, DueMinor: due, Author: actor.ID()}); err != nil {
			return err
		}
		if err := advanceAppliedDue(ctx, tx, space, d.ID, actor.ID(), "Payment applied"); err != nil {
			return err
		}
	}
	return nil
}

func operationEffectAccountIDs(effects []PositionEffectView) []string {
	ids := make([]string, 0, len(effects))
	for _, e := range effects {
		ids = append(ids, e.AccountID)
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

func advanceAppliedDue(ctx context.Context, tx pgx.Tx, space, id, author, reason string) error {
	if err := financedb.New(tx).AdvanceDueItem(ctx, financedb.AdvanceDueItemParams{SpaceID: space, ID: id}); err != nil {
		return err
	}
	return captureDue(ctx, tx, space, id, author, reason)
}

func (s *Service) DueApplications(ctx context.Context, authorization, space, id string) ([]PaymentApplicationView, error) {
	out := []PaymentApplicationView{}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		if _, err := readDue(ctx, tx, space, id, m.ID); err != nil {
			return err
		}
		rows, err := financedb.New(tx).DueApplications(ctx, financedb.DueApplicationsParams{SpaceID: space, DueID: id, Limit: 100})
		if err != nil {
			return err
		}
		out = []PaymentApplicationView{}
		for _, p := range rows {
			out = append(out, PaymentApplicationView{ID: p.ID, OperationID: p.OperationID, SourceRevision: p.SourceRevision, Effect: p.EffectOrdinal, MovementMinor: strconv.FormatInt(p.MovementMinor, 10), DueMinor: strconv.FormatInt(p.DueMinor, 10)})
		}
		return nil
	})
	return out, err
}

type PaymentUnlink struct {
	FinancialVersionConfirmation
	Reason string `json:"reason" minLength:"1" maxLength:"500"`
}

func (s *Service) UnlinkPayment(ctx context.Context, c access.SpaceCommand, due, id string, v PaymentUnlink) (string, error) {
	if v.Version < 1 || !v.Confirm || !text(v.Reason, 500, false) {
		return "", access.ErrInvalid
	}
	c.Kind, c.Capability, c.Content = "payment.unlink", "finance.write", struct {
		Due, ID string
		Value   PaymentUnlink
	}{due, id, v}
	return s.access.RunSpaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership, ref string) error {
		if m.Role == "reader" {
			return access.ErrForbidden
		}
		row, err := readDue(ctx, tx, c.SpaceID, due, m.ID)
		if err != nil {
			return err
		}
		p, err := financedb.New(tx).PaymentApplication(ctx, financedb.PaymentApplicationParams{SpaceID: c.SpaceID, DueID: due, ID: id})
		if err != nil {
			return absent(err)
		}
		if _, err := readOperation(ctx, tx, c.SpaceID, p.OperationID, m.ID, 0); err != nil {
			return err
		}
		if ref != "" {
			return nil
		}
		return s.authorizePlanningWrite(ctx, tx, actor, m, c.SpaceID, row.DueItem.AccountID.String, row.DueItem.ResourceID.String)
	}, func(ctx context.Context, tx pgx.Tx, actor access.Actor, _ accessdb.Membership) (string, error) {
		q := financedb.New(tx)
		p, err := q.PaymentApplication(ctx, financedb.PaymentApplicationParams{SpaceID: c.SpaceID, DueID: due, ID: id})
		if err != nil {
			return "", absent(err)
		}
		if _, err := q.LockApplicationOperation(ctx, financedb.LockApplicationOperationParams{SpaceID: c.SpaceID, ID: p.OperationID}); err != nil {
			return "", absent(err)
		}
		d, err := q.LockDueItem(ctx, financedb.LockDueItemParams{SpaceID: c.SpaceID, ID: due})
		if err != nil {
			return "", absent(err)
		}
		if d.Version != v.Version {
			return "", execution.ErrVersion
		}
		released, err := q.ReleasePaymentApplication(ctx, financedb.ReleasePaymentApplicationParams{SpaceID: c.SpaceID, ApplicationID: id, Author: actor.ID(), Reason: v.Reason})
		if err != nil {
			return "", err
		}
		if released != 1 {
			return "", access.ErrInvalid
		}
		if err := advanceAppliedDue(ctx, tx, c.SpaceID, due, actor.ID(), v.Reason); err != nil {
			return "", err
		}
		return due, audit(ctx, tx, actor, c.SpaceID, c.Kind, id)
	})
}

// validateAppliedCorrection never guesses how to redistribute existing payments.
// A correction may retain them only while their account, currency, meaning and
// total capacity remain valid. Otherwise unlink explicitly before correcting.
func validateAppliedCorrection(ctx context.Context, tx pgx.Tx, space, member string, old OperationView, p operationPlan) error {
	if err := authorizeAppliedDues(ctx, tx, space, old.ID, member); err != nil {
		return err
	}
	rows, err := financedb.New(tx).OperationApplicationBuckets(ctx, financedb.OperationApplicationBucketsParams{SpaceID: space, OperationID: old.ID})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.EffectOrdinal < 0 || int(row.EffectOrdinal) >= len(p.effects) || int(row.EffectOrdinal) >= len(old.Effects) {
			return access.ErrInvalid
		}
		e := p.effects[row.EffectOrdinal]
		if e.account.ID != row.AccountID || e.account.Currency != row.Currency || !e.tracked || e.kind != old.Effects[row.EffectOrdinal].Kind {
			return access.ErrInvalid
		}
		used, ok := new(big.Int).SetString(row.AppliedMinor, 10)
		capacity := new(big.Int).Abs(big.NewInt(e.amount.Minor()))
		if !ok || used.Cmp(capacity) > 0 {
			return access.ErrInvalid
		}
	}
	return nil
}

// Capacity is derived data too. A readable movement must not become an oracle
// for the amounts allocated to obligations that the actor cannot currently read.
func authorizeAppliedDues(ctx context.Context, tx pgx.Tx, space, operation, member string) error {
	q := financedb.New(tx)
	ids, err := q.OperationAppliedDues(ctx, financedb.OperationAppliedDuesParams{SpaceID: space, OperationID: operation})
	if err != nil || len(ids) == 0 {
		return err
	}
	rows, err := q.AuthorizedDueItems(ctx, financedb.AuthorizedDueItemsParams{SpaceID: space, MemberID: member, ApplicationOperation: operation, PageSize: 101})
	if err != nil {
		return err
	}
	if len(rows) != len(ids) {
		return access.ErrNotFound
	}
	return nil
}

func releaseVoidedApplications(ctx context.Context, tx pgx.Tx, space, operation, author string) error {
	q := financedb.New(tx)
	dues, err := q.OperationAppliedDues(ctx, financedb.OperationAppliedDuesParams{SpaceID: space, OperationID: operation})
	if err != nil {
		return err
	}
	for _, id := range dues {
		if _, err := q.LockDueItem(ctx, financedb.LockDueItemParams{SpaceID: space, ID: id}); err != nil {
			return err
		}
	}
	if err := q.ReleaseVoidedApplications(ctx, financedb.ReleaseVoidedApplicationsParams{SpaceID: space, OperationID: operation, Author: author}); err != nil {
		return err
	}
	for _, id := range dues {
		if err := advanceAppliedDue(ctx, tx, space, id, author, "Source operation voided"); err != nil {
			return err
		}
	}
	return nil
}
