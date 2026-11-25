package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/consultas/domain"
	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

type Store struct {
	database *sql.DB
}

func New(database *sql.DB) *Store {
	return &Store{database: database}
}

// ListExpedientes reads the current projection of every expediente. The
// projection points at the newest source revision, not at the last row that
// happened to be imported, so an out-of-order reread cannot rewind it.
func (s *Store) ListExpedientes(ctx context.Context) ([]domain.Expediente, error) {
	rows, err := s.database.QueryContext(ctx, `
		SELECT e.id, e.buyer_scheme, e.buyer_id, e.buyer_name, e.folder_id, e.object, e.status,
		       e.projection_state,
		       p.id, p.source, p.external_id, p.url, p.published_at, COALESCE(p.effective_date, '')
		FROM expediente e
		JOIN publication p ON p.id = COALESCE(
			e.current_publication_id,
			(SELECT p2.id FROM publication p2
			 WHERE p2.expediente_id = e.id
			 ORDER BY julianday(p2.published_at) DESC, p2.id DESC
			 LIMIT 1))
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
			&expediente.FolderID, &expediente.Object, &expediente.Status, &expediente.State,
			&publicationID, &expediente.Publication.Source, &expediente.Publication.ExternalID,
			&expediente.Publication.URL, &expediente.Publication.UpdatedAt,
			&expediente.Publication.EffectiveDate,
		); err != nil {
			return nil, fmt.Errorf("scan expediente: %w", err)
		}

		if err := s.fillExpediente(ctx, &expediente, expedienteID, publicationID, false); err != nil {
			return nil, err
		}
		expedientes = append(expedientes, expediente)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list expedientes: %w", err)
	}
	return expedientes, nil
}

// Expediente reads one current projection by its internal id, with the
// original budget of every scope and every document announced by any
// revision. The list reading keeps the current budget only; a detail has to
// show the original beside the correction and the evidence behind both.
func (s *Store) Expediente(ctx context.Context, id int64) (domain.Expediente, error) {
	var expediente domain.Expediente
	var publicationID int64
	err := s.database.QueryRowContext(ctx, `
		SELECT e.id, e.buyer_scheme, e.buyer_id, e.buyer_name, e.folder_id, e.object, e.status,
		       e.projection_state,
		       p.id, p.source, p.external_id, p.url, p.published_at, COALESCE(p.effective_date, '')
		FROM expediente e
		JOIN publication p ON p.id = COALESCE(
			e.current_publication_id,
			(SELECT p2.id FROM publication p2
			 WHERE p2.expediente_id = e.id
			 ORDER BY julianday(p2.published_at) DESC, p2.id DESC
			 LIMIT 1))
		WHERE e.id = ?`, id,
	).Scan(
		&id,
		&expediente.Buyer.Scheme, &expediente.Buyer.ID, &expediente.Buyer.Name,
		&expediente.FolderID, &expediente.Object, &expediente.Status, &expediente.State,
		&publicationID, &expediente.Publication.Source, &expediente.Publication.ExternalID,
		&expediente.Publication.URL, &expediente.Publication.UpdatedAt,
		&expediente.Publication.EffectiveDate,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Expediente{}, domain.ErrExpedienteNotFound
	}
	if err != nil {
		return domain.Expediente{}, fmt.Errorf("read expediente %d: %w", id, err)
	}
	if err := s.fillExpediente(ctx, &expediente, id, publicationID, true); err != nil {
		return domain.Expediente{}, err
	}
	return expediente, nil
}

func (s *Store) fillExpediente(ctx context.Context, expediente *domain.Expediente, expedienteID, publicationID int64, detail bool) error {
	var err error
	if expediente.Budget, err = budget(ctx, s.database, publicationID, ""); err != nil {
		return err
	}
	if detail {
		if expediente.OriginalBudget, err = originalBudget(ctx, s.database, expedienteID, ""); err != nil {
			return err
		}
	}
	if expediente.Lots, err = lots(ctx, s.database, expedienteID, publicationID, detail); err != nil {
		return err
	}
	if expediente.Awards, err = awards(ctx, s.database, expedienteID); err != nil {
		return err
	}
	if expediente.Facts, err = facts(ctx, s.database, expedienteID); err != nil {
		return err
	}
	if detail {
		expediente.Documents, err = allDocuments(ctx, s.database, expedienteID)
		return err
	}
	expediente.Documents, err = documents(ctx, s.database, publicationID)
	return err
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

func lots(ctx context.Context, database *sql.DB, expedienteID, publicationID int64, detail bool) ([]domain.Lot, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT lot_id, object, projection_state
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
		if err := rows.Scan(&lot.ID, &lot.Object, &lot.State); err != nil {
			return nil, fmt.Errorf("scan lot: %w", err)
		}
		if lot.Budget, err = budget(ctx, database, publicationID, lot.ID); err != nil {
			return nil, err
		}
		if detail {
			if lot.OriginalBudget, err = originalBudget(ctx, database, expedienteID, lot.ID); err != nil {
				return nil, err
			}
		}
		result = append(result, lot)
	}
	return result, rows.Err()
}

// awards reads the materialised current award, not the awards embedded in one
// revision. A repeated photo from another channel therefore cannot create a
// second victory, and an indeterminate projection keeps its state visible.
func awards(ctx context.Context, database *sql.DB, expedienteID int64) ([]domain.Award, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT lot_id, state, result_code, COALESCE(award_date, ''),
		       winner_scheme, winner_id, winner_name,
		       contract_id, COALESCE(contract_issue_date, '')
		FROM award_projection
		WHERE expediente_id = ?
		ORDER BY lot_id`, expedienteID)
	if err != nil {
		return nil, fmt.Errorf("read awards: %w", err)
	}
	defer rows.Close()

	var result []domain.Award
	for rows.Next() {
		var award domain.Award
		var contractID, contractIssueDate string
		if err := rows.Scan(
			&award.LotID, &award.State, &award.ResultCode, &award.AwardDate,
			&award.Winner.Scheme, &award.Winner.ID, &award.Winner.Name,
			&contractID, &contractIssueDate,
		); err != nil {
			return nil, fmt.Errorf("scan award: %w", err)
		}
		if contractID != "" || contractIssueDate != "" {
			award.Contract = &domain.Contract{ID: contractID, IssueDate: contractIssueDate}
		}
		if award.Amounts, err = awardProjectionAmounts(ctx, database, expedienteID, award.LotID); err != nil {
			return nil, err
		}
		result = append(result, award)
	}
	return result, rows.Err()
}

