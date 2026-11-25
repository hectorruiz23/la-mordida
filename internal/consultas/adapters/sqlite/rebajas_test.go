package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hectorruiz23/la-mordida/internal/consultas/domain"
	contratossqlite "github.com/hectorruiz23/la-mordida/internal/contratos/adapters/sqlite"
	contratosapp "github.com/hectorruiz23/la-mordida/internal/contratos/app"
	fixture "github.com/hectorruiz23/la-mordida/internal/ingesta/adapters/fixture"
	ingestaapp "github.com/hectorruiz23/la-mordida/internal/ingesta/app"
	"github.com/hectorruiz23/la-mordida/internal/platform/db"
	"github.com/hectorruiz23/la-mordida/migrations"
)

func rebatesFixture(t *testing.T) (*sql.DB, *Store) {
	t.Helper()
	ctx := context.Background()

	database, err := db.Open(filepath.Join(t.TempDir(), "contratos.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(ctx, database, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "testdata", "consultas", "rebajas.json")
	importer := ingestaapp.NewImporter(fixture.New(path), contratosapp.NewImporter(contratossqlite.New(database)))
	if _, err := importer.Run(ctx, ingestaapp.Window{}); err != nil {
		t.Fatalf("import fixture: %v", err)
	}
	return database, New(database)
}

func rebatesByFolder(t *testing.T, ranking domain.RebateRanking) map[string]domain.Rebate {
	t.Helper()
	byFolder := make(map[string]domain.Rebate, len(ranking.Ranking))
	for _, rebate := range ranking.Ranking {
		byFolder[rebate.FolderID] = rebate
	}
	return byFolder
}

func TestRebatesRankComparableUnitsByTheExactQuotient(t *testing.T) {
	ctx := context.Background()
	_, store := rebatesFixture(t)

	ranking, err := store.Rebates(ctx, domain.RankingQuery{
		BuyerScheme: "NIF",
		BuyerID:     "P1000000T",
		Limit:       100,
	})
	if err != nil {
		t.Fatalf("read rebates: %v", err)
	}
	if ranking.Total != 6 || len(ranking.Ranking) != 6 {
		t.Fatalf("comparable units = %d, want 6", ranking.Total)
	}
	if ranking.ExcludedTotal != 9 || len(ranking.Excluded) != 9 {
		t.Fatalf("excluded units = %d, want 9", ranking.ExcludedTotal)
	}

	// The 20 % group ties and is ordered by the unit key; the table pins the
	// four requested cases: 20 %, 0 %, tie and correction.
	cases := []struct {
		folder          string
		budgetCents     int64
		awardedCents    int64
		differenceCents int64
		ratio           string
		percent         string
	}{
		{"REB-20/2025", 10_000_000, 8_000_000, 2_000_000, "0.200000", "20.0000"},
		{"REB-CORR/2025", 2_000_000, 1_600_000, 400_000, "0.200000", "20.0000"},
		{"REB-TIE-A/2025", 3_000_000, 2_400_000, 600_000, "0.200000", "20.0000"},
		{"REB-TIE-B/2025", 4_000_000, 3_200_000, 800_000, "0.200000", "20.0000"},
		{"REB-INCLUSIVE/2025", 121_000, 100_000, 21_000, "0.173554", "17.3554"},
		{"REB-0/2025", 5_000_000, 5_000_000, 0, "0.000000", "0.0000"},
	}
	byFolder := rebatesByFolder(t, ranking)
	for index, test := range cases {
		rebate, ok := byFolder[test.folder]
		if !ok {
			t.Fatalf("%s not in the ranking: %+v", test.folder, ranking.Ranking)
		}
		if rebate.InitialBudget.Cents != test.budgetCents || rebate.AwardedAmount.Cents != test.awardedCents {
			t.Errorf("%s amounts = %+v / %+v", test.folder, rebate.InitialBudget, rebate.AwardedAmount)
		}
		if rebate.Difference.Cents != test.differenceCents {
			t.Errorf("%s difference = %+v", test.folder, rebate.Difference)
		}
		if rebate.Ratio != test.ratio || rebate.Percent != test.percent {
			t.Errorf("%s ratio = %q, %q, want %q, %q", test.folder, rebate.Ratio, rebate.Percent, test.ratio, test.percent)
		}
		if rebate.InitialBudgetPublication.URL == "" || rebate.AwardPublication.URL == "" {
			t.Errorf("%s does not recover both publications: %+v", test.folder, rebate)
		}
		if got := ranking.Ranking[index].FolderID; got != test.folder {
			t.Errorf("position %d = %q, want %q", index, got, test.folder)
		}
	}

	// The exact quotients are equal inside the tie, so the key is what orders
	// those four rows.
	for index := 1; index < 4; index++ {
		if ranking.Ranking[index-1].UnitKey >= ranking.Ranking[index].UnitKey {
			t.Fatalf("tie not broken by key: %+v", ranking.Ranking[:4])
		}
	}
	if ranking.Ranking[0].FolderID != "REB-20/2025" || ranking.Ranking[1].FolderID != "REB-CORR/2025" {
		t.Fatalf("tie order = %+v", ranking.Ranking[:4])
	}
}

func TestRebatesKeepTheInitialBudgetWhenTheBudgetIsCorrected(t *testing.T) {
	ctx := context.Background()
	_, store := rebatesFixture(t)

	ranking, err := store.Rebates(ctx, domain.RankingQuery{
		BuyerScheme: "NIF",
		BuyerID:     "P1000000T",
		Limit:       100,
	})
	if err != nil {
		t.Fatalf("read rebates: %v", err)
	}

	corrected := rebatesByFolder(t, ranking)["REB-CORR/2025"]
	if corrected.InitialBudget.Cents != 2_000_000 {
		t.Fatalf("initial budget = %+v, want the original 20000.00", corrected.InitialBudget)
	}
	if corrected.InitialBudgetPublication.UpdatedAt != "2025-01-20T09:00:00Z" {
		t.Fatalf("initial budget publication = %+v", corrected.InitialBudgetPublication)
	}
	if corrected.Ratio != "0.200000" {
		t.Fatalf("corrected row ratio = %q, want the original 20 %%", corrected.Ratio)
	}
	if !corrected.BudgetCorrected || corrected.LatestBudget == nil || corrected.LatestBudget.Cents != 2_500_000 {
		t.Fatalf("correction not preserved: %+v", corrected)
	}
	if corrected.LatestBudgetPublication == nil || corrected.LatestBudgetPublication.UpdatedAt != "2025-05-01T09:00:00Z" {
		t.Fatalf("latest budget publication = %+v", corrected.LatestBudgetPublication)
	}
	if corrected.BudgetScope != "expediente" {
		t.Fatalf("budget scope = %q", corrected.BudgetScope)
	}

	plain := rebatesByFolder(t, ranking)["REB-0/2025"]
	if plain.BudgetCorrected || plain.LatestBudget != nil {
		t.Fatalf("uncorrected row claims a correction: %+v", plain)
	}
}

func TestRebatesKeepTheReasonOfEveryExclusion(t *testing.T) {
	ctx := context.Background()
	_, store := rebatesFixture(t)

	ranking, err := store.Rebates(ctx, domain.RankingQuery{
		BuyerScheme: "NIF",
		BuyerID:     "P1000000T",
		Limit:       100,
	})
	if err != nil {
		t.Fatalf("read rebates: %v", err)
	}

	expected := map[string]string{
		"REB-LOTS/2025|1":      domain.ReasonNoLotBudget,
		"REB-LOTS/2025|2":      domain.ReasonNoLotBudget,
		"REB-NOCURRENCY/2025|": domain.ReasonCurrencyUnknown,
		"REB-CURRENCY/2025|":   domain.ReasonCurrencyMismatch,
		"REB-NOBUDGET/2025|":   domain.ReasonBudgetMissing,
		"REB-ZEROBUDGET/2025|": domain.ReasonBudgetNotPositive,
		"REB-NOAMOUNT/2025|":   domain.ReasonAmountMissing,
		"REB-ZEROAMOUNT/2025|": domain.ReasonAmountNotPositive,
		"REB-TAXBASE/2025|":    domain.ReasonTaxBaseMismatch,
	}
	if len(ranking.Excluded) != len(expected) {
		t.Fatalf("excluded = %d, want %d: %+v", len(ranking.Excluded), len(expected), ranking.Excluded)
	}
	for _, exclusion := range ranking.Excluded {
		key := exclusion.FolderID + "|" + exclusion.LotID
		reason, ok := expected[key]
		if !ok {
			t.Errorf("unexpected exclusion %s", key)
			continue
		}
		if exclusion.Reason != reason {
			t.Errorf("%s reason = %q, want %q", key, exclusion.Reason, reason)
		}
		if exclusion.Detail == "" || exclusion.AwardPublication.URL == "" {
			t.Errorf("%s does not justify the exclusion: %+v", key, exclusion)
		}
	}
}

func TestRebatesDoNotTurnAnAmplificationIntoANewRebate(t *testing.T) {
	ctx := context.Background()
	_, store := rebatesFixture(t)

	expedientes, err := store.ListExpedientes(ctx)
	if err != nil {
		t.Fatalf("list expedientes: %v", err)
	}
	var amplification bool
	for _, expediente := range expedientes {
		if expediente.FolderID != "REB-20/2025" {
			continue
		}
		for _, fact := range expediente.Facts {
			if fact.Kind != "amplification" {
				continue
			}
			for _, amount := range fact.Amounts {
				if amount.Cents == 1_000_000 {
					amplification = true
				}
			}
		}
	}
	if !amplification {
		t.Fatal("the later amplification is not stored; the fixture does not prove the rule")
	}

	ranking, err := store.Rebates(ctx, domain.RankingQuery{
		BuyerScheme: "NIF",
		BuyerID:     "P1000000T",
		Limit:       100,
	})
	if err != nil {
		t.Fatalf("read rebates: %v", err)
	}
	rebate := rebatesByFolder(t, ranking)["REB-20/2025"]
	if rebate.InitialBudget.Cents != 10_000_000 || rebate.Ratio != "0.200000" || rebate.BudgetCorrected {
		t.Fatalf("amplification changed the initial rebate: %+v", rebate)
	}
}

func TestRebatesApplyTheBuyerAndAwardDateFilters(t *testing.T) {
	ctx := context.Background()
	_, store := rebatesFixture(t)

	december, err := store.Rebates(ctx, domain.RankingQuery{
		BuyerScheme: "NIF",
		BuyerID:     "P1000000T",
		From:        "2025-12-01",
		To:          "2025-12-31",
		Limit:       100,
	})
	if err != nil {
		t.Fatalf("read December rebates: %v", err)
	}
	if december.Total != 1 || len(december.Ranking) != 1 || december.Ranking[0].FolderID != "REB-INCLUSIVE/2025" {
		t.Fatalf("December ranking = %+v", december.Ranking)
	}
	if december.ExcludedTotal != 1 || december.Excluded[0].FolderID != "REB-TAXBASE/2025" {
		t.Fatalf("December exclusions = %+v", december.Excluded)
	}

	unknown := domain.RankingQuery{BuyerScheme: "NIF", BuyerID: "P0000000X", Limit: 20}
	if _, err := store.Rebates(ctx, unknown); !errors.Is(err, domain.ErrUnknownBuyer) {
		t.Fatalf("unknown buyer error = %v, want ErrUnknownBuyer", err)
	}
}
