package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	contratosdomain "github.com/hectorruiz23/la-mordida/internal/contratos/domain"
	ingestadomain "github.com/hectorruiz23/la-mordida/internal/ingesta/domain"
)

// Sink stores one neutral publication as an expediente snapshot with its
// awards and documents.
type Sink interface {
	StorePublication(ctx context.Context, publication contratosdomain.Publication) error
}

// Importer runs a reader and hands every publication to the sink.
type Importer struct {
	reader Reader
	sink   Sink
}

func NewImporter(reader Reader, sink Sink) *Importer {
	return &Importer{reader: reader, sink: sink}
}

// Run reads the window and returns how many publications it stored.
func (i *Importer) Run(ctx context.Context, window Window) (int, error) {
	stored := 0
	err := i.reader.Read(ctx, window, func(publication ingestadomain.Publication) error {
		normalized, err := toContratos(publication)
		if err != nil {
			return fmt.Errorf("publication %s/%s: %w", publication.Reference.Source, publication.Reference.ID, err)
		}
		if err := i.sink.StorePublication(ctx, normalized); err != nil {
			return fmt.Errorf("store publication %s/%s: %w", publication.Reference.Source, publication.Reference.ID, err)
		}
		stored++
		return nil
	})
	if err != nil {
		return stored, err
	}
	return stored, nil
}

func toContratos(publication ingestadomain.Publication) (contratosdomain.Publication, error) {
	payload, err := json.Marshal(publication)
	if err != nil {
		return contratosdomain.Publication{}, fmt.Errorf("hash publication: %w", err)
	}
	sum := sha256.Sum256(payload)

	normalized := contratosdomain.Publication{
		Source:     publication.Reference.Source,
		ExternalID: publication.Reference.ID,
		URL:        publication.Reference.URL,
		UpdatedAt:  publication.UpdatedAt,
		BodyHash:   hex.EncodeToString(sum[:]),
		Expediente: contratosdomain.Expediente{
			Buyer: contratosdomain.Party{
				Scheme: publication.Expediente.Buyer.Scheme,
				ID:     publication.Expediente.Buyer.ID,
				Name:   publication.Expediente.Buyer.Name,
			},
			FolderID: publication.Expediente.FolderID,
			Object:   publication.Expediente.Object,
			Status:   publication.Expediente.Status,
			Budget:   publication.Expediente.Budget,
		},
		Documents: make([]contratosdomain.Document, 0, len(publication.Documents)),
	}

	for _, lot := range publication.Lots {
		normalized.Lots = append(normalized.Lots, contratosdomain.Lot{
			ID:     lot.ID,
			Object: lot.Object,
			Budget: lot.Budget,
		})
	}

	for _, award := range publication.Awards {
		normalizedAward := contratosdomain.Award{
			LotID:      award.LotID,
			ResultCode: award.ResultCode,
			AwardDate:  award.AwardDate,
			Winner: contratosdomain.Party{
				Scheme: award.Winner.Scheme,
				ID:     award.Winner.ID,
				Name:   award.Winner.Name,
			},
			Amounts: award.Amounts,
		}
		if award.Contract != nil {
			normalizedAward.Contract = &contratosdomain.Contract{
				ID:        award.Contract.ID,
				IssueDate: award.Contract.IssueDate,
			}
		}
		normalized.Awards = append(normalized.Awards, normalizedAward)
	}

	for _, document := range publication.Documents {
		kind := document.Kind
		if kind == "" {
			kind = "publication"
		}
		normalized.Documents = append(normalized.Documents, contratosdomain.Document{
			Kind:  kind,
			Ref:   document.Ref,
			URL:   document.URL,
			Hash:  document.Hash,
			Title: document.Title,
		})
	}

	if err := normalized.Validate(); err != nil {
		return contratosdomain.Publication{}, err
	}
	return normalized, nil
}
