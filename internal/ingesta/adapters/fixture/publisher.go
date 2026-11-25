// Package fixture is the test publisher: it reads a local JSON file of
// neutral publications and yields them through the same contract PLACSP will
// implement. It knows no XML, no network and no database.
package fixture

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

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
	contents, err := p.contents(ctx)
	if err != nil {
		return err
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
		if !window.Contains(publication.UpdatedAt) {
			continue
		}
		if err := yield(publication); err != nil {
			return err
		}
	}
	return nil
}

// contents reads the fixture from the local file or from an HTTP URL. The
// scenario serves the repository fixtures over loopback HTTP so the import
// path is exercised through the same fetch a channel would use.
func (p *Publisher) contents(ctx context.Context) ([]byte, error) {
	if strings.HasPrefix(p.path, "http://") || strings.HasPrefix(p.path, "https://") {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.path, nil)
		if err != nil {
			return nil, fmt.Errorf("read fixture %s: %w", p.path, err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return nil, fmt.Errorf("read fixture %s: %w", p.path, err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("read fixture %s: HTTP %d", p.path, response.StatusCode)
		}
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return nil, fmt.Errorf("read fixture %s: %w", p.path, err)
		}
		return body, nil
	}
	contents, err := os.ReadFile(p.path)
	if err != nil {
		return nil, fmt.Errorf("read fixture %s: %w", p.path, err)
	}
	return contents, nil
}

// ReadResumable treats the whole file as one page: a fixture is small enough
// that a half read file is simply read again, and the store's revision
// identity makes the overlap harmless.
func (p *Publisher) ReadResumable(ctx context.Context, window app.Window, resume app.Resume, yield func(domain.Publication) error, onPage func(app.Page) error) error {
	if onPage == nil {
		return fmt.Errorf("fixture: page callback is required")
	}
	last := time.Time{}
	wrapped := func(publication domain.Publication) error {
		if publication.UpdatedAt.After(last) {
			last = publication.UpdatedAt
		}
		return yield(publication)
	}
	if err := p.Read(ctx, window, wrapped); err != nil {
		return err
	}
	return onPage(app.Page{Location: p.path, LastUpdated: last})
}
