package finance

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/finance/financedb"
	"github.com/stephanofer/facets-server/internal/money"
)

// AppliedAmount is always the amount actually applied to an account, not an
// estimated conversion of the merchant's original price.
type AppliedAmount struct {
	AccountID   string `json:"account_id" format:"uuid"`
	AmountMinor string `json:"amount_minor" pattern:"^[1-9][0-9]{0,18}$"`
}

type CategoryPart struct {
	AmountMinor string `json:"amount_minor" pattern:"^[1-9][0-9]{0,18}$"`
	CategoryID  string `json:"category_id,omitempty" format:"uuid"`
}

type OperationFee struct {
	AppliedAmount
	CategoryID          string `json:"category_id" format:"uuid"`
	ExchangeReferenceID string `json:"exchange_reference_id,omitempty" format:"uuid"`
}

type OriginalPrice struct {
	AmountMinor string `json:"amount_minor" pattern:"^[1-9][0-9]{0,18}$"`
	Currency    string `json:"currency" minLength:"3" maxLength:"3"`
}

type OperationCreate struct {
	Kind                string         `json:"kind" enum:"income,expense,transfer,refund"`
	Refund              *RefundLink    `json:"refund,omitempty"`
	Date                string         `json:"date" format:"date"`
	Applied             AppliedAmount  `json:"applied"`
	Received            *AppliedAmount `json:"received,omitempty"`
	Parts               []CategoryPart `json:"parts" maxItems:"100"`
	Fees                []OperationFee `json:"fees" maxItems:"2"`
	Note                string         `json:"note,omitempty" maxLength:"1000"`
	Exceptional         bool           `json:"exceptional"`
	MerchantID          string         `json:"merchant_id,omitempty" format:"uuid"`
	CounterpartyID      string         `json:"counterparty_id,omitempty" format:"uuid"`
	TagIDs              []string       `json:"tag_ids" maxItems:"20" uniqueItems:"true"`
	Original            *OriginalPrice `json:"original,omitempty"`
	ExchangeReferenceID string         `json:"exchange_reference_id,omitempty" format:"uuid"`
	Confirm             bool           `json:"confirm"`
}

type plannedEffect struct {
	account AccountView
	kind    string
	amount  money.Amount
	tracked bool
}
type plannedComponent struct {
	effect    int32
	direction string
	amount    int64
	category  string
	exchange  string
}
type operationPlan struct {
	date           pgtype.Date
	effects        []plannedEffect
	components     []plannedComponent
	original       pgtype.Int8
	accounts       map[string]AccountView
	refundRevision int64
}

func positiveMinor(value, currency string) (money.Amount, error) {
	c, err := money.ParseCurrency(currency)
	if err != nil {
		return money.Amount{}, access.ErrInvalid
	}
	a, err := money.ParseMinor(value, c)
	if err != nil || a.Minor() <= 0 {
		return money.Amount{}, access.ErrInvalid
	}
	return a, nil
}

func (v OperationCreate) validate() error {
	if !v.Confirm || !slices.Contains([]string{"income", "expense", "transfer", "refund"}, v.Kind) || !utf8.ValidString(v.Note) || utf8.RuneCountInString(v.Note) > 1000 || strings.ContainsRune(v.Note, '\x00') || len(v.Parts) > 100 || len(v.Fees) > 2 || len(v.TagIDs) > 20 {
		return access.ErrInvalid
	}
	d, err := time.Parse(time.DateOnly, v.Date)
	if err != nil || d.Year() < 1 || d.Year() > 9999 {
		return access.ErrInvalid
	}
	ids := []string{v.Applied.AccountID}
	if v.Refund != nil {
		if v.Kind != "refund" {
			return access.ErrInvalid
		}
		ids = append(ids, v.Refund.OperationID)
		if v.Refund.OperationID == "" {
			return access.ErrInvalid
		}
	}
	if v.Kind == "transfer" {
		if v.Received == nil || v.Received.AccountID == "" || v.Received.AccountID == v.Applied.AccountID || len(v.Parts) != 0 || v.Original != nil || v.ExchangeReferenceID != "" || v.MerchantID != "" || v.Exceptional {
			return access.ErrInvalid
		}
		ids = append(ids, v.Received.AccountID)
	} else if v.Received != nil || len(v.Fees) != 0 {
		return access.ErrInvalid
	}
	ids = append(ids, v.MerchantID, v.CounterpartyID, v.ExchangeReferenceID)
	for _, p := range v.Parts {
		ids = append(ids, p.CategoryID)
	}
	feeAccounts := make(map[string]bool)
	for _, fee := range v.Fees {
		if v.Received == nil || fee.AccountID != v.Applied.AccountID && fee.AccountID != v.Received.AccountID || fee.CategoryID == "" || feeAccounts[fee.AccountID] {
			return access.ErrInvalid
		}
		feeAccounts[fee.AccountID] = true
		ids = append(ids, fee.AccountID, fee.CategoryID, fee.ExchangeReferenceID)
	}
	tags := make(map[string]bool)
	for _, id := range v.TagIDs {
		if id == "" || tags[id] {
			return access.ErrInvalid
		}
		tags[id] = true
		ids = append(ids, id)
	}
	for _, id := range ids {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				return access.ErrInvalid
			}
		}
	}
	if v.Applied.AccountID == "" {
		return access.ErrInvalid
	}
	return nil
}

