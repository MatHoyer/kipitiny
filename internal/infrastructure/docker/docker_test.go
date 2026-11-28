package docker

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestTarFiles(t *testing.T) {
	r, err := tarFiles([]domain.ContainerFile{{Path: "/etc/traefik/a.yml", Content: []byte("x")}, {Path: "/etc/traefik/b.yml"}})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	// No directory entries: they would reset existing directories.
	want := []string{"etc/traefik/a.yml", "etc/traefik/b.yml"}
	if len(names) != len(want) {
		t.Fatalf("entries = %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entries = %v, want %v", names, want)
		}
	}
}

// fakeDaemon answers a container's inspection with inspect (nil: never, until
// the request is cancelled) and its logs with one line.
func fakeDaemon(t *testing.T, inspect func() *container.InspectResponse) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/json"):
			res := inspect()
			if res == nil {
				<-r.Context().Done()
				return
			}
			_ = json.NewEncoder(w).Encode(res)
		case strings.HasSuffix(r.URL.Path, "/logs"):
			line := "waiting for the disk\n" // one stderr frame of Docker's multiplexed stream
			_, _ = w.Write(append([]byte{byte(stdcopy.Stderr), 0, 0, 0, 0, 0, 0, byte(len(line))}, line...))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	cl, err := client.New(client.WithHost("tcp://"+srv.Listener.Addr().String()), client.WithAPIVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	return &Client{Client: cl}
}

// The timeout running out during a Docker call is a timeout like any other,
// with the last log lines; the caller cancelling stays the caller's error.
func TestWaitHealthyTimeout(t *testing.T) {
	ctx := context.Background()
	c := fakeDaemon(t, func() *container.InspectResponse { return nil })
	err := c.WaitHealthy(ctx, "db", 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "not healthy after 50ms") || !strings.Contains(err.Error(), "waiting for the disk") {
		t.Errorf("timeout during inspect: %v", err)
	}

	starting := &container.InspectResponse{State: &container.State{Status: container.StateRunning, Running: true,
		Health: &container.Health{Status: container.Starting}}}
	asked := make(chan struct{}, 1)
	c = fakeDaemon(t, func() *container.InspectResponse {
		select {
		case asked <- struct{}{}:
		default:
		}
		return starting
	})
	ctx, cancel := context.WithCancel(ctx)
	go func() { <-asked; cancel() }()
	if err := c.WaitHealthy(ctx, "db", time.Minute); !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "not healthy") {
		t.Errorf("caller cancelled: %v", err)
	}
}
