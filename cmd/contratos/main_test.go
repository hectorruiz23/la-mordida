package main

import (
	"context"
	"path/filepath"
	"testing"

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
