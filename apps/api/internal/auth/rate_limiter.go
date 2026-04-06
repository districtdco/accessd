package auth

import (
	"sync"
	"time"
)

// LoginRateLimiter provides simple in-memory rate limiting for login attempts.
// It tracks failed attempts per key (username or IP) and blocks requests that
// exceed the configured threshold within a sliding window.
type LoginRateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	window   time.Duration
	max      int
}

func NewLoginRateLimiter(window time.Duration, maxAttempts int) *LoginRateLimiter {
	if window <= 0 {
		window = 5 * time.Minute
	}
	if maxAttempts <= 0 {
		maxAttempts = 10
	}
	return &LoginRateLimiter{
		attempts: make(map[string][]time.Time),
		window:   window,
		max:      maxAttempts,
	}
}

// RecordFailure records a failed login attempt for the given key.
func (rl *LoginRateLimiter) RecordFailure(key string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	rl.attempts[key] = append(rl.pruneLockedUnsafe(key, now), now)
}

// IsBlocked returns true if the key has exceeded the max allowed failures within the window.
func (rl *LoginRateLimiter) IsBlocked(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	recent := rl.pruneLockedUnsafe(key, now)
	rl.attempts[key] = recent
	return len(recent) >= rl.max
}

// Reset clears the failure history for a key (e.g., after successful login).
func (rl *LoginRateLimiter) Reset(key string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.attempts, key)
}

// Cleanup removes expired entries. Should be called periodically to prevent unbounded growth.
func (rl *LoginRateLimiter) Cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	for key := range rl.attempts {
		recent := rl.pruneLockedUnsafe(key, now)
		if len(recent) == 0 {
			delete(rl.attempts, key)
		} else {
			rl.attempts[key] = recent
		}
	}
}

func (rl *LoginRateLimiter) pruneLockedUnsafe(key string, now time.Time) []time.Time {
	entries := rl.attempts[key]
	cutoff := now.Add(-rl.window)
	start := 0
	for start < len(entries) && entries[start].Before(cutoff) {
		start++
	}
	if start == 0 {
		return entries
	}
	pruned := make([]time.Time, len(entries)-start)
	copy(pruned, entries[start:])
	return pruned
}
