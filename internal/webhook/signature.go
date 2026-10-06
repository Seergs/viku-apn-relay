// Package webhook verifies and handles Vikunja webhook deliveries.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Verify reports whether header is the hex-encoded HMAC-SHA256 of body under
// secret. The comparison runs in constant time.
func Verify(secret, body []byte, header string) bool {
	sig, err := hex.DecodeString(header)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(body)

	return hmac.Equal(sig, mac.Sum(nil))
}