func awardProjectionAmounts(ctx context.Context, database *sql.DB, expedienteID int64, lotID string) ([]kernel.Money, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT tax_base, currency, amount_cents
		FROM award_projection_amount
		WHERE expediente_id = ? AND lot_id = ?
		ORDER BY tax_base`, expedienteID, lotID)
	if err != nil {
		return nil, fmt.Errorf("read award projection amounts: %w", err)
	}
	defer rows.Close()

	var amounts []kernel.Money
	for rows.Next() {
		var base, currency string
		var cents int64
		if err := rows.Scan(&base, &currency, &cents); err != nil {
			return nil, fmt.Errorf("scan award projection amount: %w", err)
		}
		amounts = append(amounts, kernel.Money{Cents: cents, Currency: currency, TaxBase: kernel.TaxBase(base)})
	}
	return amounts, rows.Err()
}

// facts reads the lifecycle facts of every revision, newest first. A newer
// snapshot without a fact does not erase the older one.
func facts(ctx context.Context, database *sql.DB, expedienteID int64) ([]domain.Fact, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT f.id, f.kind, f.external_ref, f.lot_id, COALESCE(f.effective_date, ''),
		       f.deadline_status, f.delivery_status, f.detail,
		       p.source, p.external_id, p.url, p.published_at, COALESCE(p.effective_date, '')
		FROM fact f
		JOIN publication p ON p.id = f.publication_id
		WHERE f.expediente_id = ?
		ORDER BY COALESCE(f.effective_date, '') DESC, julianday(p.published_at) DESC, f.id DESC`, expedienteID)
	if err != nil {
		return nil, fmt.Errorf("read facts: %w", err)
	}
	defer rows.Close()

	var result []domain.Fact
	for rows.Next() {
		var fact domain.Fact
		var factID int64
		if err := rows.Scan(
			&factID, &fact.Kind, &fact.Reference, &fact.LotID, &fact.EffectiveDate,
			&fact.DeadlineStatus, &fact.DeliveryStatus, &fact.Detail,
			&fact.Publication.Source, &fact.Publication.ExternalID, &fact.Publication.URL,
			&fact.Publication.UpdatedAt, &fact.Publication.EffectiveDate,
		); err != nil {
			return nil, fmt.Errorf("scan fact: %w", err)
		}
		if fact.Amounts, err = factAmounts(ctx, database, factID); err != nil {
			return nil, err
		}
		result = append(result, fact)
	}
	return result, rows.Err()
}

