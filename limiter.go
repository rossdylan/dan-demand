package main

import (
	"context"
	"time"
)

// Limiter is a super simple throttle structure based on the one in the golang wiki. Its wrapped
// up to make it a little more ergonomic
type Limiter struct {
	limit    time.Duration
	throttle chan struct{}
	cancel   context.CancelFunc
}

func (l *Limiter) Stop() {
	l.cancel()
}

func (l *Limiter) driver(ctx context.Context) {
	ticker := time.NewTicker(l.limit)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.throttle <- struct{}{}
		case <-ctx.Done():
			return
		}
	}
}

func NewLimiter(limit time.Duration) *Limiter {
	ctx, cancel := context.WithCancel(context.Background())
	lim := &Limiter{
		limit:    limit,
		throttle: make(chan struct{}),
		cancel:   cancel,
	}
	go lim.driver(ctx)
	return lim
}

func (l *Limiter) Acquire(ctx context.Context) bool {
	select {
	case <-l.throttle:
		return true
	case <-ctx.Done():
		return false
	}
}

func (l *Limiter) TryAcquire() bool {
	select {
	case <-l.throttle:
		return true
	default:
		return false
	}
}
