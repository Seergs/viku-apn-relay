// Package dedup suppresses a push the relay already sent for the same
// underlying Vikunja event within a short window.
//
// It exists because a user-directed event (task.overdue, task.reminder.fired,
// tasks.overdue) can legitimately match both a project-level and a
// user-level webhook for the same registration, when the event's target user
// is also a project member who turned on project-level notifications.
// Vikunja dispatches a separate HTTP delivery per matching webhook (see
// pkg/models/listeners.go in the Vikunja server), so the relay receives the
// same reminder twice through the same /h/{id} endpoint and has to collapse
// it itself. Vikunja has no cross-webhook deduplication of its own.
package dedup

import (
	"sync"
	"time"
)

// entry is one tracked key. inFlight means a send for this key is currently
// being attempted (by this goroutine or another one); once that attempt
// finishes, release replaces the entry (on success) or removes it (on
// failure, so the next delivery, including a genuine retry, is free to try
// again).
type entry struct {
	inFlight bool
	at       time.Time
}

// Cache coordinates concurrent and repeated attempts to send the same key.
// Safe for concurrent use: net/http runs each accepted connection on its own
// goroutine, so two deliveries for the same event can call Claim from
// separate goroutines at nearly the same instant. The check and the insert
// happen under the same lock, so they can never both observe "no one is
// handling this key yet": one goroutine's Lock blocks until the other's
// Unlock, and by then the entry is already written.
type Cache struct {
	window time.Duration

	mu   sync.Mutex
	seen map[string]entry

	stop chan struct{}
}

// New returns a Cache whose window bounds two things: how long a
// successfully sent key is treated as a duplicate, and how long an
// in-flight claim is honored before it is treated as abandoned (so a
// goroutine that never calls release, for example after a panic, cannot
// wedge a key shut forever). New starts a background goroutine that evicts
// stale entries every window; call Close to stop it.
func New(window time.Duration) *Cache {
	c := &Cache{
		window: window,
		seen:   make(map[string]entry),
		stop:   make(chan struct{}),
	}
	go c.evictLoop()
	return c
}

// Claim reports whether the caller should attempt to send key now. When
// proceed is true, the caller must call release exactly once when the send
// finishes, reporting whether it succeeded:
//
//   - release(true) marks key as sent, so another delivery of the same key
//     within window is treated as a duplicate and dropped.
//   - release(false) clears key immediately, so the next delivery, whether
//     it is Vikunja retrying the same failed delivery or a genuine new
//     occurrence, is free to send again right away.
//
// When proceed is false, key is already marked as sent within window, or
// another goroutine's send for it is still in flight and not stale; this
// delivery is a duplicate and release is a no-op.
func (c *Cache) Claim(key string) (proceed bool, release func(sent bool)) {
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.seen[key]; ok && now.Sub(e.at) < c.window {
		return false, func(bool) {}
	}

	c.seen[key] = entry{inFlight: true, at: now}
	return true, func(sent bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if sent {
			c.seen[key] = entry{at: time.Now()}
			return
		}
		delete(c.seen, key)
	}
}

func (c *Cache) evictLoop() {
	ticker := time.NewTicker(c.window)
	defer ticker.Stop()

	for {
		select {
		case <-c.stop:
			return
		case now := <-ticker.C:
			c.mu.Lock()
			for k, e := range c.seen {
				if now.Sub(e.at) >= c.window {
					delete(c.seen, k)
				}
			}
			c.mu.Unlock()
		}
	}
}

// Close stops the eviction goroutine. The Cache must not be used afterward.
func (c *Cache) Close() {
	close(c.stop)
}
