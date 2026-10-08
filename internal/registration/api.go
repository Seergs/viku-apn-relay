package registration

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Seergs/viku-apn-relay/internal/ratelimit"
	"golang.org/x/time/rate"
)

const maxRequestBytes = 4 << 10

// Registration is the only unauthenticated, write endpoint, so it is the one
// a client could hammer. 5 requests per minute per IP, with a burst of 5 to
// tolerate retries, is enough for a device that registers on launch.
const (
	registerRateLimit = rate.Limit(5.0 / 60.0)
	registerRateBurst = 5
	registerRateTTL   = 10 * time.Minute
)

// API serves the device registration endpoints the iOS app calls.
type API struct {
	store       *Store
	baseURL     string
	registerLim *ratelimit.Limiter
}

// NewAPI returns the registration handlers. baseURL is the public origin of
// the relay, used to build the webhook URL returned to the app.
func NewAPI(store *Store, baseURL string) *API {
	return &API{
		store:       store,
		baseURL:     strings.TrimSuffix(baseURL, "/"),
		registerLim: ratelimit.New(registerRateLimit, registerRateBurst, registerRateTTL),
	}
}

// Routes registers the endpoints on mux. Registration is rate limited per
// client IP; it is the only endpoint with no credential to authenticate it.
func (a *API) Routes(mux *http.ServeMux) {
	mux.Handle("POST /v1/registrations", a.registerLim.Middleware(http.HandlerFunc(a.register)))
	mux.HandleFunc("DELETE /v1/registrations/{id}", a.unregister)
}

type registerRequest struct {
	APNsToken     string `json:"apns_token"`
	WebhookSecret string `json:"webhook_secret"`
	VikunjaUserID int64  `json:"vikunja_user_id"`
	AccountKey    string `json:"account_key"`
}

type registerResponse struct {
	ID              string `json:"id"`
	WebhookURL      string `json:"webhook_url"`
	ManagementToken string `json:"management_token"`
}

// register creates or replaces the registration for an account_key.
// Repeating the call with the same account_key leaves one registration
// behind, even if the apns_token changed (device token rotation).
func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decode(w, r, &req) {
		return
	}

	reg, err := a.store.Register(NewRegistration{
		APNsToken:     req.APNsToken,
		WebhookSecret: []byte(req.WebhookSecret),
		VikunjaUserID: req.VikunjaUserID,
		AccountKey:    req.AccountKey,
	})
	if err != nil {
		writeInternalOrInvalid(w, err)
		return
	}

	writeJSON(w, http.StatusOK, registerResponse{
		ID:              reg.ID,
		WebhookURL:      a.baseURL + "/h/" + reg.ID,
		ManagementToken: reg.ManagementToken,
	})
}

// unregister removes the registration. Authorized by the management token.
func (a *API) unregister(w http.ResponseWriter, r *http.Request) {
	err := a.store.Delete(r.PathValue("id"), bearer(r))
	if errors.Is(err, ErrUnauthorized) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if err != nil {
		writeInternalOrInvalid(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decode reads a JSON body of at most maxRequestBytes into v and writes the
// error response itself when the body is invalid.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return false
		}
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return false
	}
	return true
}

// bearer returns the token from an "Authorization: Bearer <token>" header, or
// an empty string when the header is missing or malformed.
func bearer(r *http.Request) string {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return token
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeInternalOrInvalid maps input errors to 400. Anything else is a 500
// with no detail, so internal errors never reach the client.
func writeInternalOrInvalid(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrInvalid) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}
