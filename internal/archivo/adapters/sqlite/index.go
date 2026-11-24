// Package sqlite records archived objects, their provenance and the document
// links that cite them. It is the only archivo package that knows SQL.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/archivo/domain"
)

type Index struct {
	database *sql.DB
}

func New(database *sql.DB) *Index {
	return &Index{database: database}
}

// Links lists the document links waiting to be archived, oldest first. With
// includeEvidence it also returns the links already stored, so a source whose
// URL changed can be downloaded again and keep both versions.
func (i *Index) Links(ctx context.Context, includeEvidence bool) ([]domain.Link, error) {
	query := `
		SELECT p.source, p.external_id, p.updated_at,
		       d.target_kind, d.target_ref, d.url, d.title,
		       d.state, d.sha256, d.mime, d.obtained_at, d.attempts, d.last_error
		FROM document_link d
		JOIN publication p ON p.id = d.publication_id`
	if !includeEvidence {
		query += ` WHERE d.state <> 'evidence'`
	}
	query += ` ORDER BY p.updated_at, d.id`

	rows, err := i.database.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list document links: %w", err)
	}
	defer rows.Close()

	var links []domain.Link
	for rows.Next() {
		var link domain.Link
		var updatedAt string
		var obtainedAt sql.NullInt64
		if err := rows.Scan(
			&link.Publication.Source, &link.Publication.ExternalID, &updatedAt,
			&link.TargetKind, &link.TargetRef, &link.URL, &link.Title,
			&link.State, &link.SHA256, &link.MIME, &obtainedAt, &link.Attempts, &link.LastError,
		); err != nil {
			return nil, fmt.Errorf("scan document link: %w", err)
		}
		link.Publication.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("document link %s: updated_at %q: %w", link.URL, updatedAt, err)
		}
		if obtainedAt.Valid {
			link.ObtainedAt = time.Unix(obtainedAt.Int64, 0).UTC()
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list document links: %w", err)
	}
	return links, nil
}

// Pending records that the download failed and stays retryable.
func (i *Index) Pending(ctx context.Context, link domain.Link, cause error) error {
	tx, err := i.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pending transaction: %w", err)
	}
	defer tx.Rollback()

	linkID, err := i.linkID(ctx, tx, link)
	if err != nil {
		return err
	}
	// A failed refresh never demotes a version already stored: only a link
	// without evidence becomes pending.
	if _, err := tx.ExecContext(ctx, `
		UPDATE document_link
		SET state = CASE WHEN state = 'evidence' THEN 'evidence' ELSE 'pending' END,
		    attempts = ?, last_error = ?
		WHERE id = ?`,
		link.Attempts, cause.Error(), linkID,
	); err != nil {
		return fmt.Errorf("mark document %s pending: %w", link.URL, err)
	}
	return tx.Commit()
}

// Store records the object, its provenance and the evidence only after the
// bytes are on disk. The same content from another URL adds a provenance; one
// URL with other bytes adds a second object and evidence row beside the first.
func (i *Index) Store(ctx context.Context, link domain.Link, origin domain.Origin) error {
	tx, err := i.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin evidence transaction: %w", err)
	}
	defer tx.Rollback()

	linkID, err := i.linkID(ctx, tx, link)
	if err != nil {
		return err
	}

	storedAt := origin.ObtainedAt.UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO objeto (sha256, mime, bytes, stored_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (sha256) DO UPDATE SET
			mime = CASE WHEN excluded.mime <> '' THEN excluded.mime ELSE objeto.mime END,
			bytes = CASE WHEN excluded.bytes > 0 THEN excluded.bytes ELSE objeto.bytes END,
			stored_at = MIN(objeto.stored_at, excluded.stored_at)`,
		origin.Object.SHA256, origin.Object.MIME, origin.Object.Bytes, storedAt.Unix(),
	); err != nil {
		return fmt.Errorf("store object %s: %w", origin.Object.SHA256, err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO procedencia (sha256, url, obtained_at)
		VALUES (?, ?, ?)
		ON CONFLICT (sha256, url) DO UPDATE SET obtained_at = excluded.obtained_at`,
		origin.Object.SHA256, origin.URL, origin.ObtainedAt.Unix(),
	); err != nil {
		return fmt.Errorf("store provenance of %s: %w", origin.Object.SHA256, err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO document_evidence (document_link_id, sha256, obtained_at)
		VALUES (?, ?, ?)
		ON CONFLICT (document_link_id, sha256) DO NOTHING`,
		linkID, origin.Object.SHA256, origin.ObtainedAt.Unix(),
	); err != nil {
		return fmt.Errorf("store evidence of %s: %w", link.URL, err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE document_link
		SET state = 'evidence', sha256 = ?, mime = ?, obtained_at = ?,
		    attempts = ?, last_error = ''
		WHERE id = ?`,
		origin.Object.SHA256, origin.Object.MIME, origin.ObtainedAt.Unix(),
		link.Attempts, linkID,
	); err != nil {
		return fmt.Errorf("store evidence link %s: %w", link.URL, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit evidence of %s: %w", link.URL, err)
	}
	return nil
}

func (i *Index) linkID(ctx context.Context, tx *sql.Tx, link domain.Link) (int64, error) {
	var linkID int64
	err := tx.QueryRowContext(ctx, `
		SELECT d.id
		FROM document_link d
		JOIN publication p ON p.id = d.publication_id
		WHERE p.source = ? AND p.external_id = ? AND p.updated_at = ?
		  AND d.target_kind = ? AND d.target_ref = ? AND d.url = ?`,
		link.Publication.Source, link.Publication.ExternalID,
		link.Publication.UpdatedAt.UTC().Format(time.RFC3339Nano),
		link.TargetKind, link.TargetRef, link.URL,
	).Scan(&linkID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, fmt.Errorf("document %s is not announced by %s/%s", link.URL, link.Publication.Source, link.Publication.ExternalID)
	case err != nil:
		return 0, fmt.Errorf("look up document %s: %w", link.URL, err)
	}
	return linkID, nil
}
