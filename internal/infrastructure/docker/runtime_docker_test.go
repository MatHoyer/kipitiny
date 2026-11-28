package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

// The port's calls on a real daemon: a volume, a container on a network
// with files, its listing and inspection, exec, logs, stats, a wait and the
// cleanup. Nothing is kipitiny.managed, so a manager on the daemon leaves
// it alone.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/infrastructure/docker/ -run RuntimeDocker
func TestRuntimeDocker(t *testing.T) {
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	ctx := context.Background()
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	var rt domain.ContainerRuntime = c
	if err := rt.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	name := "kipitiny-test-rt-" + strings.ToLower(ids.New())
	labels := map[string]string{"kipitiny.test": name}

	if err := rt.CreateVolume(ctx, name, labels); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.RemoveVolume(context.Background(), name) })
	vols, err := rt.ListVolumes(ctx, domain.LabelFilter(labels))
	if err != nil || len(vols) != 1 || vols[0].Name != name {
		t.Fatalf("volumes = %+v, %v", vols, err)
	}
	if err := rt.EnsureNetwork(ctx, name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.RemoveNetwork(context.Background(), name) })
	if err := rt.EnsureImage(ctx, "busybox", "", ""); err != nil {
		t.Fatal(err)
	}
	img, err := rt.InspectImage(ctx, "busybox", "")
	if err != nil || img.ID == "" {
		t.Fatalf("image = %+v, %v", img, err)
	}

	id, err := rt.Run(ctx, domain.ContainerSpec{
		Name: name, Image: "busybox", Cmd: []string{"sh", "-c", "cat /etc/hello; sleep 300"}, Labels: labels, Log: true,
		Networks: map[string][]string{name: {"web"}},
		Mounts:   []domain.Mount{{Type: domain.MountVolume, Source: name, Target: "/data"}},
	}, domain.ContainerFile{Path: "/etc/hello", Content: []byte("hello\n")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.RemoveContainer(context.Background(), id, 0) })

	cts, err := rt.ListContainers(ctx, labels)
	if err != nil || len(cts) != 1 || cts[0].ID != id || cts[0].Names[0] != "/"+name || cts[0].State != domain.StateRunning || !cts[0].Networks[name].IP.IsValid() {
		t.Fatalf("containers = %+v, %v", cts, err)
	}
	info, err := rt.InspectContainer(ctx, id)
	if err != nil || info.Name != "/"+name || info.Image != "busybox" || info.State == nil || !info.State.Running {
		t.Fatalf("inspect = %+v, %v", info, err)
	}

	var out bytes.Buffer
	if err := rt.Exec(ctx, id, domain.ExecOptions{Cmd: []string{"sh", "-c", "echo ok > /data/f && cat /data/f"}, Stdout: &out}); err != nil || out.String() != "ok\n" {
		t.Fatalf("exec = %q, %v", out.String(), err)
	}
	var exit *domain.ExecError
	if err := rt.Exec(ctx, id, domain.ExecOptions{Cmd: []string{"false"}}); !errors.As(err, &exit) || exit.ExitCode != 1 {
		t.Fatalf("failing exec: %v", err)
	}
	if st, err := rt.Stats(ctx, id); err != nil || st.Read.IsZero() {
		t.Errorf("stats = %+v, %v", st, err)
	}

	stop := 0
	if err := rt.StopContainer(ctx, id, &stop); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if code, err := rt.WaitContainer(wctx, id); err != nil || code != 137 {
		t.Errorf("wait = %d, %v", code, err)
	}
	if logs := readLogs(t, rt, id); logs != "hello\n" { // exited: the cat ran
		t.Errorf("logs = %q", logs)
	}
	if err := rt.RemoveContainer(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.InspectContainer(ctx, id); !cerrdefs.IsNotFound(err) {
		t.Errorf("removed container: %v", err)
	}
}

