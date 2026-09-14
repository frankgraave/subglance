package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestConcurrentSetupCreatesExactlyOneAdmin reproduces the first-run race.
//
// GET /api/v1/setup is public and announces `setup_required: true`, so a fresh
// instance can be polled by anyone who knows the address. If the count and the
// insert are not one atomic step, whoever posts alongside the owner also gets
// an administrator account — and the owner never learns there is a second one.
func TestConcurrentSetupCreatesExactlyOneAdmin(t *testing.T) {
	db := openEmptyDB(t)
	srv := New(testLogger(), db)
	h := srv.Handler()

	const attempts = 8

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		codes = map[int]int{}
	)
	start := make(chan struct{})
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := fmt.Sprintf(`{"email":"racer%d@example.com","password":"correct-horse-battery-staple"}`, i)
			<-start
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/setup", body))
			mu.Lock()
			codes[rec.Code]++
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	if codes[http.StatusCreated] != 1 {
		t.Errorf("%d of %d concurrent setup requests were accepted, want exactly 1: %v",
			codes[http.StatusCreated], attempts, codes)
	}
	if codes[http.StatusConflict] != attempts-1 {
		t.Errorf("got %d conflicts, want %d: %v", codes[http.StatusConflict], attempts-1, codes)
	}

	n, err := db.CountUsers(t.Context())
	if err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 1 {
		t.Errorf("the instance ended up with %d accounts, want 1", n)
	}
}
