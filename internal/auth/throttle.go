package auth

import (
	"sync"
	"time"
)

const (
	throttleBaseLockout = time.Minute
	throttleMaxLockout  = time.Hour
	throttleMaxEntries  = 100000
	throttleForgetAfter = 24 * time.Hour
)

type RetryError struct {
	Err        error
	RetryAfter time.Duration
}

func (e *RetryError) Error() string { return e.Err.Error() }

func (e *RetryError) Unwrap() error { return e.Err }

type attempt struct {
	failures    int
	lockedUntil time.Time
	lastFailure time.Time
}

type LoginThrottle struct {
	mu        sync.Mutex
	threshold int
	attempts  map[string]*attempt
	now       func() time.Time
}

func NewLoginThrottle(threshold int) *LoginThrottle {
	if threshold <= 0 {
		return nil
	}
	return &LoginThrottle{threshold: threshold, attempts: make(map[string]*attempt), now: time.Now}
}

func (t *LoginThrottle) Blocked(key string) time.Duration {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.attempts[key]
	if !ok {
		return 0
	}
	if remaining := entry.lockedUntil.Sub(t.now()); remaining > 0 {
		return remaining
	}
	return 0
}

func (t *LoginThrottle) Fail(key string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if len(t.attempts) >= throttleMaxEntries {
		t.prune(now)
	}
	entry, ok := t.attempts[key]
	if !ok {
		if len(t.attempts) >= throttleMaxEntries {
			return
		}
		entry = &attempt{}
		t.attempts[key] = entry
	}
	entry.failures++
	entry.lastFailure = now
	if over := entry.failures - t.threshold; over >= 0 {
		lockout := min(throttleBaseLockout<<min(over, 6), throttleMaxLockout)
		entry.lockedUntil = now.Add(lockout)
	}
}

func (t *LoginThrottle) Succeed(key string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.attempts, key)
}

func (t *LoginThrottle) prune(now time.Time) {
	for key, entry := range t.attempts {
		if now.Sub(entry.lastFailure) > throttleForgetAfter && now.After(entry.lockedUntil) {
			delete(t.attempts, key)
		}
	}
}
