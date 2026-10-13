package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/progress"
)

func TestProgress(t *testing.T) {
	a := &API{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	run := func(op func(context.Context) (any, error)) (string, int) {
		rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
		a.progress(rec, httptest.NewRequest("PUT", "/api/secret-connections/x?progress", nil), op)
		return rec.ResponseWriter.(*httptest.ResponseRecorder).Body.String(), rec.status
	}

	body, status := run(func(ctx context.Context) (any, error) {
		progress.Report(ctx, "login")
		return map[string]bool{"connected": true}, nil
	})
	if want := "{\"step\":\"login\"}\n{\"result\":{\"connected\":true}}\n"; body != want || status != http.StatusOK {
		t.Errorf("ok: %d %q, want %q", status, body, want)
	}

	body, status = run(func(context.Context) (any, error) {
		return nil, fmt.Errorf("%w: Invalid token", core.ErrInvalid)
	})
	if want := "{\"error\":\"Invalid token\"}\n"; body != want || status != http.StatusBadRequest {
		t.Errorf("error: %d %q, want %q", status, body, want)
	}
}
