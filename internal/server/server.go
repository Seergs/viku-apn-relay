package server

import (
	"net/http"

	"github.com/Seergs/viku-apn-relay/internal/health"
	"github.com/Seergs/viku-apn-relay/internal/privacy"
	"github.com/Seergs/viku-apn-relay/internal/registration"
)

// New builds the HTTP handler with all routes registered. webhooks serves
// POST /h/{id}, and registrations serves the /v1/registrations endpoints.
func New(webhooks http.Handler, registrations *registration.API) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health.Live)
	mux.HandleFunc("GET /privacy", privacy.Handler)
	mux.Handle("POST /h/{id}", webhooks)
	registrations.Routes(mux)
	return mux
}
