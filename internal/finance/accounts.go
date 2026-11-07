// Package finance owns financial positions and their authorized organization.
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
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance/financedb"
	"github.com/stephanofer/facets-server/internal/httpapi"
	"github.com/stephanofer/facets-server/internal/money"
)

type Service struct {
	access *access.Service
	jobs   *execution.Store
}

func New(identity *access.Service, jobs *execution.Store) *Service {
	return &Service{access: identity, jobs: jobs}
}

type AccountProposal struct {
	Visibility                   string `json:"visibility" enum:"private,space"`
	SpacePermission              string `json:"space_permission" enum:"read,operate"`
	RequiresExplicitConfirmation bool   `json:"requires_explicit_confirmation"`
	ExistingAccountsUnchanged    bool   `json:"existing_accounts_unchanged"`
}

func (s *Service) AccountProposal(ctx context.Context, authorization, space string) (AccountProposal, error) {
	var out AccountProposal
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		v, err := accessdb.New(tx).GetSpace(ctx, space)
		if err != nil {
			return err
		}
		visibility := "private"
		if v.NewAccountVisibility == "space" {
			visibility = "space"
		}
		if v.NewAccountVisibility == "propose_shared" {
			shared, err := financedb.New(tx).HasOtherMembers(ctx, financedb.HasOtherMembersParams{SpaceID: space, ID: m.ID})
			if err != nil {
				return err
			}
			if shared {
				visibility = "space"
			}
		}
		out = AccountProposal{Visibility: visibility, SpacePermission: "read", RequiresExplicitConfirmation: true, ExistingAccountsUnchanged: true}
		return nil
	})
	return out, err
}

type OpeningReference struct {
	Date        string  `json:"date" format:"date"`
	AmountMinor *string `json:"amount_minor" nullable:"true" maxLength:"20"`
	Quality     string  `json:"quality" enum:"unknown,estimated,confirmed"`
}

type Grant struct {
	MemberID   string `json:"member_id" format:"uuid"`
	Permission string `json:"permission" enum:"read,operate"`
}

type Sharing struct {
	Visibility      string  `json:"visibility" enum:"private,space,selected"`
	SpacePermission string  `json:"space_permission" enum:"read,operate"`
	Grants          []Grant `json:"grants" maxItems:"100"`
}

type AccountCreate struct {
	Name           string           `json:"name" minLength:"1" maxLength:"100"`
	Kind           string           `json:"kind" enum:"cash,bank,savings,wallet,credit_card,simple_debt,loan,financed_purchase,lent_capital,recoverable_advance,service_receivable,refund_receivable"`
	Currency       string           `json:"currency" minLength:"3" maxLength:"3"`
	Institution    string           `json:"institution,omitempty" maxLength:"100"`
	LastDigits     string           `json:"last_digits,omitempty" pattern:"^[0-9]{4}$"`
	CounterpartyID string           `json:"counterparty_id,omitempty" format:"uuid"`
	Opening        OpeningReference `json:"opening"`
	Sharing        Sharing          `json:"sharing"`
}

type AccountEdit struct {
	Name           string `json:"name" minLength:"1" maxLength:"100"`
	Institution    string `json:"institution,omitempty" maxLength:"100"`
	LastDigits     string `json:"last_digits,omitempty" pattern:"^[0-9]{4}$"`
	CounterpartyID string `json:"counterparty_id,omitempty" format:"uuid"`
	State          string `json:"state" enum:"active,archived,closed"`
	Version        int64  `json:"version" minimum:"1"`
}

type AccountView struct {
	ID                string           `json:"id"`
	SpaceID           string           `json:"space_id"`
	Name              string           `json:"name"`
	Kind              string           `json:"kind"`
	Position          string           `json:"position" enum:"liquid,obligation,receivable"`
	Currency          string           `json:"currency"`
	Scale             int32            `json:"scale"`
	ResponsibleMember string           `json:"responsible_member_id"`
	Visibility        string           `json:"visibility"`
	SpacePermission   string           `json:"space_permission"`
	Institution       string           `json:"institution,omitempty"`
	LastDigits        string           `json:"last_digits,omitempty"`
	CounterpartyID    string           `json:"counterparty_id,omitempty"`
	Opening           OpeningReference `json:"opening"`
	BalanceMinor      *string          `json:"balance_minor" nullable:"true"`
	State             string           `json:"state" enum:"active,archived,closed,immobilized"`
	StoredState       string           `json:"stored_state"`
	CanOperate        bool             `json:"can_operate"`
	CanAdminister     bool             `json:"can_administer"`
	Version           int64            `json:"version"`
}

