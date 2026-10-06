package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubWebhooks struct{}

func (stubWebhooks) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusUnauthorized)
}

func TestRoutes(t *testing.T) {
	h := New(stubWebhooks{})

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{name: "healthz", method: http.MethodGet, path: "/healthz", wantStatus: http.StatusOK},
		{name: "healthz wrong method", method: http.MethodPost, path: "/healthz", wantStatus: http.StatusMethodNotAllowed},
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