func operationAccountIDs(v OperationCreate) []string {
	ids := []string{v.Applied.AccountID}
	if v.Received != nil {
		ids = append(ids, v.Received.AccountID)
	}
	slices.Sort(ids)
	return ids
}

func operationCatalogIDs(v OperationCreate) []string {
	ids := append([]string{v.MerchantID, v.CounterpartyID}, v.TagIDs...)
	for _, part := range v.Parts {
		ids = append(ids, part.CategoryID)
	}
	for _, fee := range v.Fees {
		ids = append(ids, fee.CategoryID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) > 0 && ids[0] == "" {
		ids = ids[1:]
	}
	return ids
}

func (s *Service) planOperation(ctx context.Context, tx pgx.Tx, actor access.Actor, space string, v OperationCreate, previous *OperationView) (operationPlan, error) {
	d, _ := time.Parse(time.DateOnly, v.Date) // Structural validation precedes the transaction.
	p := operationPlan{date: pgtype.Date{Time: d, Valid: true}, accounts: make(map[string]AccountView)}
	settings, err := accessdb.New(tx).GetSpace(ctx, space)
	if err != nil {
		return p, err
	}
	zone, err := time.LoadLocation(settings.Zone)
	if err != nil {
		return p, err
	}
	if v.Date > time.Now().In(zone).Format(time.DateOnly) {
		return p, access.ErrInvalid // Expectations are not financial facts.
	}
	for _, id := range operationAccountIDs(v) {
		a, err := s.AuthorizeAccount(ctx, tx, actor, space, id, "operate")
		if err != nil {
			return p, err
		}
		p.accounts[id] = a
	}
	references, err := resolveCatalogReferences(ctx, tx, space, operationCatalogIDs(v))
	if err != nil {
		return p, err
	}
	// Continuing an existing classification is not new use of an archived entry.
	if previous != nil {
		for _, id := range retainedCatalogIDs(*previous) {
			if entry, ok := references[id]; ok {
				entry.Archived = false
				references[id] = entry
			}
		}
	}
	historical := func(id, currency string) error {
		if previous != nil && previous.Date == v.Date {
			for _, component := range previous.Components {
				if component.ExchangeReferenceID == id && previous.Effects[component.EffectOrdinal].Currency == currency {
					return nil // Preserve the original presentation context after a base-currency change.
				}
			}
		}
		return validateHistoricalReference(ctx, tx, space, id, currency, settings.Currency, d)
	}
	for _, reference := range []struct{ id, kind string }{{v.MerchantID, "merchant"}, {v.CounterpartyID, "counterparty"}} {
		if reference.id != "" {
			if err := validateOperationReference(references, reference.id, reference.kind, ""); err != nil {
				return p, err
			}
		}
	}
	for _, id := range v.TagIDs {
		if err := validateOperationReference(references, id, "tag", ""); err != nil {
			return p, err
		}
	}
	if v.Original != nil {
		original, err := positiveMinor(v.Original.AmountMinor, v.Original.Currency)
		if err != nil {
			return p, err
		}
		p.original = pgtype.Int8{Int64: original.Minor(), Valid: true}
	}
	primary := p.accounts[v.Applied.AccountID]
	amount, err := positiveMinor(v.Applied.AmountMinor, primary.Currency)
	if err != nil {
		return p, err
	}
	kind, sign := "income", int64(1)
	if v.Kind == "expense" || v.Kind == "refund" {
		kind = "purchase"
		if primary.Position == "liquid" {
			sign = -1
		} else if primary.Position != "obligation" {
			return p, access.ErrInvalid
		}
		if v.Kind == "refund" {
			kind, sign = "refund", -sign
			p.refundRevision, err = s.planRefundLink(ctx, tx, actor, space, v, previous, primary.Currency)
			if err != nil {
				return p, err
			}
		}
	} else if primary.Position != "liquid" {
		return p, access.ErrInvalid
	}
	if v.Kind == "transfer" {
		kind, sign = "transfer_out", -1
	}
	signed, _ := money.Multiply(amount, sign) // Positive int64 times +/-1 cannot overflow.
	p.effects = append(p.effects, plannedEffect{account: primary, kind: kind, amount: signed, tracked: v.Date >= primary.Opening.Date})
	if v.Kind == "transfer" {
		destination := p.accounts[v.Received.AccountID]
		if destination.Position != "liquid" {
			return p, access.ErrInvalid
		}
		received, err := positiveMinor(v.Received.AmountMinor, destination.Currency)
		if err != nil || destination.Currency == primary.Currency && received.Minor() != amount.Minor() {
			return p, access.ErrInvalid
		}
		p.effects = append(p.effects, plannedEffect{account: destination, kind: "transfer_in", amount: received, tracked: v.Date >= destination.Opening.Date})
	} else {
		parts := v.Parts
		if len(parts) == 0 {
			parts = []CategoryPart{{AmountMinor: v.Applied.AmountMinor}}
		}
		total, _ := money.New(0, amount.Currency())
		for _, part := range parts {
			n, err := positiveMinor(part.AmountMinor, primary.Currency)
			if err != nil {
				return p, err
			}
			total, err = money.Add(total, n)
			if err != nil {
				return p, access.ErrInvalid
			}
			direction, componentAmount := v.Kind, n.Minor()
			if v.Kind == "refund" {
				direction, componentAmount = "expense", -componentAmount
			}
			if err := validateOperationReference(references, part.CategoryID, "category", direction); err != nil {
				return p, err
			}
			p.components = append(p.components, plannedComponent{effect: 0, direction: direction, amount: componentAmount, category: part.CategoryID, exchange: v.ExchangeReferenceID})
		}
		if total.Minor() != amount.Minor() {
			return p, access.ErrInvalid
		}
		if err := historical(v.ExchangeReferenceID, primary.Currency); err != nil {
			return p, err
		}
	}
	// Transfer fees follow the two principal effects. The ordinal stays int32
	// throughout; input validation bounds the number of fees to two.
	feeOrdinal := int32(2)
	for _, fee := range v.Fees {
		a := p.accounts[fee.AccountID]
		n, err := positiveMinor(fee.AmountMinor, a.Currency)
		if err != nil {
			return p, err
		}
		if err := validateOperationReference(references, fee.CategoryID, "category", "expense"); err != nil {
			return p, err
		}
		if err := historical(fee.ExchangeReferenceID, a.Currency); err != nil {
			return p, err
		}
		p.components = append(p.components, plannedComponent{effect: feeOrdinal, direction: "expense", amount: n.Minor(), category: fee.CategoryID, exchange: fee.ExchangeReferenceID})
		negative, _ := money.Multiply(n, -1)
		p.effects = append(p.effects, plannedEffect{account: a, kind: "fee", amount: negative, tracked: v.Date >= a.Opening.Date})
		feeOrdinal++
	}
	return p, nil
}

func validateOperationReference(references map[string]CatalogView, id, kind, direction string) error {
	if id == "" {
		return nil // Known facts may remain unclassified.
	}
	c, ok := references[id]
	if !ok {
		return access.ErrNotFound
	}
	if c.Kind != kind || c.Archived || c.Value.Direction != direction {
		return access.ErrInvalid
	}
	return nil
}

func validateHistoricalReference(ctx context.Context, tx pgx.Tx, space, id, from, to string, date time.Time) error {
	if id == "" {
		return nil // Missing is explicit, never an implicit 1:1 conversion.
	}
	r, err := financedb.New(tx).ExchangeReference(ctx, financedb.ExchangeReferenceParams{SpaceID: space, ID: id})
	if err != nil {
		return absent(err)
	}
	if from == to || r.FromCurrency != from || r.ToCurrency != to || r.Context != "historical" || !r.EffectiveDate.Time.Equal(date) {
		return access.ErrInvalid
	}
	return nil
}

func (s *Service) CreateOperation(ctx context.Context, c access.SpaceCommand, v OperationCreate) (string, error) {
	if err := v.validate(); err != nil {
		return "", err
	}
	c.Kind, c.Capability, c.Content = "operation.create", "finance.write", v
	return s.access.RunSpaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership, ref string) error {
		if m.Role == "reader" {
			return access.ErrForbidden
		}
		// Replay rechecks current data access, but does not attempt another write
		// or require the account to remain open after the original commit.
		if ref != "" {
			_, err := financedb.New(tx).Operation(ctx, financedb.OperationParams{SpaceID: c.SpaceID, ID: ref, MemberID: m.ID})
			return absent(err)
		}
		for _, id := range operationAccountIDs(v) {
			if _, err := s.AuthorizeAccount(ctx, tx, actor, c.SpaceID, id, "read"); err != nil {
				return err
			}
		}
		return nil
	}, func(ctx context.Context, tx pgx.Tx, actor access.Actor, _ accessdb.Membership) (string, error) {
		id, err := s.createOperation(ctx, tx, actor, c.SpaceID, v)
		if err != nil {
			return "", err
		}
		return id, audit(ctx, tx, actor, c.SpaceID, c.Kind, id)
	})
}

