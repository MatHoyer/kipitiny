package progress

import (
	"strings"
	"sync"
	"sync/atomic"
)

// Steps holds the step each running operation (a backup, a restore, a
// restore test, a cleanup) is at, for the UI to show while it runs. Nothing
// is stored: a restarted manager has no running operation.
type Steps struct {
	mu sync.Mutex
	m  map[string]*Step
}

// Step is an operation's current step, what it applies to (e.g. the server
// a cleanup is on), and the bytes it has moved so far when it streams.
type Step struct {
	name   string
	detail string
	bytes  atomic.Int64
}

// Write counts p, so a Step can tee a stream.
func (s *Step) Write(p []byte) (int, error) {
	s.bytes.Add(int64(len(p)))
	return len(p), nil
}

// Set moves the operation key to step, returning it to count bytes on.
func (o *Steps) Set(key, step string) *Step { return o.SetOn(key, step, "") }

// SetOn moves the operation key to step, on detail.
func (o *Steps) SetOn(key, step, detail string) *Step {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.m == nil {
		o.m = map[string]*Step{}
	}
	s := &Step{name: step, detail: detail}
	o.m[key] = s
	return s
}

func (o *Steps) Done(key string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.m, key)
}

// Get returns the operation's step and bytes, "" when it isn't running.
func (o *Steps) Get(key string) (string, int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if s, ok := o.m[key]; ok {
		return s.name, s.bytes.Load()
	}
	return "", 0
}

// Running returns the keys of the running operations starting with prefix.
func (o *Steps) Running(prefix string) []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var keys []string
	for k := range o.m {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys
}

// DetailOf returns what the operation's step applies to.
func (o *Steps) DetailOf(key string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if s, ok := o.m[key]; ok {
		return s.detail
	}
	return ""
}
