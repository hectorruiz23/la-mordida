// Package sqlite stores the expediente, its lots, awards, lifecycle facts,
// amounts, documents and publication revisions. It is the only contratos
// package that knows SQL.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/contratos/domain"
	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

type Store struct {
	database *sql.DB
}

func New(database *sql.DB) *Store {
	return &Store{database: database}
}

// StorePublication writes one source revision and, in the same transaction,
// refreshes the current projection. The revision identity is (source,
// external id, published instant, body hash):
//
//   - an exact replay is a no-op;
//   - the same instant with other bytes keeps a second revision;
//   - an older revision is stored beside the newer one but never rewinds the
//     projection, which follows the newest published revision;
//   - a source snapshot that omits a field never cancels what an earlier one
//     stated: only an explicit annulment fact does that.
func (s *Store) StorePublication(ctx context.Context, publication domain.Publication) error {
	publishedAt := publication.UpdatedAt.UTC().Format(time.RFC3339Nano)
	observedAt := publication.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now()
	}

	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin publication transaction: %w", err)
	}
	defer tx.Rollback()

	// A source may publish two irreconcilable snapshots at the same instant:
	// the hash is part of the identity, so the row is inserted beside the
	// first. Only an exact replay short-circuits.
	var existingHash string
	err = tx.QueryRowContext(ctx, `
		SELECT body_sha256 FROM publication
		WHERE source = ? AND external_id = ? AND published_at = ? AND body_sha256 = ?`,
		publication.Source, publication.ExternalID, publishedAt, publication.BodyHash,
	).Scan(&existingHash)
	switch {
	case err == nil:
		return tx.Commit()
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("look up publication revision: %w", err)
	}

	enter, filter, err := admit(ctx, tx, publication)
	if err != nil {
		return err
	}
	if !enter {
		// An excluded unit leaves no trace: no expediente, no publication, no
		// amount and no document link. The importer counts it before this
		// point; the store enforces the same rule for a direct caller.
		return nil
	}
	includedLots := map[string]bool(nil)
	if filter {
		includedLots = publication.IncludedLots()
	}
	lots := publication.Lots
	if filter {
		lots = includedLotsOf(publication.Lots, includedLots)
	}

	expedienteID, err := resolveExpediente(ctx, tx, publication, observedAt.Unix())
	if err != nil {
		return err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO publication (source, external_id, url, published_at, effective_date,
			observed_at, body_sha256, object, status, expediente_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		publication.Source, publication.ExternalID, publication.URL, publishedAt,
		nullableDate(publication.EffectiveDate), observedAt.Unix(), publication.BodyHash,
		publication.Expediente.Object, publication.Expediente.Status, expedienteID,
	)
	if err != nil {
		return fmt.Errorf("store publication: %w", err)
	}
	publicationID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("publication id: %w", err)
	}

	if err := upsertLots(ctx, tx, expedienteID, publicationID, lots); err != nil {
		return err
	}
	if err := storeBudgets(ctx, tx, publicationID, "", publication.Expediente.Budget); err != nil {
		return fmt.Errorf("store expediente budget: %w", err)
	}
	if err := storeClassification(ctx, tx, publicationID, "expediente", "", publication.Expediente.CPV); err != nil {
		return fmt.Errorf("store expediente classification: %w", err)
	}
	for _, lot := range lots {
		if err := storeBudgets(ctx, tx, publicationID, lot.ID, lot.Budget); err != nil {
			return fmt.Errorf("store lot %s budget: %w", lot.ID, err)
		}
		if err := storeClassification(ctx, tx, publicationID, "lote", lot.ID, lot.CPV); err != nil {
			return fmt.Errorf("store lot %s classification: %w", lot.ID, err)
		}
	}

	for position, notice := range publication.Notices {
		if err := storeNotice(ctx, tx, publicationID, position, notice); err != nil {
			return fmt.Errorf("store notice %d: %w", position, err)
		}
	}

	keptAwards := 0
	for position, award := range publication.Awards {
		if filter && !unitIncluded(award.LotID, publication, includedLots) {
			continue
		}
		if err := storeAward(ctx, tx, publicationID, expedienteID, position, award); err != nil {
			return fmt.Errorf("store award %d: %w", position, err)
		}
		keptAwards++
	}

	for position, fact := range publication.Facts {
		if filter && !unitIncluded(fact.LotID, publication, includedLots) {
			continue
		}
		if err := storeFact(ctx, tx, publicationID, expedienteID, position, fact); err != nil {
			return fmt.Errorf("store fact %d: %w", position, err)
		}
	}

	for _, document := range publication.Documents {
		if filter && !documentIncluded(document, includedLots, keptAwards > 0) {
			continue
		}
		kind := document.Kind
		if kind == "" {
			kind = "publication"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO document_link (publication_id, target_kind, target_ref, url, title, state, sha256)
			VALUES (?, ?, ?, ?, ?, 'announced', ?)
			ON CONFLICT (publication_id, target_kind, target_ref, url) DO UPDATE SET
				title  = CASE WHEN excluded.title <> '' THEN excluded.title ELSE document_link.title END,
				sha256 = CASE WHEN excluded.sha256 <> '' THEN excluded.sha256 ELSE document_link.sha256 END`,
			publicationID, kind, document.Ref, document.URL, document.Title, document.Hash,
		); err != nil {
			return fmt.Errorf("store document %s: %w", document.URL, err)
		}
	}

	if err := rebuildExpedienteProjection(ctx, tx, expedienteID); err != nil {
		return err
	}
	if err := rebuildAwardProjections(ctx, tx, expedienteID); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit publication: %w", err)
	}
	return nil
}

// Followed says whether the source publication already maps to a stored
// procedure, either through its own identity or through the buyer and folder
// pair another channel used before.
func (s *Store) Followed(ctx context.Context, publication domain.Publication) (bool, error) {
	return followed(ctx, s.database, publication)
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func followed(ctx context.Context, queryer queryRower, publication domain.Publication) (bool, error) {
	var found bool
	err := queryer.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM expediente e
			WHERE EXISTS (
					SELECT 1 FROM expediente_identity i
					WHERE i.expediente_id = e.id
					  AND i.source = ? AND i.external_id = ?)
			   OR (e.buyer_ref = (
						SELECT buyer_id FROM buyer_identity
						WHERE scheme = ? AND external_id = ?)
				   AND e.folder_id = ?)
			   OR (e.buyer_scheme = ? AND e.buyer_id = ? AND e.folder_id = ?))`,
		publication.Source, publication.ExternalID,
		publication.Expediente.Buyer.Scheme, publication.Expediente.Buyer.ID,
		publication.Expediente.FolderID,
		publication.Expediente.Buyer.Scheme, publication.Expediente.Buyer.ID,
		publication.Expediente.FolderID,
	).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("look up followed expediente: %w", err)
	}
	return found, nil
}