// createOperation participates in the caller's transaction so a composed command
// cannot commit the financial fact separately from its applications.
func (s *Service) createOperation(ctx context.Context, tx pgx.Tx, actor access.Actor, space string, v OperationCreate) (string, error) {
	p, err := s.planOperation(ctx, tx, actor, space, v, nil)
	if err != nil {
		return "", err
	}
	id := uuid.New().String()
	q := financedb.New(tx)
	if err := q.InsertOperation(ctx, financedb.InsertOperationParams{ID: id, SpaceID: space, Kind: v.Kind, Author: actor.ID()}); err != nil {
		return "", err
	}
	if err := insertPlannedRevision(ctx, tx, space, id, 1, actor.ID(), "", "initial", v, p); err != nil {
		return "", err
	}
	balances, err := correctedBalances(p.accounts, nil, p.effects)
	if err != nil {
		return "", err
	}
	if err := setAccountProjections(ctx, tx, space, balances); err != nil {
		return "", err
	}
	if err := q.MarkAffectedReconciliations(ctx, financedb.MarkAffectedReconciliationsParams{SpaceID: space, OperationID: id, NewRevision: 1}); err != nil {
		return "", err
	}
	return id, nil
}

type PositionEffectView struct {
	AccountID   string `json:"account_id"`
	Kind        string `json:"kind"`
	AmountMinor string `json:"amount_minor"`
	Currency    string `json:"currency"`
	Scale       int32  `json:"scale"`
	InTracking  bool   `json:"in_tracking"`
}
type EconomicComponentView struct {
	EffectOrdinal       int32  `json:"effect_ordinal"`
	Direction           string `json:"direction"`
	AmountMinor         string `json:"amount_minor"`
	CategoryID          string `json:"category_id,omitempty"`
	CanonicalCategoryID string `json:"canonical_category_id,omitempty"`
	ExchangeReferenceID string `json:"exchange_reference_id,omitempty"`
}
type OperationTagView struct {
	ID          string `json:"id"`
	CanonicalID string `json:"canonical_id"`
}
type OperationView struct {
	ID             string                  `json:"id"`
	Kind           string                  `json:"kind"`
	Date           string                  `json:"date"`
	Revision       int64                   `json:"revision"`
	Author         string                  `json:"author"`
	RecordedAt     time.Time               `json:"recorded_at"`
	Origin         string                  `json:"origin"`
	Note           string                  `json:"note"`
	Exceptional    bool                    `json:"exceptional"`
	MerchantID     string                  `json:"merchant_id,omitempty"`
	CounterpartyID string                  `json:"counterparty_id,omitempty"`
	Original       *OriginalPrice          `json:"original,omitempty"`
	Effects        []PositionEffectView    `json:"effects"`
	Components     []EconomicComponentView `json:"components"`
	Tags           []OperationTagView      `json:"tags"`
	ChangeKind     string                  `json:"change_kind"`
	Reason         string                  `json:"reason"`
	Voided         bool                    `json:"voided"`
	RevisionAuthor string                  `json:"revision_author"`
	RevisedAt      time.Time               `json:"revised_at"`
	Refund         *RefundLink             `json:"refund,omitempty"`
}

