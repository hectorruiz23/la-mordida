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
	fixture "github.com/hectorruiz23/la-mordida/internal/ingesta/adapters/fixture"
	ingestaapp "github.com/hectorruiz23/la-mordida/internal/ingesta/app"
	"github.com/hectorruiz23/la-mordida/internal/platform/db"
	"github.com/hectorruiz23/la-mordida/migrations"
)

func rankingFixture(t *testing.T) (*sql.DB, *Store) {
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
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "testdata", "consultas", "ranking.json")
	importer := ingestaapp.NewImporter(fixture.New(path), contratossqlite.New(database))
	if _, err := importer.Run(ctx, ingestaapp.Window{}); err != nil {
		t.Fatalf("import fixture: %v", err)
	}

	// The relation between entities is documented data, not something the
	// source publishes. It is stored with its source and validity.
	curation := []string{
		`INSERT INTO entity (id, kind, legal_name) VALUES
			(100, 'ute', 'UTE A y B'),
			(200, 'company', ''),
			(201, 'company', ''),
			(300, 'group', 'Grupo A')`,
		`INSERT INTO entity_identity (entity_id, scheme, external_id, source, valid_from, valid_to) VALUES
			(100, 'OTROS', 'U00000001', 'fixture', '2025-01-01', ''),
			(200, 'NIF', 'B10000001', 'fixture', '', ''),
			(201, 'NIF', 'B20000002', 'fixture', '', '')`,
		`INSERT INTO entity_name (entity_id, name, source) VALUES
			(200, 'Construcciones A, SL', 'fixture'),
			(201, 'Construcciones A, SL', 'fixture')`,
		`INSERT INTO entity_member (group_id, member_id, source, valid_from, valid_to, percentage_bps) VALUES
			(100, 200, 'fixture', '2025-01-01', '', 6000),
			(100, 201, 'fixture', '2025-01-01', '', 4000)`,
		`INSERT INTO entity_group (entity_id, group_id, relation, source, valid_from, valid_to) VALUES
			(200, 300, 'legal_holder', 'fixture', '2024-01-01', '')`,
	}
	for _, statement := range curation {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatalf("curate entities: %v", err)
		}
	}
	return database, New(database)
}

func TestRankingCountsOneUnitPerResultAndLot(t *testing.T) {
	ctx := context.Background()
	_, store := rankingFixture(t)

	ranking, err := store.Ranking(ctx, domain.RankingQuery{
		BuyerScheme: "NIF",
		BuyerID:     "P1000000A",
		Limit:       20,
	})
	if err != nil {
		t.Fatalf("rank by buyer: %v", err)
	}
	if ranking.Total != 3 {
		t.Fatalf("total = %d, want 3", ranking.Total)
	}
	if len(ranking.Ranking) != 2 {
		t.Fatalf("ranking = %+v, want two winners", ranking.Ranking)
	}

	first := ranking.Ranking[0]
	if first.Entity.Key != domain.EntityKey(200) || first.Wins != 2 || first.Percentage != "66.67" {
		t.Fatalf("first winner = %+v, want A with 2 of 3", first)
	}
	second := ranking.Ranking[1]
	if second.Entity.Key != domain.EntityKey(201) || second.Wins != 1 || second.Percentage != "33.33" {
		t.Fatalf("second winner = %+v, want B with 1 of 3", second)
	}

	// Two entities can publish the same name; the identifier keeps them apart.
	if first.Entity.Name != "Construcciones A, SL" || second.Entity.Name != first.Entity.Name {
		t.Fatalf("homonym names = %q / %q", first.Entity.Name, second.Entity.Name)
	}
}

func TestRankingCountsTheUTEOnceAndKeepsItsMembers(t *testing.T) {
	ctx := context.Background()
	_, store := rankingFixture(t)

	ranking, err := store.Ranking(ctx, domain.RankingQuery{Limit: 20})
	if err != nil {
		t.Fatalf("rank every buyer: %v", err)
	}
	if ranking.Total != 4 {
		t.Fatalf("total = %d, want 4 award units", ranking.Total)
	}
	if len(ranking.Ranking) != 3 {
		t.Fatalf("ranking = %+v, want three entities", ranking.Ranking)
	}

	ute := ranking.Ranking[1]
	if ute.Entity.Key != domain.EntityKey(100) || ute.Wins != 1 || ute.Percentage != "25.00" {
		t.Fatalf("UTE row = %+v, want one win of four", ute)
	}
	if ute.Entity.Kind != "ute" || len(ute.Entity.Members) != 2 {
		t.Fatalf("UTE entity = %+v, want two members", ute.Entity)
	}
	percentages := map[string]string{}
	for _, member := range ute.Entity.Members {
		percentages[member.Member.ID] = member.Percentage
	}
	if percentages["B10000001"] != "60.00" || percentages["B20000002"] != "40.00" {
		t.Fatalf("member percentages = %+v", percentages)
	}

	a := ranking.Ranking[0]
	if len(a.Entity.Groups) != 1 {
		t.Fatalf("A groups = %+v, want its legal holder", a.Entity.Groups)
	}
	group := a.Entity.Groups[0]
	if group.Relation != "legal_holder" || group.Group.Name != "Grupo A" || group.ValidFrom != "2024-01-01" {
		t.Fatalf("A group link = %+v", group)
	}
}