func readLogs(t *testing.T, rt domain.ContainerRuntime, id string) string {
	t.Helper()
	rc, err := rt.Logs(context.Background(), id, domain.LogOptions{Tail: "10"})
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// WaitHealthy on containers that exit on start, like a MongoDB whose first
// start lost the race for its port (exit 48): the error carries the last log
// lines, and one restart by the restart policy is waited through.
func TestWaitHealthyExitDocker(t *testing.T) {
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	ctx := context.Background()
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureImage(ctx, "busybox", "", ""); err != nil {
		t.Fatal(err)
	}
	start := func(t *testing.T, cmd string, restart domain.RestartPolicy) string {
		name := "kipitiny-test-exit-" + strings.ToLower(ids.New())
		id, err := c.Run(ctx, domain.ContainerSpec{
			Name: name, Image: "busybox", Labels: map[string]string{"kipitiny.test": name}, Log: true,
			Cmd: []string{"sh", "-c", cmd}, Restart: restart,
			// Starting until /ready exists: running alone would count as healthy.
			Healthcheck: &domain.HealthConfig{Test: []string{"CMD", "test", "-f", "/ready"}, Interval: time.Hour, StartPeriod: time.Minute, StartInterval: time.Second},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.RemoveContainer(context.Background(), id, 0) })
		return id
	}
	const fail = "echo starting; echo 'bind: address already in use' >&2; exit 48"
	wantLogs := func(t *testing.T, err error, msg string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), msg) || !strings.Contains(err.Error(), "starting") ||
			!strings.Contains(err.Error(), "bind: address already in use") {
			t.Fatalf("err = %v", err)
		}
	}

	t.Run("exited", func(t *testing.T) {
		wantLogs(t, c.WaitHealthy(ctx, start(t, fail, domain.RestartPolicy{}), time.Minute), "container exited (code 48)")
	})
	t.Run("restarted once", func(t *testing.T) {
		// Exits the first time, serves the second: the writable layer survives a restart.
		id := start(t, "[ -f /started ] && touch /ready && exec sleep 300; touch /started; exit 48",
			domain.RestartPolicy{Name: domain.RestartOnFailure, MaxRetries: 3})
		if err := c.WaitHealthy(ctx, id, time.Minute); err != nil {
			t.Fatal(err)
		}
		if info, err := c.InspectContainer(ctx, id); err != nil || info.RestartCount != 1 {
			t.Fatalf("restarts = %d, %v", info.RestartCount, err)
		}
	})
	t.Run("crash loop", func(t *testing.T) {
		id := start(t, fail, domain.RestartPolicy{Name: domain.RestartUnlessStopped})
		wantLogs(t, c.WaitHealthy(ctx, id, time.Minute), "container keeps restarting")
	})
}

// A container whose healthcheck fails says why: the probe's last output and
// the program's last log lines.
func TestWaitHealthyUnhealthyDocker(t *testing.T) {
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	ctx := context.Background()
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureImage(ctx, "busybox", "", ""); err != nil {
		t.Fatal(err)
	}
	name := "kipitiny-test-unhealthy-" + strings.ToLower(ids.New())
	id, err := c.Run(ctx, domain.ContainerSpec{
		Name: name, Image: "busybox", Labels: map[string]string{"kipitiny.test": name}, Log: true,
		Cmd:         []string{"sh", "-c", "echo 'table users is corrupt' >&2; exec sleep 300"},
		Healthcheck: &domain.HealthConfig{Test: []string{"CMD-SHELL", "echo 'probe: connection refused'; exit 1"}, Interval: 100 * time.Millisecond, Retries: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.RemoveContainer(context.Background(), id, 0) })
	err = c.WaitHealthy(ctx, id, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "container is unhealthy") ||
		!strings.Contains(err.Error(), "probe: connection refused") || !strings.Contains(err.Error(), "table users is corrupt") {
		t.Fatalf("err = %v", err)
	}
}
