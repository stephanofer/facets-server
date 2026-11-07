package finance

import (
	"context"
	"math/big"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/finance/financedb"
)

// RefundLink attributes part of a refund to a purchase in the purchase's account
// currency. AppliedAmount independently records the amount actually credited.
type RefundLink struct {
	OperationID string `json:"operation_id" format:"uuid"`
	AmountMinor string `json:"amount_minor" pattern:"^[1-9][0-9]{0,18}$"`
}

func (s *Service) planRefundLink(ctx context.Context, tx pgx.Tx, actor access.Actor, space string, v OperationCreate, previous *OperationView, currency string) (int64, error) {
	if v.Refund == nil {
		return 0, nil // A real refund can be recorded when the original purchase is unavailable.
	}
	m, err := accessdb.New(tx).Membership(ctx, accessdb.MembershipParams{SpaceID: space, PrincipalID: actor.ID()})
	if err != nil {
		return 0, absent(err)
	}
	purchase, err := readOperation(ctx, tx, space, v.Refund.OperationID, m.ID, 0)
	if err != nil {
		return 0, err
	}
	if purchase.Kind != "expense" || purchase.Voided || purchase.Date > v.Date || previous != nil && previous.ID == purchase.ID {
		return 0, access.ErrInvalid
	}
	attributed, err := positiveMinor(v.Refund.AmountMinor, purchase.Effects[0].Currency)
	if err != nil {
		return 0, err
	}
	actual, err := positiveMinor(v.Applied.AmountMinor, currency)
	if err != nil || currency == purchase.Effects[0].Currency && actual.Minor() != attributed.Minor() {
		return 0, access.ErrInvalid
	}
	exclude := ""
	if previous != nil {
		exclude = previous.ID
	}
	used, err := financedb.New(tx).RefundedPurchaseAmount(ctx, financedb.RefundedPurchaseAmountParams{SpaceID: space, PurchaseID: purchase.ID, ExcludeID: exclude})
	if err != nil {
		return 0, err
	}
	total, ok := new(big.Int).SetString(used.AmountMinor, 10)
	if !ok {
		return 0, access.ErrInvalid
	}
	total.Add(total, big.NewInt(attributed.Minor()))
	available, _ := new(big.Int).SetString(purchase.Effects[0].AmountMinor, 10)
	available.Abs(available)
	if total.Cmp(available) > 0 {
		return 0, access.ErrInvalid
	}
	return purchase.Revision, nil
}

// Corrections cannot orphan a confirmed refund or retrospectively make its
// attributed amount exceed the purchase. The existing space lock serializes
// refund creation, correction and voiding across application processes.
func validateRefundedPurchase(ctx context.Context, tx pgx.Tx, space string, old OperationView, replacement *OperationCreate, p operationPlan) error {
	if old.Kind != "expense" {
		return nil
	}
	used, err := financedb.New(tx).RefundedPurchaseAmount(ctx, financedb.RefundedPurchaseAmountParams{SpaceID: space, PurchaseID: old.ID})
	if err != nil || used.AmountMinor == "0" {
		return err
	}
	if replacement == nil || replacement.Date > used.EarliestDate.Time.Format("2006-01-02") || p.effects[0].account.Currency != old.Effects[0].Currency {
		return access.ErrInvalid
	}
	n, _ := new(big.Int).SetString(used.AmountMinor, 10)
	amount, err := strconv.ParseInt(replacement.Applied.AmountMinor, 10, 64)
	if err != nil || n.Cmp(big.NewInt(amount)) > 0 {
		return access.ErrInvalid
	}
	return nil
}
