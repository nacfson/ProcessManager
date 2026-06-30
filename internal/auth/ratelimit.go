package auth

import (
	"sync"
	"time"
)

type rateLimitKey struct {
	identifier string
	email      string
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[rateLimitKey][]time.Time
	max      int
	window   time.Duration
	ticker   *time.Ticker
	done     chan struct{}
	stopOnce sync.Once
}

func NewLoginLimiter(max int, window time.Duration) *loginLimiter {
	l := &loginLimiter{
		attempts: make(map[rateLimitKey][]time.Time),
		max:      max,
		window:   window,
		ticker:   time.NewTicker(5 * time.Minute),
		done:     make(chan struct{}),
	}
	go l.sweep()
	return l
}

func (l *loginLimiter) allow(identifier, email string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	key := rateLimitKey{identifier: identifier, email: email}
	now := time.Now()
	cutoff := now.Add(-l.window)

	var recent []time.Time
	for _, t := range l.attempts[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	if len(recent) >= l.max {
		l.setAttempts(key, recent)
		return false
	}

	recent = append(recent, now)
	l.setAttempts(key, recent)
	return true
}

func (l *loginLimiter) setAttempts(key rateLimitKey, recent []time.Time) {
	if len(recent) == 0 {
		delete(l.attempts, key)
	} else {
		l.attempts[key] = recent
	}
}

func (l *loginLimiter) sweep() {
	for {
		select {
		case <-l.done:
			return
		case <-l.ticker.C:
			l.evictStale()
		}
	}
}

func (l *loginLimiter) evictStale() {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := time.Now().Add(-l.window)
	for key, times := range l.attempts {
		if len(times) == 0 || times[len(times)-1].Before(cutoff) {
			delete(l.attempts, key)
		}
	}
}

// Stop halts the background sweeper. It is safe to call more than once.
func (l *loginLimiter) Stop() {
	l.stopOnce.Do(func() {
		l.ticker.Stop()
		close(l.done)
	})
}
