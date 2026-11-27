package data

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

// filesState holds the running helper of each service being browsed.
type filesState struct {
	mu      sync.Mutex
	helpers map[string]*filesHelper // helperKey ->
}

type filesHelper struct {
	mu        sync.Mutex
	key       string
	serviceID string
	serverID  string
	id        string // container; empty when not running
	sig       string // what it mounts
	busy      int
	idle      *time.Timer
}

// filesExec runs cmd where vol's files are, and hands its stdout to read:
// in svc's helper for a volume (started if needed; one removed behind the
// manager's back is started again once), or as root in the container.
func (c *Data) filesExec(ctx context.Context, svc domain.Service, vol fileVolume, cmd []string, read func(io.Reader) error) error {
	return c.filesExecIn(ctx, svc, vol, docker.ExecOptions{Cmd: cmd}, read)
}

// filesExecIn is filesExec with stdin; then the helper isn't retried, since
// stdin may be partly consumed.
func (c *Data) filesExecIn(ctx context.Context, svc domain.Service, vol fileVolume, opts docker.ExecOptions, read func(io.Reader) error) error {
	if vol.container {
		id, err := c.filesContainer(ctx, svc, vol.name)
		if err != nil {
			return err
		}
		opts.User = "0" // whatever user the image runs as
		return filesStream(ctx, c.mgr.DockerFor(svc.ServerID), id, opts, read)
	}
	return c.helperExec(ctx, svc, false, opts, read)
}

// helperExec runs opts in svc's helper (the SQLite one if sqlite), retried
// once in a new helper if it was removed behind the manager's back, unless
// stdin can't be rewound.
func (c *Data) helperExec(ctx context.Context, svc domain.Service, sqlite bool, opts docker.ExecOptions, read func(io.Reader) error) error {
	for attempt := 0; ; attempt++ {
		h, id, err := c.acquireFilesHelper(ctx, svc, sqlite)
		if err != nil {
			return err
		}
		err = filesStream(ctx, c.mgr.DockerFor(svc.ServerID), id, opts, read)
		gone := cerrdefs.IsNotFound(err) || cerrdefs.IsConflict(err) // removed, or not running
		c.releaseFilesHelper(h, gone)
		if gone && attempt == 0 {
			if opts.Stdin == nil {
				continue
			}
			if s, ok := opts.Stdin.(io.Seeker); ok {
				if _, serr := s.Seek(0, io.SeekStart); serr == nil {
					continue
				}
			}
		}
		return err
	}
}

// helperKey names svc's helper of a kind in filesState.
func helperKey(serviceID string, sqlite bool) string {
	if sqlite {
		return serviceID + "/sqlite"
	}
	return serviceID
}

// filesStream runs opts in container id and hands its stdout to read. If read
// fails, the exec is cancelled; errStopLines from read means it stopped on
// purpose and is not an error.
func filesStream(ctx context.Context, dk *docker.Client, id string, opts docker.ExecOptions, read func(io.Reader) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	opts.Stdout = pw
	done := make(chan error, 1)
	go func() {
		done <- dk.Exec(ctx, id, opts)
		pw.Close()
	}()
	if err := read(pr); err != nil {
		cancel()
		pr.CloseWithError(err)
		execErr := <-done
		var ee *docker.ExecError
		switch {
		case errors.Is(err, errStopLines):
			return nil
		case errors.As(execErr, &ee):
			return execErr // the script failed: its output was cut short
		}
		return err
	}
	_, _ = io.Copy(io.Discard, pr)
	return <-done
}

// filesContainer resolves "@container" (the first running replica of the
// serving deployment) or "@<container ID>" to a running container of svc.
func (c *Data) filesContainer(ctx context.Context, svc domain.Service, ref string) (string, error) {
	cts, err := c.mgr.ActiveContainers(ctx, svc)
	if ref != FilesContainer {
		cts, err = c.mgr.ServiceContainers(ctx, svc)
	}
	if err != nil {
		return "", err
	}
	slices.SortFunc(cts, func(a, b domain.Container) int {
		return strings.Compare(a.Labels[domain.LabelReplica], b.Labels[domain.LabelReplica])
	})
	id := strings.TrimPrefix(ref, containerPrefix)
	for _, ct := range cts {
		if ct.State == domain.StateRunning && (ref == FilesContainer || strings.HasPrefix(ct.ID, id)) {
			return ct.ID, nil
		}
	}
	if ref == FilesContainer {
		return "", fmt.Errorf("%w: %s has no running container: start it to browse its files", ErrInvalid, svc.Name)
	}
	return "", fmt.Errorf("%w: container %s of %s isn't running (it may have been replaced by a deploy)", ErrInvalid, id, svc.Name)
}