func position(kind string) string {
	switch kind {
	case "cash", "bank", "savings", "wallet":
		return "liquid"
	case "credit_card", "simple_debt", "loan", "financed_purchase":
		return "obligation"
	case "lent_capital", "recoverable_advance", "service_receivable", "refund_receivable":
		return "receivable"
	default:
		return ""
	}
}

func text(s string, max int, optional bool) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && !strings.ContainsAny(s, "\r\n\x00") && (optional || strings.TrimSpace(s) != "")
}

func digits(s string) bool {
	if s == "" {
		return true
	}
	if len(s) != 4 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func absent(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return access.ErrNotFound
	}
	return err
}

func parseOpening(v OpeningReference, currency money.Currency) (pgtype.Date, pgtype.Int8, error) {
	d, err := time.Parse(time.DateOnly, v.Date)
	if err != nil || d.Year() < 1 || d.Year() > 9999 {
		return pgtype.Date{}, pgtype.Int8{}, access.ErrInvalid
	}
	var n pgtype.Int8
	if v.Quality == "unknown" {
		if v.AmountMinor != nil {
			return pgtype.Date{}, n, access.ErrInvalid
		}
	} else {
		if (v.Quality != "confirmed" && v.Quality != "estimated") || v.AmountMinor == nil {
			return pgtype.Date{}, n, access.ErrInvalid
		}
		a, err := money.ParseMinor(*v.AmountMinor, currency)
		if err != nil {
			return pgtype.Date{}, n, access.ErrInvalid
		}
		n = pgtype.Int8{Int64: a.Minor(), Valid: true}
	}
	return pgtype.Date{Time: d, Valid: true}, n, nil
}

func (v Sharing) validate() error {
	if !slices.Contains([]string{"private", "space", "selected"}, v.Visibility) || !slices.Contains([]string{"read", "operate"}, v.SpacePermission) || len(v.Grants) > 100 {
		return access.ErrInvalid
	}
	if v.Visibility != "selected" && len(v.Grants) != 0 {
		return access.ErrInvalid
	}
	if v.Visibility == "selected" && len(v.Grants) == 0 {
		return access.ErrInvalid
	}
	seen := make(map[string]bool, len(v.Grants))
	for _, g := range v.Grants {
		if _, err := uuid.Parse(g.MemberID); err != nil || seen[g.MemberID] || (g.Permission != "read" && g.Permission != "operate") {
			return access.ErrInvalid
		}
		seen[g.MemberID] = true
	}
	return nil
}

func (s *Service) validateGrants(ctx context.Context, tx pgx.Tx, space, responsible string, v Sharing) error {
	ids := make([]string, 0, len(v.Grants))
	grants := make(map[string]string, len(v.Grants))
	for _, g := range v.Grants {
		ids = append(ids, g.MemberID)
		grants[g.MemberID] = g.Permission
	}
	rows, err := financedb.New(tx).GrantMembers(ctx, financedb.GrantMembersParams{SpaceID: space, MemberIds: ids})
	if err != nil {
		return err
	}
	if len(rows) != len(ids) {
		return access.ErrNotFound
	}
	for _, m := range rows {
		if m.ID == responsible || (m.Role == "reader" && grants[m.ID] == "operate") {
			return access.ErrInvalid
		}
	}
	return nil
}

func replaceGrants(ctx context.Context, tx pgx.Tx, space, id string, v Sharing) error {
	q := financedb.New(tx)
	if err := q.ClearAccountGrants(ctx, financedb.ClearAccountGrantsParams{SpaceID: space, AccountID: id}); err != nil {
		return err
	}
	ids := make([]string, 0, len(v.Grants))
	permissions := make([]string, 0, len(v.Grants))
	for _, g := range v.Grants {
		ids = append(ids, g.MemberID)
		permissions = append(permissions, g.Permission)
	}
	return q.PutAccountGrants(ctx, financedb.PutAccountGrantsParams{SpaceID: space, AccountID: id, MemberIds: ids, Permissions: permissions})
}

