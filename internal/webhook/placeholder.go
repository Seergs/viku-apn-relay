package webhook

// NoRegistrations rejects every registration id. It is used by tests that
// need a registry with no entries.
type NoRegistrations struct{}

// Lookup always reports the id as unknown.
func (NoRegistrations) Lookup(string) (Target, bool) {
	return Target{}, false
}

// DiscardDispatcher drops verified deliveries.
type DiscardDispatcher struct{}

// Dispatch ignores the target and the body.
func (DiscardDispatcher) Dispatch(Target, []byte) error {
	return nil
}
