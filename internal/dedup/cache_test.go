package dedup

import (
	"sync"
	"testing"
	"time"
)

func TestSeenRecentlyFirstCallIsNotADuplicate(t *testing.T) {
	c := New(time.Minute)
	defer c.Close()

	if c.SeenRecently("a") {
		t.Fatal("first call reported a duplicate")
	}
}

func TestSeenRecentlyWithinWindowIsADuplicate(t *testing.T) {
	c := New(time.Minute)
	defer c.Close()

	c.SeenRecently("a")
	if !c.SeenRecently("a") {
		t.Fatal("second call within the window was not reported as a duplicate")
	}
}

func TestSeenRecentlyDifferentKeysAreIndependent(t *testing.T) {
	c := New(time.Minute)
	defer c.Close()

	c.SeenRecently("a")
	if c.SeenRecently("b") {
		t.Fatal("a different key was reported as a duplicate")
	}
}

func TestSeenRecentlyAfterWindowIsNotADuplicate(t *testing.T) {
	c := New(20 * time.Millisecond)
	defer c.Close()

	c.SeenRecently("a")
	time.Sleep(40 * time.Millisecond)
	if c.SeenRecently("a") {
		t.Fatal("call after the window elapsed was reported as a duplicate")
	}
}

// TestSeenRecentlyConcurrentCallsAgreeOnExactlyOneWinner reproduces the
// scenario this package exists for: two goroutines (one per net/http
// connection) call SeenRecently with the same key at nearly the same
// instant. Run with -race; a broken check-then-set implementation (read the
// map, then write it, as two separate steps) would let both calls observe
// "not seen" and race the write, which -race flags even if the counts below
// still happened to come out right.
func TestSeenRecentlyConcurrentCallsAgreeOnExactlyOneWinner(t *testing.T) {
	c := New(time.Minute)
	defer c.Close()

	const goroutines = 50
	var wg sync.WaitGroup
	var firstCount int32
	var mu sync.Mutex

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			if !c.SeenRecently("race-key") {
				mu.Lock()
				firstCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if firstCount != 1 {
		t.Fatalf("goroutines that saw 'not a duplicate' = %d, want exactly 1", firstCount)
	}
}
