package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/contratos/domain"
)

// resolveExpediente maps the source publication to the internal procedure. A
// folder id alone never identifies a buyer: the mapping is tried first, then
// the buyer identity, and only the pair (buyer, folder) creates a procedure.
func resolveExpediente(ctx context.Context, tx *sql.Tx, publication domain.Publication, observedAt int64) (int64, error) {
	var expedienteID int64
	err := tx.QueryRowContext(ctx, `
		SELECT expediente_id FROM expediente_identity WHERE source = ? AND external_id = ?`,
		publication.Source, publication.ExternalID,
	).Scan(&expedienteID)
	switch {
	case err == nil:
		if _, err := tx.ExecContext(ctx, `
			UPDATE expediente_identity SET last_seen = MAX(last_seen, ?)
			WHERE source = ? AND external_id = ?`,
			observedAt, publication.Source, publication.ExternalID,
		); err != nil {
			return 0, fmt.Errorf("touch expediente identity: %w", err)
		}
		return expedienteID, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, fmt.Errorf("look up expediente identity: %w", err)
	}

	buyerID, err := resolveBuyer(ctx, tx, publication.Expediente.Buyer, publication.Source, observedAt)
	if err != nil {
		return 0, err
	}

	err = tx.QueryRowContext(ctx, `
		SELECT id FROM expediente WHERE buyer_ref = ? AND folder_id = ?`,
		buyerID, publication.Expediente.FolderID,
	).Scan(&expedienteID)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `
			SELECT id FROM expediente
			WHERE buyer_scheme = ? AND buyer_id = ? AND folder_id = ?`,
			publication.Expediente.Buyer.Scheme, publication.Expediente.Buyer.ID, publication.Expediente.FolderID,
		).Scan(&expedienteID)
	}
	switch {
	case err == nil:
		if _, err := tx.ExecContext(ctx, `
			UPDATE expediente SET buyer_ref = ? WHERE id = ? AND buyer_ref IS NULL`,
			buyerID, expedienteID,
		); err != nil {
			return 0, fmt.Errorf("link expediente buyer: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		result, err := tx.ExecContext(ctx, `
			INSERT INTO expediente (buyer_ref, buyer_scheme, buyer_id, folder_id, buyer_name, object, status)
			VALUES (?, ?, ?, ?, ?, '', '')`,
			buyerID, publication.Expediente.Buyer.Scheme, publication.Expediente.Buyer.ID,
			publication.Expediente.FolderID, publication.Expediente.Buyer.Name,
		)
		if err != nil {
			return 0, fmt.Errorf("store expediente: %w", err)
		}
		expedienteID, err = result.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("expediente id: %w", err)
		}
	default:
		return 0, fmt.Errorf("read expediente: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO expediente_identity (expediente_id, source, external_id, first_seen, last_seen)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (source, external_id) DO UPDATE SET
			expediente_id = excluded.expediente_id,
			last_seen = MAX(expediente_identity.last_seen, excluded.last_seen)`,
		expedienteID, publication.Source, publication.ExternalID, observedAt, observedAt,
	); err != nil {
		return 0, fmt.Errorf("store expediente identity: %w", err)
	}
	return expedienteID, nil
}

func resolveBuyer(ctx context.Context, tx *sql.Tx, party domain.Party, source string, observedAt int64) (int64, error) {
	var buyerID int64
	err := tx.QueryRowContext(ctx, `
		SELECT buyer_id FROM buyer_identity WHERE scheme = ? AND external_id = ?`,
		party.Scheme, party.ID,
	).Scan(&buyerID)
	switch {
	case err == nil:
		if _, err := tx.ExecContext(ctx, `
			UPDATE buyer SET name = CASE WHEN name = '' AND ? <> '' THEN ? ELSE name END
			WHERE id = ?`, party.Name, party.Name, buyerID,
		); err != nil {
			return 0, fmt.Errorf("update buyer name: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE buyer_identity SET last_seen = MAX(last_seen, ?)
			WHERE scheme = ? AND external_id = ?`,
			observedAt, party.Scheme, party.ID,
		); err != nil {
			return 0, fmt.Errorf("touch buyer identity: %w", err)
		}
		return buyerID, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, fmt.Errorf("look up buyer identity: %w", err)
	}

	result, err := tx.ExecContext(ctx, `INSERT INTO buyer (name) VALUES (?)`, party.Name)
	if err != nil {
		return 0, fmt.Errorf("store buyer: %w", err)
	}
	buyerID, err = result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("buyer id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO buyer_identity (buyer_id, scheme, external_id, source, first_seen, last_seen)
		VALUES (?, ?, ?, ?, ?, ?)`,
		buyerID, party.Scheme, party.ID, source, observedAt, observedAt,
	); err != nil {
		return 0, fmt.Errorf("store buyer identity: %w", err)
	}
	return buyerID, nil
}

// upsertLots keeps one current row per lot. An older revision never rewinds
// the lot, and a newer revision that omits a lot does not delete it: absence
// is unknown, not a cancellation. A same-instant conflict marks the disputed
// object indeterminate.
func upsertLots(ctx context.Context, tx *sql.Tx, expedienteID, publicationID int64, lots []domain.Lot) error {
	var published string
	if err := tx.QueryRowContext(ctx, `SELECT published_at FROM publication WHERE id = ?`, publicationID).Scan(&published); err != nil {
		return fmt.Errorf("read revision instant: %w", err)
	}

	for _, lot := range lots {
		var currentPublished, currentObject string
		err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(p.published_at, ''), l.object
			FROM lot l LEFT JOIN publication p ON p.id = l.current_publication_id
			WHERE l.expediente_id = ? AND l.lot_id = ?`,
			expedienteID, lot.ID,
		).Scan(&currentPublished, &currentObject)

		if errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO lot (expediente_id, lot_id, object, current_publication_id, projection_state)
				VALUES (?, ?, ?, ?, 'known')`,
				expedienteID, lot.ID, lot.Object, publicationID,
			); err != nil {
				return fmt.Errorf("store lot %s: %w", lot.ID, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("read lot %s: %w", lot.ID, err)
		}

		currentInstant, currentErr := time.Parse(time.RFC3339Nano, currentPublished)
		publishedInstant, publishedErr := time.Parse(time.RFC3339Nano, published)
		if currentErr != nil || publishedErr != nil {
			return fmt.Errorf("compare lot %s instants: %v / %v", lot.ID, currentErr, publishedErr)
		}

		switch {
		case currentInstant.After(publishedInstant):
			// The stored projection is newer; this revision is history only.
		case currentInstant.Equal(publishedInstant):
			if currentObject != lot.Object {
				if _, err := tx.ExecContext(ctx, `
					UPDATE lot SET object = '', projection_state = 'indeterminate'
					WHERE expediente_id = ? AND lot_id = ?`,
					expedienteID, lot.ID,
				); err != nil {
					return fmt.Errorf("mark lot %s indeterminate: %w", lot.ID, err)
				}
			}
		default:
			if _, err := tx.ExecContext(ctx, `
				UPDATE lot SET object = ?, current_publication_id = ?, projection_state = 'known'
				WHERE expediente_id = ? AND lot_id = ?`,
				lot.Object, publicationID, expedienteID, lot.ID,
			); err != nil {
				return fmt.Errorf("update lot %s: %w", lot.ID, err)
			}
		}
	}
	return nil
}
