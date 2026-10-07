package registration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestAPI(t *testing.T) (*http.ServeMux, *Store) {
	t.Helper()
	store := openTestStore(t)
	mux := http.NewServeMux()
	NewAPI(store, "https://relay.test/").Routes(mux)
	return mux, store
}

func do(mux http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

const registerBody = `{
	"apns_token": "apns-token-1",
	"webhook_secret": "ssssssssssssssssssssssssssssssss",
	"vikunja_user_id": 42
}`

type registerResp struct {
	ID              string `json:"id"`
	WebhookURL      string `json:"webhook_url"`
	ManagementToken string `json:"management_token"`
}

func register(t *testing.T, mux http.Handler, body string) registerResp {
	t.Helper()
	rec := do(mux, http.MethodPost, "/v1/registrations", body, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("register status = %d, body = %s", rec.Code, rec.Body)
	}
	var resp registerResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	return resp
}

func TestRegisterReturnsWebhookURLAndToken(t *testing.T) {
	mux, _ := newTestAPI(t)

	resp := register(t, mux, registerBody)

	if resp.WebhookURL != "https://relay.test/h/"+resp.ID {
		t.Fatalf("webhook_url = %q, want https://relay.test/h/%s", resp.WebhookURL, resp.ID)
	}
	if resp.ManagementToken == "" {
		t.Fatal("management_token is empty")
	}
}

func TestRegisterIsIdempotent(t *testing.T) {
	mux, store := newTestAPI(t)

	first := register(t, mux, registerBody)
	second := register(t, mux, registerBody)

	if first.ID != second.ID {
		t.Fatalf("id changed on repeat register: %q -> %q", first.ID, second.ID)
	}
	if rowCount(t, store) != 1 {
		t.Fatalf("rows = %d, want 1", rowCount(t, store))
	}
}

func TestRegisterRejectsBadInput(t *testing.T) {
	mux, store := newTestAPI(t)

	tests := []struct {
		name string
		body string
	}{
		{"invalid json", `{`},
		{"short secret", `{"apns_token":"t","webhook_secret":"short","vikunja_user_id":1}`},
		{"zero user id", `{"apns_token":"t","webhook_secret":"ssssssssssssssssssssssssssssssss","vikunja_user_id":0}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(mux, http.MethodPost, "/v1/registrations", tt.body, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}
	if rowCount(t, store) != 0 {
		t.Fatal("rejected requests must not create rows")
	}
}

func TestRegisterRejectsOversizedBody(t *testing.T) {
	mux, _ := newTestAPI(t)
	big := `{"apns_token":"` + strings.Repeat("a", maxRequestBytes) + `"}`

	rec := do(mux, http.MethodPost, "/v1/registrations", big, "")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestUnregisterRemovesRegistration(t *testing.T) {
	mux, store := newTestAPI(t)
	resp := register(t, mux, registerBody)

	rec := do(mux, http.MethodDelete, "/v1/registrations/"+resp.ID, "", resp.ManagementToken)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rowCount(t, store) != 0 {
		t.Fatal("row still present after unregister")
	}
	if _, ok := store.Lookup(resp.ID); ok {
		t.Fatal("webhook secret still resolves after unregister")
	}
}

func TestUnregisterRequiresValidToken(t *testing.T) {
	mux, store := newTestAPI(t)
	resp := register(t, mux, registerBody)

	tests := []struct {
		name  string
		id    string
		token string
	}{
		{"no token", resp.ID, ""},
		{"wrong token", resp.ID, "not-the-token"},
		{"unknown id", "does-not-exist", resp.ManagementToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(mux, http.MethodDelete, "/v1/registrations/"+tt.id, "", tt.token)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}
	if rowCount(t, store) != 1 {
		t.Fatal("rejected unregister must not delete the row")
	}
}

func TestRegisterStoresWebhookSecret(t *testing.T) {
	mux, store := newTestAPI(t)
	resp := register(t, mux, registerBody)

	target, ok := store.Lookup(resp.ID)
	secret := target.Secret
	if !ok || string(secret) != "ssssssssssssssssssssssssssssssss" {
		t.Fatalf("Secret(%q) = %q, %v", resp.ID, secret, ok)
	}
}

func TestRegisterIsRateLimitedPerIP(t *testing.T) {
	mux, store := newTestAPI(t)

	registerFrom := func(remoteAddr, apnsToken string) int {
		body := `{"apns_token":"` + apnsToken + `","webhook_secret":"ssssssssssssssssssssssssssssssss","vikunja_user_id":1}`
		req := httptest.NewRequest(http.MethodPost, "/v1/registrations", strings.NewReader(body))
		req.RemoteAddr = remoteAddr
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	for i := 0; i < registerRateBurst; i++ {
		if code := registerFrom("203.0.113.9:1234", "burst-token-"+string(rune('a'+i))); code != http.StatusOK {
			t.Fatalf("request %d within the burst = %d, want 200", i, code)
		}
	}
	if code := registerFrom("203.0.113.9:1234", "over-the-burst"); code != http.StatusTooManyRequests {
		t.Fatalf("request over the burst = %d, want 429", code)
	}
	if rowCount(t, store) != registerRateBurst {
		t.Fatalf("rows = %d, want %d (the 429 must not create a row)", rowCount(t, store), registerRateBurst)
	}

	// A different client IP has its own budget.
	if code := registerFrom("198.51.100.2:1234", "other-client"); code != http.StatusOK {
		t.Fatalf("request from a different IP = %d, want 200", code)
	}
}
