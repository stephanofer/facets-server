package finance

import (
	"testing"
	"time"
)

func TestReminderTiming(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, date, zone, want string
		days, hour, minute     int32
	}{
		{name: "missing civil time", date: "2026-03-08", zone: "America/New_York", hour: 2, minute: 30, want: "2026-03-08T07:00:00Z"},
		{name: "repeated civil time", date: "2026-11-01", zone: "America/New_York", hour: 1, minute: 30, want: "2026-11-01T05:30:00Z"},
		{name: "civil days across DST", date: "2026-03-09", zone: "America/New_York", days: 1, hour: 2, minute: 30, want: "2026-03-08T07:00:00Z"},
		{name: "month boundary", date: "2027-03-01", zone: "America/Lima", days: 1, hour: 9, want: "2027-02-28T14:00:00Z"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (ReminderTiming{DaysBefore: tt.days, Hour: tt.hour, Minute: tt.minute, Zone: tt.zone}).at(tt.date)
			if err != nil || got.Format(time.RFC3339) != tt.want {
				t.Fatal(got, err)
			}
		})
	}
	for _, tt := range []struct {
		name, date string
		value      ReminderTiming
	}{
		{name: "local zone", date: "2026-11-01", value: ReminderTiming{Zone: "Local"}},
		{name: "unknown zone", date: "2026-11-01", value: ReminderTiming{Zone: "Not/AZone"}},
		{name: "negative offset", date: "2026-11-01", value: ReminderTiming{Zone: "UTC", DaysBefore: -1}},
		{name: "unbounded offset", date: "2026-11-01", value: ReminderTiming{Zone: "UTC", DaysBefore: 367}},
		{name: "invalid hour", date: "2026-11-01", value: ReminderTiming{Zone: "UTC", Hour: 24}},
		{name: "civil underflow", date: "0001-01-01", value: ReminderTiming{Zone: "UTC", DaysBefore: 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.value.at(tt.date); err == nil {
				t.Fatal("accepted invalid timing")
			}
		})
	}
}
