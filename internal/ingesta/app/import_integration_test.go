package app_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	contratossqlite "github.com/hectorruiz23/la-mordida/internal/contratos/adapters/sqlite"
	contratosapp "github.com/hectorruiz23/la-mordida/internal/contratos/app"
	ingestasqlite "github.com/hectorruiz23/la-mordida/internal/ingesta/adapters/sqlite"
	ingestaapp "github.com/hectorruiz23/la-mordida/internal/ingesta/app"
	ingestadomain "github.com/hectorruiz23/la-mordida/internal/ingesta/domain"
	"github.com/hectorruiz23/la-mordida/internal/platform/db"
	"github.com/hectorruiz23/la-mordida/migrations"
)

type fakePage struct {
	publications []ingestadomain.Publication
	fail         bool
}

type fakeReader struct {
	pages []fakePage
}

func (f *fakeReader) Read(ctx context.Context, window ingestaapp.Window, yield func(ingestadomain.Publication) error) error {
	return f.ReadResumable(ctx, window, ingestaapp.Resume{}, yield, func(ingestaapp.Page) error { return nil })
}

func (f *fakeReader) ReadResumable(ctx context.Context, window ingestaapp.Window, resume ingestaapp.Resume, yield func(ingestadomain.Publication) error, onPage func(ingestaapp.Page) error) error {
	for index, page := range f.pages {
		if page.fail {
			return fmt.Errorf("feed page %d is unavailable", index+1)
		}
		last := time.Time{}
		for _, publication := range page.publications {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !window.Contains(publication.UpdatedAt) {
				continue
			}
			if err := yield(publication); err != nil {
				return err
			}
			if publication.UpdatedAt.After(last) {
				last = publication.UpdatedAt
			}
		}
		if err := onPage(ingestaapp.Page{Location: fmt.Sprintf("page-%d", index+1), LastUpdated: last}); err != nil {
			return err
		}
	}
	return nil
}

func publication(id string, at time.Time, folder string) ingestadomain.Publication {
	return ingestadomain.Publication{
		Reference: ingestadomain.Reference{Source: "placsp:sindicacion_643", ID: id, URL: "https://example.test/" + id},
		UpdatedAt: at,
		Expediente: ingestadomain.Expediente{
			FolderID: folder,
			Buyer:    ingestadomain.Party{Scheme: "NIF", ID: "B1", Name: "Comprador"},
			CPV: ingestadomain.Classification{
				Source:   ingestadomain.ClassificationPublished,
				Included: true,
				Codes:    []ingestadomain.CPV{{Code: "30200000", Family: "302", Included: true, Reason: "family_selected"}},
			},
		},
		Awards: []ingestadomain.Award{{
			ResultCode: "ADJ",
			Winner:     ingestadomain.Party{Scheme: "NIF", ID: "W1", Name: "Ganador"},
		}},
	}
}

func inventory(t *testing.T, path string) []string {
	t.Helper()
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer database.Close()

	rows, err := database.QueryContext(context.Background(), `
		SELECT source || '|' || external_id || '|' || published_at || '|' || body_sha256
		FROM publication ORDER BY 1`)
	if err != nil {
		t.Fatalf("list publications: %v", err)
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatalf("scan publication: %v", err)
		}
		result = append(result, row)
	}
	return result
}

