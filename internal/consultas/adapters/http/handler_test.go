package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hectorruiz23/la-mordida/internal/consultas/app"
	"github.com/hectorruiz23/la-mordida/internal/consultas/domain"
	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

type fakeStore struct {
	expedientes []domain.Expediente
}

func (f fakeStore) ListExpedientes(context.Context) ([]domain.Expediente, error) {
	return f.expedientes, nil
}

func (f fakeStore) Expediente(context.Context, int64) (domain.Expediente, error) {
	return domain.Expediente{}, domain.ErrExpedienteNotFound
}

func (f fakeStore) Coverage(context.Context) (domain.Coverage, error) {
	return domain.Coverage{}, nil
}

func (f fakeStore) Ranking(context.Context, domain.RankingQuery) (domain.Ranking, error) {
	return domain.Ranking{}, nil
}

func (f fakeStore) Awards(context.Context, domain.RankingQuery, string) (domain.AwardPage, error) {
	return domain.AwardPage{}, nil
}

func (f fakeStore) Rebates(context.Context, domain.RankingQuery) (domain.RebateRanking, error) {
	return domain.RebateRanking{}, nil
}

func TestListServesImportedExpedienteWithExactAmounts(t *testing.T) {
	handler := New(app.New(fakeStore{expedientes: []domain.Expediente{{
		Buyer:    domain.Party{Scheme: "NIF", ID: "P3704600J", Name: "Alcaldía del Ayuntamiento de Béjar"},
		FolderID: "1787/2025",
		Status:   "ADJ",
		Awards: []domain.Award{{
			LotID:      "1",
			ResultCode: "8",
			AwardDate:  "2025-10-10",
			Winner:     domain.Party{Scheme: "NIF", ID: "B37491404", Name: "Suministros Lavabar, SL"},
			Amounts: []kernel.Money{{
				Cents: 78411, Currency: "EUR", TaxBase: kernel.TaxExclusive,
			}},
		}},
	}}}))

	request := httptest.NewRequest(http.MethodGet, "/api/expedientes", nil)
	response := httptest.NewRecorder()
	handler.List(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}

	var payload struct {
		Expedientes []struct {
			FolderID string `json:"folder_id"`
			Awards   []struct {
				AwardDate string `json:"award_date"`
				Amounts   []struct {
					Amount  string `json:"amount"`
					TaxBase string `json:"tax_base"`
				} `json:"amounts"`
			} `json:"awards"`
		} `json:"expedientes"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Expedientes) != 1 || payload.Expedientes[0].FolderID != "1787/2025" {
		t.Fatalf("payload = %+v", payload)
	}
	awards := payload.Expedientes[0].Awards
	if len(awards) != 1 || awards[0].AwardDate != "2025-10-10" {
		t.Fatalf("awards = %+v", awards)
	}
	if len(awards[0].Amounts) != 1 || awards[0].Amounts[0].Amount != "784.11" || awards[0].Amounts[0].TaxBase != "tax_exclusive" {
		t.Fatalf("amounts = %+v", awards[0].Amounts)
	}
}
