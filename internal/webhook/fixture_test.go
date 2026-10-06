package webhook

import (
	"bytes"
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestRecordedFixture verifies a delivery recorded from a real Vikunja v2.7.0
// instance. The body was anonymized and re-signed with the test secret.
// The secret comes from VIKU_TEST_WEBHOOK_SECRET and is never stored in the repo.
func TestRecordedFixture(t *testing.T) {
	secret := os.Getenv("VIKU_TEST_WEBHOOK_SECRET")
	if secret == "" {
		t.Skip("VIKU_TEST_WEBHOOK_SECRET not set")
	}

	body, err := os.ReadFile("testdata/task_created.json")
	if err != nil {
		t.Fatalf("read body fixture: %v", err)
	}
	sig, err := os.ReadFile("testdata/task_created.sig")
	if err != nil {
		t.Fatalf("read signature fixture: %v", err)
	}
	signature := strings.TrimSpace(string(sig))

	if !Verify([]byte(secret), body, signature) {
		t.Fatal("recorded fixture does not verify with VIKU_TEST_WEBHOOK_SECRET")
	}

	d := &recordingDispatcher{}
	h := NewHandler(fakeRegistrations{"fixture": []byte(secret)}, d)
	rec := deliver(h, "fixture", body, signature)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(d.bodies) != 1 || !bytes.Equal(d.bodies[0], body) {
		t.Fatal("handler did not dispatch the recorded body unchanged")
	}
}
