package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/ingesta/domain"
)

// Kind tells a historical range from the incremental update of a channel. Both
// go through the same use case: they only differ in where the period starts and
// in the span they record.
type Kind string

const (
	Historical  Kind = "historical"
	Incremental Kind = "incremental"
)

// RunState is the state of one importer invocation.
type RunState string

const (
	RunRunning   RunState = "running"
	RunCompleted RunState = "completed"
	RunPartial   RunState = "partial"
	RunFailed    RunState = "failed"
)

// SpanState is what a span actually covered. A complete span with zero
// publications is a measured zero, never a gap.
type SpanState string

const (
	SpanComplete SpanState = "complete"
	SpanPartial  SpanState = "partial"
	SpanGap      SpanState = "gap"
)

// ErrImportRunning says another importer already holds the single active slot.
var ErrImportRunning = errors.New("another import is already running")

// ErrNoCursor says an incremental load was asked for but no channel was ever
// loaded: importing everything would hide that there is no covered period.
var ErrNoCursor = errors.New("no previous import to resume from")

// Cursor is the last whole point a channel reached. It is kept per source so a
// historical range and the incremental update that follows it share it.
type Cursor struct {
	Source         string
	LastCompleteAt time.Time
	LastPage       string
}

func (c Cursor) IsZero() bool { return c.LastCompleteAt.IsZero() && c.LastPage == "" }

// Page is one whole page the reader finished. The importer only advances the
// cursor when it receives one: a page half read is never a resume point.
type Page struct {
	Location    string
	LastUpdated time.Time
	Entries     int
}

// Resume tells a reader where the previous run stopped. After is an inclusive
// lower bound (the caller widens it by the overlap) and Page names the last
// whole page for readers whose pages are not ordered by time.
type Resume struct {
	After time.Time
	Page  string
	// RecoverBoundary asks the reader to also yield the entries older than the
	// window on a page that straddles its start, so an announcement published
	// before the cut that the next revision needs is not lost.
	RecoverBoundary bool
}

// ResumableReader is a Reader that reports whole pages and can restart from a
// cursor. The fixture and PLACSP both satisfy it.
type ResumableReader interface {
	ReadResumable(ctx context.Context, window Window, resume Resume, yield func(domain.Publication) error, onPage func(Page) error) error
}

// Request is one load. Without SinceLast the period is From..Until; with it
// the period starts at the channel cursor and From is not used.
type Request struct {
	Source          string
	Kind            Kind
	Reader          Reader
	From            time.Time
	Until           time.Time
	UntilExclusive  bool
	SinceLast       bool
	Overlap         time.Duration
	RecoverBoundary bool
}

// Run is one importer invocation as it is persisted.
type Run struct {
	ID             int64
	SpanID         int64
	Source         string
	Kind           Kind
	RequestedFrom  time.Time
	RequestedUntil time.Time
	PeriodFrom     time.Time
	PeriodUntil    time.Time
	UntilExclusive bool
	State          RunState
	StartedAt      time.Time
	FinishedAt     time.Time
	Publications   int
	Pages          int
	Detail         string
}

// Span is the period a run covered, with its counters and last whole point.
type Span struct {
	ID             int64
	RunID          int64
	Source         string
	Kind           Kind
	PeriodFrom     time.Time
	PeriodUntil    time.Time
	UntilExclusive bool
	State          SpanState
	Publications   int
	Pages          int
	LastCompleteAt time.Time
	LastPage       string
	Detail         string
}

// Progress is what the importer records after a whole page.
type Progress struct {
	RunID        int64
	SpanID       int64
	Publications int
	Pages        int
	Cursor       Cursor
}

// RunLog persists runs, spans and cursors. It is implemented over SQLite.
type RunLog interface {
	Begin(ctx context.Context, run Run) (Run, error)
	Record(ctx context.Context, progress Progress) error
	Finish(ctx context.Context, run Run, progress Progress, span SpanState, detail string) error
	Cursor(ctx context.Context, source string) (Cursor, bool, error)
	Spans(ctx context.Context, source string) ([]Span, error)
}

// Report is the outcome of one load.
type Report struct {
	RunID          int64
	Source         string
	Kind           Kind
	State          RunState
	Span           SpanState
	From           time.Time
	Until          time.Time
	UntilExclusive bool
	Publications   int
	Pages          int
	Cursor         Cursor
	Detail         string
}

