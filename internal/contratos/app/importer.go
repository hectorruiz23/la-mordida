package app

import (
	"context"

	"github.com/hectorruiz23/la-mordida/internal/contratos/domain"
)

// Store persists one publication version with its expediente, lots, awards
// and documents.
type Store interface {
	StorePublication(ctx context.Context, publication domain.Publication) error
}

// Importer validates a normalized publication and hands it to the store.
type Importer struct {
	store Store
}

func NewImporter(store Store) *Importer {
	return &Importer{store: store}
}

func (i *Importer) StorePublication(ctx context.Context, publication domain.Publication) error {
	if err := publication.Validate(); err != nil {
		return err
	}
	return i.store.StorePublication(ctx, publication)
}
