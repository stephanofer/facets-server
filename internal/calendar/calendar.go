// Package calendar separates civil dates and nominal anchors from technical instants.
package calendar

import (
	"errors"
	"time"
	_ "time/tzdata" // Keep IANA rules available in minimal deployment images.
)

type Date struct {
	year  int
	month time.Month
	day   int
}

func New(year int, month time.Month, day int) (Date, error) {
	if year < 1 || year > 9999 || month < 1 || month > 12 || day < 1 || day > 31 {
		return Date{}, errors.New("invalid civil date")
	}
	t := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if t.Year() != year || t.Month() != month || t.Day() != day {
		return Date{}, errors.New("invalid civil date")
	}
	return Date{year: year, month: month, day: day}, nil
}
func Parse(s string) (Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil || t.Format(time.DateOnly) != s {
		return Date{}, errors.New("invalid civil date")
	}
	return New(t.Year(), t.Month(), t.Day())
}
func (d Date) String() string {
	if d.year == 0 {
		return ""
	}
	return d.utc().Format(time.DateOnly)
}
func (d Date) Valid() bool            { return d.year != 0 }
func (d Date) Before(other Date) bool { return d.utc().Before(other.utc()) }
func (d Date) utc() time.Time         { return time.Date(d.year, d.month, d.day, 0, 0, 0, 0, time.UTC) }
func (d Date) AddDays(days int) (Date, error) {
	if !d.Valid() || days < -3660000 || days > 3660000 {
		return Date{}, errors.New("invalid date offset")
	}
	t := d.utc().AddDate(0, 0, days)
	return New(t.Year(), t.Month(), t.Day())
}
func (d Date) MarshalText() ([]byte, error) {
	if !d.Valid() {
		return nil, errors.New("invalid civil date")
	}
	return []byte(d.String()), nil
}
func (d *Date) UnmarshalText(b []byte) error {
	v, err := Parse(string(b))
	if err == nil {
		*d = v
	}
	return err
}

type Zone struct{ location *time.Location }

func ParseZone(name string) (Zone, error) {
	if name == "" || name == "Local" {
		return Zone{}, errors.New("IANA zone required")
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		return Zone{}, errors.New("invalid IANA zone")
	}
	return Zone{location: l}, nil
}
func (z Zone) Name() string {
	if z.location == nil {
		return ""
	}
	return z.location.String()
}

type Anchor struct{ origin Date }

func NewAnchor(origin Date) (Anchor, error) {
	if !origin.Valid() {
		return Anchor{}, errors.New("invalid anchor")
	}
	return Anchor{origin: origin}, nil
}

// Month always derives from the original nominal day, never the prior clipped date.
func (a Anchor) Month(offset int) (Date, error) {
	if !a.origin.Valid() || offset < -120000 || offset > 120000 {
		return Date{}, errors.New("invalid month offset")
	}
	t := time.Date(a.origin.year, a.origin.month+time.Month(offset), 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return New(t.Year(), t.Month(), min(a.origin.day, last))
}

type Period struct{ Start, End Date }

func (a Anchor) Period(offset int) (Period, error) {
	s, err := a.Month(offset)
	if err != nil {
		return Period{}, err
	}
	e, err := a.Month(offset + 1)
	if err != nil {
		return Period{}, err
	}
	return Period{Start: s, End: e}, nil
}
func (p Period) Contains(d Date) bool {
	return d.Valid() && p.Start.Valid() && p.End.Valid() && !d.Before(p.Start) && d.Before(p.End)
}

// At resolves civil reminder minutes. Repeated minutes choose the first instant;
// missing minutes choose the first valid minute after the requested civil minute.
// The bounded search also covers IANA whole-day skips without changing Date itself.
func (z Zone) At(d Date, hour, minute int) (time.Time, error) {
	if z.location == nil || !d.Valid() || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return time.Time{}, errors.New("invalid local reminder")
	}
	target := d.utc().Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
	start := target.Add(-26 * time.Hour)
	var best time.Time
	var bestCivil time.Time
	for i := range 79 * 60 {
		instant := start.Add(time.Duration(i) * time.Minute)
		local := instant.In(z.location)
		// Some historical IANA offsets contain seconds rather than whole minutes.
		instant = instant.Add(-time.Duration(local.Second()) * time.Second)
		local = instant.In(z.location)
		civil := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), 0, 0, time.UTC)
		if civil.Equal(target) {
			return instant, nil
		}
		if civil.After(target) && (best.IsZero() || civil.Before(bestCivil)) {
			best, bestCivil = instant, civil
		}
	}
	if best.IsZero() {
		return time.Time{}, errors.New("local reminder outside supported range")
	}
	return best, nil
}
