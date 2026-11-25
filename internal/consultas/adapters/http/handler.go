// Package http serves the read side of the imported data as JSON.
package http

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/hectorruiz23/la-mordida/internal/consultas/app"
	"github.com/hectorruiz23/la-mordida/internal/consultas/domain"
)

type Handler struct {
	service *app.Service
}

func New(service *app.Service) *Handler {
	return &Handler{service: service}
}

type listResponse struct {
	Expedientes []domain.Expediente `json:"expedientes"`
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	expedientes, err := h.service.ListExpedientes(r.Context())
	if err != nil {
		log.Printf("consultas: list expedientes: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no se pudieron leer los expedientes"})
		return
	}
	if expedientes == nil {
		expedientes = []domain.Expediente{}
	}
	writeJSON(w, http.StatusOK, listResponse{Expedientes: expedientes})
}

// Ranking serves who wins the most awards inside a buyer and award period.
func (h *Handler) Ranking(w http.ResponseWriter, r *http.Request) {
	query, err := rankingQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "consulta no válida: revisa el organismo, las fechas y la página"})
		return
	}
	ranking, err := h.service.Ranking(r.Context(), query)
	if err != nil {
		writeReadingError(w, "ranking", err)
		return
	}
	writeJSON(w, http.StatusOK, ranking)
}

// Rebates serves the comparable rebates of one buyer and award period.
func (h *Handler) Rebates(w http.ResponseWriter, r *http.Request) {
	query, err := rankingQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "consulta no válida: revisa el organismo, las fechas y la página"})
		return
	}
	rebates, err := h.service.Rebates(r.Context(), query)
	if err != nil {
		writeReadingError(w, "rebates", err)
		return
	}
	writeJSON(w, http.StatusOK, rebates)
}

// Awards serves the justifying list of one ranked entity.
func (h *Handler) Awards(w http.ResponseWriter, r *http.Request) {
	query, err := rankingQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "consulta no válida: revisa el organismo, las fechas y la página"})
		return
	}
	entityKey := r.URL.Query().Get("entity")
	if entityKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "consulta no válida: falta la entidad"})
		return
	}
	page, err := h.service.Awards(r.Context(), query, entityKey)
	if err != nil {
		writeReadingError(w, "awards", err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func rankingQuery(r *http.Request) (domain.RankingQuery, error) {
	values := r.URL.Query()
	query := domain.RankingQuery{
		BuyerScheme: values.Get("buyer_scheme"),
		BuyerID:     values.Get("buyer_id"),
		From:        values.Get("from"),
		To:          values.Get("to"),
	}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return domain.RankingQuery{}, err
		}
		query.Limit = limit
	}
	if raw := values.Get("offset"); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil {
			return domain.RankingQuery{}, err
		}
		query.Offset = offset
	}
	return query, nil
}

func writeReadingError(w http.ResponseWriter, reading string, err error) {
	switch {
	case errors.Is(err, domain.ErrUnknownBuyer):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "organismo desconocido"})
	case errors.Is(err, app.ErrInvalidQuery):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "consulta no válida: revisa el organismo, las fechas y la página"})
	default:
		log.Printf("consultas: %s: %v", reading, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no se pudo leer la consulta"})
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("consultas: encode response: %v", err)
	}
}