// Import runs the reader over the requested period and records the run, its
// span and the cursor. A reader failure leaves the last whole page as the
// resume point, so a retry reprocesses the overlap instead of starting over or
// skipping the page that failed.
func (i *Importer) Import(ctx context.Context, request Request) (Report, error) {
	if i.runs == nil {
		return Report{}, errors.New("import: a run log is required")
	}
	if request.Reader == nil {
		return Report{}, errors.New("import: a reader is required")
	}
	if request.Source == "" {
		return Report{}, errors.New("import: a source is required")
	}
	if request.Kind == "" {
		request.Kind = Historical
	}
	if request.Kind != Historical && request.Kind != Incremental {
		return Report{}, fmt.Errorf("import: unknown kind %q", request.Kind)
	}

	report := Report{
		Source:         request.Source,
		Kind:           request.Kind,
		From:           request.From,
		Until:          request.Until,
		UntilExclusive: request.UntilExclusive,
	}

	var cursor Cursor
	if request.SinceLast {
		stored, ok, err := i.runs.Cursor(ctx, request.Source)
		if err != nil {
			return report, err
		}
		if !ok || stored.IsZero() {
			return report, fmt.Errorf("%w for %s", ErrNoCursor, request.Source)
		}
		cursor = stored
		report.From = stored.LastCompleteAt
		if request.From.After(report.From) {
			report.From = request.From
		}
	}

	run := Run{
		Source:         request.Source,
		Kind:           request.Kind,
		RequestedFrom:  request.From,
		RequestedUntil: request.Until,
		PeriodFrom:     report.From,
		PeriodUntil:    request.Until,
		UntilExclusive: request.UntilExclusive,
		State:          RunRunning,
		StartedAt:      i.clock(),
	}
	run, err := i.runs.Begin(ctx, run)
	if err != nil {
		return report, err
	}
	report.RunID = run.ID

	span := Span{
		RunID:          run.ID,
		Source:         request.Source,
		Kind:           request.Kind,
		PeriodFrom:     report.From,
		PeriodUntil:    request.Until,
		UntilExclusive: request.UntilExclusive,
		State:          SpanPartial,
	}
	span.ID = run.SpanID

	progress := Progress{RunID: run.ID, SpanID: span.ID}
	lastSeen := time.Time{}
	stored := 0

	yield := func(publication domain.Publication) error {
		normalized, err := toContratos(publication)
		if err != nil {
			return fmt.Errorf("publication %s/%s: %w", publication.Reference.Source, publication.Reference.ID, err)
		}
		if err := i.sink.StorePublication(ctx, normalized); err != nil {
			return fmt.Errorf("store publication %s/%s: %w", publication.Reference.Source, publication.Reference.ID, err)
		}
		stored++
		if publication.UpdatedAt.After(lastSeen) {
			lastSeen = publication.UpdatedAt
		}
		return nil
	}

	onPage := func(page Page) error {
		progress.Pages++
		progress.Publications = stored
		if !page.LastUpdated.IsZero() && page.LastUpdated.After(progress.Cursor.LastCompleteAt) {
			progress.Cursor.LastCompleteAt = page.LastUpdated
		}
		if !progress.Cursor.LastCompleteAt.After(lastSeen) {
			progress.Cursor.LastCompleteAt = lastSeen
		}
		if page.Location != "" {
			progress.Cursor.LastPage = page.Location
		}
		progress.Cursor.Source = request.Source
		return i.runs.Record(ctx, progress)
	}

	window := Window{From: request.From, To: request.Until, ToExclusive: request.UntilExclusive}
	resume := Resume{Page: cursor.LastPage, RecoverBoundary: request.RecoverBoundary}
	if !cursor.LastCompleteAt.IsZero() {
		resume.After = cursor.LastCompleteAt.Add(-request.Overlap)
		// A page cursor resumes inside the feed: the page is reprocessed and
		// the read continues from it, so the time cursor must not filter the
		// pages that were never read. Only a fresh incremental run, which has
		// no page, uses the time cursor as its lower bound.
		if cursor.LastPage == "" && resume.After.After(window.From) {
			window.From = resume.After
		}
	}

	var readErr error
	if resumable, ok := request.Reader.(ResumableReader); ok {
		readErr = resumable.ReadResumable(ctx, window, resume, yield, onPage)
	} else {
		readErr = request.Reader.Read(ctx, window, yield)
		if readErr == nil {
			readErr = onPage(Page{Location: request.Source, LastUpdated: lastSeen, Entries: stored})
		}
	}

	report.Publications = stored
	report.Pages = progress.Pages
	report.Cursor = progress.Cursor

	if readErr != nil {
		spanState := SpanGap
		if progress.Pages > 0 {
			spanState = SpanPartial
		}
		detail := readErr.Error()
		if finishErr := i.runs.Finish(ctx, run, progress, spanState, detail); finishErr != nil {
			return report, fmt.Errorf("%v (recording the failed run: %w)", readErr, finishErr)
		}
		report.State = RunPartial
		if spanState == SpanGap {
			report.State = RunFailed
		}
		report.Span = spanState
		report.Detail = detail
		return report, readErr
	}

	// A completed run has no page to resume inside: the time cursor is the
	// whole point and the next load is a fresh incremental read from the head.
	progress.Cursor.LastPage = ""
	if err := i.runs.Finish(ctx, run, progress, SpanComplete, ""); err != nil {
		return report, err
	}
	report.State = RunCompleted
	report.Span = SpanComplete
	return report, nil
}
