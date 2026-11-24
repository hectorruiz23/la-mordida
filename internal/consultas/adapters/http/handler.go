// Package http serves the read side of the imported data as JSON.
package http

import (
	"encoding/json"
	"log"
	"net/http"

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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("consultas: encode response: %v", err)
	}
}