func audit(ctx context.Context, tx pgx.Tx, a access.Actor, space, kind, id string) error {
	return execution.Audit(ctx, tx, a.ID(), space, kind, id, httpapi.RequestID(ctx))
}

func captureAccount(ctx context.Context, tx pgx.Tx, a access.Actor, space, id string) error {
	q := financedb.New(tx)
	if err := q.CaptureAccountConfiguration(ctx, financedb.CaptureAccountConfigurationParams{SpaceID: space, ID: id, Actor: a.ID()}); err != nil {
		return err
	}
	return q.CaptureAccountGrants(ctx, financedb.CaptureAccountGrantsParams{SpaceID: space, AccountID: id})
}

func (s *Service) CreateAccount(ctx context.Context, c access.SpaceCommand, v AccountCreate) (string, error) {
	currency, err := money.ParseCurrency(v.Currency)
	if err != nil || position(v.Kind) == "" || !text(v.Name, 100, false) || !text(v.Institution, 100, true) || !digits(v.LastDigits) {
		return "", access.ErrInvalid
	}
	d, n, err := parseOpening(v.Opening, currency)
	if err != nil {
		return "", err
	}
	if err := v.Sharing.validate(); err != nil {
		return "", err
	}
	c.Kind, c.Capability, c.Content = "account.create", "finance.write", v
	return s.access.RunSpaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership, ref string) error {
		if m.Role == "reader" {
			return access.ErrForbidden
		}
		if ref != "" {
			_, err := financedb.New(tx).Account(ctx, financedb.AccountParams{SpaceID: c.SpaceID, ID: ref, MemberID: m.ID})
			return absent(err)
		}
		return nil
	}, func(ctx context.Context, tx pgx.Tx, a access.Actor, m accessdb.Membership) (string, error) {
		if err := s.validateGrants(ctx, tx, c.SpaceID, m.ID, v.Sharing); err != nil {
			return "", err
		}
		if v.CounterpartyID != "" {
			if _, err := ResolveCatalog(ctx, tx, c.SpaceID, v.CounterpartyID, "counterparty", true); err != nil {
				return "", err
			}
		}
		id := uuid.New().String()
		err := financedb.New(tx).InsertAccount(ctx, financedb.InsertAccountParams{ID: id, SpaceID: c.SpaceID, Name: v.Name, Kind: v.Kind, Currency: v.Currency, Scale: currency.Scale(), ResponsibleMember: m.ID, Visibility: v.Sharing.Visibility, SpacePermission: v.Sharing.SpacePermission, Institution: v.Institution, LastDigits: v.LastDigits, TrackingDate: d, OpeningMinor: n, Quality: v.Opening.Quality, CounterpartyID: nullable(v.CounterpartyID)})
		if err != nil {
			return "", err
		}
		if err := replaceGrants(ctx, tx, c.SpaceID, id, v.Sharing); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, a, c.SpaceID, c.Kind, id)
	})
}

type accountAccess struct{ ResponsibleActive, GrantedOperate, WriteAvailable bool }

func accountView(v financedb.FinancialAccount, m accessdb.Membership, flags accountAccess) AccountView {
	state := v.State
	if !flags.ResponsibleActive {
		state = "immobilized"
	}
	var opening, balance *string
	if v.OpeningMinor.Valid {
		opening = new(strconv.FormatInt(v.OpeningMinor.Int64, 10))
	}
	if v.BalanceMinor.Valid {
		balance = new(strconv.FormatInt(v.BalanceMinor.Int64, 10))
	}
	return AccountView{
		ID: v.ID, SpaceID: v.SpaceID, Name: v.Name,
		Kind: v.Kind, Position: position(v.Kind), Currency: v.Currency, Scale: v.Scale,
		ResponsibleMember: v.ResponsibleMember,
		Visibility:        v.Visibility, SpacePermission: v.SpacePermission,
		Institution: v.Institution, LastDigits: v.LastDigits, CounterpartyID: v.CounterpartyID.String,
		Opening: OpeningReference{
			Date: v.TrackingDate.Time.Format(time.DateOnly), AmountMinor: opening, Quality: v.Quality,
		},
		BalanceMinor: balance, State: state, StoredState: v.State, Version: v.Version,
		CanOperate:    flags.WriteAvailable && access.ResourcePermission(m.Role, "operate", true, flags.GrantedOperate, false) && state == "active",
		CanAdminister: flags.WriteAvailable && access.ResourcePermission(m.Role, "administer", true, false, m.ID == v.ResponsibleMember),
	}
}