func TestRankingBuyerAndPeriodFilterTheNumeratorAndDenominator(t *testing.T) {
	ctx := context.Background()
	_, store := rankingFixture(t)

	february, err := store.Ranking(ctx, domain.RankingQuery{
		BuyerScheme: "NIF",
		BuyerID:     "P1000000A",
		From:        "2025-02-01",
		To:          "2025-02-28",
		Limit:       20,
	})
	if err != nil {
		t.Fatalf("rank February: %v", err)
	}
	if february.Total != 1 || len(february.Ranking) != 1 {
		t.Fatalf("February ranking = %+v", february)
	}
	if february.Ranking[0].Wins != 1 || february.Ranking[0].Percentage != "100.00" {
		t.Fatalf("February winner = %+v", february.Ranking[0])
	}

	ute, err := store.Ranking(ctx, domain.RankingQuery{
		BuyerScheme: "NIF",
		BuyerID:     "P9000000Z",
		Limit:       20,
	})
	if err != nil {
		t.Fatalf("rank the UTE buyer: %v", err)
	}
	if ute.Total != 1 || len(ute.Ranking) != 1 || ute.Ranking[0].Entity.Key != domain.EntityKey(100) || ute.Ranking[0].Wins != 1 {
		t.Fatalf("UTE buyer ranking = %+v", ute)
	}

	unknown := domain.RankingQuery{BuyerScheme: "NIF", BuyerID: "P0000000X", Limit: 20}
	if _, err := store.Ranking(ctx, unknown); !errors.Is(err, domain.ErrUnknownBuyer) {
		t.Fatalf("unknown buyer error = %v, want ErrUnknownBuyer", err)
	}
}

func TestAwardsDetailJustifiesEachUnitWithTheCurrentRevision(t *testing.T) {
	ctx := context.Background()
	_, store := rankingFixture(t)

	page, err := store.Awards(ctx, domain.RankingQuery{
		BuyerScheme: "NIF",
		BuyerID:     "P1000000A",
		Limit:       20,
	}, domain.EntityKey(200))
	if err != nil {
		t.Fatalf("read A awards: %v", err)
	}
	if page.Total != 2 || len(page.Awards) != 2 {
		t.Fatalf("A page = %+v, want two awards", page)
	}
	if page.Awards[0].LotID != "1" || page.Awards[1].LotID != "2" {
		t.Fatalf("A lots = %q / %q", page.Awards[0].LotID, page.Awards[1].LotID)
	}
	// The correction of 2025-05-01 changed the amount; the unit is still one
	// and the detail follows the current revision, not the older one.
	if len(page.Awards[0].Amounts) != 1 || page.Awards[0].Amounts[0].Cents != 3_900_000 {
		t.Fatalf("lot 1 amounts = %+v, want the corrected 39000.00", page.Awards[0].Amounts)
	}
	if page.Awards[0].Publication.UpdatedAt != "2025-05-01T09:00:00Z" {
		t.Fatalf("lot 1 publication = %+v", page.Awards[0].Publication)
	}
	if len(page.Awards[0].Documents) != 1 || page.Awards[0].Documents[0].State != "announced" {
		t.Fatalf("lot 1 documents = %+v", page.Awards[0].Documents)
	}

	// A global award has no lot and still has a unit key and one line.
	minor, err := store.Awards(ctx, domain.RankingQuery{
		BuyerScheme: "NIF",
		BuyerID:     "P1000000A",
		Limit:       20,
	}, domain.EntityKey(201))
	if err != nil {
		t.Fatalf("read B awards: %v", err)
	}
	if minor.Total != 1 || len(minor.Awards) != 1 {
		t.Fatalf("B page = %+v", minor)
	}
	if minor.Awards[0].LotID != "" || minor.Awards[0].UnitKey == "" {
		t.Fatalf("global award = %+v, want an empty lot and a unit key", minor.Awards[0])
	}
	if len(minor.Awards[0].Amounts) != 1 || minor.Awards[0].Amounts[0].Cents != 1_000_000 {
		t.Fatalf("B amounts = %+v", minor.Awards[0].Amounts)
	}
}

func TestPercentageIsExactIntegerArithmetic(t *testing.T) {
	cases := []struct {
		wins, total int
		want        string
	}{
		{2, 3, "66.67"},
		{1, 3, "33.33"},
		{0, 3, "0.00"},
		{3, 3, "100.00"},
	}
	for _, test := range cases {
		if got := domain.Percentage(test.wins, test.total); got != test.want {
			t.Errorf("Percentage(%d, %d) = %q, want %q", test.wins, test.total, got, test.want)
		}
	}
}

func TestAwardUnitKeyKeepsTheMissingLot(t *testing.T) {
	if got := domain.UnitKey(7, ""); got != "7:" {
		t.Fatalf("UnitKey without lot = %q", got)
	}
	if got := domain.UnitKey(7, "1"); got != "7:1" {
		t.Fatalf("UnitKey with lot = %q", got)
	}
}
