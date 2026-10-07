package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Seergs/viku-apn-relay/internal/registration"
)

type stubWebhooks struct{}

func (stubWebhooks) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusUnauthorized)
}

func TestRoutes(t *testing.T) {
	store, err := registration.Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	h := New(stubWebhooks{}, registration.NewAPI(store, "https://relay.test"))

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{name: "healthz", method: http.MethodGet, path: "/healthz", wantStatus: http.StatusOK},
		{name: "healthz wrong method", method: http.MethodPost, path: "/healthz", wantStatus: http.StatusMethodNotAllowed},
		{name: "privacy", method: http.MethodGet, path: "/privacy", wantStatus: http.StatusOK},
		{name: "privacy wrong method", method: http.MethodPost, path: "/privacy", wantStatus: http.StatusMethodNotAllowed},
		{name: "unknown route", method: http.MethodGet, path: "/nope", wantStatus: http.StatusNotFound},
		{name: "webhook route", method: http.MethodPost, path: "/h/abc", wantStatus: http.StatusUnauthorized},
		{name: "webhook wrong method", method: http.MethodGet, path: "/h/abc", wantStatus: http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}