func (s *Service) Account(ctx context.Context, authorization, space, id string) (AccountView, error) {
	var out AccountView
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		v, err := financedb.New(tx).Account(ctx, financedb.AccountParams{SpaceID: space, ID: id, MemberID: m.ID})
		if err != nil {
			return absent(err)
		}
		out = accountView(v.FinancialAccount, m, accountAccess{ResponsibleActive: v.ResponsibleActive, GrantedOperate: v.GrantedOperate, WriteAvailable: v.WriteAvailable})
		return nil
	})
	return out, err
}

func validPage(cursor string, limit int32) error {
	if limit < 1 || limit > 100 {
		return access.ErrInvalid
	}
	if cursor != "" {
		if _, err := uuid.Parse(cursor); err != nil {
			return access.ErrInvalid
		}
	}
	return nil
}

func (s *Service) Accounts(ctx context.Context, authorization, space, cursor string, limit int32) (access.Page[AccountView], error) {
	out := access.Page[AccountView]{Items: []AccountView{}}
	if err := validPage(cursor, limit); err != nil {
		return out, err
	}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		rows, err := financedb.New(tx).Accounts(ctx, financedb.AccountsParams{SpaceID: space, Cursor: cursor, MemberID: m.ID, PageSize: limit + 1})
		if err != nil {
			return err
		}
		out.Items = []AccountView{}
		out.NextCursor = ""
		if len(rows) > int(limit) {
			rows = rows[:limit]
			out.NextCursor = rows[len(rows)-1].FinancialAccount.ID
		}
		for _, r := range rows {
			out.Items = append(out.Items, accountView(r.FinancialAccount, m, accountAccess{ResponsibleActive: r.ResponsibleActive, GrantedOperate: r.GrantedOperate, WriteAvailable: r.WriteAvailable}))
		}
		return nil
	})
	return out, err
}

// AuthorizeAccount is the transaction participant for financial operations. The
// space lock is acquired before the account lock, matching membership revocation.
func (s *Service) AuthorizeAccount(ctx context.Context, tx pgx.Tx, a access.Actor, space, id, permission string) (AccountView, error) {
	capability := "read"
	if permission != "read" {
		capability = "finance.write"
	}
	m, err := s.access.AuthorizeSpace(ctx, tx, a, space, capability)
	if err != nil {
		return AccountView{}, err
	}
	v, err := financedb.New(tx).Account(ctx, financedb.AccountParams{SpaceID: space, ID: id, MemberID: m.ID})
	if err != nil {
		return AccountView{}, absent(err)
	}
	out := accountView(v.FinancialAccount, m, accountAccess{ResponsibleActive: v.ResponsibleActive, GrantedOperate: v.GrantedOperate, WriteAvailable: v.WriteAvailable})
	read := permission == "read"
	operate := permission == "operate" && out.CanOperate
	administer := permission == "administer" && out.CanAdminister
	if read || operate || administer {
		return out, nil
	}
	return AccountView{}, access.ErrForbidden
}

func (s *Service) accountCommand(ctx context.Context, c access.SpaceCommand, id string, content any, sensitive bool, apply func(context.Context, pgx.Tx, access.Actor, accessdb.Membership) (string, error)) (string, error) {
	c.Capability, c.Content = "finance.write", struct {
		ID    string
		Value any
	}{ID: id, Value: content}
	return s.access.RunSpaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, a access.Actor, _ accessdb.Membership, _ string) error {
		if sensitive {
			if err := s.access.RequireRecent(a); err != nil {
				return err
			}
		}
		_, err := s.AuthorizeAccount(ctx, tx, a, c.SpaceID, id, "administer")
		return err
	}, apply)
}

