package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/contratos/domain"
	"github.com/hectorruiz23/la-mordida/internal/kernel"
	"github.com/hectorruiz23/la-mordida/internal/platform/db"
	"github.com/hectorruiz23/la-mordida/migrations"
)

func count(t *testing.T, database *sql.DB, query string, args ...any) int {
	t.Helper()
	var got int
	if err := database.QueryRowContext(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return got
}

func TestTwoBuyersWithTheSameFolderAreDifferentProcedures(t *testing.T) {
	store, database := testStore(t)
	ctx := context.Background()

	first := samplePublication(t)
	first.ExternalID = "urn:test:buyer-a"
	first.BodyHash = "hash-a"
	first.Expediente.Buyer = domain.Party{Scheme: "NIF", ID: "A", Name: "Comprador A"}
	first.Expediente.FolderID = "MISMO/2025"
	first.Expediente.Object = "Objeto A"
	first.Lots = nil
	first.Awards = nil
	first.Documents = nil

	second := first
	second.ExternalID = "urn:test:buyer-b"
	second.BodyHash = "hash-b"
	second.Expediente.Buyer = domain.Party{Scheme: "NIF", ID: "B", Name: "Comprador B"}
	second.Expediente.Object = "Objeto B"

	for _, publication := range []domain.Publication{first, second} {
		if err := store.StorePublication(ctx, publication); err != nil {
			t.Fatalf("store %s: %v", publication.ExternalID, err)
		}
	}

	if got := count(t, database, `SELECT COUNT(*) FROM expediente`); got != 2 {
		t.Fatalf("expedientes = %d, want 2: the folder id alone does not identify a buyer", got)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM expediente WHERE folder_id = 'MISMO/2025'`); got != 2 {
		t.Fatalf("same-folder procedures = %d, want 2", got)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM buyer`); got != 2 {
		t.Fatalf("buyers = %d, want 2", got)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM buyer_identity`); got != 2 {
		t.Fatalf("buyer identities = %d, want 2", got)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM expediente_identity`); got != 2 {
		t.Fatalf("expediente identities = %d, want 2", got)
	}

	var objects, buyerIDs int
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT object), COUNT(DISTINCT buyer_id) FROM expediente
		WHERE folder_id = 'MISMO/2025'`).Scan(&objects, &buyerIDs); err != nil {
		t.Fatalf("read same-folder procedures: %v", err)
	}
	if objects != 2 || buyerIDs != 2 {
		t.Fatalf("same-folder objects/buyers = %d/%d, want 2/2", objects, buyerIDs)
	}
}

func TestTwoAwardsInOneProcedureAndOneWithoutLot(t *testing.T) {
	store, database := testStore(t)
	ctx := context.Background()

	publication := samplePublication(t)
	publication.Expediente.FolderID = "DOS-LOTES/2025"
	publication.ExternalID = "urn:test:two-awards"
	publication.BodyHash = "hash-two-awards"
	publication.Awards = []domain.Award{{
		LotID:      "1",
		ResultCode: "8",
		AwardDate:  date(t, "2025-10-10"),
		Winner:     domain.Party{Scheme: "NIF", ID: "B1", Name: "Ganador del lote 1"},
		Amounts:    []kernel.Money{money(t, "100", "EUR", kernel.TaxExclusive)},
	}, {
		LotID:      "2",
		ResultCode: "8",
		AwardDate:  date(t, "2025-10-10"),
		Winner:     domain.Party{Scheme: "NIF", ID: "B2", Name: "Ganador del lote 2"},
		Amounts:    []kernel.Money{money(t, "200", "EUR", kernel.TaxExclusive)},
	}}

	if err := store.StorePublication(ctx, publication); err != nil {
		t.Fatalf("store two-lot publication: %v", err)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM award`); got != 2 {
		t.Fatalf("award history rows = %d, want 2", got)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM award_projection`); got != 2 {
		t.Fatalf("award projections = %d, want one per lot", got)
	}

	global := samplePublication(t)
	global.Expediente.FolderID = "GLOBAL/2025"
	global.ExternalID = "urn:test:global-award"
	global.BodyHash = "hash-global-award"
	global.Lots = nil
	global.Awards = []domain.Award{{
		ResultCode: "8",
		AwardDate:  date(t, "2025-10-11"),
		Winner:     domain.Party{Scheme: "NIF", ID: "B3", Name: "Ganador global"},
		Amounts:    []kernel.Money{money(t, "300", "EUR", kernel.TaxExclusive)},
	}}
	if err := store.StorePublication(ctx, global); err != nil {
		t.Fatalf("store global award: %v", err)
	}

	var lotID string
	var state string
	if err := database.QueryRowContext(ctx, `
		SELECT lot_id, state FROM award_projection
		WHERE expediente_id = (SELECT id FROM expediente WHERE folder_id = 'GLOBAL/2025')`,
	).Scan(&lotID, &state); err != nil {
		t.Fatalf("read global projection: %v", err)
	}
	if lotID != "" || state != "known" {
		t.Fatalf("global award projection = lot %q / %s, want empty lot and known", lotID, state)
	}
}

func TestOutOfOrderReplayKeepsTheNewestEffectiveRevision(t *testing.T) {
	store, database := testStore(t)
	ctx := context.Background()

	base := samplePublication(t)
	base.Expediente.FolderID = "ORDEN/2025"
	base.ExternalID = "urn:test:order"
	base.BodyHash = "hash-newer"
	base.UpdatedAt = time.Date(2025, 10, 22, 9, 0, 0, 500000000, time.UTC)
	base.Expediente.Object = "Objeto corregido"
	base.Awards[0].Amounts = []kernel.Money{money(t, "700", "EUR", kernel.TaxExclusive)}

	older := samplePublication(t)
	older.Expediente.FolderID = base.Expediente.FolderID
	older.ExternalID = base.ExternalID
	older.BodyHash = "hash-older"
	older.UpdatedAt = time.Date(2025, 10, 20, 9, 0, 0, 0, time.UTC)
	older.Expediente.Object = "Objeto original"
	older.Awards[0].Amounts = []kernel.Money{money(t, "800", "EUR", kernel.TaxExclusive)}

	// Newer first, then the older reread, then an exact replay of the older.
	for _, publication := range []domain.Publication{base, older, older} {
		if err := store.StorePublication(ctx, publication); err != nil {
			t.Fatalf("store %s: %v", publication.BodyHash, err)
		}
	}

	if got := count(t, database, `SELECT COUNT(*) FROM publication`); got != 2 {
		t.Fatalf("publications = %d, want two versions and no replay row", got)
	}

	var object, published string
	if err := database.QueryRowContext(ctx, `
		SELECT e.object, p.published_at
		FROM expediente e JOIN publication p ON p.id = e.current_publication_id
		WHERE e.folder_id = 'ORDEN/2025'`,
	).Scan(&object, &published); err != nil {
		t.Fatalf("read current projection: %v", err)
	}
	if object != "Objeto corregido" || published != base.UpdatedAt.Format(time.RFC3339Nano) {
		t.Fatalf("current projection = %q at %s, want the newer revision", object, published)
	}

	var cents int64
	if err := database.QueryRowContext(ctx, `
		SELECT amount_cents FROM award_projection_amount
		WHERE expediente_id = (SELECT id FROM expediente WHERE folder_id = 'ORDEN/2025')
		  AND lot_id = '1' AND tax_base = 'tax_exclusive'`,
	).Scan(&cents); err != nil {
		t.Fatalf("read projected amount: %v", err)
	}
	if cents != 70000 {
		t.Fatalf("projected amount = %d, want the newer 700.00", cents)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM award`); got != 2 {
		t.Fatalf("award history rows = %d, want both versions", got)
	}
}

