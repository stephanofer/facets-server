package finance

import (
	"context"
	"strings"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance/financedb"
)

type CatalogValue struct {
	Name      string `json:"name" minLength:"1" maxLength:"100"`
	Direction string `json:"direction,omitempty" enum:"income,expense"`
	ParentID  string `json:"parent_id,omitempty" format:"uuid"`
	Icon      string `json:"icon,omitempty" maxLength:"50"`
	Color     string `json:"color,omitempty" pattern:"^#[0-9A-Fa-f]{6}$"`
}
type CatalogView struct {
	ID         string       `json:"id"`
	Kind       string       `json:"kind"`
	Value      CatalogValue `json:"value"`
	DefaultKey string       `json:"default_key,omitempty"`
	Archived   bool         `json:"archived"`
	MergedInto string       `json:"merged_into,omitempty"`
	Version    int64        `json:"version"`
}

func catalogKind(kind string) bool {
	return kind == "category" || kind == "merchant" || kind == "counterparty" || kind == "tag"
}
func nullable(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }
func color(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		digit := c >= '0' && c <= '9'
		lower := c >= 'a' && c <= 'f'
		upper := c >= 'A' && c <= 'F'
		if !digit && !lower && !upper {
			return false
		}
	}
	return true
}
func (v CatalogValue) validate(kind string) error {
	if !catalogKind(kind) || !text(v.Name, 100, false) {
		return access.ErrInvalid
	}
	if kind != "category" {
		if v.Direction != "" || v.ParentID != "" || v.Icon != "" || v.Color != "" {
			return access.ErrInvalid
		}
		return nil
	}
	if v.Direction != "income" && v.Direction != "expense" || !validIcon(v.Icon) || !color(v.Color) {
		return access.ErrInvalid
	}
	if v.ParentID != "" {
		if _, err := uuid.Parse(v.ParentID); err != nil {
			return access.ErrInvalid
		}
	}
	return nil
}
func catalogView(v financedb.FinancialCatalog) CatalogView {
	return CatalogView{ID: v.ID, Kind: v.Kind, Value: CatalogValue{Name: v.Name, Direction: v.Direction, ParentID: v.ParentID.String, Icon: v.Icon, Color: v.Color}, DefaultKey: v.DefaultKey.String, Archived: v.Archived, MergedInto: v.MergedInto.String, Version: v.Version}
}

func validateParent(ctx context.Context, tx pgx.Tx, space, id string, v CatalogValue) error {
	if v.ParentID == "" {
		return nil
	}
	if id == v.ParentID {
		return access.ErrInvalid
	}
	p, err := financedb.New(tx).CatalogEntry(ctx, financedb.CatalogEntryParams{SpaceID: space, ID: v.ParentID})
	if err != nil {
		return absent(err)
	}
	if p.Kind != "category" || p.Direction != v.Direction || p.ParentID.Valid || p.Archived {
		return access.ErrInvalid
	}
	if id != "" {
		n, err := financedb.New(tx).CatalogChildren(ctx, financedb.CatalogChildrenParams{SpaceID: space, ParentID: nullable(id)})
		if err != nil {
			return err
		}
		if n > 0 {
			return access.ErrInvalid
		}
	}
	return nil
}

// ResolveCatalog preserves original identifiers while resolving consolidations
// to a current entry. Aliases are flattened transactionally, never deleted.
func ResolveCatalog(ctx context.Context, tx pgx.Tx, space, id, kind string, forWrite bool) (CatalogView, error) {
	v, err := financedb.New(tx).CatalogEntry(ctx, financedb.CatalogEntryParams{SpaceID: space, ID: id})
	if err != nil {
		return CatalogView{}, absent(err)
	}
	if v.Kind != kind {
		return CatalogView{}, access.ErrInvalid
	}
	if v.MergedInto.Valid {
		v, err = financedb.New(tx).CatalogEntry(ctx, financedb.CatalogEntryParams{SpaceID: space, ID: v.MergedInto.String})
		if err != nil {
			return CatalogView{}, err
		}
	}
	if forWrite && v.Archived {
		return CatalogView{}, access.ErrInvalid
	}
	return catalogView(v), nil
}

func (s *Service) catalogCommand(ctx context.Context, c access.SpaceCommand, content any, apply func(context.Context, pgx.Tx, access.Actor, accessdb.Membership) (string, error)) (string, error) {
	c.Capability, c.Content = "manage", content
	return s.access.RunSpaceCommand(ctx, c, func(_ context.Context, _ pgx.Tx, _ access.Actor, _ accessdb.Membership, _ string) error { return nil }, apply)
}

