package auth

import (
	"errors"
	"sync"
	"time"
)

// ErrRateLimited means too many failed sign-ins; the caller must wait.
var ErrRateLimited = errors.New("too many failed sign-in attempts; try again later")

// Default sign-in failure limits, per fixed window.
const (
	DefaultFailureWindow      = 15 * time.Minute
	DefaultMaxFailuresPerUser = 10
	DefaultMaxFailuresPerIP   = 50
)

// limiter counts failures per key in fixed windows. It is in memory: limits
// reset on restart, which is acceptable for a single controller process.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	counts map[string]*window
}

type window struct {
	start time.Time
	fails int
}

const limiterSweepSize = 10_000

func newLimiter(max int, w time.Duration, now func() time.Time) *limiter {
	return &limiter{max: max, window: w, now: now, counts: map[string]*window{}}
}

// blocked reports whether key has used up its failures in the current window.
func (l *limiter) blocked(key string) bool {
	if l == nil || l.max <= 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.current(key)
	return w != nil && w.fails >= l.max
}

func (l *limiter) fail(key string) {
	if l == nil || l.max <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.current(key)
	if w == nil {
		if len(l.counts) >= limiterSweepSize {
			l.sweep()
		}
		w = &window{start: l.now()}
		l.counts[key] = w
	}
	w.fails++
}

func (l *limiter) reset(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.counts, key)
}

// current returns key's window if it has not expired. l.mu must be held.
func (l *limiter) current(key string) *window {
	w := l.counts[key]
	if w != nil && l.now().Sub(w.start) >= l.window {
		delete(l.counts, key)
		return nil
	}
	return w
}

// sweep drops expired windows. l.mu must be held.
func (l *limiter) sweep() {
	now := l.now()
	for k, w := range l.counts {
		if now.Sub(w.start) >= l.window {
			delete(l.counts, k)
		}
	}
}
