// Package sqlite stores the expediente, its lots, awards, amounts, documents
// and publication versions. It is the only package that knows SQL.
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

// StorePublication writes one immutable publication version and the current
// expediente state. Replaying the same version is a no-op; the same version
// with different bytes is refused instead of silently overwritten.
func (s *Store) StorePublication(ctx context.Context, publication domain.Publication) error {
	updatedAt := publication.UpdatedAt.UTC().Format(time.RFC3339Nano)

	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin publication transaction: %w", err)
	}
	defer tx.Rollback()

	var existingID int64
	var existingHash string
	err = tx.QueryRowContext(ctx,
		`SELECT id, body_sha256 FROM publication WHERE source = ? AND external_id = ? AND updated_at = ?`,
		publication.Source, publication.ExternalID, updatedAt,
	).Scan(&existingID, &existingHash)
	switch {
	case err == nil:
		if existingHash != publication.BodyHash {
			return fmt.Errorf("publication %s/%s at %s is already stored with different bytes", publication.Source, publication.ExternalID, updatedAt)
		}
		return tx.Commit()
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("look up publication version: %w", err)
	}

	expedienteID, err := upsertExpediente(ctx, tx, publication.Expediente)
	if err != nil {
		return err
	}

	for _, lot := range publication.Lots {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO lot (expediente_id, lot_id, object)
			VALUES (?, ?, ?)
			ON CONFLICT (expediente_id, lot_id) DO UPDATE SET
				object = CASE WHEN excluded.object <> '' THEN excluded.object ELSE lot.object END`,
			expedienteID, lot.ID, lot.Object,
		); err != nil {
			return fmt.Errorf("store lot %s: %w", lot.ID, err)
		}
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO publication (source, external_id, url, updated_at, body_sha256, imported_at, expediente_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		publication.Source, publication.ExternalID, publication.URL, updatedAt,
		publication.BodyHash, time.Now().Unix(), expedienteID,
	)
	if err != nil {
		return fmt.Errorf("store publication: %w", err)
	}
	publicationID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("publication id: %w", err)
	}

	if err := storeBudgets(ctx, tx, publicationID, "", publication.Expediente.Budget); err != nil {
		return fmt.Errorf("store expediente budget: %w", err)
	}
	if err := storeClassification(ctx, tx, publicationID, "expediente", "", publication.Expediente.CPV); err != nil {
		return fmt.Errorf("store expediente classification: %w", err)
	}
	for _, lot := range publication.Lots {
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

	for position, award := range publication.Awards {
		if err := storeAward(ctx, tx, publicationID, expedienteID, position, award); err != nil {
			return fmt.Errorf("store award %d: %w", position, err)
		}
	}

	for _, document := range publication.Documents {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO document_reference (publication_id, url, sha256, title)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (publication_id, url, sha256) DO NOTHING`,
			publicationID, document.URL, document.Hash, document.Title,
		); err != nil {
			return fmt.Errorf("store document %s: %w", document.URL, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit publication: %w", err)
	}
	return nil
}

func upsertExpediente(ctx context.Context, tx *sql.Tx, expediente domain.Expediente) (int64, error) {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO expediente (buyer_scheme, buyer_id, folder_id, buyer_name, object, status)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (buyer_scheme, buyer_id, folder_id) DO UPDATE SET
			buyer_name = CASE WHEN excluded.buyer_name <> '' THEN excluded.buyer_name ELSE expediente.buyer_name END,
			object     = CASE WHEN excluded.object <> '' THEN excluded.object ELSE expediente.object END,
			status     = CASE WHEN excluded.status <> '' THEN excluded.status ELSE expediente.status END`,
		expediente.Buyer.Scheme, expediente.Buyer.ID, expediente.FolderID,
		expediente.Buyer.Name, expediente.Object, expediente.Status,
	); err != nil {
		return 0, fmt.Errorf("store expediente: %w", err)
	}

	var id int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM expediente WHERE buyer_scheme = ? AND buyer_id = ? AND folder_id = ?`,
		expediente.Buyer.Scheme, expediente.Buyer.ID, expediente.FolderID,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("read expediente id: %w", err)
	}
	return id, nil
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
