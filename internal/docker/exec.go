package docker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

type ExecOptions struct {
	Cmd []string
	Env []string
	// User runs the process as this user (e.g. "0"); empty is the container's.
	User string
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
		User:         opts.User,
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

// RunAttached creates and starts a one-off container, streams stdin into it
// and its output to out, waits for it to exit and removes it. It returns the
// exit code.
func (c *Client) RunAttached(ctx context.Context, opts client.ContainerCreateOptions, stdin io.Reader, out io.Writer) (int, error) {
	opts.Config.OpenStdin = stdin != nil
	opts.Config.StdinOnce = stdin != nil
	opts.Config.AttachStdin = stdin != nil
	opts.Config.AttachStdout, opts.Config.AttachStderr = true, true
	created, err := c.ContainerCreate(ctx, opts)
	if err != nil {
		return -1, fmt.Errorf("create %s: %w", opts.Name, err)
	}
	defer func() {
		_, _ = c.ContainerRemove(context.WithoutCancel(ctx), created.ID, client.ContainerRemoveOptions{Force: true})
	}()

	att, err := c.ContainerAttach(ctx, created.ID, client.ContainerAttachOptions{
		Stream: true, Stdin: stdin != nil, Stdout: true, Stderr: true,
	})
	if err != nil {
		return -1, fmt.Errorf("attach %s: %w", opts.Name, err)
	}
	defer att.Close()
	stop := context.AfterFunc(ctx, func() { att.Close() })
	defer stop()

	// Wait for "next exit" before starting, so a fast exit isn't missed.
	wait := c.ContainerWait(ctx, created.ID, client.ContainerWaitOptions{Condition: container.WaitConditionNextExit})
	if _, err := c.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return -1, fmt.Errorf("start %s: %w", opts.Name, err)
	}

	var stdinErr error
	var wg sync.WaitGroup
	if stdin != nil {
		wg.Go(func() {
			_, stdinErr = io.Copy(att.Conn, stdin)
			_ = att.CloseWrite()
		})
	}
	_, _ = stdcopy.StdCopy(out, out, att.Reader)
	wg.Wait()

	select {
	case res := <-wait.Result:
		if res.StatusCode == 0 && stdinErr != nil {
			return -1, fmt.Errorf("stdin: %w", stdinErr)
		}
		return int(res.StatusCode), nil
	case err := <-wait.Error:
		return -1, err
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}
