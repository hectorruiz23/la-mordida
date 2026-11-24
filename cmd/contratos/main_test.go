package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	archivodisk "github.com/hectorruiz23/la-mordida/internal/archivo/adapters/disk"
	consultassqlite "github.com/hectorruiz23/la-mordida/internal/consultas/adapters/sqlite"
	"github.com/hectorruiz23/la-mordida/internal/kernel"
	"github.com/hectorruiz23/la-mordida/internal/platform/db"
)

func TestImportFixtureEndToEndThenServeReadsTheSameDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contratos.db")
	fixture := filepath.Join("..", "..", "testdata", "neutral", "sample-award.json")

	if err := runImport([]string{"-db", path, "-fixture", fixture}); err != nil {
		t.Fatalf("import: %v", err)
	}

	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open imported database: %v", err)
	}
	defer database.Close()

	expedientes, err := consultassqlite.New(database).ListExpedientes(context.Background())
	if err != nil {
		t.Fatalf("read imported data: %v", err)
	}
	if len(expedientes) != 2 {
		t.Fatalf("expedientes = %d, want 2", len(expedientes))
	}

	type summary struct {
		folderID, buyerName string
		lots, awards        int
	}
	var withLots, global *summary
	for index := range expedientes {
		expediente := &expedientes[index]
		current := &summary{
			folderID:  expediente.FolderID,
			buyerName: expediente.Buyer.Name,
			lots:      len(expediente.Lots),
			awards:    len(expediente.Awards),
		}
		switch expediente.FolderID {
		case "1787/2025":
			withLots = current
		case "20250145":
			global = current
		}
	}
	if withLots == nil || global == nil {
		t.Fatalf("expedientes = %+v", expedientes)
	}
	if withLots.lots != 2 || withLots.awards != 2 || withLots.buyerName != "Alcaldía del Ayuntamiento de Béjar" {
		t.Fatalf("lot expediente = %+v", withLots)
	}
	if global.lots != 0 || global.awards != 1 || global.buyerName != "Dirección del Instituto de Estudios Fiscales" {
		t.Fatalf("global expediente = %+v", global)
	}

	for _, expediente := range expedientes {
		for _, award := range expediente.Awards {
			if award.AwardDate == "" {
				t.Errorf("award %s has no civil date", award.Winner.ID)
			}
			exclusive, ok := amount(award.Amounts, kernel.TaxExclusive)
			if !ok {
				t.Errorf("award %s has no tax-exclusive amount", award.Winner.ID)
				continue
			}
			inclusive, ok := amount(award.Amounts, kernel.TaxInclusive)
			if !ok {
				t.Errorf("award %s has no tax-inclusive amount", award.Winner.ID)
				continue
			}
			if inclusive.Cents <= exclusive.Cents {
				t.Errorf("award %s: inclusive %d <= exclusive %d", award.Winner.ID, inclusive.Cents, exclusive.Cents)
			}
			for _, money := range []kernel.Money{exclusive, inclusive} {
				if money.Currency != "EUR" {
					t.Errorf("award %s currency = %q, want EUR", award.Winner.ID, money.Currency)
				}
			}
		}
	}
}

func amount(amounts []kernel.Money, base kernel.TaxBase) (kernel.Money, bool) {
	for _, money := range amounts {
		if money.TaxBase == base {
			return money, true
		}
	}
	return kernel.Money{}, false
}

func TestArchiveCommandStoresDocumentsAndRetriesAFailedDownload(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pliego":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF pliego"))
		case "/reintento":
			attempts++
			if attempts == 1 {
				http.Error(w, "boom", http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("anexo"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "contratos.db")
	objectsPath := filepath.Join(dir, "objects")
	fixturePath := filepath.Join(dir, "fixture.json")
	writeArchiveFixture(t, fixturePath, server.URL+"/pliego", server.URL+"/reintento")

	if err := runImport([]string{"-db", dbPath, "-fixture", fixturePath}); err != nil {
		t.Fatalf("import: %v", err)
	}
	if err := runArchive([]string{"-db", dbPath, "-objects", objectsPath}); err == nil {
		t.Fatal("archive did not report the pending download")
	}

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open archived database: %v", err)
	}
	defer database.Close()

	if got := count(t, database, `SELECT COUNT(*) FROM document_evidence`); got != 1 {
		t.Fatalf("evidence after first archive = %d, want 1", got)
	}
	var state string
	var failedAttempts int
	if err := database.QueryRowContext(context.Background(), `
		SELECT state, attempts FROM document_link WHERE url = ?`, server.URL+"/reintento",
	).Scan(&state, &failedAttempts); err != nil {
		t.Fatalf("read pending link: %v", err)
	}
	if state != "pending" || failedAttempts != 1 {
		t.Fatalf("pending link = %s after %d attempts", state, failedAttempts)
	}

	if err := runArchive([]string{"-db", dbPath, "-objects", objectsPath}); err != nil {
		t.Fatalf("retry archive: %v", err)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM document_evidence`); got != 2 {
		t.Fatalf("evidence after retry = %d, want 2", got)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM document_link WHERE state = 'evidence'`); got != 2 {
		t.Fatalf("evidence links = %d, want 2", got)
	}

	var hash string
	if err := database.QueryRowContext(context.Background(), `
		SELECT sha256 FROM document_link WHERE url = ?`, server.URL+"/reintento",
	).Scan(&hash); err != nil {
		t.Fatalf("read stored link: %v", err)
	}
	stored, err := archivodisk.New(objectsPath).Open(hash)
	if err != nil {
		t.Fatalf("open stored retry: %v", err)
	}
	defer stored.Close()
	body, err := io.ReadAll(stored)
	if err != nil {
		t.Fatalf("read stored retry: %v", err)
	}
	if string(body) != "anexo" {
		t.Fatalf("stored retry = %q, want %q", body, "anexo")
	}
}

