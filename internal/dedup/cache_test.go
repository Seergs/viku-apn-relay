package dedup

import (
	"sync"
	"testing"
	"time"
)

func newTestCache(t *testing.T, window time.Duration) *Cache {
	t.Helper()
	c := New(window)
	t.Cleanup(c.Close)
	return c
}

func TestClaimFirstCallProceeds(t *testing.T) {
	c := newTestCache(t, time.Minute)

	proceed, _ := c.Claim("a")
	if !proceed {
		t.Fatal("first claim did not proceed")
	}
}

func TestClaimWithinWindowAfterSuccessIsADuplicate(t *testing.T) {
	c := newTestCache(t, time.Minute)

	proceed, release := c.Claim("a")
	if !proceed {
		t.Fatal("first claim did not proceed")
	}
	release(true)

	if proceed, _ := c.Claim("a"); proceed {
		t.Fatal("second claim within the window proceeded after a successful send")
	}
}

func TestClaimAfterFailureAllowsAnotherAttempt(t *testing.T) {
	c := newTestCache(t, time.Minute)

	proceed, release := c.Claim("a")
	if !proceed {
		t.Fatal("first claim did not proceed")
	}
	release(false)

	if proceed, _ := c.Claim("a"); !proceed {
		t.Fatal("claim after a failed send did not proceed, so a retry would be silently dropped")
	}
}

func TestClaimWhileInFlightIsADuplicate(t *testing.T) {
	c := newTestCache(t, time.Minute)

	proceed, _ := c.Claim("a")
	if !proceed {
		t.Fatal("first claim did not proceed")
	}

	if proceed, _ := c.Claim("a"); proceed {
		t.Fatal("a second claim while the first is still in flight proceeded")
	}
}

func TestClaimDifferentKeysAreIndependent(t *testing.T) {
	c := newTestCache(t, time.Minute)

	c.Claim("a")
	if proceed, _ := c.Claim("b"); !proceed {
		t.Fatal("a different key did not proceed")
	}
}

func TestClaimAfterWindowElapsedAllowsAnotherAttempt(t *testing.T) {
	c := newTestCache(t, 20*time.Millisecond)

	_, release := c.Claim("a")
	release(true)

	time.Sleep(40 * time.Millisecond)
	if proceed, _ := c.Claim("a"); !proceed {
		t.Fatal("claim after the window elapsed did not proceed")
	}
}

// TestClaimConcurrentCallsAgreeOnExactlyOneWinner reproduces the scenario
// this package exists for: two goroutines (one per net/http connection)
// call Claim with the same key at nearly the same instant. Run with -race;
// a broken check-then-set implementation (read the map, then write it, as
// two separate steps) would let both calls proceed and race the write,
// which -race flags even if the counts below still happened to come out
// right.
func TestClaimConcurrentCallsAgreeOnExactlyOneWinner(t *testing.T) {
	c := newTestCache(t, time.Minute)

	const goroutines = 50
	var wg sync.WaitGroup
	var winners int32
	var mu sync.Mutex

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			if proceed, release := c.Claim("race-key"); proceed {
				mu.Lock()
				winners++
				mu.Unlock()
				release(true)
			}
		}()
	}
	wg.Wait()

	if winners != 1 {
		t.Fatalf("goroutines that proceeded = %d, want exactly 1", winners)
	}
}
