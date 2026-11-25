// Package sqlite persists importer runs, the spans they covered and the
// per-channel cursor. It is the only ingesta package that knows SQL.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/ingesta/app"
)

// staleAfter is how long a running mark survives a process that died without
// closing it. A live importer renews nothing; a run that takes longer than this
// is taken as interrupted rather than blocking the next attempt forever.
const staleAfter = 10 * time.Minute

type Store struct {
	database *sql.DB
	now      func() time.Time
}

func New(database *sql.DB) *Store {
	return &Store{database: database, now: time.Now}
}

func (s *Store) Begin(ctx context.Context, run app.Run) (app.Run, error) {
	staleBefore := s.now().Add(-staleAfter).Unix()
	if _, err := s.database.ExecContext(ctx, `
		UPDATE import_span
		SET state = CASE WHEN pages = 0 THEN 'gap' ELSE 'partial' END,
		    detail = 'interrupted: the importer stopped without closing the run'
		WHERE run_id IN (
			SELECT id FROM import_run WHERE state = 'running' AND started_at < ?
		)`,
		staleBefore,
	); err != nil {
		return app.Run{}, fmt.Errorf("close interrupted spans: %w", err)
	}
	if _, err := s.database.ExecContext(ctx, `
		UPDATE import_run
		SET state = 'partial', finished_at = ?,
		    detail = 'interrupted: the importer stopped without closing the run'
		WHERE state = 'running' AND started_at < ?`,
		s.now().Unix(), staleBefore,
	); err != nil {
		return app.Run{}, fmt.Errorf("retire interrupted import: %w", err)
	}

	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return app.Run{}, fmt.Errorf("begin import run: %w", err)
	}
	defer tx.Rollback()

	if run.StartedAt.IsZero() {
		run.StartedAt = s.now()
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO import_run (
			source, kind, requested_from, requested_until, period_from, period_until,
			until_exclusive, state, started_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 'running', ?)`,
		run.Source, string(run.Kind), formatTime(run.RequestedFrom), formatTime(run.RequestedUntil),
		formatTime(run.PeriodFrom), formatTime(run.PeriodUntil), boolInt(run.UntilExclusive),
		run.StartedAt.Unix(),
	)
	if err != nil {
		if isActiveRunConflict(err) {
			return app.Run{}, app.ErrImportRunning
		}
		return app.Run{}, fmt.Errorf("insert import run: %w", err)
	}
	run.ID, err = result.LastInsertId()
	if err != nil {
		return app.Run{}, fmt.Errorf("import run id: %w", err)
	}

	spanResult, err := tx.ExecContext(ctx, `
		INSERT INTO import_span (
			run_id, source, kind, period_from, period_until, until_exclusive, state
		) VALUES (?, ?, ?, ?, ?, ?, 'partial')`,
		run.ID, run.Source, string(run.Kind), formatTime(run.PeriodFrom),
		formatTime(run.PeriodUntil), boolInt(run.UntilExclusive),
	)
	if err != nil {
		return app.Run{}, fmt.Errorf("insert import span: %w", err)
	}
	run.SpanID, err = spanResult.LastInsertId()
	if err != nil {
		return app.Run{}, fmt.Errorf("import span id: %w", err)
	}

	if err := tx.Commit(); err != nil {
		if isActiveRunConflict(err) {
			return app.Run{}, app.ErrImportRunning
		}
		return app.Run{}, fmt.Errorf("commit import run: %w", err)
	}
	run.State = app.RunRunning
	return run, nil
}

func (s *Store) Record(ctx context.Context, progress app.Progress) error {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin import progress: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE import_span
		SET state = 'partial', seen = ?, publications = ?, excluded = ?, pages = ?,
		    last_complete_at = ?, last_page = ?
		WHERE id = ?`,
		progress.Seen, progress.Publications, progress.Excluded, progress.Pages,
		formatTime(progress.Cursor.LastCompleteAt), progress.Cursor.LastPage,
		progress.SpanID,
	); err != nil {
		return fmt.Errorf("record import span: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE import_run
		SET seen = ?, publications = ?, excluded = ?, pages = ?
		WHERE id = ?`,
		progress.Seen, progress.Publications, progress.Excluded, progress.Pages, progress.RunID,
	); err != nil {
		return fmt.Errorf("record import counters: %w", err)
	}
	if err := replaceExclusions(ctx, tx, progress.SpanID, progress.Exclusions); err != nil {
		return err
	}
	if !progress.Cursor.IsZero() {
		if err := upsertCursor(ctx, tx, progress.Cursor, s.now()); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit import progress: %w", err)
	}
	return nil
}

func (s *Store) Finish(ctx context.Context, run app.Run, progress app.Progress, span app.SpanState, detail string) error {
	state := app.RunCompleted
	switch span {
	case app.SpanPartial:
		state = app.RunPartial
	case app.SpanGap:
		state = app.RunFailed
	}

	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin import finish: %w", err)
	}
	defer tx.Rollback()

	finishedAt := s.now().Unix()
	if _, err := tx.ExecContext(ctx, `
		UPDATE import_run
		SET state = ?, finished_at = ?, seen = ?, publications = ?, excluded = ?,
		    pages = ?, detail = ?
		WHERE id = ?`,
		string(state), finishedAt, progress.Seen, progress.Publications,
		progress.Excluded, progress.Pages, detail, run.ID,
	); err != nil {
		return fmt.Errorf("finish import run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE import_span
		SET state = ?, seen = ?, publications = ?, excluded = ?, pages = ?,
		    last_complete_at = ?, last_page = ?, detail = ?
		WHERE id = ?`,
		string(span), progress.Seen, progress.Publications, progress.Excluded,
		progress.Pages, formatTime(progress.Cursor.LastCompleteAt),
		progress.Cursor.LastPage, detail, progress.SpanID,
	); err != nil {
		return fmt.Errorf("finish import span: %w", err)
	}
	if err := replaceExclusions(ctx, tx, progress.SpanID, progress.Exclusions); err != nil {
		return err
	}
	if !progress.Cursor.IsZero() {
		if err := upsertCursor(ctx, tx, progress.Cursor, s.now()); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit import finish: %w", err)
	}
	return nil
}

