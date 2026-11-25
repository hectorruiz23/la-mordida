// Package httpserver holds the process-level HTTP server and the health probe.
package httpserver

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
)

type Pinger interface {
	PingContext(ctx context.Context) error
}

func Health(pinger Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := pinger.PingContext(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]bool{"ok": false})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func New(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:     addr,
		Handler:  handler,
		ErrorLog: log.Default(),
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("httpserver: encode response: %v", err)
	}
}
