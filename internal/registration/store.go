package registration

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Seergs/viku-apn-relay/internal/webhook"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS registrations (
	id                    TEXT    PRIMARY KEY,
	apns_token            TEXT    NOT NULL UNIQUE,
	webhook_secret        BLOB    NOT NULL,
	vikunja_user_id       INTEGER NOT NULL,
	management_token_hash TEXT    NOT NULL,
	created_at            INTEGER NOT NULL,
	updated_at            INTEGER NOT NULL
);`

// ErrUnauthorized reports a missing or wrong management token, or an unknown id.
var ErrUnauthorized = errors.New("unauthorized")

// Store persists registrations in SQLite. It is the only place registration
// rows are read or written.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Registration is one device registration. ManagementToken is only set on
// the value returned by Register; it is never read back from the database.
type Registration struct {
	ID              string
	WebhookSecret   []byte
	ManagementToken string
}

// Open opens or creates the SQLite database at path and applies the schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// SQLite allows one writer at a time. A single connection keeps writes
	// serialized and avoids SQLITE_BUSY under concurrent registrations.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db, now: time.Now}, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// Register creates a registration for the APNs token, or replaces the
// existing one for that token. Replacing keeps the opaque id so the webhook
// URL stays the same, and issues a new management token that invalidates the
// previous one.
func (s *Store) Register(in NewRegistration) (Registration, error) {
	if err := in.validate(); err != nil {
		return Registration{}, err
	}

	mgmt, err := newToken()
	if err != nil {
		return Registration{}, err
	}
	now := s.now().Unix()

	tx, err := s.db.Begin()
	if err != nil {
		return Registration{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	var id string
	err = tx.QueryRow(`SELECT id FROM registrations WHERE apns_token = ?`, in.APNsToken).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id, err = newID()
		if err != nil {
			return Registration{}, err
		}
		_, err = tx.Exec(`INSERT INTO registrations
			(id, apns_token, webhook_secret, vikunja_user_id, management_token_hash, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, in.APNsToken, in.WebhookSecret, in.VikunjaUserID, hashToken(mgmt), now, now)
	case err != nil:
		return Registration{}, fmt.Errorf("lookup by apns token: %w", err)
	default:
		_, err = tx.Exec(`UPDATE registrations SET
			webhook_secret = ?, vikunja_user_id = ?, management_token_hash = ?, updated_at = ?
			WHERE id = ?`,
			in.WebhookSecret, in.VikunjaUserID, hashToken(mgmt), now, id)
	}
	if err != nil {
		return Registration{}, fmt.Errorf("write registration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Registration{}, fmt.Errorf("commit: %w", err)
	}

	return Registration{
		ID:              id,
		WebhookSecret:   in.WebhookSecret,
		ManagementToken: mgmt,
	}, nil
}

// Delete removes the registration after checking the management token.
func (s *Store) Delete(id, managementToken string) error {
	if err := s.authorize(id, managementToken); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM registrations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete registration: %w", err)
	}
	return nil
}

// Lookup returns the delivery target for id. It implements
// webhook.Registrations.
func (s *Store) Lookup(id string) (webhook.Target, bool) {
	var t webhook.Target
	err := s.db.QueryRow(
		`SELECT webhook_secret, vikunja_user_id, apns_token FROM registrations WHERE id = ?`, id,
	).Scan(&t.Secret, &t.VikunjaUserID, &t.APNsToken)
	if err != nil {
		return webhook.Target{}, false
	}
	t.ID = id
	return t, true
}

// Remove deletes the registration without a management token. Only the push
// dispatcher calls it, after APNs reports the device token as unregistered.
func (s *Store) Remove(id string) error {
	_, err := s.db.Exec(`DELETE FROM registrations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("remove registration: %w", err)
	}
	return nil
}

// authorize returns ErrUnauthorized when id is unknown or the management token
// does not match. Both cases look the same to the caller, so the response does
// not reveal which ids exist.
func (s *Store) authorize(id, managementToken string) error {
	var stored string
	err := s.db.QueryRow(`SELECT management_token_hash FROM registrations WHERE id = ?`, id).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return fmt.Errorf("lookup registration: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(hashToken(managementToken))) != 1 {
		return ErrUnauthorized
	}
	return nil
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
