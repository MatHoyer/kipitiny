package core

import (
	"errors"
	"testing"
	"testing/synctest"

	"github.com/kipit-labs/kipitiny/internal/config"
)

// An operation waits out the reconciler's short pass on a service (scaling
// right after a save) instead of failing busy; it still fails on another
// operation, and the reconciler never waits.
func TestLockServiceWaitsForReconciler(t *testing.T) {
	c := newTestCore(t, config.Config{DataDir: t.TempDir()})
	synctest.Test(t, func(t *testing.T) {
		unlock, err := c.lockServiceBackground("s1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.lockServiceBackground("s1"); !errors.Is(err, ErrBusy) {
			t.Fatalf("second reconciler pass: %v", err)
		}
		var opErr error
		done := make(chan struct{})
		go func() {
			defer close(done)
			var unlock func()
			if unlock, opErr = c.lockService("s1"); opErr == nil {
				unlock()
			}
		}()
		synctest.Wait() // the operation returned, or waits for the reconciler
		select {
		case <-done:
			t.Fatalf("operation did not wait for the reconciler: %v", opErr)
		default:
		}
		unlock()
		<-done
		if opErr != nil {
			t.Fatalf("operation after the reconciler: %v", opErr)
		}

		unlock, err = c.lockService("s1")
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		if _, err := c.lockService("s1"); !errors.Is(err, ErrBusy) {
			t.Fatalf("second operation: %v", err)
		}
		if _, err := c.lockServiceBackground("s1"); !errors.Is(err, ErrBusy) {
			t.Fatalf("reconciler during an operation: %v", err)
		}
	})
}
