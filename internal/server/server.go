package server

import (
	"net/http"

	"github.com/Seergs/viku-apn-relay/internal/health"
)

// New builds the HTTP handler with all routes registered.
func New() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health.Live)
	return mux
}
