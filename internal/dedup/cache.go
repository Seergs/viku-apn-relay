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
// it itself; Vikunja has no cross-webhook deduplication of its own.
package dedup

import (
	"sync"
	"time"
)

// Cache tracks keys seen within the last Window. Safe for concurrent use:
// net/http runs each accepted connection on its own goroutine, so two
// deliveries for the same event can reach SeenRecently from separate
// goroutines at nearly the same instant. The check and the insert happen
// under the same lock, so they can never both observe "not seen yet" — one
// goroutine's Lock blocks until the other's Unlock, and by then the key is
// already written.
type Cache struct {
	window time.Duration

	mu   sync.Mutex
	seen map[string]time.Time

	stop chan struct{}
}

// New returns a Cache that treats a key as a duplicate for window after it is
// first seen, and starts a background goroutine that evicts stale entries
// every window. Call Close to stop that goroutine.
func New(window time.Duration) *Cache {
	c := &Cache{
		window: window,
		seen:   make(map[string]time.Time),
		stop:   make(chan struct{}),
	}
	go c.evictLoop()
	return c
}

// SeenRecently reports whether key was already marked within window, and
// marks it as seen now if not.
func (c *Cache) SeenRecently(key string) bool {
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if last, ok := c.seen[key]; ok && now.Sub(last) < c.window {
		return true
	}
	c.seen[key] = now
	return false
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
			for k, t := range c.seen {
				if now.Sub(t) >= c.window {
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
