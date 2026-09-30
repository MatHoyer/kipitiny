package core

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/MatHoyer/kipitiny/internal/config"
)

func TestShutdownDrainsOperations(t *testing.T) {
	c := New(config.Config{}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	finished := make(chan struct{})
	release := make(chan struct{})
	if err := c.goBackground(func() {
		<-release
		if c.bg.Err() == nil { // not cancelled: it ran to completion
			close(finished)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.goLoop(func() { <-c.bg.Done() }); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- c.Shutdown(context.Background()) }()
	time.Sleep(50 * time.Millisecond)
	if err := c.goBackground(func() {}); !errors.Is(err, ErrShuttingDown) {
		t.Errorf("new work during drain: %v", err)
	}
	select {
	case <-done:
		t.Fatal("Shutdown returned before the operation finished")
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Error("operation was cancelled instead of drained")
	}
}

func TestShutdownCancelsAfterDeadline(t *testing.T) {
	c := New(config.Config{}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_ = c.goBackground(func() { <-c.bg.Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
}