func TestSameInstantConflictKeepsBothRevisionsAndLeavesTheAmountIndeterminate(t *testing.T) {
	store, database := testStore(t)
	ctx := context.Background()

	at := time.Date(2025, 10, 20, 8, 0, 0, 0, time.UTC)
	first := samplePublication(t)
	first.Expediente.FolderID = "CONFLICTO/2025"
	first.ExternalID = "urn:test:conflict"
	first.UpdatedAt = at
	first.BodyHash = "hash-first"
	first.Awards[0].Amounts = []kernel.Money{money(t, "100", "EUR", kernel.TaxExclusive)}
	first.Awards[0].Winner = domain.Party{Scheme: "NIF", ID: "B1", Name: "Ganador"}

	second := first
	second.BodyHash = "hash-second"
	second.Awards = []domain.Award{{
		LotID:      "1",
		ResultCode: "8",
		AwardDate:  date(t, "2025-10-10"),
		Winner:     domain.Party{Scheme: "NIF", ID: "B1", Name: "Ganador"},
		Amounts:    []kernel.Money{money(t, "200", "EUR", kernel.TaxExclusive)},
	}}

	for _, publication := range []domain.Publication{first, second} {
		if err := store.StorePublication(ctx, publication); err != nil {
			t.Fatalf("store %s: %v", publication.BodyHash, err)
		}
	}

	if got := count(t, database, `SELECT COUNT(*) FROM publication`); got != 2 {
		t.Fatalf("publications = %d, want both same-instant revisions", got)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM award`); got != 2 {
		t.Fatalf("award evidence rows = %d, want both", got)
	}

	var state, winnerID string
	if err := database.QueryRowContext(ctx, `
		SELECT state, winner_id FROM award_projection
		WHERE expediente_id = (SELECT id FROM expediente WHERE folder_id = 'CONFLICTO/2025')
		  AND lot_id = '1'`,
	).Scan(&state, &winnerID); err != nil {
		t.Fatalf("read conflict projection: %v", err)
	}
	if state != "indeterminate" {
		t.Fatalf("projection state = %q, want indeterminate", state)
	}
	if winnerID != "B1" {
		t.Fatalf("agreed winner = %q, want B1 preserved", winnerID)
	}
	if got := count(t, database, `
		SELECT COUNT(*) FROM award_projection_amount
		WHERE expediente_id = (SELECT id FROM expediente WHERE folder_id = 'CONFLICTO/2025')`,
	); got != 0 {
		t.Fatalf("disputed amount rows = %d, want none", got)
	}
}

func TestRepeatedPhotoThroughTwoChannelsDoesNotCreateASecondVictory(t *testing.T) {
	store, database := testStore(t)
	ctx := context.Background()

	first := samplePublication(t)
	first.Source = "canal-a"
	first.ExternalID = "urn:test:same-photo"
	first.BodyHash = "same-body-hash"
	first.Expediente.FolderID = "CANAL/2025"

	second := first
	second.Source = "canal-b"

	for _, publication := range []domain.Publication{first, second} {
		if err := store.StorePublication(ctx, publication); err != nil {
			t.Fatalf("store %s: %v", publication.Source, err)
		}
	}

	if got := count(t, database, `SELECT COUNT(*) FROM publication`); got != 2 {
		t.Fatalf("revisions = %d, want both channels' revisions kept", got)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM expediente`); got != 1 {
		t.Fatalf("expedientes = %d, want one procedure", got)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM award_projection`); got != 1 {
		t.Fatalf("award projections = %d, want one victory", got)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM award`); got != 2 {
		t.Fatalf("award evidence rows = %d, want both revisions kept", got)
	}
}

