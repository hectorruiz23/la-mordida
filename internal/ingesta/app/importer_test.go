package app

import (
	"testing"
	"time"

	ingestadomain "github.com/hectorruiz23/la-mordida/internal/ingesta/domain"
	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

func TestToContratosCarriesCPVSelectionAndNotices(t *testing.T) {
	issueDate := kernel.Date{Year: 2025, Month: time.July, Day: 16}
	classification := ingestadomain.Classification{
		Source: ingestadomain.ClassificationPublished,
		Codes: []ingestadomain.CPV{{
			Code: "30237475", Family: "302", Included: true, Reason: "family_selected",
		}},
	}

	publication := ingestadomain.Publication{
		Reference: ingestadomain.Reference{Source: "placsp:sindicacion_643", ID: "urn:test:1"},
		UpdatedAt: time.Date(2025, 10, 20, 10, 0, 0, 0, time.UTC),
		Expediente: ingestadomain.Expediente{
			FolderID: "1787/2025",
			Buyer:    ingestadomain.Party{Scheme: "NIF", ID: "P3704600J"},
			CPV:      classification,
		},
		Lots: []ingestadomain.Lot{{
			ID:  "1",
			CPV: ingestadomain.Classification{Source: ingestadomain.ClassificationInherited, Codes: classification.Codes},
		}},
		Notices: []ingestadomain.Notice{{
			Type:       "DOC_CN",
			Medium:     "Perfil del contratante",
			IssueDates: []kernel.Date{issueDate},
		}},
	}

	normalized, err := toContratos(publication)
	if err != nil {
		t.Fatalf("toContratos: %v", err)
	}

	if len(normalized.Expediente.CPV.Codes) != 1 || normalized.Expediente.CPV.Codes[0].Code != "30237475" {
		t.Errorf("expediente CPV = %+v", normalized.Expediente.CPV)
	}
	if len(normalized.Lots[0].CPV.Codes) != 1 || normalized.Lots[0].CPV.Source != "inherited" {
		t.Errorf("lot CPV = %+v", normalized.Lots[0].CPV)
	}
	if len(normalized.Notices) != 1 || len(normalized.Notices[0].IssueDates) != 1 || normalized.Notices[0].IssueDates[0].String() != "2025-07-16" {
		t.Errorf("notices = %+v", normalized.Notices)
	}
	if normalized.BodyHash == "" {
		t.Error("body hash is empty")
	}
}