func (s *Service) CreateCatalog(ctx context.Context, c access.SpaceCommand, kind string, v CatalogValue) (string, error) {
	if err := v.validate(kind); err != nil {
		return "", err
	}
	c.Kind = "catalog.create"
	return s.catalogCommand(ctx, c, struct {
		Kind  string
		Value CatalogValue
	}{Kind: kind, Value: v}, func(ctx context.Context, tx pgx.Tx, a access.Actor, _ accessdb.Membership) (string, error) {
		if err := validateParent(ctx, tx, c.SpaceID, "", v); err != nil {
			return "", err
		}
		id := uuid.New().String()
		err := financedb.New(tx).InsertCatalog(ctx, financedb.InsertCatalogParams{ID: id, SpaceID: c.SpaceID, Kind: kind, Name: v.Name, Direction: v.Direction, ParentID: nullable(v.ParentID), Icon: v.Icon, Color: v.Color})
		if err != nil {
			return "", err
		}
		return id, audit(ctx, tx, a, c.SpaceID, c.Kind, id)
	})
}

func (s *Service) Catalog(ctx context.Context, authorization, space, kind, cursor string, limit int32) (access.Page[CatalogView], error) {
	out := access.Page[CatalogView]{Items: []CatalogView{}}
	if err := validPage(cursor, limit); err != nil {
		return out, err
	}
	if !catalogKind(kind) {
		return out, access.ErrInvalid
	}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, _ accessdb.Membership) error {
		rows, err := financedb.New(tx).CatalogEntries(ctx, financedb.CatalogEntriesParams{SpaceID: space, Kind: kind, ID: cursor, Limit: limit + 1})
		if err != nil {
			return err
		}
		out.Items = []CatalogView{}
		out.NextCursor = ""
		if len(rows) > int(limit) {
			rows = rows[:limit]
			out.NextCursor = rows[len(rows)-1].ID
		}
		for _, row := range rows {
			out.Items = append(out.Items, catalogView(row))
		}
		return nil
	})
	return out, err
}

func (s *Service) CatalogEntry(ctx context.Context, authorization, space, id string) (CatalogView, error) {
	var out CatalogView
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, _ accessdb.Membership) error {
		v, err := financedb.New(tx).CatalogEntry(ctx, financedb.CatalogEntryParams{SpaceID: space, ID: id})
		if err != nil {
			return absent(err)
		}
		out = catalogView(v)
		return nil
	})
	return out, err
}

func (s *Service) EditCatalog(ctx context.Context, c access.SpaceCommand, id string, v CatalogValue, archived bool, version int64) (string, error) {
	c.Kind = "catalog.edit"
	return s.catalogCommand(ctx, c, struct {
		ID       string
		Value    CatalogValue
		Archived bool
		Version  int64
	}{ID: id, Value: v, Archived: archived, Version: version}, func(ctx context.Context, tx pgx.Tx, a access.Actor, _ accessdb.Membership) (string, error) {
		q := financedb.New(tx)
		old, err := q.CatalogEntry(ctx, financedb.CatalogEntryParams{SpaceID: c.SpaceID, ID: id})
		if err != nil {
			return "", absent(err)
		}
		if err := v.validate(old.Kind); err != nil {
			return "", err
		}
		if v.Direction != old.Direction || old.MergedInto.Valid {
			return "", access.ErrInvalid
		}
		if err := validateParent(ctx, tx, c.SpaceID, id, v); err != nil {
			return "", err
		}
		if archived && !old.Archived {
			children, err := q.CatalogActiveChildren(ctx, financedb.CatalogActiveChildrenParams{SpaceID: c.SpaceID, ParentID: nullable(id)})
			if err != nil {
				return "", err
			}
			if children > 0 {
				return "", access.ErrInvalid
			}
		}
		if err := q.CaptureCatalogConfiguration(ctx, financedb.CaptureCatalogConfigurationParams{SpaceID: c.SpaceID, ID: id, Actor: a.ID()}); err != nil {
			return "", err
		}
		n, err := q.EditCatalog(ctx, financedb.EditCatalogParams{SpaceID: c.SpaceID, ID: id, Version: version, Name: v.Name, ParentID: nullable(v.ParentID), Icon: v.Icon, Color: v.Color, Archived: archived})
		if err != nil {
			return "", err
		}
		if err := execution.CheckVersion(n, version); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, a, c.SpaceID, c.Kind, id)
	})
}

type CatalogMerge struct {
	TargetID      string `json:"target_id" format:"uuid"`
	Version       int64  `json:"version" minimum:"1"`
	TargetVersion int64  `json:"target_version" minimum:"1"`
	Confirm       bool   `json:"confirm"`
}