// admit decides the revision and whether its excluded lots have to be left
// out. A followed procedure applies every correction, even one that publishes
// no CPV, so its revision is stored whole; a new unit only enters through a
// selected family and drops the lots that do not belong to one.
func admit(ctx context.Context, tx *sql.Tx, publication domain.Publication) (bool, bool, error) {
	if publication.Admission().Store {
		return true, true, nil
	}
	exists, err := followed(ctx, tx, publication)
	if err != nil {
		return false, false, err
	}
	if exists {
		return true, false, nil
	}
	return false, false, nil
}

func includedLotsOf(lots []domain.Lot, included map[string]bool) []domain.Lot {
	kept := make([]domain.Lot, 0, len(lots))
	for _, lot := range lots {
		if included[lot.ID] {
			kept = append(kept, lot)
		}
	}
	return kept
}

// unitIncluded says whether an award or fact belongs to a kept scope. A global
// one (no lot) belongs to the expediente; a lot one belongs to its lot.
func unitIncluded(lotID string, publication domain.Publication, includedLots map[string]bool) bool {
	if lotID == "" {
		return publication.Expediente.CPV.Includes()
	}
	return includedLots[lotID]
}

func documentIncluded(document domain.Document, includedLots map[string]bool, hasAward bool) bool {
	switch document.Kind {
	case "lot":
		return includedLots[document.Ref]
	case "award":
		return hasAward
	default:
		return true
	}
}

func nullableDate(date *kernel.Date) any {
	if date == nil || date.IsZero() {
		return nil
	}
	return date.String()
}