func importInto(t *testing.T, path string, reader ingestaapp.Reader, request ingestaapp.Request) (ingestaapp.Report, error) {
	t.Helper()
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer database.Close()
	if err := db.Migrate(context.Background(), database, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	request.Source = "placsp:sindicacion_643"
	request.Reader = reader
	importer := ingestaapp.NewResumableImporter(reader, contratosapp.NewImporter(contratossqlite.New(database)), ingestasqlite.New(database))
	return importer.Import(context.Background(), request)
}

func TestFailedLoadAndRetryMatchACleanLoad(t *testing.T) {
	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2021, 3, 1, 12, 0, 0, 0, time.UTC)

	pages := []fakePage{
		{publications: []ingestadomain.Publication{
			publication("urn:old-a", time.Date(2020, 6, 1, 10, 0, 0, 0, time.UTC), "F-A"),
			publication("urn:old-b", time.Date(2020, 5, 1, 10, 0, 0, 0, time.UTC), "F-B"),
		}},
		{publications: []ingestadomain.Publication{
			publication("urn:old-a", late, "F-A"),
		}},
	}

	dirty := filepath.Join(t.TempDir(), "dirty.db")
	dirtyReader := &fakeReader{pages: []fakePage{pages[0], {fail: true}}}
	report, err := importInto(t, dirty, dirtyReader, ingestaapp.Request{
		Kind: ingestaapp.Historical, From: from, Until: until,
	})
	if err == nil {
		t.Fatal("first load succeeded, want the second page to fail")
	}
	if report.State != ingestaapp.RunPartial || report.Span != ingestaapp.SpanPartial {
		t.Fatalf("failed load = %+v, want a partial run over a partial span", report)
	}
	if report.Publications != 2 {
		t.Fatalf("failed load stored %d publications, want the whole first page", report.Publications)
	}

	retryReader := &fakeReader{pages: pages}
	if _, err := importInto(t, dirty, retryReader, ingestaapp.Request{
		Kind: ingestaapp.Incremental, SinceLast: true, Until: until.AddDate(0, 6, 0),
	}); err != nil {
		t.Fatalf("retry: %v", err)
	}

	clean := filepath.Join(t.TempDir(), "clean.db")
	cleanReader := &fakeReader{pages: pages}
	if _, err := importInto(t, clean, cleanReader, ingestaapp.Request{
		Kind: ingestaapp.Historical, From: from, Until: until,
	}); err != nil {
		t.Fatalf("clean historical load: %v", err)
	}
	if _, err := importInto(t, clean, &fakeReader{pages: pages}, ingestaapp.Request{
		Kind: ingestaapp.Incremental, SinceLast: true, Until: until.AddDate(0, 6, 0),
	}); err != nil {
		t.Fatalf("clean incremental load: %v", err)
	}

	dirtyInventory := inventory(t, dirty)
	cleanInventory := inventory(t, clean)
	if fmt.Sprint(dirtyInventory) != fmt.Sprint(cleanInventory) {
		t.Fatalf("retry result differs from a clean load:\n retry: %v\n clean: %v", dirtyInventory, cleanInventory)
	}
	if len(dirtyInventory) != 3 {
		t.Fatalf("publications = %d, want the two historical revisions and the late 2021 one", len(dirtyInventory))
	}
}

func TestCoverageTellsAZeroFromAGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coverage.db")
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	report, err := importInto(t, path, &fakeReader{pages: []fakePage{{}}}, ingestaapp.Request{
		Kind: ingestaapp.Historical, From: from, Until: until,
	})
	if err != nil {
		t.Fatalf("empty load: %v", err)
	}
	if report.Span != ingestaapp.SpanComplete || report.Publications != 0 {
		t.Fatalf("empty load = %+v, want a complete span with zero publications", report)
	}

	gapPath := filepath.Join(t.TempDir(), "gap.db")
	if _, err := importInto(t, gapPath, &fakeReader{pages: []fakePage{{fail: true}}}, ingestaapp.Request{
		Kind: ingestaapp.Historical, From: from, Until: until,
	}); err == nil {
		t.Fatal("a reader that fails on its first page was reported as covered")
	}

	check := func(path string, want ingestaapp.SpanState) {
		t.Helper()
		database, err := db.Open(path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer database.Close()
		spans, err := ingestasqlite.New(database).Spans(context.Background(), "placsp:sindicacion_643")
		if err != nil {
			t.Fatalf("spans: %v", err)
		}
		if len(spans) != 1 || spans[0].State != want {
			t.Fatalf("spans = %+v, want one %s span", spans, want)
		}
	}
	check(path, ingestaapp.SpanComplete)
	check(gapPath, ingestaapp.SpanGap)
}

func TestSinceLastWithoutAPreviousLoadIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.db")
	_, err := importInto(t, path, &fakeReader{pages: []fakePage{{}}}, ingestaapp.Request{
		Kind: ingestaapp.Incremental, SinceLast: true,
	})
	if !errors.Is(err, ingestaapp.ErrNoCursor) {
		t.Fatalf("since-last on an empty database: err = %v, want ErrNoCursor", err)
	}
}
