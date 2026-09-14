package auth

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestArgon2BoundsConcurrency is the memory ceiling, stated as a test.
//
// Every argon2id verification allocates 64 MiB by design, and that design is
// correct: it is what makes a stolen hash expensive to attack. What was
// missing is a bound on how many of them may run at once. Unbounded, the
// public login endpoint multiplies that 64 MiB by however many requests an
// attacker cares to open, and a 2 GB self-hosted box is out of memory long
// before it is out of CPU.
func TestArgon2BoundsConcurrency(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	var peak atomic.Int64
	var wg sync.WaitGroup

	start := make(chan struct{})
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := VerifyPassword("correct-horse-battery-staple", hash); err != nil {
				t.Errorf("verify: %v", err)
			}
			// Hashing is on the same budget: /api/v1/setup is public and
			// hashes before it knows whether an account exists.
			if _, err := HashPassword("correct-horse-battery-staple"); err != nil {
				t.Errorf("hash: %v", err)
			}
			// Record the highest count any goroutine saw inside the hash.
			for {
				now := inFlightHashes.Load()
				old := peak.Load()
				if now <= old || peak.CompareAndSwap(old, now) {
					break
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := peak.Load(); got > maxConcurrentHashes {
		t.Errorf("peak concurrent verifications = %d, want at most %d — "+
			"each one holds %d MiB", got, maxConcurrentHashes, argonMemory/1024)
	}
}
