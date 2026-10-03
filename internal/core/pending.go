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

func (p *pending[T]) put(key string, v T, exp time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]pendingEntry[T]{}
	}
	now := time.Now()
	for k, e := range p.m {
		if now.After(e.exp) {
			delete(p.m, k)
		}
	}
	p.m[key] = pendingEntry[T]{v, exp}
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
