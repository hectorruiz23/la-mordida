// Package app answers the reads the HTTP adapter exposes.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hectorruiz23/la-mordida/internal/consultas/domain"
	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

// ErrInvalidQuery says the request cannot be read as a scope. It is a client
// error, never an empty result.
var ErrInvalidQuery = errors.New("invalid query")

const (
	defaultLimit = 20
	maxLimit     = 100
)

// Store reads the current view of imported expedientes and the ranking over
// the counted award units.
type Store interface {
	ListExpedientes(ctx context.Context) ([]domain.Expediente, error)
	Ranking(ctx context.Context, query domain.RankingQuery) (domain.Ranking, error)
	Awards(ctx context.Context, query domain.RankingQuery, entityKey string) (domain.AwardPage, error)
	Rebates(ctx context.Context, query domain.RankingQuery) (domain.RebateRanking, error)
}

type Service struct {
	store Store
}

func New(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) ListExpedientes(ctx context.Context) ([]domain.Expediente, error) {
	return s.store.ListExpedientes(ctx)
}

// Ranking reads who wins the most awards inside one buyer and award period.
func (s *Service) Ranking(ctx context.Context, query domain.RankingQuery) (domain.Ranking, error) {
	normalized, err := normalize(query)
	if err != nil {
		return domain.Ranking{}, err
	}
	return s.store.Ranking(ctx, normalized)
}

// Rebates reads the comparable award units of one scope ordered by the exact
// quotient between their initial budget and their current awarded price.
func (s *Service) Rebates(ctx context.Context, query domain.RankingQuery) (domain.RebateRanking, error) {
	normalized, err := normalize(query)
	if err != nil {
		return domain.RebateRanking{}, err
	}
	return s.store.Rebates(ctx, normalized)
}

// Awards reads the justifying list of one ranked entity under the same scope
// as the ranking, so numerator, denominator and detail cannot drift apart.
func (s *Service) Awards(ctx context.Context, query domain.RankingQuery, entityKey string) (domain.AwardPage, error) {
	normalized, err := normalize(query)
	if err != nil {
		return domain.AwardPage{}, err
	}
	if _, _, _, _, _, err := domain.ParseEntityKey(strings.TrimSpace(entityKey)); err != nil {
		return domain.AwardPage{}, fmt.Errorf("%w: entity key", ErrInvalidQuery)
	}
	return s.store.Awards(ctx, normalized, strings.TrimSpace(entityKey))
}

func normalize(query domain.RankingQuery) (domain.RankingQuery, error) {
	query.BuyerScheme = strings.TrimSpace(query.BuyerScheme)
	query.BuyerID = strings.TrimSpace(query.BuyerID)
	query.From = strings.TrimSpace(query.From)
	query.To = strings.TrimSpace(query.To)

	if (query.BuyerScheme == "") != (query.BuyerID == "") {
		return domain.RankingQuery{}, fmt.Errorf("%w: buyer needs both scheme and identifier", ErrInvalidQuery)
	}
	if query.From != "" {
		date, err := kernel.ParseDate(query.From)
		if err != nil {
			return domain.RankingQuery{}, fmt.Errorf("%w: from", ErrInvalidQuery)
		}
		query.From = date.String()
	}
	if query.To != "" {
		date, err := kernel.ParseDate(query.To)
		if err != nil {
			return domain.RankingQuery{}, fmt.Errorf("%w: to", ErrInvalidQuery)
		}
		query.To = date.String()
	}
	if query.From != "" && query.To != "" && query.From > query.To {
		return domain.RankingQuery{}, fmt.Errorf("%w: to is before from", ErrInvalidQuery)
	}
	if query.Limit < 0 || query.Limit > maxLimit {
		return domain.RankingQuery{}, fmt.Errorf("%w: limit", ErrInvalidQuery)
	}
	if query.Limit == 0 {
		query.Limit = defaultLimit
	}
	if query.Offset < 0 {
		return domain.RankingQuery{}, fmt.Errorf("%w: offset", ErrInvalidQuery)
	}
	return query, nil
}