func TestArchiveRefreshKeepsBothVersionsWhenAURLChanges(t *testing.T) {
	versions := []string{"primera versión", "segunda versión"}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls >= len(versions) {
			http.Error(w, "no more versions", http.StatusGone)
			return
		}
		_, _ = w.Write([]byte(versions[calls]))
		calls++
	}))
	defer server.Close()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "contratos.db")
	objectsPath := filepath.Join(dir, "objects")
	fixturePath := filepath.Join(dir, "fixture.json")
	writeArchiveFixture(t, fixturePath, server.URL+"/cambia", "")

	if err := runImport([]string{"-db", dbPath, "-fixture", fixturePath}); err != nil {
		t.Fatalf("import: %v", err)
	}
	if err := runArchive([]string{"-db", dbPath, "-objects", objectsPath}); err != nil {
		t.Fatalf("first archive: %v", err)
	}
	if err := runArchive([]string{"-db", dbPath, "-objects", objectsPath, "-refresh"}); err != nil {
		t.Fatalf("refresh archive: %v", err)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open archived database: %v", err)
	}
	defer database.Close()

	if got := count(t, database, `SELECT COUNT(*) FROM objeto`); got != 2 {
		t.Fatalf("objects = %d, want 2", got)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM procedencia`); got != 2 {
		t.Fatalf("provenances = %d, want 2", got)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM document_evidence`); got != 2 {
		t.Fatalf("evidence = %d, want 2", got)
	}

	rows, err := database.QueryContext(context.Background(), `SELECT sha256 FROM objeto ORDER BY stored_at`)
	if err != nil {
		t.Fatalf("read objects: %v", err)
	}
	defer rows.Close()
	store := archivodisk.New(objectsPath)
	var contents []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			t.Fatalf("scan object: %v", err)
		}
		file, err := store.Open(hash)
		if err != nil {
			t.Fatalf("open object %s: %v", hash, err)
		}
		body, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			t.Fatalf("read object %s: %v", hash, err)
		}
		contents = append(contents, string(body))
	}
	if len(contents) != 2 || contents[0] != versions[0] || contents[1] != versions[1] {
		t.Fatalf("stored versions = %q, want %q", contents, versions)
	}
}

func writeArchiveFixture(t *testing.T, path, firstURL, secondURL string) {
	t.Helper()

	documents := fmt.Sprintf(`{"url": %q, "title": "Pliego"}`, firstURL)
	if secondURL != "" {
		documents += fmt.Sprintf(`, {"url": %q, "title": "Anexo"}`, secondURL)
	}
	fixture := fmt.Sprintf(`{
  "publications": [
    {
      "reference": {"source": "fixture", "id": "urn:test:archivo", "url": "https://example.test/publicacion"},
      "updated_at": "2025-10-20T10:23:07.464+02:00",
      "expediente": {
        "scope": "expediente",
        "folder_id": "A/2025",
        "status": "ADJ",
        "buyer": {"scheme": "NIF", "id": "P3704600J", "name": "Ayuntamiento"},
        "budget": [{"amount": "100", "currency": "EUR", "tax_base": "tax_exclusive"}]
      },
      "documents": [%s]
    }
  ]
}`, documents)
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func count(t *testing.T, database *sql.DB, query string) int {
	t.Helper()
	var got int
	if err := database.QueryRowContext(context.Background(), query).Scan(&got); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return got
}
