package registration

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

var testSecret = []byte(strings.Repeat("s", 32))

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func newInput() NewRegistration {
	return NewRegistration{
		APNsToken:     "apns-token-1",
		WebhookSecret: testSecret,
		VikunjaUserID: 42,
	}
}

func rowCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM registrations`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestRegisterCreatesRegistration(t *testing.T) {
	s := openTestStore(t)

	reg, err := s.Register(newInput())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if reg.ID == "" || reg.ManagementToken == "" {
		t.Fatalf("Register returned empty id or token: %+v", reg)
	}

	secret, ok := s.Secret(reg.ID)
	if !ok || !bytes.Equal(secret, testSecret) {
		t.Fatalf("Secret(%q) = %q, %v; want the registered secret", reg.ID, secret, ok)
	}
}

func TestRegisterStoresOnlyTokenHash(t *testing.T) {
	s := openTestStore(t)

	reg, err := s.Register(newInput())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	var stored string
	if err := s.db.QueryRow(`SELECT management_token_hash FROM registrations WHERE id = ?`, reg.ID).Scan(&stored); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if stored == reg.ManagementToken {
		t.Fatal("management token must be stored hashed, not in plain text")
	}
}

func TestRegisterIsIdempotentPerAPNsToken(t *testing.T) {
	s := openTestStore(t)

	first, err := s.Register(newInput())
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}
	changed := newInput()
	changed.VikunjaUserID = 7
	second, err := s.Register(changed)
	if err != nil {
		t.Fatalf("second Register: %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("re-register changed id: %q -> %q", first.ID, second.ID)
	}
	if rowCount(t, s) != 1 {
		t.Fatalf("rows = %d, want 1", rowCount(t, s))
	}
	if err := s.Delete(first.ID, first.ManagementToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old management token still accepted after re-register: %v", err)
	}
	if err := s.Delete(second.ID, second.ManagementToken); err != nil {
		t.Fatalf("new management token rejected: %v", err)
	}
}

func TestDeleteRemovesRow(t *testing.T) {
	s := openTestStore(t)
	reg, err := s.Register(newInput())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := s.Delete(reg.ID, reg.ManagementToken); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if rowCount(t, s) != 0 {
		t.Fatalf("rows after delete = %d, want 0", rowCount(t, s))
	}
	if _, ok := s.Secret(reg.ID); ok {
		t.Fatal("Secret still resolves after delete")
	}
}

func TestDeleteRejectsWrongTokenAndUnknownID(t *testing.T) {
	s := openTestStore(t)
	reg, err := s.Register(newInput())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := s.Delete(reg.ID, "wrong-token"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Delete with wrong token = %v, want ErrUnauthorized", err)
	}
	if err := s.Delete("unknown-id", reg.ManagementToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Delete with unknown id = %v, want ErrUnauthorized", err)
	}
	if rowCount(t, s) != 1 {
		t.Fatal("rejected deletes must not remove the row")
	}
}

func TestRegisterRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*NewRegistration)
	}{
		{"empty apns token", func(n *NewRegistration) { n.APNsToken = "" }},
		{"short secret", func(n *NewRegistration) { n.WebhookSecret = []byte("short") }},
		{"zero user id", func(n *NewRegistration) { n.VikunjaUserID = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openTestStore(t)
			in := newInput()
			tt.mutate(&in)

			_, err := s.Register(in)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Register = %v, want ErrInvalid", err)
			}
			if rowCount(t, s) != 0 {
				t.Fatal("invalid input must not create a row")
			}
		})
	}
}

func TestOpenIsRepeatable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay.db")
	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatalf("second Open on existing database: %v", err)
	}
	defer second.Close()
	if _, err := second.db.Exec(`SELECT 1 FROM registrations`); err != nil {
		t.Fatalf("schema missing after reopen: %v", err)
	}
}
