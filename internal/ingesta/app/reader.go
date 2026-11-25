// Package app consumes a source reader and stores the neutral publications it
// yields. The reader contract lives here, in the consumer, so a fixture and
// PLACSP can both satisfy it without the consumer knowing either one.
package app

import (
	"context"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/ingesta/domain"
)

// Window is the period of source updates a read covers. A zero bound is open.
// ToExclusive makes To a half-open upper bound, as a date range asks: a
// publication stamped exactly at the bound belongs to the next period.
type Window struct {
	From        time.Time
	To          time.Time
	ToExclusive bool
}

// Contains reports whether an update instant falls inside the window.
func (w Window) Contains(instant time.Time) bool {
	if !w.From.IsZero() && instant.Before(w.From) {
		return false
	}
	if w.To.IsZero() {
		return true
	}
	if w.ToExclusive {
		return instant.Before(w.To)
	}
	return !instant.After(w.To)
}

// Reader is the source contract: it yields neutral publications in the window.
// The fixture publisher and the PLACSP publisher implement this same contract.
type Reader interface {
	Read(ctx context.Context, window Window, yield func(domain.Publication) error) error
}
