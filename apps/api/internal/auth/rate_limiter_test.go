package auth

import (
	"testing"
	"time"
)

func TestRateLimiter_AllowsUnderThreshold(t *testing.T) {
	rl := NewLoginRateLimiter(5*time.Minute, 3)

	rl.RecordFailure("user:alice")
	rl.RecordFailure("user:alice")

	if rl.IsBlocked("user:alice") {
		t.Fatal("expected user:alice to not be blocked after 2 failures (threshold=3)")
	}
}

func TestRateLimiter_BlocksAtThreshold(t *testing.T) {
	rl := NewLoginRateLimiter(5*time.Minute, 3)

	rl.RecordFailure("user:alice")
	rl.RecordFailure("user:alice")
	rl.RecordFailure("user:alice")

	if !rl.IsBlocked("user:alice") {
		t.Fatal("expected user:alice to be blocked after 3 failures (threshold=3)")
	}
}

func TestRateLimiter_ResetClearsFailures(t *testing.T) {
	rl := NewLoginRateLimiter(5*time.Minute, 2)

	rl.RecordFailure("user:bob")
	rl.RecordFailure("user:bob")

	if !rl.IsBlocked("user:bob") {
		t.Fatal("expected blocked after 2 failures")
	}

	rl.Reset("user:bob")

	if rl.IsBlocked("user:bob") {
		t.Fatal("expected unblocked after reset")
	}
}

func TestRateLimiter_IndependentKeys(t *testing.T) {
	rl := NewLoginRateLimiter(5*time.Minute, 2)

	rl.RecordFailure("user:alice")
	rl.RecordFailure("user:alice")
	rl.RecordFailure("user:bob")

	if !rl.IsBlocked("user:alice") {
		t.Fatal("expected alice blocked")
	}
	if rl.IsBlocked("user:bob") {
		t.Fatal("expected bob not blocked (only 1 failure)")
	}
}

func TestRateLimiter_CleanupRemovesExpired(t *testing.T) {
	rl := NewLoginRateLimiter(1*time.Millisecond, 2)

	rl.RecordFailure("user:expired")
	rl.RecordFailure("user:expired")

	time.Sleep(5 * time.Millisecond)
	rl.Cleanup()

	if rl.IsBlocked("user:expired") {
		t.Fatal("expected not blocked after window expired and cleanup ran")
	}
}
