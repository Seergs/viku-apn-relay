package webhook

// NoRegistrations rejects every registration id. It stands in until the
// registration store exists.
type NoRegistrations struct{}

// Secret always reports the id as unknown.
func (NoRegistrations) Secret(string) ([]byte, bool) {
	return nil, false
}

// DiscardDispatcher drops verified deliveries. It stands in until the event
// mapping stage exists.
type DiscardDispatcher struct{}

// Dispatch ignores the body.
func (DiscardDispatcher) Dispatch([]byte) {}
