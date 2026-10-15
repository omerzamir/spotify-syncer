package ratelimit_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"spotify-syncer/internal/ratelimit"
)

func TestLimiterBurstThenSteadyRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := ratelimit.New(8, 4) // one token every 125ms, burst of 4
		start := time.Now()

		var elapsed []time.Duration
		for range 6 {
			if err := limiter.Wait(t.Context()); err != nil {
				t.Fatalf("Wait: %v", err)
			}
			elapsed = append(elapsed, time.Since(start))
		}

		want := []time.Duration{0, 0, 0, 0, 125 * time.Millisecond, 250 * time.Millisecond}
		for i := range want {
			if elapsed[i] != want[i] {
				t.Errorf("event %d at %v, want %v", i, elapsed[i], want[i])
			}
		}
	})
}

func TestLimiterRefillsWhileIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := ratelimit.New(10, 2)

		for range 2 {
			if err := limiter.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
		}

		time.Sleep(time.Second) // bucket refills to the burst size, not beyond

		start := time.Now()
		for range 3 {
			if err := limiter.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
		}

		if got, want := time.Since(start), 100*time.Millisecond; got != want {
			t.Errorf("3 events after idle took %v, want %v", got, want)
		}
	})
}

func TestLimiterWaitHonoursCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := ratelimit.New(1, 1)
		if err := limiter.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()

		if err := limiter.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Wait() error = %v, want DeadlineExceeded", err)
		}
	})
}

func TestNewPanicsOnInvalidRate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New(0, 1) did not panic")
		}
	}()
	ratelimit.New(0, 1)
}
