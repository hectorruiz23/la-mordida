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
		SELECT source, kind, period_from, period_until, until_exclusive,
		       state, publications, pages, last_complete_at, last_page, detail
		FROM import_span
		ORDER BY source, id`)
	if err != nil {
		return domain.Coverage{}, fmt.Errorf("read coverage: %w", err)
	}
	defer rows.Close()

	coverage := domain.Coverage{Sources: []domain.CoverageSource{}}
	for rows.Next() {
		var source string
		var span domain.CoverageSpan
		var from, until, lastComplete string
		var exclusive int
		if err := rows.Scan(
			&source, &span.Kind, &from, &until, &exclusive,
			&span.State, &span.Publications, &span.Pages, &lastComplete,
			&span.LastPage, &span.Detail,
		); err != nil {
			return domain.Coverage{}, fmt.Errorf("scan coverage: %w", err)
		}
		span.PeriodFrom = coverageDate(from)
		span.PeriodUntil = coverageDate(until)
		span.UntilExclusive = exclusive == 1
		span.LastCompleteAt = coverageDate(lastComplete)

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
		coverage.Sources[index].Spans = append(coverage.Sources[index].Spans, span)
	}
	if err := rows.Err(); err != nil {
		return domain.Coverage{}, fmt.Errorf("iterate coverage: %w", err)
	}
	return coverage, nil
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
