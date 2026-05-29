package circuitbreaker

import (
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
