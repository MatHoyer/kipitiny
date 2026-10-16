package core

import (
	"bytes"
	"context"
	"os"
	"strconv"
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

// TestFilesDocker writes an app's files into its container on the local
// Docker daemon.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run FilesDocker
func TestFilesDocker(t *testing.T) {
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
	svc := store.Service{ID: "01FILESTEST", Name: "files", Kind: store.ServiceKindApp, Image: "busybox", Replicas: 1,
		Runtime: store.Runtime{Command: []string{"sh", "-c", "cat /etc/kip/a.conf /run/secrets/token; stat -c %a /etc/kip/a.conf /bin/run.sh /tmp; stat -c %u /home/app; cat /home/app/c/x && echo made"}, User: "0"},
		Files: []store.ServiceFile{{Path: "/etc/kip/a.conf", Content: []byte("conf\n"), Mode: 0o640},
			{Path: "/run/secrets/token", Content: []byte("{{ project.T }}\n"), Secret: true}, {Path: "/bin/run.sh", Content: []byte("#!/bin/sh"), Mode: 0o755}, {Path: "/tmp/x"}, {Path: "/home/app/c/x"}}}
	if err := validateService(svc); err != nil {
		t.Fatal(err)
	}
	spec := containerSpec(store.Project{ID: "P", Name: "kipitiny-test"}, svc, envSources{}, "D1", 1, route{})
	delete(spec.Config.Labels, docker.LabelManaged)
	spec.NetworkingConfig = &network.NetworkingConfig{}
	spec.HostConfig.RestartPolicy = container.RestartPolicy{}
	id, err := dk.Run(ctx, spec, serviceFiles(svc, envSources{project: map[string]string{"T": "s3cret"}})...)
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
	if got := out.String(); !strings.Contains(got, "conf\ns3cret\n640\n755\n1777\n0\nmade\n") {
		t.Errorf("output = %q", got)
	}
}

// TestJobsDocker checks the readiness of jobs (restart "no" and
// on-failure) on the local Docker daemon.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run JobsDocker
func TestJobsDocker(t *testing.T) {
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
	c := &Core{}
	for i, tc := range []struct {
		restart, script string
		ok              bool
	}{
		{"no", "sleep 7; exit 0", true}, // longer than stableFor: waits for the exit
		{"no", "exit 3", false},
		{"on-failure:3", "[ -f /tmp/again ] && exit 0; touch /tmp/again; exit 1", true},
		{"on-failure:1", "exit 1", false},
		{"", "exit 0", false},
	} {
		svc := store.Service{ID: "01JOBTEST" + strconv.Itoa(i), Name: "job", Kind: store.ServiceKindApp, Image: "busybox", Replicas: 1,
			Runtime: store.Runtime{Command: []string{"sh", "-c", tc.script}, Restart: tc.restart}}
		spec := containerSpec(store.Project{ID: "P", Name: "kipitiny-test"}, svc, envSources{}, "D1", 1, route{})
		delete(spec.Config.Labels, docker.LabelManaged)
		spec.NetworkingConfig = &network.NetworkingConfig{}
		spec.Name += strconv.Itoa(i)
		id, err := dk.Run(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = dk.RemoveContainer(context.Background(), id, 0) })
		if err := c.waitReady(ctx, dk, id, tc.restart); (err == nil) != tc.ok {
			t.Errorf("restart %q, %q: err = %v", tc.restart, tc.script, err)
		}
	}
}
