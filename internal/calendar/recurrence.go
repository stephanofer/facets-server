package calendar

import (
	"errors"
	"time"
)

// Recurrence uses nominal calendar anchors. Interval applies only to the
// every_weeks/every_months frequencies; Days applies only to twice_monthly.
type Recurrence struct {
	Start     string `json:"start" format:"date"`
	Frequency string `json:"frequency" enum:"weekly,biweekly,monthly,twice_monthly,yearly,every_weeks,every_months"`
	Interval  int    `json:"interval" minimum:"0" maximum:"1200"`
	FirstDay  int    `json:"first_day" minimum:"0" maximum:"31"`
	SecondDay int    `json:"second_day" minimum:"0" maximum:"31"`
	End       string `json:"end,omitempty" format:"date"`
	Count     int    `json:"count" minimum:"0" maximum:"100000" doc:"Zero means no count limit; end is inclusive."`
}

func (r Recurrence) Validate() error {
	start, err := Parse(r.Start)
	if err != nil || r.Count < 0 || r.Count > 100000 {
		return errors.New("invalid recurrence")
	}
	if r.End != "" {
		end, err := Parse(r.End)
		if err != nil || end.Before(start) {
			return errors.New("invalid recurrence end")
		}
	}
	if r.Frequency == "twice_monthly" {
		if r.Interval != 0 || r.FirstDay < 1 || r.SecondDay > 31 || r.FirstDay >= r.SecondDay {
			return errors.New("invalid nominal days")
		}
		return nil
	}
	if r.FirstDay != 0 || r.SecondDay != 0 {
		return errors.New("unexpected nominal days")
	}
	switch r.Frequency {
	case "weekly", "biweekly", "monthly", "yearly":
		if r.Interval == 0 {
			return nil
		}
	case "every_weeks", "every_months":
		if r.Interval >= 1 && r.Interval <= 1200 {
			return nil
		}
	}
	return errors.New("invalid recurrence frequency or interval")
}

type Occurrence struct {
	Ordinal int    `json:"ordinal"`
	Date    string `json:"date"`
	Slot    int    `json:"slot" doc:"Distinct nominal slots remain distinct even on the same date."`
}

// Window returns at most 108 occurrences in a bounded, half-open civil window.
// Count is measured from Start, not from the requested window. Arithmetic jumps
// directly to the first candidate instead of walking years of prior history.
func (r Recurrence) Window(from, until string) ([]Occurrence, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	a, err := Parse(from)
	if err != nil {
		return nil, err
	}
	b, err := Parse(until)
	if err != nil || !a.Before(b) || b.utc().Sub(a.utc()) > 366*24*time.Hour {
		return nil, errors.New("invalid recurrence horizon")
	}
	start, _ := Parse(r.Start)
	out := []Occurrence{}
	if !start.Before(b) || r.End != "" && r.End < from {
		return out, nil
	}
	week, months := 0, 0
	switch r.Frequency {
	case "weekly":
		week = 1
	case "biweekly":
		week = 2
	case "every_weeks":
		week = r.Interval
	case "monthly", "twice_monthly":
		months = 1
	case "yearly":
		months = 12
	case "every_months":
		months = r.Interval
	}
	period := 0
	if week > 0 {
		period = max(0, int((a.utc().Unix()-start.utc().Unix())/86400)/(7*week))
	} else {
		period = max(0, ((a.year-start.year)*12+int(a.month-start.month))/months)
	}
	// The first partial month can exclude one or both nominal slots.
	firstSlots := 0
	if r.Frequency == "twice_monthly" {
		for _, day := range []int{r.FirstDay, r.SecondDay} {
			if clippedDay(start, day).Before(start) {
				firstSlots++
			}
		}
	}
	for {
		var dates []Date
		if week > 0 {
			days := period * week * 7
			last := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
			if int64(days) > (last.Unix()-start.utc().Unix())/86400 {
				break // The finite civil calendar has no further occurrences.
			}
			d, err := start.AddDays(days)
			if err != nil {
				return nil, err
			}
			dates = []Date{d}
		} else {
			if (start.year-1)*12+int(start.month)-1+period*months >= 9999*12 {
				break
			}
			anchor, _ := NewAnchor(start)
			d, err := anchor.Month(period * months)
			if err != nil {
				return nil, err
			}
			dates = []Date{d}
			if r.Frequency == "twice_monthly" {
				dates = []Date{clippedDay(d, r.FirstDay), clippedDay(d, r.SecondDay)}
			}
		}
		for slot, d := range dates {
			if d.Before(start) {
				continue
			}
			ordinal := period
			if r.Frequency == "twice_monthly" {
				ordinal = period*2 + slot - firstSlots
			}
			if !d.Before(b) || r.Count > 0 && ordinal >= r.Count || r.End != "" && d.String() > r.End {
				return out, nil
			}
			if !d.Before(a) {
				out = append(out, Occurrence{Ordinal: ordinal, Date: d.String(), Slot: slot})
			}
		}
		period++
	}
	return out, nil
}

func clippedDay(month Date, day int) Date {
	last := time.Date(month.year, month.month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	d, _ := New(month.year, month.month, min(day, last))
	return d
}
