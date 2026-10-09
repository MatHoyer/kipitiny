package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// The data browser runs the database's own client (psql, redis-cli) inside
// its container, like backups: no driver in the manager, no network path to
// the database, and the client always matches the server. Every result is
// capped so a huge table or value never lands in memory.
const (
	// DataPageMax caps the rows (or keys, or members) of one page.
	DataPageMax = 200
	// DataCellMax caps the bytes of one cell or value shown; longer ones are
	// cut and flagged truncated.
	DataCellMax = 4096
	// dataLineMax bounds one line of client output (a row of capped cells).
	dataLineMax = 32 << 20

	dataBrowseTimeout = 5 * time.Second
	dataExportTimeout = 10 * time.Minute
)

// dataTarget is a database's running container, ready for an exec.
type dataTarget struct {
	svc       store.Service
	dk        *docker.Client
	container string
	// db is the postgres database queries run against.
	db string
}

// dataTarget resolves a database service of the given kind to its running
// container.
func (c *Core) dataTarget(ctx context.Context, id string, kind store.ServiceKind) (dataTarget, error) {
	svc, err := c.store.GetService(ctx, id)
	if err != nil {
		return dataTarget{}, err
	}
	if svc.Kind != kind {
		return dataTarget{}, fmt.Errorf("%w: %s is not a %s database", ErrInvalid, svc.Name, kind)
	}
	ct, err := c.runningContainer(ctx, svc)
	if err != nil {
		return dataTarget{}, err
	}
	return dataTarget{svc: svc, dk: c.dockerFor(svc.ServerID), container: ct, db: svc.Env[pgDatabase]}, nil
}

// execLines runs opts in the target's container and hands each stdout line
// to fn. Returning errStopLines from fn ends the read early (the exec is
// cancelled and its error ignored).
func (t dataTarget) execLines(ctx context.Context, opts docker.ExecOptions, fn func([]byte) error) error {
	return t.execStream(ctx, opts, func(r io.Reader) error {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64<<10), dataLineMax)
		for sc.Scan() {
			if err := fn(sc.Bytes()); err != nil {
				return err
			}
		}
		return sc.Err()
	})
}

// execStream runs opts in the target's container and hands its stdout to
// read. If read fails, the exec is cancelled; errStopLines from read means it
// stopped on purpose and is not an error.
func (t dataTarget) execStream(ctx context.Context, opts docker.ExecOptions, read func(io.Reader) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	opts.Stdout = pw
	done := make(chan error, 1)
	go func() {
		// The reader sees EOF either way; the exit status comes from done.
		done <- t.dk.Exec(ctx, t.container, opts)
		pw.Close()
	}()
	if err := read(pr); err != nil {
		cancel()
		pr.CloseWithError(err)
		<-done
		if errors.Is(err, errStopLines) {
			return nil
		}
		return err
	}
	_, _ = io.Copy(io.Discard, pr)
	return clientError(<-done)
}

var errStopLines = errors.New("stop reading")

// clientError turns a failed client run into a message for the caller: the
// database's own error ("relation does not exist", "WRONGTYPE ...") is the
// useful part.
func clientError(err error) error {
	var ee *docker.ExecError
	if !errors.As(err, &ee) {
		return err
	}
	msg := strings.TrimSpace(ee.Stderr)
	if msg == "" {
		msg = ee.Error()
	}
	// psql prefixes "psql:<stdin>:3: ERROR:  ", redis-cli "(error) ".
	for _, p := range []string{"ERROR:", "(error)"} {
		if i := strings.Index(msg, p); i >= 0 {
			msg = strings.TrimSpace(msg[i+len(p):])
			break
		}
	}
	return fmt.Errorf("%w: %s", ErrInvalid, msg)
}

// truncate caps s at DataCellMax bytes without splitting a UTF-8 sequence.
func truncate(s string) (string, bool) {
	if len(s) <= DataCellMax {
		return s, false
	}
	cut := DataCellMax
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut], true
}