func (s *Service) EditAccount(ctx context.Context, c access.SpaceCommand, id string, v AccountEdit) (string, error) {
	if !text(v.Name, 100, false) || !text(v.Institution, 100, true) || !digits(v.LastDigits) || !slices.Contains([]string{"active", "archived", "closed"}, v.State) {
		return "", access.ErrInvalid
	}
	c.Kind = "account.edit"
	return s.accountCommand(ctx, c, id, v, v.State != "active", func(ctx context.Context, tx pgx.Tx, a access.Actor, _ accessdb.Membership) (string, error) {
		if v.CounterpartyID != "" {
			if _, err := ResolveCatalog(ctx, tx, c.SpaceID, v.CounterpartyID, "counterparty", true); err != nil {
				return "", err
			}
		}
		if err := captureAccount(ctx, tx, a, c.SpaceID, id); err != nil {
			return "", err
		}
		n, err := financedb.New(tx).EditAccount(ctx, financedb.EditAccountParams{SpaceID: c.SpaceID, ID: id, Version: v.Version, Name: v.Name, Institution: v.Institution, LastDigits: v.LastDigits, State: v.State, CounterpartyID: nullable(v.CounterpartyID)})
		if err != nil {
			return "", err
		}
		if err := execution.CheckVersion(n, v.Version); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, a, c.SpaceID, c.Kind, id)
	})
}

func (s *Service) ShareAccount(ctx context.Context, c access.SpaceCommand, id string, v Sharing, version int64) (string, error) {
	if err := v.validate(); err != nil {
		return "", err
	}
	c.Kind = "account.share"
	return s.accountCommand(ctx, c, id, struct {
		Sharing Sharing
		Version int64
	}{Sharing: v, Version: version}, true, func(ctx context.Context, tx pgx.Tx, a access.Actor, m accessdb.Membership) (string, error) {
		if err := s.validateGrants(ctx, tx, c.SpaceID, m.ID, v); err != nil {
			return "", err
		}
		if err := captureAccount(ctx, tx, a, c.SpaceID, id); err != nil {
			return "", err
		}
		n, err := financedb.New(tx).ShareAccount(ctx, financedb.ShareAccountParams{SpaceID: c.SpaceID, ID: id, Version: version, Visibility: v.Visibility, SpacePermission: v.SpacePermission})
		if err != nil {
			return "", err
		}
		if err := execution.CheckVersion(n, version); err != nil {
			return "", err
		}
		if err := replaceGrants(ctx, tx, c.SpaceID, id, v); err != nil {
			return "", err
		}
		if err := accessdb.New(tx).RevokeSpaceArtifacts(ctx, c.SpaceID); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, a, c.SpaceID, c.Kind, id)
	})
}

func (s *Service) TransferAccount(ctx context.Context, c access.SpaceCommand, id, target string, version int64) (string, error) {
	c.Kind = "account.transfer"
	return s.accountCommand(ctx, c, id, struct {
		Target  string
		Version int64
	}{Target: target, Version: version}, true, func(ctx context.Context, tx pgx.Tx, a access.Actor, m accessdb.Membership) (string, error) {
		other, err := accessdb.New(tx).GetMember(ctx, accessdb.GetMemberParams{SpaceID: c.SpaceID, ID: target})
		if err != nil {
			return "", absent(err)
		}
		if other.ID == m.ID || other.Role == "reader" {
			return "", access.ErrInvalid
		}
		// Responsibility cannot be assigned to a person without current account access.
		if _, err := financedb.New(tx).Account(ctx, financedb.AccountParams{SpaceID: c.SpaceID, ID: id, MemberID: target}); err != nil {
			return "", absent(err)
		}
		if err := captureAccount(ctx, tx, a, c.SpaceID, id); err != nil {
			return "", err
		}
		n, err := financedb.New(tx).TransferAccount(ctx, financedb.TransferAccountParams{SpaceID: c.SpaceID, ID: id, Version: version, ResponsibleMember: target})
		if err != nil {
			return "", err
		}
		if err := execution.CheckVersion(n, version); err != nil {
			return "", err
		}
		if err := accessdb.New(tx).RevokeSpaceArtifacts(ctx, c.SpaceID); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, a, c.SpaceID, c.Kind, id)
	})
}

