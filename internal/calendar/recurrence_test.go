package calendar

import (
	"reflect"
	"testing"
	"time"
)

func TestRecurrenceWindow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		rule        Recurrence
		from, until string
		want        []string
	}{
		{"weekly", Recurrence{Start: "2026-01-31", Frequency: "weekly"}, "2026-01-01", "2026-03-01", []string{"2026-01-31", "2026-02-07", "2026-02-14", "2026-02-21", "2026-02-28"}},
		{"biweekly", Recurrence{Start: "2026-01-31", Frequency: "biweekly"}, "2026-02-01", "2026-03-01", []string{"2026-02-14", "2026-02-28"}},
		{"monthly nominal", Recurrence{Start: "2026-01-31", Frequency: "monthly"}, "2026-01-01", "2026-04-01", []string{"2026-01-31", "2026-02-28", "2026-03-31"}},
		{"two clipped slots", Recurrence{Start: "2026-02-01", Frequency: "twice_monthly", FirstDay: 30, SecondDay: 31}, "2026-02-01", "2026-04-01", []string{"2026-02-28", "2026-02-28", "2026-03-30", "2026-03-31"}},
		{"every weeks", Recurrence{Start: "2026-01-01", Frequency: "every_weeks", Interval: 3}, "2026-01-01", "2026-02-01", []string{"2026-01-01", "2026-01-22"}},
		{"every months", Recurrence{Start: "2026-01-31", Frequency: "every_months", Interval: 2}, "2026-01-01", "2026-06-01", []string{"2026-01-31", "2026-03-31", "2026-05-31"}},
		{"leap restored", Recurrence{Start: "2024-02-29", Frequency: "yearly"}, "2028-01-01", "2029-01-01", []string{"2028-02-29"}},
		{"nonleap", Recurrence{Start: "2024-02-29", Frequency: "yearly"}, "2027-01-01", "2028-01-01", []string{"2027-02-28"}},
		{"count anchored", Recurrence{Start: "2026-01-31", Frequency: "monthly", Count: 2}, "2026-02-01", "2026-05-01", []string{"2026-02-28"}},
		{"inclusive end", Recurrence{Start: "2026-01-01", Frequency: "weekly", End: "2026-01-08"}, "2026-01-01", "2026-02-01", []string{"2026-01-01", "2026-01-08"}},
		{"partial first month count", Recurrence{Start: "2026-01-20", Frequency: "twice_monthly", FirstDay: 1, SecondDay: 25, Count: 3}, "2026-01-01", "2026-04-01", []string{"2026-01-25", "2026-02-01", "2026-02-25"}},
		{"distant origin", Recurrence{Start: "0001-01-31", Frequency: "monthly"}, "9999-02-01", "9999-04-01", []string{"9999-02-28", "9999-03-31"}},
		{"distant weekly origin", Recurrence{Start: "0001-01-01", Frequency: "weekly"}, "9999-12-20", "9999-12-31", []string{"9999-12-20", "9999-12-27"}},
		{"finite yearly calendar", Recurrence{Start: "9999-01-01", Frequency: "yearly"}, "9999-01-01", "9999-12-31", []string{"9999-01-01"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.rule.Window(tc.from, tc.until)
			if err != nil {
				t.Fatal(err)
			}
			dates := []string{}
			for i, o := range got {
				dates = append(dates, o.Date)
				if i > 0 && got[i-1].Ordinal+1 != o.Ordinal {
					t.Fatal("unstable identity", got)
				}
			}
			if !reflect.DeepEqual(dates, tc.want) {
				t.Fatalf("got %v want %v", dates, tc.want)
			}
		})
	}
}

func FuzzRecurrenceWindow(f *testing.F) {
	f.Add(2026, 1, 31, 4, 29, 30, 0)
	f.Add(2024, 2, 29, 3, 1, 15, 5)
	f.Fuzz(func(t *testing.T, year, month, day, frequency, first, second, count int) {
		start, err := New(year, time.Month(month), day)
		if err != nil || frequency < 0 || frequency > 6 || first < 1 || first > 30 || second <= first || second > 31 || count < 0 || count > 100000 {
			return
		}
		mid, err := start.AddDays(180)
		if err != nil {
			return
		}
		end, err := start.AddDays(366)
		if err != nil {
			return
		}
		rule := Recurrence{Start: start.String(), Frequency: []string{"weekly", "biweekly", "monthly", "yearly", "twice_monthly", "every_weeks", "every_months"}[frequency], Count: count}
		if rule.Frequency == "twice_monthly" {
			rule.FirstDay, rule.SecondDay = first, second
		}
		if frequency >= 5 {
			rule.Interval = 3
		}
		all, err := rule.Window(start.String(), end.String())
		if err != nil {
			t.Fatal(err)
		}
		left, err := rule.Window(start.String(), mid.String())
		if err != nil {
			t.Fatal(err)
		}
		right, err := rule.Window(mid.String(), end.String())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(all, append(left, right...)) {
			t.Fatal("window split changed identity or dates", all, left, right)
		}
		if len(all) > 54 {
			t.Fatal("unbounded horizon", len(all))
		}
	})
}

func TestRecurrenceValidation(t *testing.T) {
	t.Parallel()
	for _, rule := range []Recurrence{
		{Start: "2026-02-30", Frequency: "monthly"},
		{Start: "2026-01-01", Frequency: "every_weeks"},
		{Start: "2026-01-01", Frequency: "weekly", Interval: 1},
		{Start: "2026-01-01", Frequency: "monthly", FirstDay: 31},
		{Start: "2026-01-01", Frequency: "twice_monthly", FirstDay: 31, SecondDay: 31},
		{Start: "2026-01-01", Frequency: "twice_monthly", FirstDay: -1, SecondDay: 15},
		{Start: "2026-01-01", Frequency: "daily"},
		{Start: "2026-01-01", Frequency: "yearly", End: "2025-12-31"},
	} {
		t.Run(rule.Frequency+rule.Start, func(t *testing.T) {
			if rule.Validate() == nil {
				t.Fatal("invalid rule accepted", rule)
			}
		})
	}
	rule := Recurrence{Start: "2026-01-01", Frequency: "weekly"}
	for _, until := range []string{"2026-01-01", "2025-12-31", "2027-01-03", "bad"} {
		t.Run(until, func(t *testing.T) {
			if _, err := rule.Window("2026-01-01", until); err == nil {
				t.Fatal("invalid horizon accepted")
			}
		})
	}
}
