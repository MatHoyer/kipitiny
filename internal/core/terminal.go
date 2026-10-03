package core

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// terminalShells are the shells a container terminal can run. "auto" takes
// bash when the image has it, sh otherwise.
var terminalShells = map[string][]string{
	"auto": {"/bin/sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash || exec sh"},
	"sh":   {"sh"},
	"bash": {"bash"},
}

var terminalEnv = []string{"TERM=xterm-256color"}

// Terminal is an interactive shell session. Every session is recorded in the
// audit log when it opens and when it closes.
type Terminal struct {
	*docker.TTY
	once  sync.Once
	close func()
}

// Close ends the session.
func (t *Terminal) Close() error {
	t.once.Do(t.close)
	return nil
}

// openTerminal records the session's start (or failure to start) and wraps
// tty so closing it records the end.
func (c *Core) openTerminal(ctx context.Context, target string, tty *docker.TTY, err error) (*Terminal, error) {
	if err != nil {
		c.Audit(ctx, "terminal open", target, http.StatusBadGateway, err)
		return nil, err
	}
	c.Audit(ctx, "terminal open", target, http.StatusSwitchingProtocols, nil)
	start := time.Now()
	return &Terminal{TTY: tty, close: func() {
		_ = tty.Close()
		c.Audit(ctx, "terminal close", fmt.Sprintf("%s after %s", target, time.Since(start).Round(time.Second)), http.StatusOK, nil)
	}}, nil
}

// OpenServiceTerminal starts a shell in one of the service's running
// containers (by ID prefix or name; the first replica when empty). Admin only.
func (c *Core) OpenServiceTerminal(ctx context.Context, serviceID, containerRef, shell string, cols, rows uint) (*Terminal, error) {
	if err := Require(ctx, store.ScopeAdmin); err != nil {
		return nil, err
	}
	if shell == "" {
		shell = "auto"
	}
	cmd, ok := terminalShells[shell]
	if !ok {
		return nil, fmt.Errorf("%w: shell must be auto, sh or bash", ErrInvalid)
	}
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	cts, err := c.activeContainers(ctx, svc)
	if err != nil {
		return nil, err
	}
	ct, ok := pickContainer(cts, containerRef)
	if !ok {
		if containerRef == "" {
			return nil, fmt.Errorf("%w: no running container", ErrInvalid)
		}
		return nil, fmt.Errorf("%w: container %s is not running", ErrInvalid, containerRef)
	}
	tty, err := c.dockerFor(svc.ServerID).ExecTTY(ctx, ct.ID, cmd, terminalEnv, cols, rows)
	return c.openTerminal(ctx, containerName(ct), tty, err)
}

// pickContainer finds a running container by ID prefix or name, or the
// lowest replica when ref is empty.
func pickContainer(cts []container.Summary, ref string) (container.Summary, bool) {
	var best container.Summary
	found := false
	for _, ct := range cts {
		if ct.State != container.StateRunning {
			continue
		}
		if ref != "" {
			if len(ref) >= 12 && strings.HasPrefix(ct.ID, ref) || containerName(ct) == ref {
				return ct, true
			}
			continue
		}
		if !found || replicaOf(ct) < replicaOf(best) {
			best, found = ct, true
		}
	}
	return best, found
}

func replicaOf(ct container.Summary) int {
	n, _ := strconv.Atoi(ct.Labels[docker.LabelReplica])
	return n
}
