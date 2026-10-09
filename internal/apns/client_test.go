package apns

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestKey creates a throwaway ES256 key for the test server. It is not an
// Apple credential.
func newTestKey(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// verifyJWT checks the ES256 signature and returns the decoded claims header.
func verifyJWT(t *testing.T, key *ecdsa.PublicKey, token string) (header, claims map[string]any) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts, want 3", len(parts))
	}
	decode := func(s string) []byte {
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		return b
	}
	sig := decode(parts[2])
	if len(sig) != 64 {
		t.Fatalf("signature length = %d, want 64", len(sig))
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(key, digest[:], r, s) {
		t.Fatal("provider token signature does not verify")
	}
	_ = json.Unmarshal(decode(parts[0]), &header)
	_ = json.Unmarshal(decode(parts[1]), &claims)
	return header, claims
}

type testServer struct {
	srv    *httptest.Server
	key    *ecdsa.PrivateKey
	keyPEM []byte
}

// newTestServer answers every push with status and reason. It records the
// last request in the returned pointers.
func newTestServer(t *testing.T, status int, reason string) (*testServer, *http.Request, *[]byte) {
	t.Helper()
	key, keyPEM := newTestKey(t)
	ts := &testServer{key: key, keyPEM: keyPEM}
	var last http.Request
	var lastBody []byte

	ts.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = *r
		lastBody, _ = io.ReadAll(r.Body)
		if status != http.StatusOK {
			w.WriteHeader(status)
			w.Write([]byte(`{"reason":"` + reason + `"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	ts.srv.EnableHTTP2 = true
	ts.srv.StartTLS()
	t.Cleanup(ts.srv.Close)
	return ts, &last, &lastBody
}

func newTestClient(t *testing.T, ts *testServer) *Client {
	t.Helper()
	c, err := NewClient(Config{
		KeyID:      "KEYID12345",
		TeamID:     "TEAMID1234",
		Topic:      "com.example.app",
		KeyPEM:     ts.keyPEM,
		Endpoint:   ts.srv.URL,
		HTTPClient: ts.srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestSendSucceedsWithSignedRequest(t *testing.T) {
	ts, last, body := newTestServer(t, http.StatusOK, "")
	c := newTestClient(t, ts)
	payload := []byte(`{"aps":{"alert":{"title":"Inbox"}}}`)

	if err := c.Send(context.Background(), "device-token-1", payload, "task-255"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if last.Method != http.MethodPost || last.URL.Path != "/3/device/device-token-1" {
		t.Fatalf("request = %s %s, want POST /3/device/device-token-1", last.Method, last.URL.Path)
	}
	if got := last.Header.Get("apns-topic"); got != "com.example.app" {
		t.Fatalf("apns-topic = %q", got)
	}
	if got := last.Header.Get("apns-push-type"); got != "alert" {
		t.Fatalf("apns-push-type = %q", got)
	}
	if got := last.Header.Get("apns-priority"); got != "10" {
		t.Fatalf("apns-priority = %q", got)
	}
	if got := last.Header.Get("apns-collapse-id"); got != "task-255" {
		t.Fatalf("apns-collapse-id = %q", got)
	}
	if string(*body) != string(payload) {
		t.Fatalf("body = %q, want the payload unchanged", *body)
	}

	auth := last.Header.Get("authorization")
	token, ok := strings.CutPrefix(auth, "bearer ")
	if !ok {
		t.Fatalf("authorization = %q, want bearer token", auth)
	}
	header, claims := verifyJWT(t, &ts.key.PublicKey, token)
	if header["alg"] != "ES256" || header["kid"] != "KEYID12345" {
		t.Fatalf("header = %v", header)
	}
	if claims["iss"] != "TEAMID1234" {
		t.Fatalf("iss = %v", claims["iss"])
	}
}

func TestSendOmitsCollapseIDHeaderWhenEmpty(t *testing.T) {
	ts, last, _ := newTestServer(t, http.StatusOK, "")
	c := newTestClient(t, ts)

	if err := c.Send(context.Background(), "device-token-1", []byte(`{}`), ""); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, ok := last.Header["Apns-Collapse-Id"]; ok {
		t.Fatalf("apns-collapse-id header sent with an empty collapse id: %v", last.Header)
	}
}

func TestSendReturnsUnregisteredOn410(t *testing.T) {
	ts, _, _ := newTestServer(t, http.StatusGone, "Unregistered")
	c := newTestClient(t, ts)

	err := c.Send(context.Background(), "device-token-1", []byte(`{}`), "")
	if !errors.Is(err, ErrUnregistered) {
		t.Fatalf("Send = %v, want ErrUnregistered", err)
	}
}

func TestSendReturnsStatusErrorForOtherRejections(t *testing.T) {
	ts, _, _ := newTestServer(t, http.StatusBadRequest, "BadDeviceToken")
	c := newTestClient(t, ts)

	err := c.Send(context.Background(), "device-token-1", []byte(`{}`), "")
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("Send = %v, want *StatusError", err)
	}
	if se.Code != http.StatusBadRequest || se.Reason != "BadDeviceToken" {
		t.Fatalf("StatusError = %+v", se)
	}
	if strings.Contains(err.Error(), "device-token-1") {
		t.Fatal("error message contains the device token")
	}
}

func TestProviderTokenIsReusedWithinTTL(t *testing.T) {
	ts, _, _ := newTestServer(t, http.StatusOK, "")
	c := newTestClient(t, ts)
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }

	first, err := c.providerToken()
	if err != nil {
		t.Fatalf("providerToken: %v", err)
	}
	now = now.Add(tokenTTL - time.Minute)
	second, _ := c.providerToken()
	if first != second {
		t.Fatal("token regenerated before TTL")
	}
	now = now.Add(2 * time.Minute)
	third, _ := c.providerToken()
	if first == third {
		t.Fatal("token not regenerated after TTL")
	}
}

func TestNewClientRejectsBadConfig(t *testing.T) {
	_, keyPEM := newTestKey(t)
	tests := []struct {
		name string
		cfg  Config
	}{
		{"missing key id", Config{TeamID: "t", Topic: "x", KeyPEM: keyPEM}},
		{"missing team id", Config{KeyID: "k", Topic: "x", KeyPEM: keyPEM}},
		{"missing topic", Config{KeyID: "k", TeamID: "t", KeyPEM: keyPEM}},
		{"not pem", Config{KeyID: "k", TeamID: "t", Topic: "x", KeyPEM: []byte("nope")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewClient(tt.cfg); err == nil {
				t.Fatal("NewClient accepted bad config")
			}
		})
	}
}
