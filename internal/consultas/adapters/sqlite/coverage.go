package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/hectorruiz23/la-mordida/internal/consultas/domain"
)

// Coverage reads every recorded load with the span it covered. It is the same
// table the importer writes; the reading never invents a period and never
// hides a partial load behind a successful one.
func (s *Store) Coverage(ctx context.Context) (domain.Coverage, error) {
	rows, err := s.database.QueryContext(ctx, `
		SELECT id, source, kind, period_from, period_until, until_exclusive,
		       state, seen, publications, excluded, pages,
		       last_complete_at, last_page, detail
		FROM import_span
		ORDER BY source, id`)
	if err != nil {
		return domain.Coverage{}, fmt.Errorf("read coverage: %w", err)
	}
	defer rows.Close()

	coverage := domain.Coverage{Sources: []domain.CoverageSource{}}
	spanIDs := []int64{}
	positions := []struct{ source, span int }{}
	for rows.Next() {
		var source string
		var spanID int64
		var span domain.CoverageSpan
		var from, until, lastComplete string
		var exclusive int
		if err := rows.Scan(
			&spanID, &source, &span.Kind, &from, &until, &exclusive,
			&span.State, &span.Seen, &span.Publications, &span.Excluded, &span.Pages,
			&lastComplete, &span.LastPage, &span.Detail,
		); err != nil {
			return domain.Coverage{}, fmt.Errorf("scan coverage: %w", err)
		}
		span.PeriodFrom = coverageDate(from)
		span.PeriodUntil = coverageDate(until)
		span.UntilExclusive = exclusive == 1
		span.LastCompleteAt = coverageDate(lastComplete)
		span.Errors = coverageErrors(span.Seen, span.Publications, span.Excluded)

		index := -1
		for i := range coverage.Sources {
			if coverage.Sources[i].Source == source {
				index = i
				break
			}
		}
		if index < 0 {
			coverage.Sources = append(coverage.Sources, domain.CoverageSource{Source: source})
			index = len(coverage.Sources) - 1
		}
		spanIDs = append(spanIDs, spanID)
		positions = append(positions, struct{ source, span int }{index, len(coverage.Sources[index].Spans)})
		coverage.Sources[index].Spans = append(coverage.Sources[index].Spans, span)
	}
	if err := rows.Err(); err != nil {
		return domain.Coverage{}, fmt.Errorf("iterate coverage: %w", err)
	}
	if err := rows.Close(); err != nil {
		return domain.Coverage{}, fmt.Errorf("close coverage: %w", err)
	}
	if len(spanIDs) > 0 {
		exclusions, err := coverageExclusions(ctx, s, spanIDs)
		if err != nil {
			return domain.Coverage{}, err
		}
		for position, place := range positions {
			coverage.Sources[place.source].Spans[place.span].Exclusions = exclusions[spanIDs[position]]
		}
	}
	return coverage, nil
}

func coverageErrors(seen, accepted, excluded int) int {
	errors := seen - accepted - excluded
	if errors < 0 {
		return 0
	}
	return errors
}

func coverageExclusions(ctx context.Context, s *Store, ids []int64) (map[int64][]domain.CoverageExclusion, error) {
	placeholders := questionMarks(len(ids))
	args := idArgs(ids)
	rows, err := s.database.QueryContext(ctx, `
		SELECT span_id, reason, units FROM import_exclusion
		WHERE span_id IN (`+placeholders+`)
		ORDER BY span_id, reason`, args...)
	if err != nil {
		return nil, fmt.Errorf("read coverage exclusions: %w", err)
	}
	defer rows.Close()

	exclusions := make(map[int64][]domain.CoverageExclusion, len(ids))
	for rows.Next() {
		var spanID int64
		var exclusion domain.CoverageExclusion
		if err := rows.Scan(&spanID, &exclusion.Reason, &exclusion.Units); err != nil {
			return nil, fmt.Errorf("scan coverage exclusion: %w", err)
		}
		exclusions[spanID] = append(exclusions[spanID], exclusion)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate coverage exclusions: %w", err)
	}
	return exclusions, nil
}

func coverageDate(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return raw
	}
	return parsed.UTC().Format("2006-01-02")
}
