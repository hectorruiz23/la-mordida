// Package fixture is the test publisher: it reads a local JSON file of
// neutral publications and yields them through the same contract PLACSP will
// implement. It knows no XML, no network and no database.
package fixture

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/hectorruiz23/la-mordida/internal/ingesta/app"
	"github.com/hectorruiz23/la-mordida/internal/ingesta/domain"
)

type Publisher struct {
	path string
}

func New(path string) *Publisher {
	return &Publisher{path: path}
}

type document struct {
	Publications []domain.Publication `json:"publications"`
}

func (p *Publisher) Read(ctx context.Context, window app.Window, yield func(domain.Publication) error) error {
	contents, err := os.ReadFile(p.path)
	if err != nil {
		return fmt.Errorf("read fixture %s: %w", p.path, err)
	}

	var payload document
	if err := json.Unmarshal(contents, &payload); err != nil {
		return fmt.Errorf("decode fixture %s: %w", p.path, err)
	}
	if len(payload.Publications) == 0 {
		return fmt.Errorf("fixture %s carries no publications", p.path)
	}

	sort.SliceStable(payload.Publications, func(i, j int) bool {
		return payload.Publications[i].UpdatedAt.Before(payload.Publications[j].UpdatedAt)
	})

	for _, publication := range payload.Publications {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !window.From.IsZero() && publication.UpdatedAt.Before(window.From) {
			continue
		}
		if !window.To.IsZero() && publication.UpdatedAt.After(window.To) {
			continue
		}
		if err := yield(publication); err != nil {
			return err
		}
	}
	return nil
}
