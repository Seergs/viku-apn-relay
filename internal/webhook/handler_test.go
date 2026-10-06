package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testSecret is a dummy value for unit tests. It is not a real webhook secret.
var testSecret = []byte("unit-test-secret")

type fakeRegistrations map[string][]byte

func (f fakeRegistrations) Secret(id string) ([]byte, bool) {
	s, ok := f[id]
	return s, ok
}

type recordingDispatcher struct {
	bodies [][]byte
}

func (r *recordingDispatcher) Dispatch(body []byte) {
	r.bodies = append(r.bodies, body)
}

func sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// deliver sends a request through a ServeMux so that r.PathValue("id") is set,
// the same way the real server routes it.
func deliver(h http.Handler, id string, body []byte, signature string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.Handle("POST /h/{id}", h)

	req := httptest.NewRequest(http.MethodPost, "/h/"+id, bytes.NewReader(body))
	if signature != "" {
		req.Header.Set("X-Vikunja-Signature", signature)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandler(t *testing.T) {
	body := []byte(`{"event_name":"task.created","time":"2026-01-01T00:00:00Z","data":{}}`)
	regs := fakeRegistrations{"reg-1": testSecret}

	t.Run("valid signature dispatches body", func(t *testing.T) {
		d := &recordingDispatcher{}
		rec := deliver(NewHandler(regs, d), "reg-1", body, sign(testSecret, body))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if len(d.bodies) != 1 || !bytes.Equal(d.bodies[0], body) {
			t.Fatalf("dispatched %d bodies, want the one delivered body", len(d.bodies))
		}
	})

	t.Run("bad signature returns 401 and does not dispatch", func(t *testing.T) {
		d := &recordingDispatcher{}
		rec := deliver(NewHandler(regs, d), "reg-1", body, sign([]byte("other"), body))

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
		if len(d.bodies) != 0 {
			t.Fatal("dispatched a body with a bad signature")
		}
	})

	t.Run("missing signature returns 401", func(t *testing.T) {
		d := &recordingDispatcher{}
		rec := deliver(NewHandler(regs, d), "reg-1", body, "")

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
		if len(d.bodies) != 0 {
			t.Fatal("dispatched a body with no signature")
		}
	})

	t.Run("unknown id returns the same 401 as a bad signature", func(t *testing.T) {
		d := &recordingDispatcher{}
		unknown := deliver(NewHandler(regs, d), "nope", body, sign(testSecret, body))
		bad := deliver(NewHandler(regs, d), "reg-1", body, sign([]byte("other"), body))

		if unknown.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", unknown.Code, http.StatusUnauthorized)
		}
		if unknown.Code != bad.Code || unknown.Body.String() != bad.Body.String() {
			t.Fatal("unknown id and bad signature produce different responses")
		}
		if len(d.bodies) != 0 {
			t.Fatal("dispatched a body for an unknown id")
		}
	})

	t.Run("oversized body returns 413 and does not dispatch", func(t *testing.T) {
		d := &recordingDispatcher{}
		big := []byte(strings.Repeat("a", maxBodyBytes+1))
		rec := deliver(NewHandler(regs, d), "reg-1", big, sign(testSecret, big))

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
		}
		if len(d.bodies) != 0 {
			t.Fatal("dispatched an oversized body")
		}
	})
}

func TestPlaceholdersRejectEverything(t *testing.T) {
	h := NewHandler(NoRegistrations{}, DiscardDispatcher{})
	rec := deliver(h, "any", []byte(`{}`), sign(testSecret, []byte(`{}`)))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
