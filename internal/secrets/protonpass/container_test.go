package protonpass

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
)

// TestContainerRunner runs the helper image on the local Docker. It needs
// KIPITINY_TEST_PROTONPASS_IMAGE, e.g. a local build of images/protonpass.
func TestContainerRunner(t *testing.T) {
	image := os.Getenv("KIPITINY_TEST_PROTONPASS_IMAGE")
	if image == "" {
		t.Skip("KIPITINY_TEST_PROTONPASS_IMAGE not set")
	}
	dk, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	defer dk.Close()
	ctx := context.Background()
	r := NewContainerRunner(dk, image, time.Second)
	r.name, r.volume = "kipitiny-protonpass-test", "kipitiny-protonpass-test"
	t.Cleanup(func() { _ = r.Reset(context.Background()) })

	out, _, err := r.Run(ctx, []string{"pass-cli", "--version"}, nil, nil)
	if err != nil || !strings.HasPrefix(string(out), "Proton Pass CLI") {
		t.Fatalf("version = %q, %v", out, err)
	}
	// Stdin and env reach the command; nothing is logged in.
	out, _, err = r.Run(ctx, []string{"sh", "-c", `read -r v; echo "$v $X"`}, []string{"X=env"}, strings.NewReader("in\n"))
	if err != nil || string(out) != "in env\n" {
		t.Errorf("stdin/env = %q, %v", out, err)
	}
	_, stderr, err := r.Run(ctx, []string{"pass-cli", "info"}, nil, nil)
	var ee *docker.ExecError
	if !errors.As(err, &ee) || cliError(string(stderr)) != "This operation requires an authenticated client" {
		t.Errorf("info = %q, %v", stderr, err)
	}

	// Removed by hand: the next call starts a new helper.
	if err := dk.RemoveContainer(ctx, r.name, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Run(ctx, []string{"true"}, nil, nil); err != nil {
		t.Errorf("after removal: %v", err)
	}

	// Gone once idle.
	time.Sleep(3 * time.Second)
	if _, err := dk.ContainerInspect(ctx, r.name, client.ContainerInspectOptions{}); !cerrdefs.IsNotFound(err) {
		t.Errorf("helper still there after idling: %v", err)
	}
}
