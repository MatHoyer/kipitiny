package core

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// TestRuntimeDocker runs a container with an app's runtime settings on the
// local Docker daemon and checks them from inside.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run RuntimeDocker
func TestRuntimeDocker(t *testing.T) {
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	ctx := context.Background()
	dk, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := dk.EnsureImage(ctx, "busybox", ""); err != nil {
		t.Fatal(err)
	}
	svc := store.Service{ID: "01RUNTIMETEST", Name: "rt", Kind: store.ServiceKindApp, Image: "busybox", Replicas: 1,
		Runtime: store.Runtime{
			Entrypoint: []string{"sh", "-c"},
			Command:    []string{"id -u; pwd; ulimit -n; grep db.local /etc/hosts; touch /x 2>/dev/null || echo ro; touch /cache/x && echo tmpfs; df -m /dev/shm | tail -1"},
			User:       "1234", WorkingDir: "/tmp", ReadOnly: true, Init: true, ShmSizeMB: 128,
			Tmpfs:      map[string]string{"/cache": "size=8m,mode=1777"},
			Ulimits:    map[string]store.Ulimit{"nofile": {Soft: 1111, Hard: 2222}},
			ExtraHosts: []string{"db.local:10.1.2.3"},
			Labels:     map[string]string{"com.example.test": "1"},
		}}
	if err := validateService(svc); err != nil {
		t.Fatal(err)
	}
	spec := containerSpec(store.Project{ID: "P", Name: "kipitiny-test"}, svc, envSources{}, "D1", 1, route{})
	// Not kipitiny.managed: a manager running on this daemon would remove
	// it as an orphan. No project network either.
	delete(spec.Config.Labels, docker.LabelManaged)
	spec.NetworkingConfig = &network.NetworkingConfig{}
	spec.HostConfig.RestartPolicy = container.RestartPolicy{}
	id, err := dk.Run(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dk.RemoveContainer(context.Background(), id, 0) })
	wait := dk.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case <-wait.Result:
	case err := <-wait.Error:
		t.Fatal(err)
	}
	var out bytes.Buffer
	copyContainerLogs(ctx, dk, []string{id}, &out)
	got := out.String()
	for _, want := range []string{"1234\n/tmp\n1111\n", "10.1.2.3", "ro\ntmpfs\n", " 128 "} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	ins, err := dk.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ins.Container.Config.Labels["com.example.test"] != "1" {
		t.Errorf("labels = %v", ins.Container.Config.Labels)
	}
}
