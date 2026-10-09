// Package apns sends alert pushes through Apple Push Notification service
// using token-based (JWT) authentication.
package apns

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// ProductionEndpoint is the APNs host for App Store and TestFlight builds.
	ProductionEndpoint = "https://api.push.apple.com"
	// SandboxEndpoint is the APNs host for development builds.
	SandboxEndpoint = "https://api.sandbox.push.apple.com"

	// tokenTTL is how long a provider token is reused. APNs rejects tokens
	// older than one hour, and Apple asks providers not to refresh more often
	// than every twenty minutes.
	tokenTTL = 40 * time.Minute
	maxReply = 1 << 10
)

// ErrUnregistered reports that APNs no longer accepts the device token. The
// registration should be removed.
var ErrUnregistered = errors.New("apns: device token unregistered")

// StatusError is an APNs rejection other than an unregistered token. Reason is
// the short code APNs returns, such as "BadDeviceToken". It never contains the
// device token or the payload.
type StatusError struct {
	Code   int
	Reason string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("apns: status %d reason %s", e.Code, e.Reason)
}

// Config holds the Apple credentials and the target host.
type Config struct {
	KeyID    string
	TeamID   string
	Topic    string
	KeyPEM   []byte
	Endpoint string
	// HTTPClient is optional. Tests use it to trust a test server.
	HTTPClient *http.Client
}

// Client sends pushes to APNs. It is safe for concurrent use.
type Client struct {
	keyID    string
	teamID   string
	topic    string
	endpoint string
	key      *ecdsa.PrivateKey
	http     *http.Client

	mu        sync.Mutex
	token     string
	tokenTime time.Time
	now       func() time.Time
}

// NewClient validates cfg and returns a Client.
func NewClient(cfg Config) (*Client, error) {
	if cfg.KeyID == "" || cfg.TeamID == "" || cfg.Topic == "" {
		return nil, errors.New("apns: key id, team id and topic are required")
	}
	key, err := parseKey(cfg.KeyPEM)
	if err != nil {
		return nil, err
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = ProductionEndpoint
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		keyID:    cfg.KeyID,
		teamID:   cfg.TeamID,
		topic:    cfg.Topic,
		endpoint: strings.TrimSuffix(endpoint, "/"),
		key:      key,
		http:     httpClient,
		now:      time.Now,
	}, nil
}

func parseKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("apns: private key is not PEM encoded")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apns: parse private key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("apns: private key is not ECDSA")
	}
	return key, nil
}

// Send delivers payload to deviceToken as an alert push. When collapseID is
// non-empty, APNs replaces any still-queued delivery sharing it instead of
// stacking another one. It returns ErrUnregistered when APNs reports the
// token as unregistered, and a *StatusError for any other rejection.
func (c *Client) Send(ctx context.Context, deviceToken string, payload []byte, collapseID string) error {
	bearer, err := c.providerToken()
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint+"/3/device/"+deviceToken, bytes.NewReader(payload))
	if err != nil {
		return errors.New("apns: build request")
	}
	req.Header.Set("authorization", "bearer "+bearer)
	req.Header.Set("apns-topic", c.topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("content-type", "application/json")
	if collapseID != "" {
		req.Header.Set("apns-collapse-id", collapseID)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Do not wrap the transport error: it includes the URL, which carries
		// the device token.
		return errors.New("apns: request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil
	}

	var reply struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, maxReply)).Decode(&reply)
	if resp.StatusCode == http.StatusGone && reply.Reason == "Unregistered" {
		return ErrUnregistered
	}
	return &StatusError{Code: resp.StatusCode, Reason: reply.Reason}
}

// providerToken returns a signed JWT, reusing the last one for tokenTTL.
func (c *Client) providerToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if c.token != "" && now.Sub(c.tokenTime) < tokenTTL {
		return c.token, nil
	}

	header := base64URL([]byte(fmt.Sprintf(`{"alg":"ES256","kid":%q}`, c.keyID)))
	claims := base64URL([]byte(fmt.Sprintf(`{"iss":%q,"iat":%d}`, c.teamID, now.Unix())))
	signingInput := header + "." + claims

	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, c.key, digest[:])
	if err != nil {
		return "", errors.New("apns: sign provider token")
	}
	// ES256 signatures are the fixed-width concatenation of r and s.
	size := (c.key.Curve.Params().BitSize + 7) / 8
	sig := make([]byte, 2*size)
	r.FillBytes(sig[:size])
	s.FillBytes(sig[size:])

	c.token = signingInput + "." + base64URL(sig)
	c.tokenTime = now
	return c.token, nil
}

func base64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
