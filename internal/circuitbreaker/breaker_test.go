package circuitbreaker

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestBreakerTransitions(t *testing.T) {
	breaker := New(2, 50*time.Millisecond)

	if !breaker.Allow() {
		t.Fatalf("expected breaker to allow in closed state")
	}

	breaker.OnFailure()
	breaker.OnFailure()
	if breaker.State() != Open {
		t.Fatalf("expected breaker to be open after failures")
	}

	if breaker.Allow() {
		t.Fatalf("expected breaker to block while open")
	}

	time.Sleep(60 * time.Millisecond)
	if !breaker.Allow() {
		t.Fatalf("expected breaker to allow in half-open")
	}

	breaker.OnSuccess()
	if breaker.State() != Closed {
		t.Fatalf("expected breaker to close after success")
	}
}

func TestBreakerHalfOpenToReopen(t *testing.T) {
	breaker := New(2, 50*time.Millisecond)

	// Drive to Open.
	breaker.OnFailure()
	breaker.OnFailure()
	if breaker.State() != Open {
		t.Fatalf("expected Open, got %v", breaker.State())
	}

	// Wait for timeout to transition to HalfOpen.
	time.Sleep(60 * time.Millisecond)
	if !breaker.Allow() {
		t.Fatalf("expected Allow in half-open")
	}
	if breaker.State() != HalfOpen {
		t.Fatalf("expected HalfOpen, got %v", breaker.State())
	}

	// Failure in HalfOpen should reopen.
	breaker.OnFailure()
	breaker.OnFailure()
	if breaker.State() != Open {
		t.Fatalf("expected re-open after failure in half-open, got %v", breaker.State())
	}
}

func TestBreakerSuccessResetCount(t *testing.T) {
	breaker := New(3, time.Second)

	// Two failures (below threshold).
	breaker.OnFailure()
	breaker.OnFailure()
	if breaker.State() != Closed {
		t.Fatalf("expected Closed after %d failures (threshold=3)", 2)
	}

	// Success resets the counter.
	breaker.OnSuccess()

	// Two more failures should not open (counter was reset).
	breaker.OnFailure()
	breaker.OnFailure()
	if breaker.State() != Closed {
		t.Fatalf("expected Closed: counter should have been reset by OnSuccess")
	}
}

func TestBreakerExecute(t *testing.T) {
	breaker := New(2, 50*time.Millisecond)

	// Successful execution.
	err := breaker.Execute(func() error { return nil })
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if breaker.State() != Closed {
		t.Fatalf("expected Closed after success")
	}

	// Failed executions drive to Open.
	testErr := errors.New("upstream error")
	_ = breaker.Execute(func() error { return testErr })
	_ = breaker.Execute(func() error { return testErr })
	if breaker.State() != Open {
		t.Fatalf("expected Open after 2 failed executions")
	}

	// Execute should fail-fast when open.
	err = breaker.Execute(func() error { return nil })
	if err == nil {
		t.Fatal("expected error when breaker is open")
	}
}

func TestBreakerDefaultValues(t *testing.T) {
	// Zero/negative values should get defaults.
	breaker := New(0, -1)
	if breaker.maxFailures != 3 {
		t.Fatalf("expected default maxFailures=3, got %d", breaker.maxFailures)
	}
	if breaker.openDuration != 5*time.Second {
		t.Fatalf("expected default openDuration=5s, got %v", breaker.openDuration)
	}
}

func TestBreakerConcurrency(t *testing.T) {
	breaker := New(100, 50*time.Millisecond)
	var wg sync.WaitGroup

	// Concurrent failures.
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			breaker.OnFailure()
		}()
	}
	wg.Wait()

	if breaker.State() != Open {
		t.Fatalf("expected Open after 200 concurrent failures (threshold=100)")
	}

	// Concurrent Allow checks while open.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = breaker.Allow()
		}()
	}
	wg.Wait()
}
