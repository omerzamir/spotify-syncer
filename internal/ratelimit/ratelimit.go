// Package ratelimit provides a context-aware token-bucket rate limiter.
package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Limiter allows events at a steady rate with bursts of up to burst events.
// It implements the generic cell rate algorithm (GCRA), which is equivalent to
// a token bucket but tracks a single timestamp instead of a token count.
// A Limiter is safe for concurrent use.
type Limiter struct {
	interval  time.Duration // time to earn one token
	tolerance time.Duration // how far ahead of schedule a burst may run

	mu  sync.Mutex
	tat time.Time // theoretical arrival time of the next conforming event
}

// New returns a Limiter allowing perSecond events per second on average and
// bursts of up to burst events. It panics if perSecond or burst is not
// positive, since that is a programming error.
func New(perSecond float64, burst int) *Limiter {
	if perSecond <= 0 || burst <= 0 {
		panic(fmt.Sprintf("ratelimit: invalid rate %v/s with burst %d", perSecond, burst))
	}

	interval := time.Duration(float64(time.Second) / perSecond)

	return &Limiter{
		interval:  interval,
		tolerance: time.Duration(burst-1) * interval,
	}
}

// Wait blocks until the next event is allowed or ctx is done. A cancelled
// wait still consumes its slot, which keeps the limiter simple and errs on
// the side of sending fewer requests.
func (l *Limiter) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("rate limiter wait: %w", err)
	}

	delay := l.reserve(time.Now())
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("rate limiter wait: %w", context.Cause(ctx))
	}
}

// reserve books the next slot and returns how long the caller must wait for it.
func (l *Limiter) reserve(now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	tat := l.tat
	if tat.Before(now) {
		tat = now
	}

	allowAt := tat.Add(-l.tolerance)
	l.tat = tat.Add(l.interval)

	return allowAt.Sub(now)
}
