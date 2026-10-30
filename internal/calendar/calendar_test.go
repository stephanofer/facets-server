package calendar_test

import (
	"github.com/stephanofer/facets-server/internal/calendar"
	"testing"
)

func TestDateAndAnchors(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"2025-02-29", "2026-13-01", "0000-01-01", "2026-1-01", "2026-02-30"} {
		if _, err := calendar.Parse(s); err == nil {
			t.Fatalf("accepted %s", s)
		}
	}
	d, _ := calendar.Parse("2024-01-31")
	a, _ := calendar.NewAnchor(d)
	for _, tc := range []struct {
		name   string
		offset int
		want   string
	}{{"leap February", 1, "2024-02-29"}, {"nominal day restored", 2, "2024-03-31"}, {"prior month", -1, "2023-12-31"}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a.Month(tc.offset)
			if err != nil || got.String() != tc.want {
				t.Fatal(got, err)
			}
		})
	}
	for i := range 24 {
		p, err := a.Period(i)
		if err != nil {
			t.Fatal(err)
		}
		next, _ := a.Period(i + 1)
		if p.End != next.Start || !p.Contains(p.Start) || p.Contains(p.End) {
			t.Fatal("invalid half-open period")
		}
	}
	leap, _ := calendar.Parse("2024-02-29")
	yearly, _ := calendar.NewAnchor(leap)
	next, _ := yearly.Month(12)
	restored, _ := yearly.Month(48)
	if next.String() != "2025-02-28" || restored.String() != "2028-02-29" {
		t.Fatal(next, restored)
	}
}
func TestZoneResolution(t *testing.T) {
	t.Parallel()
	z, err := calendar.ParseZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, date   string
		hour, minute int
		want         string
	}{{"missing minute", "2026-03-08", 2, 30, "2026-03-08T07:00:00Z"}, {"repeated minute", "2026-11-01", 1, 30, "2026-11-01T05:30:00Z"}} {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := calendar.Parse(tc.date)
			got, err := z.At(d, tc.hour, tc.minute)
			if err != nil || got.UTC().Format("2006-01-02T15:04:05Z") != tc.want {
				t.Fatal(got, err)
			}
		})
	}
	if _, err := calendar.ParseZone("Local"); err == nil {
		t.Fatal("machine-local zone accepted")
	}
}
func FuzzDate(f *testing.F) {
	f.Add("2024-02-29")
	f.Add("invalid")
	f.Fuzz(func(t *testing.T, s string) {
		d, err := calendar.Parse(s)
		if err == nil && d.String() != s {
			t.Fatal("noncanonical date accepted")
		}
	})
}
