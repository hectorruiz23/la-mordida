package kernel

import (
	"encoding/json"
	"fmt"
	"time"
)

// Date is a civil date: a day on the calendar, not an instant in time. An
// award date is kept here and never folded into a timestamp.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

func NewDate(year int, month time.Month, day int) (Date, error) {
	candidate := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if candidate.Year() != year || candidate.Month() != month || candidate.Day() != day {
		return Date{}, fmt.Errorf("date %04d-%02d-%02d: %w", year, int(month), day, errInvalidDate)
	}
	return Date{Year: year, Month: month, Day: day}, nil
}

var errInvalidDate = fmt.Errorf("invalid civil date")

func ParseDate(raw string) (Date, error) {
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return Date{}, fmt.Errorf("date %q: %w", raw, errInvalidDate)
	}
	return Date{Year: parsed.Year(), Month: parsed.Month(), Day: parsed.Day()}, nil
}

func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day)
}

func (d Date) IsZero() bool {
	return d.Year == 0 || d.Month < time.January || d.Month > time.December || d.Day == 0
}

func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Date) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := ParseDate(raw)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