func TestDocumentedAnnulmentCancelsAndSilenceDoesNot(t *testing.T) {
	store, database := testStore(t)
	ctx := context.Background()

	first := samplePublication(t)
	first.Expediente.FolderID = "ANULADA/2025"
	first.ExternalID = "urn:test:annulled"
	first.BodyHash = "hash-award"
	first.UpdatedAt = time.Date(2025, 10, 20, 8, 0, 0, 0, time.UTC)

	// A later snapshot without an award is silence, not a cancellation.
	silent := first
	silent.BodyHash = "hash-silent"
	silent.UpdatedAt = time.Date(2025, 10, 21, 8, 0, 0, 0, time.UTC)
	silent.Awards = nil

	if err := store.StorePublication(ctx, first); err != nil {
		t.Fatalf("store award: %v", err)
	}
	if err := store.StorePublication(ctx, silent); err != nil {
		t.Fatalf("store silent revision: %v", err)
	}

	var state string
	if err := database.QueryRowContext(ctx, `
		SELECT state FROM award_projection
		WHERE expediente_id = (SELECT id FROM expediente WHERE folder_id = 'ANULADA/2025') AND lot_id = '1'`,
	).Scan(&state); err != nil {
		t.Fatalf("read projection after silence: %v", err)
	}
	if state != "known" {
		t.Fatalf("projection after a snapshot without awards = %q, want known: silence is not a cancellation", state)
	}

	annulled := silent
	annulled.BodyHash = "hash-annulled"
	annulled.UpdatedAt = time.Date(2025, 10, 22, 8, 0, 0, 0, time.UTC)
	annulled.Facts = []domain.Fact{{
		Kind:          domain.FactAnnulment,
		Ref:           "deleted-entry:1",
		EffectiveDate: date(t, "2025-10-22"),
		Detail:        "ANULADA",
	}}
	if err := store.StorePublication(ctx, annulled); err != nil {
		t.Fatalf("store documented annulment: %v", err)
	}

	var expedienteState string
	if err := database.QueryRowContext(ctx, `
		SELECT projection_state FROM expediente WHERE folder_id = 'ANULADA/2025'`,
	).Scan(&expedienteState); err != nil {
		t.Fatalf("read expediente state: %v", err)
	}
	if expedienteState != "annulled" {
		t.Fatalf("expediente state = %q, want annulled", expedienteState)
	}
	if err := database.QueryRowContext(ctx, `
		SELECT state FROM award_projection
		WHERE expediente_id = (SELECT id FROM expediente WHERE folder_id = 'ANULADA/2025') AND lot_id = '1'`,
	).Scan(&state); err != nil {
		t.Fatalf("read projection after annulment: %v", err)
	}
	if state != "cancelled" {
		t.Fatalf("award projection after annulment = %q, want cancelled", state)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM award`); got != 1 {
		t.Fatalf("award evidence rows = %d, want the earlier award kept", got)
	}
}

func TestLifecycleFactsKeepTheirKindAndSeparateDeadlineFromDelivery(t *testing.T) {
	store, database := testStore(t)
	ctx := context.Background()

	kinds := []domain.FactKind{
		domain.FactRectification,
		domain.FactExtensionPlanned,
		domain.FactExtensionExercised,
		domain.FactAmplification,
		domain.FactScopeChange,
		domain.FactReception,
		domain.FactFinalPayment,
	}

	publication := samplePublication(t)
	publication.Expediente.FolderID = "HECHOS/2025"
	publication.ExternalID = "urn:test:facts"
	publication.BodyHash = "hash-facts"
	publication.EffectiveDate = date(t, "2025-10-01")
	publication.ObservedAt = time.Date(2025, 10, 23, 12, 0, 0, 0, time.UTC)
	for index, kind := range kinds {
		fact := domain.Fact{
			Kind:          kind,
			Ref:           string(kind),
			EffectiveDate: date(t, "2025-10-11"),
			Detail:        "documentado",
		}
		if index == 0 {
			fact.DeadlineStatus = domain.ComplianceLate
			fact.DeliveryStatus = domain.DeliveryUnknown
		}
		if kind == domain.FactReception {
			fact.DeadlineStatus = domain.ComplianceUnknown
			fact.DeliveryStatus = domain.DeliveryDelivered
			fact.Amounts = []kernel.Money{money(t, "123.45", "EUR", kernel.TaxExclusive)}
		}
		publication.Facts = append(publication.Facts, fact)
	}

	if err := store.StorePublication(ctx, publication); err != nil {
		t.Fatalf("store facts: %v", err)
	}

	if got := count(t, database, `SELECT COUNT(*) FROM fact`); got != len(kinds) {
		t.Fatalf("facts = %d, want %d", got, len(kinds))
	}
	if got := count(t, database, `SELECT COUNT(DISTINCT kind) FROM fact`); got != len(kinds) {
		t.Fatalf("distinct fact kinds = %d, want %d", got, len(kinds))
	}

	var effectiveDate string
	var observedAt int64
	if err := database.QueryRowContext(ctx, `
		SELECT effective_date, observed_at FROM publication WHERE body_sha256 = 'hash-facts'`,
	).Scan(&effectiveDate, &observedAt); err != nil {
		t.Fatalf("read revision dates: %v", err)
	}
	if effectiveDate != "2025-10-01" || observedAt != publication.ObservedAt.Unix() {
		t.Fatalf("revision dates = %q / %d", effectiveDate, observedAt)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM fact_amount`); got != 1 {
		t.Fatalf("fact amounts = %d, want 1", got)
	}

	var deadline, delivery string
	if err := database.QueryRowContext(ctx, `
		SELECT deadline_status, delivery_status FROM fact WHERE kind = 'reception'`,
	).Scan(&deadline, &delivery); err != nil {
		t.Fatalf("read reception statuses: %v", err)
	}
	if deadline != "unknown" || delivery != "delivered" {
		t.Fatalf("reception statuses = %q / %q, want unknown / delivered", deadline, delivery)
	}
}

