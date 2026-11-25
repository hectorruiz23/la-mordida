package app

import (
	"context"

	"github.com/hectorruiz23/la-mordida/internal/contratos/domain"
)

// Store persists one publication version with its expediente, lots, awards
// and documents. Followed says whether the procedure has already been stored,
// so a correction of a followed expediente is applied even when it publishes
// no CPV of its own.
type Store interface {
	StorePublication(ctx context.Context, publication domain.Publication) error
	Followed(ctx context.Context, publication domain.Publication) (bool, error)
}

// Importer validates a normalized publication, decides whether its unit
// enters and only then hands it to the store. Decide is the counting path: it
// answers without writing anything.
type Importer struct {
	store Store
}

func NewImporter(store Store) *Importer {
	return &Importer{store: store}
}

// Decide answers whether the revision enters. A revision of a followed
// expediente enters even without a selected CPV: a correction that omits the
// classification is still a correction of a procedure already being tracked.
func (i *Importer) Decide(ctx context.Context, publication domain.Publication) (domain.Decision, error) {
	if err := publication.Validate(); err != nil {
		return domain.Decision{}, err
	}
	decision := publication.Admission()
	if decision.Store {
		return decision, nil
	}
	followed, err := i.store.Followed(ctx, publication)
	if err != nil {
		return domain.Decision{}, err
	}
	if followed {
		return domain.Decision{Store: true}, nil
	}
	return decision, nil
}

func (i *Importer) StorePublication(ctx context.Context, publication domain.Publication) error {
	decision, err := i.Decide(ctx, publication)
	if err != nil {
		return err
	}
	if !decision.Store {
		return nil
	}
	return i.store.StorePublication(ctx, publication)
}
