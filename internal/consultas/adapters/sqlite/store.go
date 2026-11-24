package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/hectorruiz23/la-mordida/internal/consultas/domain"
	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

type Store struct {
	database *sql.DB
}

func New(database *sql.DB) *Store {
	return &Store{database: database}
}

// ListExpedientes reads the latest publication version of every expediente.
func (s *Store) ListExpedientes(ctx context.Context) ([]domain.Expediente, error) {
	rows, err := s.database.QueryContext(ctx, `
		SELECT e.id, e.buyer_scheme, e.buyer_id, e.buyer_name, e.folder_id, e.object, e.status,
		       p.id, p.source, p.external_id, p.url, p.updated_at
		FROM expediente e
		JOIN publication p ON p.id = (
			SELECT p2.id FROM publication p2
			WHERE p2.expediente_id = e.id
			ORDER BY p2.updated_at DESC, p2.id DESC
			LIMIT 1
		)
		ORDER BY e.folder_id, e.buyer_id`)
	if err != nil {
		return nil, fmt.Errorf("list expedientes: %w", err)
	}
	defer rows.Close()

	var expedientes []domain.Expediente
	for rows.Next() {
		var expediente domain.Expediente
		var expedienteID, publicationID int64
		if err := rows.Scan(
			&expedienteID,
			&expediente.Buyer.Scheme, &expediente.Buyer.ID, &expediente.Buyer.Name,
			&expediente.FolderID, &expediente.Object, &expediente.Status,
			&publicationID, &expediente.Publication.Source, &expediente.Publication.ExternalID,
			&expediente.Publication.URL, &expediente.Publication.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan expediente: %w", err)
		}

		if expediente.Budget, err = budget(ctx, s.database, publicationID, ""); err != nil {
			return nil, err
		}
		if expediente.Lots, err = lots(ctx, s.database, expedienteID, publicationID); err != nil {
			return nil, err
		}
		if expediente.Awards, err = awards(ctx, s.database, publicationID); err != nil {
			return nil, err
		}
		if expediente.Documents, err = documents(ctx, s.database, publicationID); err != nil {
			return nil, err
		}
		expedientes = append(expedientes, expediente)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list expedientes: %w", err)
	}
	return expedientes, nil
}

func budget(ctx context.Context, database *sql.DB, publicationID int64, lotKey string) ([]kernel.Money, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT tax_base, currency, amount_cents
		FROM budget_amount
		WHERE publication_id = ? AND lot_key = ?
		ORDER BY tax_base`, publicationID, lotKey)
	if err != nil {
		return nil, fmt.Errorf("read budget: %w", err)
	}
	defer rows.Close()

	var amounts []kernel.Money
	for rows.Next() {
		var base, currency string
		var cents int64
		if err := rows.Scan(&base, &currency, &cents); err != nil {
			return nil, fmt.Errorf("scan budget: %w", err)
		}
		amounts = append(amounts, kernel.Money{Cents: cents, Currency: currency, TaxBase: kernel.TaxBase(base)})
	}
	return amounts, rows.Err()
}

func lots(ctx context.Context, database *sql.DB, expedienteID, publicationID int64) ([]domain.Lot, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT lot_id, object
		FROM lot
		WHERE expediente_id = ?
		ORDER BY lot_id`, expedienteID)
	if err != nil {
		return nil, fmt.Errorf("read lots: %w", err)
	}
	defer rows.Close()

	var result []domain.Lot
	for rows.Next() {
		var lot domain.Lot
		if err := rows.Scan(&lot.ID, &lot.Object); err != nil {
			return nil, fmt.Errorf("scan lot: %w", err)
		}
		if lot.Budget, err = budget(ctx, database, publicationID, lot.ID); err != nil {
			return nil, err
		}
		result = append(result, lot)
	}
	return result, rows.Err()
}

func awards(ctx context.Context, database *sql.DB, publicationID int64) ([]domain.Award, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, lot_id, result_code, COALESCE(award_date, ''),
		       winner_scheme, winner_id, winner_name,
		       contract_id, COALESCE(contract_issue_date, '')
		FROM award
		WHERE publication_id = ?
		ORDER BY position`, publicationID)
	if err != nil {
		return nil, fmt.Errorf("read awards: %w", err)
	}
	defer rows.Close()

	var result []domain.Award
	for rows.Next() {
		var awardID int64
		var award domain.Award
		var contractID, contractIssueDate string
		if err := rows.Scan(
			&awardID, &award.LotID, &award.ResultCode, &award.AwardDate,
			&award.Winner.Scheme, &award.Winner.ID, &award.Winner.Name,
			&contractID, &contractIssueDate,
		); err != nil {
			return nil, fmt.Errorf("scan award: %w", err)
		}
		if contractID != "" || contractIssueDate != "" {
			award.Contract = &domain.Contract{ID: contractID, IssueDate: contractIssueDate}
		}
		if award.Amounts, err = awardAmounts(ctx, database, awardID); err != nil {
			return nil, err
		}
		result = append(result, award)
	}
	return result, rows.Err()
}

func awardAmounts(ctx context.Context, database *sql.DB, awardID int64) ([]kernel.Money, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT tax_base, currency, amount_cents
		FROM award_amount
		WHERE award_id = ?
		ORDER BY tax_base`, awardID)
	if err != nil {
		return nil, fmt.Errorf("read award amounts: %w", err)
	}
	defer rows.Close()

	var amounts []kernel.Money
	for rows.Next() {
		var base, currency string
		var cents int64
		if err := rows.Scan(&base, &currency, &cents); err != nil {
			return nil, fmt.Errorf("scan award amount: %w", err)
		}
		amounts = append(amounts, kernel.Money{Cents: cents, Currency: currency, TaxBase: kernel.TaxBase(base)})
	}
	return amounts, rows.Err()
}

func documents(ctx context.Context, database *sql.DB, publicationID int64) ([]domain.Document, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT url, sha256, title
		FROM document_reference
		WHERE publication_id = ?
		ORDER BY url`, publicationID)
	if err != nil {
		return nil, fmt.Errorf("read documents: %w", err)
	}
	defer rows.Close()

	var result []domain.Document
	for rows.Next() {
		var document domain.Document
		if err := rows.Scan(&document.URL, &document.Hash, &document.Title); err != nil {
			return nil, fmt.Errorf("scan document: %w", err)
		}
		result = append(result, document)
	}
	return result, rows.Err()
}
