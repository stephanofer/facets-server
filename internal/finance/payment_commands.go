package finance

import (
	"context"
	"math/big"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/finance/financedb"
)

// MovementCapacityView is advisory, not a reservation. Applying a payment always
// checks the current revision and remaining capacity again under write locks.
type MovementCapacityView struct {
	OperationID    string `json:"operation_id"`
	Revision       int64  `json:"revision"`
	Effect         int32  `json:"effect"`
	AccountID      string `json:"account_id"`
	Currency       string `json:"currency"`
	Scale          int32  `json:"scale"`
	Eligible       bool   `json:"eligible"`
	Direction      string `json:"direction" enum:",payment,receipt"`
	CapacityMinor  string `json:"capacity_minor"`
	AppliedMinor   string `json:"applied_minor"`
	AvailableMinor string `json:"available_minor"`
}

func movementCapacity(op OperationView, effect int32, used string) (MovementCapacityView, error) {
	var out MovementCapacityView
	if effect < 0 || int(effect) >= len(op.Effects) {
		return out, access.ErrInvalid
	}
	e := op.Effects[effect]
	out = MovementCapacityView{OperationID: op.ID, Revision: op.Revision, Effect: effect, AccountID: e.AccountID, Currency: e.Currency, Scale: e.Scale, CapacityMinor: "0", AppliedMinor: used, AvailableMinor: "0"}
	if !op.Voided {
		if applicableEffect(e, "payment") {
			out.Direction = "payment"
		} else if applicableEffect(e, "receipt") {
			out.Direction = "receipt"
		}
	}
	out.Eligible = out.Direction != ""
	capacity := new(big.Int)
	if out.Eligible {
		if _, ok := capacity.SetString(e.AmountMinor, 10); !ok {
			return out, access.ErrInvalid
		}
		capacity.Abs(capacity)
	}
	applied, ok := new(big.Int).SetString(used, 10)
	if !ok || applied.Sign() < 0 || applied.Cmp(capacity) > 0 {
		return out, access.ErrInvalid
	}
	out.CapacityMinor = capacity.String()
	out.AvailableMinor = new(big.Int).Sub(capacity, applied).String()
	return out, nil
}

// MovementCapacity requires current read access to the source and every active
// target. Its result describes one consistent state, without reserving capacity.
func (s *Service) MovementCapacity(ctx context.Context, authorization, space, id string, effect int32) (MovementCapacityView, error) {
	var out MovementCapacityView
	if _, err := uuid.Parse(id); err != nil || effect < 0 || effect > 3 {
		return out, access.ErrInvalid
	}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		op, err := readOperation(ctx, tx, space, id, m.ID, 0)
		if err != nil {
			return err
		}
		// Capacity must not disclose allocations to currently private obligations.
		if err := authorizeAppliedDues(ctx, tx, space, id, m.ID); err != nil {
			return err
		}
		used, err := financedb.New(tx).MovementAppliedMinor(ctx, financedb.MovementAppliedMinorParams{SpaceID: space, OperationID: id, EffectOrdinal: effect})
		if err != nil {
			return err
		}
		out, err = movementCapacity(op, effect, used)
		return err
	})
	return out, err
}

type PaymentOperationCreate struct {
	Operation                OperationCreate `json:"operation"`
	Allocations              []DueAllocation `json:"allocations" minItems:"1" maxItems:"100"`
	ConfirmAccessRestriction bool            `json:"confirm_access_restriction"`
}

func (v PaymentOperationCreate) application(id string) PaymentApplicationCreate {
	return PaymentApplicationCreate{OperationID: id, Revision: 1, Effect: 0, Allocations: v.Allocations, Confirm: v.Operation.Confirm, ConfirmAccessRestriction: v.ConfirmAccessRestriction}
}

func (v PaymentOperationCreate) validate() error {
	if err := v.Operation.validate(); err != nil {
		return err
	}
	// Only the actual purchase/income/refund can fulfill these commitments.
	// Transfers and later domain-specific debt payments are not disguised expenses.
	if v.Operation.Kind == "transfer" {
		return access.ErrInvalid
	}
	return validateAllocations(v.Allocations)
}

// CreatePaymentOperation records the fact, effects, projections, applications,
// due revisions, audit and action receipt in one transaction. No nested command
// or independent commit is allowed along this path.
func (s *Service) CreatePaymentOperation(ctx context.Context, c access.SpaceCommand, v PaymentOperationCreate) (string, error) {
	if err := v.validate(); err != nil {
		return "", err
	}
	c.Kind, c.Capability, c.Content = "payment.create", "finance.write", v
	return s.access.RunSpaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership, ref string) error {
		if m.Role == "reader" {
			return access.ErrForbidden
		}
		if ref != "" {
			if _, err := readOperation(ctx, tx, c.SpaceID, ref, m.ID, 0); err != nil {
				return err
			}
			if err := authorizeAppliedDues(ctx, tx, c.SpaceID, ref, m.ID); err != nil {
				return err
			}
		} else {
			for _, id := range operationAccountIDs(v.Operation) {
				if _, err := s.AuthorizeAccount(ctx, tx, actor, c.SpaceID, id, "read"); err != nil {
					return err
				}
			}
		}
		// Check original targets even after unlinking: replay is not a route
		// around revocation of any of the command's sensitive resources.
		for _, a := range v.Allocations {
			if _, err := readDue(ctx, tx, c.SpaceID, a.DueID, m.ID); err != nil {
				return err
			}
		}
		return nil
	}, func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership) (string, error) {
		id, err := s.createOperation(ctx, tx, actor, c.SpaceID, v.Operation)
		if err != nil {
			return "", err
		}
		if err := s.applyPayment(ctx, tx, actor, m, c.SpaceID, v.application(id)); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, actor, c.SpaceID, c.Kind, id)
	})
}