func (s *Service) MergeCatalog(ctx context.Context, c access.SpaceCommand, id string, v CatalogMerge) (string, error) {
	if !v.Confirm || id == v.TargetID {
		return "", access.ErrInvalid
	}
	c.Kind = "catalog.merge"
	return s.catalogCommand(ctx, c, struct {
		ID    string
		Merge CatalogMerge
	}{ID: id, Merge: v}, func(ctx context.Context, tx pgx.Tx, a access.Actor, _ accessdb.Membership) (string, error) {
		q := financedb.New(tx)
		old, err := q.CatalogEntry(ctx, financedb.CatalogEntryParams{SpaceID: c.SpaceID, ID: id})
		if err != nil {
			return "", absent(err)
		}
		target, err := q.CatalogEntry(ctx, financedb.CatalogEntryParams{SpaceID: c.SpaceID, ID: v.TargetID})
		if err != nil {
			return "", absent(err)
		}
		if old.Kind != target.Kind || old.Direction != target.Direction || old.MergedInto.Valid || target.Archived || target.ParentID.String == old.ID {
			return "", access.ErrInvalid
		}
		if err := execution.CheckVersion(1, v.TargetVersion); err != nil {
			return "", err
		}
		if target.Version != v.TargetVersion {
			return "", execution.ErrVersion
		}
		children, err := q.CatalogChildren(ctx, financedb.CatalogChildrenParams{SpaceID: c.SpaceID, ParentID: nullable(id)})
		if err != nil {
			return "", err
		}
		if children > 0 && target.ParentID.Valid {
			return "", access.ErrInvalid
		}
		if err := q.CaptureCatalogMergeConfiguration(ctx, financedb.CaptureCatalogMergeConfigurationParams{SpaceID: c.SpaceID, ID: id, Actor: a.ID()}); err != nil {
			return "", err
		}
		if err := q.ReparentCategories(ctx, financedb.ReparentCategoriesParams{SpaceID: c.SpaceID, ParentID: nullable(id), ParentID_2: nullable(v.TargetID)}); err != nil {
			return "", err
		}
		n, err := q.MergeCatalog(ctx, financedb.MergeCatalogParams{SpaceID: c.SpaceID, ID: id, Version: v.Version, MergedInto: nullable(v.TargetID)})
		if err != nil {
			return "", err
		}
		if err := execution.CheckVersion(n, v.Version); err != nil {
			return "", err
		}
		if err := q.RepointCatalogAliases(ctx, financedb.RepointCatalogAliasesParams{SpaceID: c.SpaceID, MergedInto: nullable(id), MergedInto_2: nullable(v.TargetID)}); err != nil {
			return "", err
		}
		if err := q.RecordCatalogMerge(ctx, financedb.RecordCatalogMergeParams{SpaceID: c.SpaceID, SourceID: id, TargetID: v.TargetID, Actor: a.ID()}); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, a, c.SpaceID, c.Kind, id)
	})
}

// SeedDefaults is a space-creation transaction participant. Keys, not translated
// display names, identify defaults; retries never overwrite edited categories.
func SeedDefaults(ctx context.Context, tx pgx.Tx, space string) error {
	for _, v := range []struct{ key, name, direction, icon, color string }{
		{key: "salary", name: "Salary", direction: "income", icon: "briefcase", color: "#4DA568"},
		{key: "other_income", name: "Other income", direction: "income", icon: "plus", color: "#4DA568"},
		{key: "food", name: "Food", direction: "expense", icon: "utensils", color: "#E99537"},
		{key: "housing", name: "Housing", direction: "expense", icon: "home", color: "#6471EB"},
		{key: "transport", name: "Transport", direction: "expense", icon: "bus", color: "#DB5A54"},
		{key: "health", name: "Health", direction: "expense", icon: "heart", color: "#DF4E92"},
		{key: "financial_costs", name: "Financial costs", direction: "expense", icon: "landmark", color: "#737373"},
	} {
		if err := financedb.New(tx).SeedCatalog(ctx, financedb.SeedCatalogParams{ID: uuid.New().String(), SpaceID: space, Name: v.name, Direction: v.direction, Icon: v.icon, Color: v.color, DefaultKey: nullable(v.key)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) InitializeCatalog(ctx context.Context, c access.SpaceCommand) (string, error) {
	c.Kind = "catalog.initialize"
	return s.catalogCommand(ctx, c, struct{ Confirm bool }{Confirm: true}, func(ctx context.Context, tx pgx.Tx, a access.Actor, _ accessdb.Membership) (string, error) {
		if err := SeedDefaults(ctx, tx, c.SpaceID); err != nil {
			return "", err
		}
		return c.SpaceID, audit(ctx, tx, a, c.SpaceID, c.Kind, c.SpaceID)
	})
}

func validIcon(v string) bool {
	if !text(v, 50, false) {
		return false
	}
	for _, c := range v {
		letter := c >= 'a' && c <= 'z'
		digit := c >= '0' && c <= '9'
		if !letter && !digit && !strings.ContainsRune("-_", c) {
			return false
		}
	}
	return true
}