func TestMigrationCarriesOldRevisionsIntoTheProjection(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/contratos.db"
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()

	old := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() >= "0004" {
			continue
		}
		content, err := migrations.FS.ReadFile(entry.Name())
		if err != nil {
			t.Fatalf("read migration %s: %v", entry.Name(), err)
		}
		old[entry.Name()] = &fstest.MapFile{Data: content}
	}
	if err := db.Migrate(ctx, database, old); err != nil {
		t.Fatalf("migrate old schema: %v", err)
	}

	if _, err := database.ExecContext(ctx, `
		INSERT INTO expediente (id, buyer_scheme, buyer_id, folder_id, buyer_name, object, status)
		VALUES (1, 'NIF', 'P1', 'OLD/2025', 'Comprador', 'Objeto antiguo', 'ADJ');
		INSERT INTO publication (id, source, external_id, url, updated_at, body_sha256, imported_at, expediente_id)
		VALUES (1, 'fixture', 'urn:old', 'https://example.test/old', '2025-10-20T08:00:00Z', 'hash-old', 1795500000, 1);
		INSERT INTO award (id, publication_id, expediente_id, position, lot_id, result_code, award_date,
			winner_scheme, winner_id, winner_name, contract_id, contract_issue_date)
		VALUES (1, 1, 1, 0, '', '8', '2025-10-19', 'NIF', 'B1', 'Ganador', '', NULL);
		INSERT INTO award_amount (award_id, tax_base, currency, amount_cents)
		VALUES (1, 'tax_exclusive', 'EUR', 12345);
		INSERT INTO budget_amount (publication_id, lot_key, tax_base, currency, amount_cents)
		VALUES (1, '', 'tax_exclusive', 'EUR', 20000);
		INSERT INTO objeto (sha256, mime, bytes, stored_at)
		VALUES ('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'application/pdf', 10, 1795500000);
		INSERT INTO document_link (id, publication_id, target_kind, target_ref, url, title, state, sha256)
		VALUES (1, 1, 'publication', '', 'https://example.test/pliego', 'Pliego', 'evidence',
		        'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa');
		INSERT INTO document_evidence (document_link_id, sha256, obtained_at)
		VALUES (1, 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 1795500001);`,
	); err != nil {
		t.Fatalf("seed old schema: %v", err)
	}

	if err := db.Migrate(ctx, database, migrations.FS); err != nil {
		t.Fatalf("migrate to history schema: %v", err)
	}

	store := New(database)
	if err := store.StorePublication(ctx, samplePublication(t)); err != nil {
		t.Fatalf("store after migration: %v", err)
	}

	var publishedAt string
	var observedAt int64
	var object, state, projectionState string
	if err := database.QueryRowContext(ctx, `
		SELECT p.published_at, p.observed_at, p.object, h.state, e.projection_state
		FROM publication p
		JOIN award_projection h ON h.expediente_id = p.expediente_id AND h.lot_id = ''
		JOIN expediente e ON e.id = p.expediente_id
		WHERE p.id = 1`,
	).Scan(&publishedAt, &observedAt, &object, &state, &projectionState); err != nil {
		t.Fatalf("read migrated projection: %v", err)
	}
	if publishedAt != "2025-10-20T08:00:00Z" || observedAt != 1795500000 || object != "Objeto antiguo" {
		t.Fatalf("migrated revision = %q / %d / %q", publishedAt, observedAt, object)
	}
	if state != "known" || projectionState != "known" {
		t.Fatalf("migrated projection = %q / %q, want known", state, projectionState)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM buyer_identity`); got != 2 {
		t.Fatalf("buyer identities after new import = %d, want the old buyer and the new one", got)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM document_link WHERE publication_id = 1`); got != 1 {
		t.Fatalf("migrated document links = %d, want 1", got)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM document_evidence`); got != 1 {
		t.Fatalf("migrated document evidence = %d, want 1", got)
	}
}

func TestLateCorrectionReplacesTheProjectionAndKeepsBothAwards(t *testing.T) {
	store, database := testStore(t)
	ctx := context.Background()

	base := samplePublication(t)
	base.Expediente.FolderID = "TARDIA/2025"
	base.ExternalID = "urn:test:late"
	base.BodyHash = "hash-base"
	base.UpdatedAt = time.Date(2025, 10, 20, 8, 0, 0, 0, time.UTC)
	base.Awards[0].Amounts = []kernel.Money{money(t, "100", "EUR", kernel.TaxExclusive)}

	correction := base
	correction.BodyHash = "hash-correction"
	correction.UpdatedAt = time.Date(2025, 10, 22, 8, 0, 0, 0, time.UTC)
	correction.Awards = []domain.Award{{
		LotID:      "1",
		ResultCode: "8",
		AwardDate:  date(t, "2025-10-10"),
		Winner:     base.Awards[0].Winner,
		Amounts:    []kernel.Money{money(t, "90", "EUR", kernel.TaxExclusive)},
	}}

	if err := store.StorePublication(ctx, base); err != nil {
		t.Fatalf("store base: %v", err)
	}
	if err := store.StorePublication(ctx, correction); err != nil {
		t.Fatalf("store late correction: %v", err)
	}

	if got := count(t, database, `SELECT COUNT(*) FROM publication`); got != 2 {
		t.Fatalf("publications = %d, want base and correction", got)
	}
	if got := count(t, database, `SELECT COUNT(*) FROM award`); got != 2 {
		t.Fatalf("award evidence rows = %d, want both", got)
	}
	var cents int64
	if err := database.QueryRowContext(ctx, `
		SELECT amount_cents FROM award_projection_amount
		WHERE expediente_id = (SELECT id FROM expediente WHERE folder_id = 'TARDIA/2025')
		  AND lot_id = '1' AND tax_base = 'tax_exclusive'`,
	).Scan(&cents); err != nil {
		t.Fatalf("read corrected projection: %v", err)
	}
	if cents != 9000 {
		t.Fatalf("corrected amount = %d, want 90.00", cents)
	}
}
