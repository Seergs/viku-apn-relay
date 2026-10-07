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

// Target is what the handler needs to know about a registered device.
type Target struct {
	ID            string
	Secret        []byte
	VikunjaUserID int64
	APNsToken     string
}

// Registrations maps an opaque registration id to its delivery target.
type Registrations interface {
	// Lookup returns the target for id, or false if the id is unknown.
	Lookup(id string) (Target, bool)
}

// Dispatcher receives the raw body of a verified delivery for its target.
// A non-nil error makes the handler answer 502 so the delivery can be retried.
type Dispatcher interface {
	Dispatch(t Target, body []byte) error
}

// Handler serves POST /h/{id}. It verifies each delivery before dispatching it.
type Handler struct {
	registrations Registrations
	dispatcher    Dispatcher
}

// NewHandler returns a Handler that looks up targets in registrations and
// hands verified bodies to dispatcher.
func NewHandler(registrations Registrations, dispatcher Dispatcher) *Handler {
	return &Handler{registrations: registrations, dispatcher: dispatcher}
}

// ServeHTTP verifies the delivery and dispatches the raw body. An unknown id
// and a bad signature get the same empty 401, so the caller cannot tell which
// check failed.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target, ok := h.registrations.Lookup(r.PathValue("id"))
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

	if !Verify(target.Secret, body, r.Header.Get(signatureHeader)) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if err := h.dispatcher.Dispatch(target, body); err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusOK)
}
