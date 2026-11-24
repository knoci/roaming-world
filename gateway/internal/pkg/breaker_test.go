package pkg

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCircuitBreaker_StateTransitions(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	cb := NewCircuitBreaker(3, 30*time.Second).WithClock(func() time.Time { return now })

	if cb.State() != StateClosed {
		t.Fatalf("expected initial state Closed, got %v", cb.State())
	}

	// 2 failures -> still Closed.
	for i := 0; i < 2; i++ {
		if err := cb.Allow(); err != nil {
			t.Fatalf("expected Allow in Closed state, got %v", err)
		}
		cb.RecordFailure()
	}
	if cb.State() != StateClosed {
		t.Fatalf("expected Closed after 2 failures, got %v", cb.State())
	}

	// Success resets consecutive failures.
	cb.RecordSuccess()
	for i := 0; i < 2; i++ {
		cb.RecordFailure()
	}
	if cb.State() != StateClosed {
		t.Fatalf("expected Closed after reset + 2 failures, got %v", cb.State())
	}

	// 3rd consecutive failure opens the circuit.
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Fatalf("expected Open after 3 consecutive failures, got %v", cb.State())
	}
	if err := cb.Allow(); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen while open, got %v", err)
	}

	// Before timeout, still Open.
	now = now.Add(29 * time.Second)
	if err := cb.Allow(); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen before cooldown, got %v", err)
	}

	// After timeout, transitions to HalfOpen and allows exactly one probe.
	now = now.Add(2 * time.Second)
	if cb.State() != StateHalfOpen {
		t.Fatalf("expected HalfOpen after cooldown, got %v", cb.State())
	}
	if err := cb.Allow(); err != nil {
		t.Fatalf("expected probe Allow in HalfOpen, got %v", err)
	}
	if err := cb.Allow(); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected concurrent request blocked while HalfOpen probe in flight, got %v", err)
	}

	// Probe failure re-opens immediately.
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Fatalf("expected Open after HalfOpen probe failure, got %v", cb.State())
	}
	if err := cb.Allow(); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen after re-open, got %v", err)
	}

	// Advance cooldown again -> HalfOpen -> CancelProbe releases slot -> probe succeeds -> Closed.
	now = now.Add(31 * time.Second)
	if err := cb.Allow(); err != nil {
		t.Fatalf("expected probe Allow after second cooldown, got %v", err)
	}
	cb.CancelProbe()
	if err := cb.Allow(); err != nil {
		t.Fatalf("expected Allow after CancelProbe, got %v", err)
	}
	cb.RecordSuccess()
	if cb.State() != StateClosed {
		t.Fatalf("expected Closed after HalfOpen probe success, got %v", cb.State())
	}
	if err := cb.Allow(); err != nil {
		t.Fatalf("expected Allow in recovered Closed state, got %v", err)
	}
}

func TestCircuitBreaker_ConcurrentSafety(t *testing.T) {
	cb := NewCircuitBreaker(5, 10*time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				if err := cb.Allow(); err == nil {
					if (id+j)%3 == 0 {
						cb.RecordFailure()
					} else {
						cb.RecordSuccess()
					}
				}
				_ = cb.State()
			}
		}(i)
	}
	wg.Wait()
}
