package proxy

import (
	"sync"
	"time"
)

type RateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	clients map[string]rateWindow
}

type rateWindow struct {
	Started time.Time
	Count   int
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	if limit <= 0 {
		limit = 120
	}
	return &RateLimiter{
		limit:   limit,
		window:  window,
		clients: map[string]rateWindow{},
	}
}

func (r *RateLimiter) Allow(key string) bool {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()

	state := r.clients[key]
	if state.Started.IsZero() || now.Sub(state.Started) >= r.window {
		r.clients[key] = rateWindow{Started: now, Count: 1}
		return true
	}
	if state.Count >= r.limit {
		return false
	}
	state.Count++
	r.clients[key] = state
	return true
}
