package docker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

type ExecOptions struct {
	Cmd []string
	Env []string
	// Stdin, if set, is streamed to the process and then closed.
	Stdin io.Reader
	// Stdout receives the process's stdout; nil discards it.
	Stdout io.Writer
}

// ExecError is returned when the process exits non-zero.
type ExecError struct {
	ExitCode int
	Stderr   string
}

func (e *ExecError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		return fmt.Sprintf("exit code %d", e.ExitCode)
	}
	return fmt.Sprintf("exit code %d: %s", e.ExitCode, msg)
}

// Exec runs a command in a running container, streaming stdin/stdout in
// constant memory. A non-zero exit yields *ExecError with the stderr tail.
// Cancelling ctx closes the connection, which stops the transfer.
func (c *Client) Exec(ctx context.Context, containerID string, opts ExecOptions) error {
	created, err := c.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		Cmd:          opts.Cmd,
		Env:          opts.Env,
		AttachStdin:  opts.Stdin != nil,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return fmt.Errorf("exec create: %w", err)
	}
	att, err := c.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("exec attach: %w", err)
	}
	defer att.Close()

	stop := context.AfterFunc(ctx, func() { att.Close() })
	defer stop()

	var stdinErr error
	var wg sync.WaitGroup
	if opts.Stdin != nil {
		wg.Go(func() {
			_, stdinErr = io.Copy(att.Conn, opts.Stdin)
			_ = att.CloseWrite() // EOF for the process
		})
	}

	stdout := opts.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := &tailBuffer{max: 16 << 10}
	_, copyErr := stdcopy.StdCopy(stdout, stderr, att.Reader)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if copyErr != nil {
		return fmt.Errorf("exec stream: %w", copyErr)
	}

	// The exit code can lag the end of the stream slightly.
	for i := 0; ; i++ {
		res, err := c.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		if err != nil {
			return fmt.Errorf("exec inspect: %w", err)
		}
		if !res.Running {
			if res.ExitCode != 0 {
				return &ExecError{ExitCode: res.ExitCode, Stderr: stderr.String()}
			}
			break
		}
		if i == 100 {
			return fmt.Errorf("exec still running after its output closed")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if stdinErr != nil {
		return fmt.Errorf("exec stdin: %w", stdinErr)
	}
	return nil
}

// tailBuffer keeps the last max bytes written.
type tailBuffer struct {
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = t.buf[over:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }
