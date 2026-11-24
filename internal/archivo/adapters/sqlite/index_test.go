package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/archivo/domain"
	contratossqlite "github.com/hectorruiz23/la-mordida/internal/contratos/adapters/sqlite"
	contratosdomain "github.com/hectorruiz23/la-mordida/internal/contratos/domain"
	"github.com/hectorruiz23/la-mordida/internal/platform/db"
	"github.com/hectorruiz23/la-mordida/migrations"
)

var updatedAt = time.Date(2025, 10, 20, 8, 23, 7, 464000000, time.UTC)

func testIndex(t *testing.T) (*Index, *sql.DB) {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "contratos.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(context.Background(), database, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return New(database), database
}

func announcedPublication(t *testing.T) contratosdomain.Publication {
	t.Helper()
	return contratosdomain.Publication{
		Source:     "fixture",
		ExternalID: "urn:test:1787-2025",
		URL:        "https://example.test/publicacion",
		UpdatedAt:  updatedAt,
		BodyHash:   "hash-publication",
		Expediente: contratosdomain.Expediente{
			Buyer:    contratosdomain.Party{Scheme: "NIF", ID: "P3704600J", Name: "Ayuntamiento"},
			FolderID: "1787/2025",
		},
		Documents: []contratosdomain.Document{
			{
				Kind:  "lot",
				Ref:   "1",
				URL:   "https://example.test/pliego",
				Title: "Pliego del lote 1",
			},
			{
				Kind:  "publication",
				URL:   "https://example.test/copia",
				Title: "Copia del pliego",
			},
		},
	}
}

func hash(letter string) string { return strings.Repeat(letter, 64) }

func TestLinkLifecycleKeepsAnnouncedPendingEvidenceAndBothVersions(t *testing.T) {
	ctx := context.Background()
	index, database := testIndex(t)

	if err := contratossqlite.New(database).StorePublication(ctx, announcedPublication(t)); err != nil {
		t.Fatalf("store publication: %v", err)
	}

	links, err := index.Links(ctx, false)
	if err != nil {
		t.Fatalf("list links: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("links = %+v", links)
	}
	pliego, copia := links[0], links[1]
	if pliego.TargetKind != "lot" || pliego.TargetRef != "1" || pliego.State != domain.Announced || pliego.URL != "https://example.test/pliego" {
		t.Fatalf("announced link = %+v", pliego)
	}
	if copia.TargetKind != "publication" || copia.URL != "https://example.test/copia" {
		t.Fatalf("announced copy = %+v", copia)
	}

	pliego.Attempts++
	if err := index.Pending(ctx, pliego, errors.New("timeout descargando")); err != nil {
		t.Fatalf("mark pending: %v", err)
	}
	pending, err := index.Links(ctx, false)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 2 || pending[0].State != domain.Pending || pending[0].Attempts != 1 || pending[0].LastError != "timeout descargando" {
		t.Fatalf("pending links = %+v", pending)
	}

	first := domain.Origin{
		URL:        pliego.URL,
		ObtainedAt: updatedAt.Add(time.Hour),
		Object:     domain.Object{SHA256: hash("a"), MIME: "application/pdf", Bytes: 12},
	}
	pliego.Attempts++
	if err := index.Store(ctx, pliego, first); err != nil {
		t.Fatalf("store first evidence: %v", err)
	}

	// The same bytes from another URL keep a second provenance and a second
	// evidence row, but only one object.
	sameBytes := first
	sameBytes.URL = copia.URL
	sameBytes.ObtainedAt = updatedAt.Add(2 * time.Hour)
	copia.Attempts++
	if err := index.Store(ctx, copia, sameBytes); err != nil {
		t.Fatalf("store same object from another url: %v", err)
	}

	// The same URL with other bytes keeps the first version beside it.
	second := domain.Origin{
		URL:        pliego.URL,
		ObtainedAt: updatedAt.Add(3 * time.Hour),
		Object:     domain.Object{SHA256: hash("b"), MIME: "application/pdf", Bytes: 13},
	}
	pliego.Attempts++
	if err := index.Store(ctx, pliego, second); err != nil {
		t.Fatalf("store second evidence: %v", err)
	}

	if evidence, err := index.Links(ctx, false); err != nil || len(evidence) != 0 {
		t.Fatalf("pending links after storing evidence = %+v (%v)", evidence, err)
	}

	all, err := index.Links(ctx, true)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 || all[0].State != domain.Evidence || all[0].SHA256 != hash("b") {
		t.Fatalf("stored links = %+v", all)
	}

	assertCount(t, database, `SELECT COUNT(*) FROM objeto`, 2)
	assertCount(t, database, `SELECT COUNT(*) FROM procedencia`, 3)
	assertCount(t, database, `SELECT COUNT(*) FROM document_evidence`, 3)
	assertCount(t, database, `SELECT COUNT(*) FROM procedencia WHERE url = 'https://example.test/pliego'`, 2)
	assertCount(t, database, `SELECT COUNT(*) FROM procedencia WHERE sha256 = ? AND url = 'https://example.test/copia'`, 1, hash("a"))
}

func assertCount(t *testing.T, database *sql.DB, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := database.QueryRowContext(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if got != want {
		t.Fatalf("%s = %d, want %d", query, got, want)
	}
}
