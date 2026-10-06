package webhook

import (
	"errors"
	"io"
	"net/http"
)

const (
	signatureHeader = "X-Vikunja-Signature"
	maxBodyBytes    = 64 << 10
)

// Registrations maps an opaque registration id to its webhook secret.
type Registrations interface {
	// Secret returns the secret for id, or false if the id is unknown.
	Secret(id string) ([]byte, bool)
}

// Dispatcher receives the raw body of a verified delivery.
type Dispatcher interface {
	Dispatch(body []byte)
}

// Handler serves POST /h/{id}. It verifies each delivery before dispatching it.
type Handler struct {
	registrations Registrations
	dispatcher    Dispatcher
}

// NewHandler returns a Handler that looks up secrets in registrations and
// hands verified bodies to dispatcher.
func NewHandler(registrations Registrations, dispatcher Dispatcher) *Handler {
	return &Handler{registrations: registrations, dispatcher: dispatcher}
}

// ServeHTTP verifies the delivery and dispatches the raw body. An unknown id
// and a bad signature get the same empty 401, so the caller cannot tell which
// check failed.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	secret, ok := h.registrations.Secret(r.PathValue("id"))
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if !Verify(secret, body, r.Header.Get(signatureHeader)) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	h.dispatcher.Dispatch(body)
	w.WriteHeader(http.StatusOK)
}
