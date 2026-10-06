package server

import (
	"net/http"

	"github.com/Seergs/viku-apn-relay/internal/health"
)

// New builds the HTTP handler with all routes registered. webhooks serves
// POST /h/{id}.
func New(webhooks http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health.Live)
	mux.Handle("POST /h/{id}", webhooks)
	return mux
}
