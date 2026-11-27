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