// acquireFilesHelper returns svc's running helper (the SQLite one if
// sqlite), marked busy until releaseFilesHelper.
func (c *Data) acquireFilesHelper(ctx context.Context, svc domain.Service, sqlite bool) (*filesHelper, string, error) {
	vols := fileVolumes(svc)
	if len(vols) == 0 {
		return nil, "", fmt.Errorf("%w: %s has no volumes", ErrInvalid, svc.Name)
	}
	names := make([]string, len(vols))
	for i, v := range vols {
		names[i] = v.name
	}
	sig := svc.ServerID + "|" + strconv.FormatBool(filesReadOnly(svc)) + "|" + strings.Join(names, ",")

	if c.mgr.ShuttingDown() {
		return nil, "", ErrShuttingDown
	}
	c.files.mu.Lock()
	if c.files.helpers == nil {
		c.files.helpers = map[string]*filesHelper{}
	}
	key := helperKey(svc.ID, sqlite)
	h := c.files.helpers[key]
	if h == nil {
		h = &filesHelper{key: key, serviceID: svc.ID}
		c.files.helpers[key] = h
	}
	c.files.mu.Unlock()

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.id != "" && h.sig != sig {
		c.removeFilesContainer(h.serverID, h.id)
		h.id = ""
	}
	if h.id == "" {
		dk := c.mgr.DockerFor(svc.ServerID)
		mounts := helperMounts(svc, names, filesReadOnly(svc))
		var id string
		var err error
		if sqlite {
			id, err = c.startSQLiteHelper(ctx, dk, "kipitiny-sqlite-"+strings.ToLower(ids.New()), mounts)
		} else {
			id, err = startVolumeHelper(ctx, dk, "kipitiny-files-"+strings.ToLower(ids.New()), mounts)
		}
		if err != nil {
			return nil, "", fmt.Errorf("start file helper: %w", err)
		}
		h.id, h.sig, h.serverID = id, sig, svc.ServerID
	}
	h.busy++
	if h.idle != nil {
		h.idle.Stop()
		h.idle = nil
	}
	return h, h.id, nil
}

// releaseFilesHelper ends a use of h; the last one arms the idle timer. gone
// drops a helper found missing.
func (c *Data) releaseFilesHelper(h *filesHelper, gone bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.busy--
	if gone && h.id != "" {
		c.removeFilesContainer(h.serverID, h.id)
		h.id = ""
	}
	if h.busy == 0 && h.id != "" {
		var t *time.Timer
		t = time.AfterFunc(filesIdle, func() { c.expireFilesHelper(h, t) })
		h.idle = t
	}
}

// expireFilesHelper removes h's container after it sat idle, unless it was
// used again meanwhile.
func (c *Data) expireFilesHelper(h *filesHelper, t *time.Timer) {
	c.files.mu.Lock()
	h.mu.Lock()
	if h.idle != t || h.busy > 0 {
		h.mu.Unlock()
		c.files.mu.Unlock()
		return
	}
	if c.files.helpers[h.key] == h {
		delete(c.files.helpers, h.key)
	}
	serverID, id := h.serverID, h.id
	h.id, h.idle = "", nil
	h.mu.Unlock()
	c.files.mu.Unlock()
	if id != "" {
		c.removeFilesContainer(serverID, id)
	}
}

// CloseFilesHelper removes svc's helpers, if any, so its volumes are no
// longer in use (before they are deleted). A transfer running in one is cut.
func (c *Data) CloseFilesHelper(serviceID string) {
	c.closeHelper(helperKey(serviceID, false))
	c.closeHelper(helperKey(serviceID, true))
}

func (c *Data) closeHelper(key string) {
	c.files.mu.Lock()
	h := c.files.helpers[key]
	delete(c.files.helpers, key)
	c.files.mu.Unlock()
	if h == nil {
		return
	}
	h.mu.Lock()
	serverID, id := h.serverID, h.id
	h.id = ""
	if h.idle != nil {
		h.idle.Stop()
		h.idle = nil
	}
	h.mu.Unlock()
	if id != "" {
		c.removeFilesContainer(serverID, id)
	}
}

// CloseFilesHelpers removes every helper, on shutdown.
func (c *Data) CloseFilesHelpers() {
	c.files.mu.Lock()
	keys := make([]string, 0, len(c.files.helpers))
	for key := range c.files.helpers {
		keys = append(keys, key)
	}
	c.files.mu.Unlock()
	for _, key := range keys {
		c.closeHelper(key)
	}
}

func (c *Data) removeFilesContainer(serverID, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c.removeHelper(ctx, c.mgr.DockerFor(serverID), id)
}
