package registration

import (
	"errors"
	"fmt"
)

const (
	maxAPNsTokenLen = 256
	minSecretLen    = 32
	maxSecretLen    = 256
)

// ErrInvalid marks input that the client must fix. The API maps it to 400.
var ErrInvalid = errors.New("invalid input")

// NewRegistration is the input to Register. It carries no task content.
type NewRegistration struct {
	APNsToken     string
	WebhookSecret []byte
	VikunjaUserID int64
}

func (n NewRegistration) validate() error {
	if n.APNsToken == "" || len(n.APNsToken) > maxAPNsTokenLen {
		return invalidf("apns_token must be 1 to %d characters", maxAPNsTokenLen)
	}
	if len(n.WebhookSecret) < minSecretLen || len(n.WebhookSecret) > maxSecretLen {
		return invalidf("webhook_secret must be %d to %d bytes", minSecretLen, maxSecretLen)
	}
	if n.VikunjaUserID <= 0 {
		return invalidf("vikunja_user_id must be positive")
	}
	return nil
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...)
}
