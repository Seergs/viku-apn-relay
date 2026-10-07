package privacy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesHTML(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler(rec, httptest.NewRequest(http.MethodGet, "/privacy", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}

	body := rec.Body.String()
	for _, want := range []string{
		"viku-support@sergiosuarez.dev",
		"Request counts and HTTP status codes only",
		"We never store task titles, project names",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q", want)
		}
	}
}
