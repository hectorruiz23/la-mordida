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
type Window struct {
	From time.Time
	To   time.Time
}

// Reader is the source contract: it yields neutral publications in the window.
// The fixture publisher and the PLACSP publisher implement this same contract.
type Reader interface {
	Read(ctx context.Context, window Window, yield func(domain.Publication) error) error
}
