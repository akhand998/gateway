package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

type Breaker struct {
	mu              sync.Mutex
	state           State
	failureCount    int
	lastFailureTime time.Time
	openUntil       time.Time
	maxFailures     int
	openDuration    time.Duration
}

func New(maxFailures int, openDuration time.Duration) *Breaker {
	if maxFailures <= 0 {
		maxFailures = 3
	}
	if openDuration <= 0 {
		openDuration = 5 * time.Second
	}

	return &Breaker{
		state:        Closed,
		maxFailures:  maxFailures,
		openDuration: openDuration,
	}
}

func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == Open {
		if time.Now().After(b.openUntil) {
			b.state = HalfOpen
			return true
		}
		return false
	}

	return true
}

func (b *Breaker) OnSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failureCount = 0
	b.state = Closed
}

func (b *Breaker) OnFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failureCount++
	b.lastFailureTime = time.Now()

	if b.failureCount >= b.maxFailures {
		b.state = Open
		b.openUntil = time.Now().Add(b.openDuration)
	}
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.state
}

func (b *Breaker) Execute(fn func() error) error {
	if !b.Allow() {
		return errors.New("circuit breaker open")
	}

	if err := fn(); err != nil {
		b.OnFailure()
		return err
	}

	b.OnSuccess()
	return nil
}