func storeBudgets(ctx context.Context, tx *sql.Tx, publicationID int64, lotKey string, amounts []kernel.Money) error {
	for _, amount := range amounts {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO budget_amount (publication_id, lot_key, tax_base, currency, amount_cents)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (publication_id, lot_key, tax_base) DO UPDATE SET
				currency = excluded.currency,
				amount_cents = excluded.amount_cents`,
			publicationID, lotKey, string(amount.TaxBase), amount.Currency, amount.Cents,
		); err != nil {
			return err
		}
	}
	return nil
}

func storeClassification(ctx context.Context, tx *sql.Tx, publicationID int64, scope, lotKey string, classification domain.Classification) error {
	if classification.Source == "" && len(classification.Codes) == 0 {
		return nil
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO classification (publication_id, scope, lot_key, source, ambiguous, reason)
		VALUES (?, ?, ?, ?, ?, ?)`,
		publicationID, scope, lotKey, classification.Source, boolInt(classification.Ambiguous), classification.Reason,
	); err != nil {
		return err
	}

	for position, code := range classification.Codes {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cpv (publication_id, scope, lot_key, position, code, family, included, reason)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			publicationID, scope, lotKey, position, code.Code, code.Family, boolInt(code.Included), code.Reason,
		); err != nil {
			return err
		}
	}
	return nil
}

func storeNotice(ctx context.Context, tx *sql.Tx, publicationID int64, position int, notice domain.Notice) error {
	if len(notice.IssueDates) == 0 {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO notice (publication_id, position, notice_type, medium, issue_index, issue_date)
			VALUES (?, ?, ?, ?, 0, NULL)`,
			publicationID, position, notice.Type, notice.Medium,
		)
		return err
	}
	for index, issueDate := range notice.IssueDates {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notice (publication_id, position, notice_type, medium, issue_index, issue_date)
			VALUES (?, ?, ?, ?, ?, ?)`,
			publicationID, position, notice.Type, notice.Medium, index, issueDate.String(),
		); err != nil {
			return err
		}
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func storeAward(ctx context.Context, tx *sql.Tx, publicationID, expedienteID int64, position int, award domain.Award) error {
	var awardDate any
	if award.AwardDate != nil && !award.AwardDate.IsZero() {
		awardDate = award.AwardDate.String()
	}

	var contractID string
	var contractIssueDate any
	if award.Contract != nil {
		contractID = award.Contract.ID
		if award.Contract.IssueDate != nil && !award.Contract.IssueDate.IsZero() {
			contractIssueDate = award.Contract.IssueDate.String()
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO award (publication_id, expediente_id, position, lot_id, result_code, award_date,
			winner_scheme, winner_id, winner_name, contract_id, contract_issue_date)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		publicationID, expedienteID, position, award.LotID, award.ResultCode, awardDate,
		award.Winner.Scheme, award.Winner.ID, award.Winner.Name, contractID, contractIssueDate,
	); err != nil {
		return err
	}

	var awardID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM award WHERE publication_id = ? AND position = ?`,
		publicationID, position,
	).Scan(&awardID); err != nil {
		return fmt.Errorf("read award id: %w", err)
	}

	for _, amount := range award.Amounts {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO award_amount (award_id, tax_base, currency, amount_cents)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (award_id, tax_base) DO UPDATE SET
				currency = excluded.currency,
				amount_cents = excluded.amount_cents`,
			awardID, string(amount.TaxBase), amount.Currency, amount.Cents,
		); err != nil {
			return fmt.Errorf("store %s amount: %w", amount.TaxBase, err)
		}
	}
	return nil
}

func storeFact(ctx context.Context, tx *sql.Tx, publicationID, expedienteID int64, position int, fact domain.Fact) error {
	deadline := fact.DeadlineStatus
	if deadline == "" {
		deadline = domain.ComplianceUnknown
	}
	delivery := fact.DeliveryStatus
	if delivery == "" {
		delivery = domain.DeliveryUnknown
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO fact (publication_id, expediente_id, lot_id, kind, external_ref,
			effective_date, deadline_status, delivery_status, detail, evidence_sha256)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		publicationID, expedienteID, fact.LotID, string(fact.Kind), fact.Ref,
		nullableDate(fact.EffectiveDate), string(deadline), string(delivery), fact.Detail, fact.EvidenceHash,
	)
	if err != nil {
		return fmt.Errorf("store fact %s %s: %w", fact.Kind, fact.Ref, err)
	}
	factID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("fact id: %w", err)
	}

	for _, amount := range fact.Amounts {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO fact_amount (fact_id, tax_base, currency, amount_cents)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (fact_id, tax_base) DO UPDATE SET
				currency = excluded.currency,
				amount_cents = excluded.amount_cents`,
			factID, string(amount.TaxBase), amount.Currency, amount.Cents,
		); err != nil {
			return fmt.Errorf("store fact %s amount: %w", amount.TaxBase, err)
		}
	}
	return nil
}
