//go:build integration

package access_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/finance"
	"github.com/stephanofer/facets-server/internal/httpapi"
)

func TestFinancialPermissionRevocation(t *testing.T) {
	f := setup(t)
	owner := f.newUser(t, "scope-owner@example.test")
	member := f.newUser(t, "scope-member@example.test")
	memberID := f.join(t, owner, member, "member")
	s := finance.New(f.service)
	base := "/v1/spaces/" + owner.SpaceID
	input := accountInput("selected")
	input.Sharing.Grants = []finance.Grant{{MemberID: memberID, Permission: "operate"}}
	id := f.request[financeReference](t, "POST", base+"/accounts", owner.Token, "create-selected", input, 200).ID
	var snapshot access.ScopeSnapshot
	err := f.store.DB.Transaction(t.Context(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		a, err := f.service.AuthenticateCustomer(ctx, tx, "Bearer "+member.Token)
		if err != nil {
			return err
		}
		v, err := s.AuthorizeAccount(ctx, tx, a, owner.SpaceID, id, "operate")
		if err != nil {
			return err
		}
		if !v.CanOperate || v.CanAdminister {
			return errors.New("operation permission unexpectedly includes administration")
		}
		snapshot, err = f.service.CaptureScope(ctx, tx, a, owner.SpaceID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	hold, err := f.db.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(context.WithoutCancel(t.Context())) }()
	if _, err := hold.Exec(t.Context(), "SELECT id FROM principals WHERE id=$1 FOR UPDATE", member.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- f.store.DB.Transaction(t.Context(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			a, err := f.service.AuthenticateCustomer(ctx, tx, "Bearer "+member.Token)
			if err != nil {
				return err
			}
			_, err = s.AuthorizeAccount(ctx, tx, a, owner.SpaceID, id, "operate")
			return err
		})
	}()
	blocked := false
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if f.count(t, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'") > 0 {
			blocked = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("participant never waited for current authorization")
	}
	sharing := finance.Sharing{Visibility: "private", SpacePermission: "read", Grants: []finance.Grant{}}
	if _, err := s.ShareAccount(t.Context(), access.SpaceCommand{Authorization: "Bearer " + owner.Token, SpaceID: owner.SpaceID, Key: "revoke-grant"}, id, sharing, 1); err != nil {
		t.Fatal(err)
	}
	if err := hold.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, access.ErrNotFound) {
			t.Fatal("participant survived account revocation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("participant did not terminate")
	}
	err = f.store.DB.Transaction(t.Context(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		a, err := f.service.AuthenticateCustomer(ctx, tx, "Bearer "+member.Token)
		if err != nil {
			return err
		}
		return f.service.RevalidateScope(ctx, tx, a, snapshot)
	})
	if !errors.Is(err, access.ErrNotFound) {
		t.Fatal("old artifact scope survived account revocation", err)
	}
	// Re-grant the current episode, then end it and rejoin: no selected grant revives.
	if _, err := s.ShareAccount(t.Context(), access.SpaceCommand{Authorization: "Bearer " + owner.Token, SpaceID: owner.SpaceID, Key: "regrant"}, id, input.Sharing, 2); err != nil {
		t.Fatal(err)
	}
	f.request[map[string]any](t, "POST", base+"/members/"+memberID+"/remove", owner.Token, "remove-grantee", map[string]any{"confirm": true}, 200)
	newID := f.join(t, owner, member, "member")
	if newID == memberID {
		t.Fatal("membership identity reused")
	}
	f.request[httpapi.Problem](t, "GET", base+"/accounts/"+id, member.Token, "", nil, 404)
	if f.count(t, "SELECT count(*) FROM account_grant_history WHERE account_id=$1", id) != 1 {
		t.Fatal("prior grant history was not preserved")
	}
	if _, err := f.store.DB.Pool.Exec(t.Context(), "DELETE FROM account_configuration_history WHERE account_id=$1", id); err == nil {
		t.Fatal("runtime can destroy configuration history")
	}
	// Space administration is independent of financial responsibility and billing.
	f.request[httpapi.Problem](t, "POST", base+"/catalog", member.Token, "member-catalog", map[string]any{"kind": "tag", "value": finance.CatalogValue{Name: "Not authorized"}}, 403)
}

func TestFinancialResponsibilityTransfer(t *testing.T) {
	f := setup(t)
	owner := f.newUser(t, "transfer-owner@example.test")
	member := f.newUser(t, "transfer-member@example.test")
	memberID := f.join(t, owner, member, "member")
	base := "/v1/spaces/" + owner.SpaceID
	id := f.request[financeReference](t, "POST", base+"/accounts", owner.Token, "private-transfer", accountInput("private"), 200).ID
	transfer := map[string]any{"member_id": memberID, "version": 1, "confirm": true}
	f.request[httpapi.Problem](t, "POST", base+"/accounts/"+id+"/responsibility", owner.Token, "no-target-access", transfer, 404)
	sharing := finance.Sharing{Visibility: "selected", SpacePermission: "read", Grants: []finance.Grant{{MemberID: memberID, Permission: "read"}}}
	f.request[map[string]any](t, "PUT", base+"/accounts/"+id+"/sharing", owner.Token, "share-before-transfer", map[string]any{"sharing": sharing, "version": 1, "confirm": true}, 200)
	transfer["version"] = 2
	f.request[map[string]any](t, "POST", base+"/accounts/"+id+"/responsibility", owner.Token, "transfer", transfer, 200)
	v := f.request[finance.AccountView](t, "GET", base+"/accounts/"+id, member.Token, "", nil, 200)
	if !v.CanAdminister || !v.CanOperate || v.ResponsibleMember != memberID || v.Version != 3 || v.BalanceMinor == nil || *v.BalanceMinor != "-12000" {
		t.Fatal("responsibility transfer changed position or failed to grant administration")
	}
	f.request[httpapi.Problem](t, "GET", base+"/accounts/"+id, owner.Token, "", nil, 404)
	// Replaying a sensitive result is not an authorization bypass after transfer.
	f.request[httpapi.Problem](t, "POST", base+"/accounts/"+id+"/responsibility", owner.Token, "transfer", transfer, 404)
	if f.count(t, "SELECT count(*) FROM space_access WHERE space_id=$1 AND billing_member=$2", owner.SpaceID, owner.MemberID) != 1 {
		t.Fatal("financial responsibility changed billing")
	}
	if f.count(t, "SELECT count(*) FROM account_configuration_history WHERE account_id=$1", id) != 2 {
		t.Fatal("responsibility/access history missing")
	}
}
