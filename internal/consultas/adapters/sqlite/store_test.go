package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	contratossqlite "github.com/hectorruiz23/la-mordida/internal/contratos/adapters/sqlite"
	contratosdomain "github.com/hectorruiz23/la-mordida/internal/contratos/domain"
	"github.com/hectorruiz23/la-mordida/internal/kernel"
	"github.com/hectorruiz23/la-mordida/internal/platform/db"
	"github.com/hectorruiz23/la-mordida/migrations"
)

func money(t *testing.T, raw string) kernel.Money {
	t.Helper()
	amount, err := kernel.ParseMoney(raw, "EUR", kernel.TaxExclusive)
	if err != nil {
		t.Fatalf("parse money: %v", err)
	}
	return amount
}

func TestListExpedientesServesTheProjectionAndItsFacts(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(filepath.Join(t.TempDir(), "contratos.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := db.Migrate(ctx, database, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	newer := contratosdomain.Publication{
		Source:     "fixture",
		ExternalID: "urn:test:read",
		UpdatedAt:  time.Date(2025, 10, 22, 8, 0, 0, 0, time.UTC),
		BodyHash:   "hash-newer",
		Expediente: contratosdomain.Expediente{
			Buyer:    contratosdomain.Party{Scheme: "NIF", ID: "P1", Name: "Comprador"},
			FolderID: "LECTURA/2025",
			Object:   "Objeto vigente",
			Status:   "ADJ",
			CPV: contratosdomain.Classification{
				Source:   contratosdomain.ClassificationPublished,
				Included: true,
				Codes:    []contratosdomain.CPV{{Code: "30200000", Family: "302", Included: true, Reason: "family_selected"}},
			},
		},
		Awards: []contratosdomain.Award{{
			LotID:      "",
			ResultCode: "8",
			Winner:     contratosdomain.Party{Scheme: "NIF", ID: "B1", Name: "Ganador"},
			Amounts:    []kernel.Money{money(t, "700")},
		}},
	}
	older := newer
	older.UpdatedAt = time.Date(2025, 10, 20, 8, 0, 0, 0, time.UTC)
	older.BodyHash = "hash-older"
	older.Expediente.Object = "Objeto anterior"
	older.Awards = []contratosdomain.Award{{
		LotID:      "",
		ResultCode: "8",
		Winner:     contratosdomain.Party{Scheme: "NIF", ID: "B1", Name: "Ganador"},
		Amounts:    []kernel.Money{money(t, "800")},
	}}

	store := contratossqlite.New(database)
	for _, publication := range []contratosdomain.Publication{newer, older} {
		if err := store.StorePublication(ctx, publication); err != nil {
			t.Fatalf("store publication: %v", err)
		}
	}

	expedientes, err := New(database).ListExpedientes(ctx)
	if err != nil {
		t.Fatalf("list expedientes: %v", err)
	}
	if len(expedientes) != 1 {
		t.Fatalf("expedientes = %d, want 1", len(expedientes))
	}
	expediente := expedientes[0]
	if expediente.Object != "Objeto vigente" || expediente.Publication.UpdatedAt != "2025-10-22T08:00:00Z" {
		t.Fatalf("current projection = %+v", expediente)
	}
	if len(expediente.Awards) != 1 || len(expediente.Awards[0].Amounts) != 1 || expediente.Awards[0].Amounts[0].Cents != 70000 {
		t.Fatalf("current award = %+v", expediente.Awards)
	}
	if expediente.Awards[0].State != "known" {
		t.Fatalf("award state = %q, want known", expediente.Awards[0].State)
	}
}
