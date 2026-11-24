// Package app answers the reads the HTTP adapter exposes.
package app

import (
	"context"

	"github.com/hectorruiz23/la-mordida/internal/consultas/domain"
)

// Store reads the current view of imported expedientes.
type Store interface {
	ListExpedientes(ctx context.Context) ([]domain.Expediente, error)
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
