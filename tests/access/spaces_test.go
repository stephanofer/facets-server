//go:build integration

package access_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
)

func TestSpaceListingValidityClock(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "clock@example.test")
	q := accessdb.New(f.store.DB.Pool)
	// Both windows are deliberately far from wall time: listing must use the
	// application's supplied instant, not PostgreSQL's clock.
	for _, year := range []int{2000, 2100} {
		start := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
		end := start.Add(time.Hour)
		t.Run(start.Format("2006"), func(t *testing.T) {
			if _, err := f.db.Pool.Exec(t.Context(), `UPDATE space_access SET valid_from=$2,valid_until=$3 WHERE space_id=$1`, u.SpaceID, start, end); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name string
				at   time.Time
				want int
			}{
				{name: "before start", at: start.Add(-time.Microsecond)},
				{name: "at start", at: start, want: 1},
				{name: "before expiry", at: end.Add(-time.Microsecond), want: 1},
				{name: "at expiry", at: end},
				{name: "after expiry", at: end.Add(time.Microsecond)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					rows, err := q.ListSpaces(t.Context(), accessdb.ListSpacesParams{
						PrincipalID: u.ID, Limit: 50,
						AsOf: pgtype.Timestamptz{Time: tc.at, Valid: true},
					})
					if err != nil {
						t.Fatal(err)
					}
					if len(rows) != tc.want {
						t.Fatalf("space count = %d; want %d", len(rows), tc.want)
					}
					if len(rows) == 1 && rows[0].ID != u.SpaceID {
						t.Fatal("listed another space")
					}
				})
			}
		})
	}
}