func (s *Service) RecoverAccount(ctx context.Context, c access.SpaceCommand, id string, version int64) (string, error) {
	c.Kind, c.Capability, c.Content = "account.recover", "finance.write", struct {
		ID      string
		Version int64
	}{ID: id, Version: version}
	return s.access.RunSpaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, a access.Actor, m accessdb.Membership, ref string) error {
		if err := s.access.RequireRecent(a); err != nil {
			return err
		}
		if m.Role == "reader" {
			return access.ErrForbidden
		}
		if ref != "" {
			_, err := s.AuthorizeAccount(ctx, tx, a, c.SpaceID, id, "administer")
			return err
		}
		// On a replay the first authorization pass precedes receipt lookup. Permit
		// only the current responsible or the same identity's ended responsibility.
		v, err := financedb.New(tx).Account(ctx, financedb.AccountParams{SpaceID: c.SpaceID, ID: id, MemberID: m.ID})
		if err == nil && v.FinancialAccount.ResponsibleMember == m.ID {
			return nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		_, err = financedb.New(tx).RecoverableAccount(ctx, financedb.RecoverableAccountParams{SpaceID: c.SpaceID, ID: id, PrincipalID: a.ID()})
		return absent(err)
	}, func(ctx context.Context, tx pgx.Tx, a access.Actor, m accessdb.Membership) (string, error) {
		if _, err := financedb.New(tx).RecoverableAccount(ctx, financedb.RecoverableAccountParams{SpaceID: c.SpaceID, ID: id, PrincipalID: a.ID()}); err != nil {
			return "", absent(err)
		}
		if err := captureAccount(ctx, tx, a, c.SpaceID, id); err != nil {
			return "", err
		}
		n, err := financedb.New(tx).TransferAccount(ctx, financedb.TransferAccountParams{SpaceID: c.SpaceID, ID: id, Version: version, ResponsibleMember: m.ID})
		if err != nil {
			return "", err
		}
		if err := execution.CheckVersion(n, version); err != nil {
			return "", err
		}
		if err := accessdb.New(tx).RevokeSpaceArtifacts(ctx, c.SpaceID); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, a, c.SpaceID, c.Kind, id)
	})
}

func (s *Service) AccountSharing(ctx context.Context, authorization, space, id string) (Sharing, error) {
	var out Sharing
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, a access.Actor, _ accessdb.Membership) error {
		v, err := s.AuthorizeAccount(ctx, tx, a, space, id, "administer")
		if err != nil {
			return err
		}
		grants, err := financedb.New(tx).AccountGrants(ctx, financedb.AccountGrantsParams{SpaceID: space, AccountID: id})
		if err != nil {
			return err
		}
		out = Sharing{Visibility: v.Visibility, SpacePermission: v.SpacePermission, Grants: []Grant{}}
		for _, g := range grants {
			out.Grants = append(out.Grants, Grant{MemberID: g.MemberID, Permission: g.Permission})
		}
		return nil
	})
	return out, err
}

type RecoveryReference struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

func (s *Service) RecoverableAccounts(ctx context.Context, authorization, space, cursor string, limit int32) (access.Page[RecoveryReference], error) {
	out := access.Page[RecoveryReference]{Items: []RecoveryReference{}}
	if err := validPage(cursor, limit); err != nil {
		return out, err
	}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, a access.Actor, _ accessdb.Membership) error {
		if err := s.access.RequireRecent(a); err != nil {
			return err
		}
		rows, err := financedb.New(tx).RecoverableAccounts(ctx, financedb.RecoverableAccountsParams{SpaceID: space, PrincipalID: a.ID(), ID: cursor, Limit: limit + 1})
		if err != nil {
			return err
		}
		out.Items = []RecoveryReference{}
		out.NextCursor = ""
		if len(rows) > int(limit) {
			rows = rows[:limit]
			out.NextCursor = rows[len(rows)-1].ID
		}
		for _, row := range rows {
			out.Items = append(out.Items, RecoveryReference{ID: row.ID, Version: row.Version})
		}
		return nil
	})
	return out, err
}