func factAmounts(ctx context.Context, database *sql.DB, factID int64) ([]kernel.Money, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT tax_base, currency, amount_cents
		FROM fact_amount
		WHERE fact_id = ?
		ORDER BY tax_base`, factID)
	if err != nil {
		return nil, fmt.Errorf("read fact amounts: %w", err)
	}
	defer rows.Close()

	var amounts []kernel.Money
	for rows.Next() {
		var base, currency string
		var cents int64
		if err := rows.Scan(&base, &currency, &cents); err != nil {
			return nil, fmt.Errorf("scan fact amount: %w", err)
		}
		amounts = append(amounts, kernel.Money{Cents: cents, Currency: currency, TaxBase: kernel.TaxBase(base)})
	}
	return amounts, rows.Err()
}

// originalBudget reads the amounts of the first publication that documented
// the scope. A later correction is not the original: the detail shows it
// beside the row, never instead of it.
func originalBudget(ctx context.Context, database *sql.DB, expedienteID int64, lotKey string) ([]kernel.Money, error) {
	var publicationID int64
	err := database.QueryRowContext(ctx, `
		SELECT b.publication_id
		FROM budget_amount b
		JOIN publication p ON p.id = b.publication_id
		WHERE p.expediente_id = ? AND b.lot_key = ?
		ORDER BY julianday(p.published_at) ASC, p.id ASC
		LIMIT 1`, expedienteID, lotKey,
	).Scan(&publicationID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read original budget publication: %w", err)
	}
	return budget(ctx, database, publicationID, lotKey)
}

// allDocuments reads every document announced by any revision of the
// expediente, with the publication that announced it.
func allDocuments(ctx context.Context, database *sql.DB, expedienteID int64) ([]domain.Document, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT d.target_kind, d.target_ref, d.url, d.sha256, d.title, d.state, d.mime,
		       COALESCE(d.obtained_at, 0),
		       p.source, p.external_id, p.url, p.published_at, COALESCE(p.effective_date, '')
		FROM document_link d
		JOIN publication p ON p.id = d.publication_id
		WHERE p.expediente_id = ?
		ORDER BY julianday(p.published_at) DESC, p.id DESC, d.target_kind, d.target_ref, d.url`, expedienteID)
	if err != nil {
		return nil, fmt.Errorf("read expediente documents: %w", err)
	}
	defer rows.Close()

	var result []domain.Document
	seen := make(map[string]bool)
	for rows.Next() {
		var document domain.Document
		var obtainedAt int64
		if err := rows.Scan(
			&document.TargetKind, &document.TargetRef, &document.URL,
			&document.SHA256, &document.Title, &document.State, &document.MIME, &obtainedAt,
			&document.Publication.Source, &document.Publication.ExternalID,
			&document.Publication.URL, &document.Publication.UpdatedAt,
			&document.Publication.EffectiveDate,
		); err != nil {
			return nil, fmt.Errorf("scan expediente document: %w", err)
		}
		// The same announced link may appear in several revisions. The link
		// is one document; the newest publication is its provenance and the
		// duplicate revision is not rendered twice.
		key := document.URL + "\x00" + document.SHA256
		if seen[key] {
			continue
		}
		seen[key] = true
		if obtainedAt > 0 {
			document.ObtainedAt = time.Unix(obtainedAt, 0).UTC().Format(time.RFC3339)
		}
		result = append(result, document)
	}
	return result, rows.Err()
}

func documents(ctx context.Context, database *sql.DB, publicationID int64) ([]domain.Document, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT target_kind, target_ref, url, sha256, title, state, mime, COALESCE(obtained_at, 0)
		FROM document_link
		WHERE publication_id = ?
		ORDER BY target_kind, target_ref, url`, publicationID)
	if err != nil {
		return nil, fmt.Errorf("read documents: %w", err)
	}
	defer rows.Close()

	var result []domain.Document
	for rows.Next() {
		var document domain.Document
		var obtainedAt int64
		if err := rows.Scan(
			&document.TargetKind, &document.TargetRef, &document.URL,
			&document.SHA256, &document.Title, &document.State, &document.MIME, &obtainedAt,
		); err != nil {
			return nil, fmt.Errorf("scan document: %w", err)
		}
		if obtainedAt > 0 {
			document.ObtainedAt = time.Unix(obtainedAt, 0).UTC().Format(time.RFC3339)
		}
		result = append(result, document)
	}
	return result, rows.Err()
}