func (s *Service) Operation(ctx context.Context, authorization, space, id string) (OperationView, error) {
	return s.OperationRevision(ctx, authorization, space, id, 0)
}

func (s *Service) OperationRevision(ctx context.Context, authorization, space, id string, revision int64) (OperationView, error) {
	var out OperationView
	if revision < 0 {
		return out, access.ErrInvalid
	}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		var err error
		out, err = readOperation(ctx, tx, space, id, m.ID, revision)
		return err
	})
	return out, err
}

func readOperation(ctx context.Context, tx pgx.Tx, space, id, member string, revision int64) (OperationView, error) {
	q := financedb.New(tx)
	row, err := q.Operation(ctx, financedb.OperationParams{SpaceID: space, ID: id, MemberID: member, RequestedRevision: revision})
	if err != nil {
		return OperationView{}, absent(err)
	}
	o, r := row.FinancialOperation, row.OperationRevision
	out := OperationView{ID: o.ID, Kind: o.Kind, Date: r.EffectiveDate.Time.Format(time.DateOnly), Revision: r.Revision, Author: o.Author, RecordedAt: o.CreatedAt.Time, Origin: o.Origin, Note: r.Note, Exceptional: r.Exceptional, MerchantID: r.MerchantID.String, CounterpartyID: r.CounterpartyID.String, Effects: []PositionEffectView{}, Components: []EconomicComponentView{}, Tags: []OperationTagView{}, ChangeKind: r.ChangeKind, Reason: r.Reason, Voided: r.Voided, RevisionAuthor: r.Author, RevisedAt: r.RecordedAt.Time}
	if r.OriginalMinor.Valid {
		out.Original = &OriginalPrice{AmountMinor: strconv.FormatInt(r.OriginalMinor.Int64, 10), Currency: r.OriginalCurrency.String}
	}
	effects, err := q.OperationEffects(ctx, financedb.OperationEffectsParams{SpaceID: space, OperationID: id, Revision: r.Revision})
	if err != nil {
		return out, err
	}
	for _, row := range effects {
		e := row.PositionEffect
		out.Effects = append(out.Effects, PositionEffectView{AccountID: e.AccountID, Kind: e.Kind, AmountMinor: strconv.FormatInt(e.AmountMinor, 10), Currency: row.Currency, Scale: row.Scale, InTracking: e.InTracking})
	}
	components, err := q.OperationComponents(ctx, financedb.OperationComponentsParams{SpaceID: space, OperationID: id, Revision: r.Revision})
	if err != nil {
		return out, err
	}
	for _, part := range components {
		out.Components = append(out.Components, EconomicComponentView{EffectOrdinal: part.EffectOrdinal, Direction: part.Direction, AmountMinor: strconv.FormatInt(part.AmountMinor, 10), CategoryID: part.CategoryID.String, CanonicalCategoryID: part.CanonicalCategoryID, ExchangeReferenceID: part.ExchangeReferenceID.String})
	}
	tags, err := q.OperationTags(ctx, financedb.OperationTagsParams{SpaceID: space, OperationID: id, Revision: r.Revision})
	if err != nil {
		return out, err
	}
	for _, tag := range tags {
		out.Tags = append(out.Tags, OperationTagView{ID: tag.TagID, CanonicalID: tag.CanonicalTagID})
	}
	link, err := q.RefundLink(ctx, financedb.RefundLinkParams{SpaceID: space, OperationID: id, Revision: r.Revision, MemberID: member})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if err == nil {
		out.Refund = &RefundLink{OperationID: link.PurchaseID, AmountMinor: strconv.FormatInt(link.PurchaseMinor, 10)}
	}
	return out, nil
}