func (s *Store) Cursor(ctx context.Context, source string) (app.Cursor, bool, error) {
	var lastComplete, lastPage string
	err := s.database.QueryRowContext(ctx, `
		SELECT last_complete_at, last_page FROM import_cursor WHERE source = ?`,
		source,
	).Scan(&lastComplete, &lastPage)
	if errors.Is(err, sql.ErrNoRows) {
		return app.Cursor{}, false, nil
	}
	if err != nil {
		return app.Cursor{}, false, fmt.Errorf("read import cursor: %w", err)
	}
	return app.Cursor{Source: source, LastCompleteAt: parseTime(lastComplete), LastPage: lastPage}, true, nil
}

func (s *Store) Spans(ctx context.Context, source string) ([]app.Span, error) {
	rows, err := s.database.QueryContext(ctx, `
		SELECT id, run_id, source, kind, period_from, period_until, until_exclusive,
		       state, seen, publications, excluded, pages, last_complete_at, last_page, detail
		FROM import_span
		WHERE source = ?
		ORDER BY id`,
		source,
	)
	if err != nil {
		return nil, fmt.Errorf("list import spans: %w", err)
	}
	defer rows.Close()

	var spans []app.Span
	for rows.Next() {
		var (
			span                      app.Span
			kind, state               string
			from, until, lastComplete string
			untilExclusive            int
		)
		if err := rows.Scan(
			&span.ID, &span.RunID, &span.Source, &kind, &from, &until, &untilExclusive,
			&state, &span.Seen, &span.Publications, &span.Excluded, &span.Pages,
			&lastComplete, &span.LastPage, &span.Detail,
		); err != nil {
			return nil, fmt.Errorf("scan import span: %w", err)
		}
		span.Kind = app.Kind(kind)
		span.State = app.SpanState(state)
		span.PeriodFrom = parseTime(from)
		span.PeriodUntil = parseTime(until)
		span.UntilExclusive = untilExclusive != 0
		span.LastCompleteAt = parseTime(lastComplete)
		span.Errors = derivedErrors(span.Seen, span.Publications, span.Excluded)
		spans = append(spans, span)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read import spans: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close import spans: %w", err)
	}
	if len(spans) == 0 {
		return spans, nil
	}
	exclusions, err := readExclusions(ctx, s.database, spanIDs(spans))
	if err != nil {
		return nil, err
	}
	for index := range spans {
		spans[index].Exclusions = exclusions[spans[index].ID]
	}
	return spans, nil
}

func replaceExclusions(ctx context.Context, tx *sql.Tx, spanID int64, exclusions map[string]int) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM import_exclusion WHERE span_id = ?`, spanID); err != nil {
		return fmt.Errorf("clear import exclusions: %w", err)
	}
	reasons := make([]string, 0, len(exclusions))
	for reason, units := range exclusions {
		if units > 0 {
			reasons = append(reasons, reason)
		}
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO import_exclusion (span_id, reason, units) VALUES (?, ?, ?)`,
			spanID, reason, exclusions[reason],
		); err != nil {
			return fmt.Errorf("record import exclusion %s: %w", reason, err)
		}
	}
	return nil
}

func readExclusions(ctx context.Context, queryer queryer, ids []int64) (map[int64][]app.Exclusion, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for index, id := range ids {
		args[index] = id
	}
	rows, err := queryer.QueryContext(ctx, `
		SELECT span_id, reason, units FROM import_exclusion
		WHERE span_id IN (`+placeholders+`)
		ORDER BY span_id, reason`, args...)
	if err != nil {
		return nil, fmt.Errorf("read import exclusions: %w", err)
	}
	defer rows.Close()

	exclusions := make(map[int64][]app.Exclusion, len(ids))
	for rows.Next() {
		var spanID int64
		var exclusion app.Exclusion
		if err := rows.Scan(&spanID, &exclusion.Reason, &exclusion.Units); err != nil {
			return nil, fmt.Errorf("scan import exclusion: %w", err)
		}
		exclusions[spanID] = append(exclusions[spanID], exclusion)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate import exclusions: %w", err)
	}
	return exclusions, nil
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func spanIDs(spans []app.Span) []int64 {
	ids := make([]int64, len(spans))
	for index, span := range spans {
		ids[index] = span.ID
	}
	return ids
}

func derivedErrors(seen, accepted, excluded int) int {
	errors := seen - accepted - excluded
	if errors < 0 {
		return 0
	}
	return errors
}

func upsertCursor(ctx context.Context, tx *sql.Tx, cursor app.Cursor, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO import_cursor (source, last_complete_at, last_page, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (source) DO UPDATE SET
			last_complete_at = excluded.last_complete_at,
			last_page = excluded.last_page,
			updated_at = excluded.updated_at`,
		cursor.Source, formatTime(cursor.LastCompleteAt), cursor.LastPage, now.Unix(),
	); err != nil {
		return fmt.Errorf("record import cursor: %w", err)
	}
	return nil
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isActiveRunConflict(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed: import_run.state")
}
