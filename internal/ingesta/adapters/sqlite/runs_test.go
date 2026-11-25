package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ingestaapp "github.com/hectorruiz23/la-mordida/internal/ingesta/app"
	"github.com/hectorruiz23/la-mordida/internal/platform/db"
	"github.com/hectorruiz23/la-mordida/migrations"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runs.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(context.Background(), database, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return New(database)
}

func TestOnlyOneImporterRunsAtATime(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	run, err := store.Begin(ctx, ingestaapp.Run{
		Source: "placsp:sindicacion_643", Kind: ingestaapp.Historical,
	})
	if err != nil {
		t.Fatalf("begin first run: %v", err)
	}

	_, err = store.Begin(ctx, ingestaapp.Run{
		Source: "placsp:sindicacion_643", Kind: ingestaapp.Incremental,
	})
	if !errors.Is(err, ingestaapp.ErrImportRunning) {
		t.Fatalf("second begin: err = %v, want ErrImportRunning", err)
	}

	progress := ingestaapp.Progress{
		RunID: run.ID, SpanID: run.SpanID, Publications: 0, Pages: 1,
		Cursor: ingestaapp.Cursor{Source: run.Source},
	}
	if err := store.Finish(ctx, run, progress, ingestaapp.SpanComplete, ""); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	if _, err := store.Begin(ctx, ingestaapp.Run{
		Source: "placsp:sindicacion_643", Kind: ingestaapp.Incremental,
	}); err != nil {
		t.Fatalf("begin after the first run closed: %v", err)
	}
}

func TestCursorAndSpansRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	source := "placsp:sindicacion_643"
	point := time.Date(2021, 1, 5, 10, 0, 0, 0, time.UTC)

	run, err := store.Begin(ctx, ingestaapp.Run{
		Source: source, Kind: ingestaapp.Historical,
		PeriodFrom: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	progress := ingestaapp.Progress{
		RunID: run.ID, SpanID: run.SpanID, Publications: 3, Pages: 2,
		Errors: 1, ErrorDetail: "entry urn:test:bad: ContractFolderID is missing",
		Cursor: ingestaapp.Cursor{Source: source, LastCompleteAt: point, LastPage: "/feed.atom"},
	}
	if err := store.Record(ctx, progress); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := store.Finish(ctx, run, progress, ingestaapp.SpanPartial, "page 3 unavailable"); err != nil {
		t.Fatalf("finish: %v", err)
	}

	cursor, ok, err := store.Cursor(ctx, source)
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if !ok || !cursor.LastCompleteAt.Equal(point) || cursor.LastPage != "/feed.atom" {
		t.Fatalf("cursor = %+v, want the last whole page", cursor)
	}

	spans, err := store.Spans(ctx, source)
	if err != nil {
		t.Fatalf("spans: %v", err)
	}
	if len(spans) != 1 || spans[0].State != ingestaapp.SpanPartial || spans[0].Publications != 3 || spans[0].Pages != 2 {
		t.Fatalf("spans = %+v, want one partial span with its counters", spans)
	}
	if spans[0].Errors != 1 || !strings.Contains(spans[0].ErrorDetail, "ContractFolderID") {
		t.Errorf("span errors = %d / %q, want the counted reason", spans[0].Errors, spans[0].ErrorDetail)
	}
}
