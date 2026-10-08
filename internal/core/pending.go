package core

import (
	"sync"
	"time"
)

// pending holds short-lived sign-in state (second-factor tickets, WebAuthn
// ceremonies) in memory: a restart only means starting over.
type pending[T any] struct {
	mu sync.Mutex
	m  map[string]pendingEntry[T]
}

type pendingEntry[T any] struct {
	v   T
	exp time.Time
}

// maxPending bounds each pending map: some entries are created by
// unauthenticated requests (passkey sign-in), which must not grow memory
// without limit.
const maxPending = 1024

func (p *pending[T]) put(key string, v T, exp time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]pendingEntry[T]{}
	}
	if len(p.m) >= maxPending {
		p.evict()
	}
	p.m[key] = pendingEntry[T]{v, exp}
}

// evict drops expired entries, then the one closest to expiry if the map is
// still full. Callers hold mu; it only runs when the map is full, so puts
// stay O(1) in the common case.
func (p *pending[T]) evict() {
	now := time.Now()
	var oldest string
	var oldestExp time.Time
	for k, e := range p.m {
		if now.After(e.exp) {
			delete(p.m, k)
		} else if oldest == "" || e.exp.Before(oldestExp) {
			oldest, oldestExp = k, e.exp
		}
	}
	if len(p.m) >= maxPending {
		delete(p.m, oldest)
	}
}

// peek returns the entry without removing it, unless it expired.
func (p *pending[T]) peek(key string) (T, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.m[key]
	if !ok || time.Now().After(e.exp) {
		var zero T
		return zero, false
	}
	return e.v, true
}

// take removes and returns the entry, unless it expired.
func (p *pending[T]) take(key string) (T, time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.m[key]
	delete(p.m, key)
	if !ok || time.Now().After(e.exp) {
		var zero T
		return zero, time.Time{}, false
	}
	return e.v, e.exp, true
}
