package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/contratos/domain"
	"github.com/hectorruiz23/la-mordida/internal/kernel"
	"github.com/hectorruiz23/la-mordida/internal/platform/db"
	"github.com/hectorruiz23/la-mordida/migrations"
)

func testStore(t *testing.T) (*Store, *sql.DB) {
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

func money(t *testing.T, raw, currency string, base kernel.TaxBase) kernel.Money {
	t.Helper()
	amount, err := kernel.ParseMoney(raw, currency, base)
	if err != nil {
		t.Fatalf("parse money %s: %v", raw, err)
	}
	return amount
}

func date(t *testing.T, raw string) *kernel.Date {
	t.Helper()
	parsed, err := kernel.ParseDate(raw)
	if err != nil {
		t.Fatalf("parse date %s: %v", raw, err)
	}
	return &parsed
}

func samplePublication(t *testing.T) domain.Publication {
	t.Helper()
	return domain.Publication{
		Source:     "fixture",
		ExternalID: "urn:test:1787-2025",
		URL:        "https://contrataciondelestado.es/sindicacion/licitacionesPerfilContratante/17680481",
		UpdatedAt:  time.Date(2025, 10, 20, 8, 23, 7, 464000000, time.UTC),
		BodyHash:   "hash-one",
		Expediente: domain.Expediente{
			Buyer:    domain.Party{Scheme: "NIF", ID: "P3704600J", Name: "Alcaldía del Ayuntamiento de Béjar"},
			FolderID: "1787/2025",
			Object:   "Suministro de productos de limpieza",
			Status:   "ADJ",
			Budget: []kernel.Money{
				money(t, "2447.22", "EUR", kernel.TaxExclusive),
				money(t, "2961.14", "EUR", kernel.TaxInclusive),
			},
		},
		Lots: []domain.Lot{{
			ID:     "1",
			Object: "Productos para la higiene personal",
			Budget: []kernel.Money{money(t, "1317.74", "EUR", kernel.TaxExclusive)},
		}},
		Awards: []domain.Award{{
			LotID:      "1",
			ResultCode: "8",
			AwardDate:  date(t, "2025-10-10"),
			Winner:     domain.Party{Scheme: "NIF", ID: "B37491404", Name: "Suministros Lavabar, SL"},
			Amounts: []kernel.Money{
				money(t, "784.11", "EUR", kernel.TaxExclusive),
				money(t, "948.77", "EUR", kernel.TaxInclusive),
			},
		}},
		Documents: []domain.Document{{
			URL:   "https://contrataciondelestado.es/documento/1",
			Hash:  "",
			Title: "Pliego de prescripciones técnicas",
		}},
	}
}

func TestStorePublicationPersistsExpedienteOptionalLotBuyerWinnerDatesAndExactAmounts(t *testing.T) {
	store, database := testStore(t)
	ctx := context.Background()

	withLot := samplePublication(t)
	global := samplePublication(t)
	global.ExternalID = "urn:test:20250145"
	global.BodyHash = "hash-two"
	global.Expediente.Buyer = domain.Party{Scheme: "NIF", ID: "S2826046A", Name: "Dirección del Instituto de Estudios Fiscales"}
	global.Expediente.FolderID = "20250145"
	global.Lots = nil
	global.Awards = []domain.Award{{
		ResultCode: "8",
		AwardDate:  date(t, "2025-09-30"),
		Winner:     domain.Party{Scheme: "NIF", ID: "B87596276", Name: "ASENGA LOGISTICA S.L"},
		Amounts: []kernel.Money{
			money(t, "14691.62", "EUR", kernel.TaxExclusive),
			money(t, "17776.86", "EUR", kernel.TaxInclusive),
		},
	}}
	global.Documents = nil

	for _, publication := range []domain.Publication{withLot, global} {
		if err := store.StorePublication(ctx, publication); err != nil {
			t.Fatalf("store publication: %v", err)
		}
	}

	var buyerName, object, status string
	if err := database.QueryRowContext(ctx, `
		SELECT buyer_name, object, status FROM expediente
		WHERE buyer_scheme = 'NIF' AND buyer_id = 'P3704600J' AND folder_id = '1787/2025'`,
	).Scan(&buyerName, &object, &status); err != nil {
		t.Fatalf("read expediente: %v", err)
	}
	if buyerName != "Alcaldía del Ayuntamiento de Béjar" || status != "ADJ" || object == "" {
		t.Fatalf("expediente = %q / %q / %q", buyerName, object, status)
	}

	var lots int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM lot`).Scan(&lots); err != nil {
		t.Fatalf("count lots: %v", err)
	}
	if lots != 1 {
		t.Fatalf("lots = %d, want 1", lots)
	}

	rows, err := database.QueryContext(ctx, `
		SELECT a.lot_id, a.award_date, a.winner_scheme, a.winner_id, a.winner_name,
		       am.tax_base, am.currency, am.amount_cents
		FROM award a
		JOIN award_amount am ON am.award_id = a.id
		ORDER BY a.lot_id, am.tax_base`)
	if err != nil {
		t.Fatalf("read awards: %v", err)
	}
	defer rows.Close()

	type awardRow struct {
		lotID, awardDate, scheme, id, name, base, currency string
		cents                                              int64
	}
	var got []awardRow
	for rows.Next() {
		var row awardRow
		if err := rows.Scan(&row.lotID, &row.awardDate, &row.scheme, &row.id, &row.name, &row.base, &row.currency, &row.cents); err != nil {
			t.Fatalf("scan award: %v", err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate awards: %v", err)
	}

	want := []awardRow{
		{lotID: "", awardDate: "2025-09-30", scheme: "NIF", id: "B87596276", name: "ASENGA LOGISTICA S.L", base: "tax_exclusive", currency: "EUR", cents: 1469162},
		{lotID: "", awardDate: "2025-09-30", scheme: "NIF", id: "B87596276", name: "ASENGA LOGISTICA S.L", base: "tax_inclusive", currency: "EUR", cents: 1777686},
		{lotID: "1", awardDate: "2025-10-10", scheme: "NIF", id: "B37491404", name: "Suministros Lavabar, SL", base: "tax_exclusive", currency: "EUR", cents: 78411},
		{lotID: "1", awardDate: "2025-10-10", scheme: "NIF", id: "B37491404", name: "Suministros Lavabar, SL", base: "tax_inclusive", currency: "EUR", cents: 94877},
	}
	if len(got) != len(want) {
		t.Fatalf("award amount rows = %d, want %d: %+v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("row %d = %+v, want %+v", index, got[index], want[index])
		}
	}
}

func TestStorePublicationKeepsVersionsAndReplaysIdempotently(t *testing.T) {
	store, database := testStore(t)
	ctx := context.Background()
	publication := samplePublication(t)

	if err := store.StorePublication(ctx, publication); err != nil {
		t.Fatalf("first store: %v", err)
	}
	if err := store.StorePublication(ctx, publication); err != nil {
		t.Fatalf("replay: %v", err)
	}

	var versions, publications int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM publication`).Scan(&versions); err != nil {
		t.Fatalf("count publications: %v", err)
	}
	if versions != 1 {
		t.Fatalf("publication versions after replay = %d, want 1", versions)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM publication WHERE source = ? AND external_id = ?`, publication.Source, publication.ExternalID).Scan(&publications); err != nil {
		t.Fatalf("count source publications: %v", err)
	}

	changedBytes := publication
	changedBytes.BodyHash = "hash-different"
	if err := store.StorePublication(ctx, changedBytes); err == nil || !strings.Contains(err.Error(), "different bytes") {
		t.Fatalf("same version with other bytes: err = %v", err)
	}

	newer := publication
	newer.UpdatedAt = publication.UpdatedAt.Add(time.Hour)
	newer.BodyHash = "hash-newer"
	newer.Awards[0].Amounts = []kernel.Money{money(t, "700", "EUR", kernel.TaxExclusive)}
	if err := store.StorePublication(ctx, newer); err != nil {
		t.Fatalf("store newer version: %v", err)
	}

	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM publication`).Scan(&versions); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if versions != 2 {
		t.Fatalf("publication versions = %d, want 2", versions)
	}
}
