package docker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// TTY is an interactive process on a pseudo-terminal: an exec in a running
// container, or a one-off container. Reads return its output (raw, a TTY
// isn't multiplexed), writes go to its input.
type TTY struct {
	att    client.HijackedResponse
	resize func(ctx context.Context, cols, rows uint) error
	wait   func(ctx context.Context) (int, error)
	// cleanup runs once on Close (removes a one-off container).
	cleanup func()
	once    sync.Once
}

func (t *TTY) Read(p []byte) (int, error)  { return t.att.Reader.Read(p) }
func (t *TTY) Write(p []byte) (int, error) { return t.att.Conn.Write(p) }

func (t *TTY) Resize(ctx context.Context, cols, rows uint) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	return t.resize(ctx, cols, rows)
}

// Wait returns the exit code once the output has ended.
func (t *TTY) Wait(ctx context.Context) (int, error) { return t.wait(ctx) }

// Close hangs up: the connection is closed and a one-off container removed.
func (t *TTY) Close() error {
	t.once.Do(func() {
		t.att.Close()
		if t.cleanup != nil {
			t.cleanup()
		}
	})
	return nil
}

// consoleSize is Docker's [height, width], or nothing when unknown.
func consoleSize(cols, rows uint) client.ConsoleSize {
	if cols == 0 || rows == 0 {
		return client.ConsoleSize{}
	}
	return client.ConsoleSize{Height: rows, Width: cols}
}

// ExecTTY starts cmd in a running container on a pseudo-terminal.
func (c *Client) ExecTTY(ctx context.Context, containerID string, cmd, env []string, cols, rows uint) (*TTY, error) {
	size := consoleSize(cols, rows)
	created, err := c.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		Cmd:          cmd,
		Env:          env,
		TTY:          true,
		ConsoleSize:  size,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, fmt.Errorf("exec create: %w", err)
	}
	att, err := c.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: true, ConsoleSize: size})
	if err != nil {
		return nil, fmt.Errorf("exec attach: %w", err)
	}
	return &TTY{
		att: att.HijackedResponse,
		resize: func(ctx context.Context, cols, rows uint) error {
			_, err := c.ExecResize(ctx, created.ID, client.ExecResizeOptions{Width: cols, Height: rows})
			return err
		},
		wait: func(ctx context.Context) (int, error) {
			// The exit code can lag the end of the stream slightly.
			for i := 0; ; i++ {
				res, err := c.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
				if err != nil {
					return -1, fmt.Errorf("exec inspect: %w", err)
				}
				if !res.Running {
					return res.ExitCode, nil
				}
				if i == 100 {
					return -1, fmt.Errorf("exec still running after its output closed")
				}
				time.Sleep(50 * time.Millisecond)
			}
		},
	}, nil
}

// RunTTY creates and starts a one-off container on a pseudo-terminal. Close
// removes it.
func (c *Client) RunTTY(ctx context.Context, opts client.ContainerCreateOptions, cols, rows uint) (*TTY, error) {
	opts.Config.Tty = true
	opts.Config.OpenStdin, opts.Config.StdinOnce = true, true
	opts.Config.AttachStdin, opts.Config.AttachStdout, opts.Config.AttachStderr = true, true, true
	if size := consoleSize(cols, rows); size.Width > 0 {
		opts.HostConfig.ConsoleSize = [2]uint{size.Height, size.Width}
	}
	created, err := c.ContainerCreate(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", opts.Name, err)
	}
	remove := func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, _ = c.ContainerRemove(ctx, created.ID, client.ContainerRemoveOptions{Force: true})
	}

	att, err := c.ContainerAttach(ctx, created.ID, client.ContainerAttachOptions{
		Stream: true, Stdin: true, Stdout: true, Stderr: true,
	})
	if err != nil {
		remove()
		return nil, fmt.Errorf("attach %s: %w", opts.Name, err)
	}
	// Wait for "next exit" before starting, so a fast exit isn't missed.
	wait := c.ContainerWait(ctx, created.ID, client.ContainerWaitOptions{Condition: container.WaitConditionNextExit})
	if _, err := c.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		att.Close()
		remove()
		return nil, fmt.Errorf("start %s: %w", opts.Name, err)
	}
	return &TTY{
		att:     att.HijackedResponse,
		cleanup: remove,
		resize: func(ctx context.Context, cols, rows uint) error {
			_, err := c.ContainerResize(ctx, created.ID, client.ContainerResizeOptions{Width: cols, Height: rows})
			return err
		},
		wait: func(ctx context.Context) (int, error) {
			select {
			case res := <-wait.Result:
				return int(res.StatusCode), nil
			case err := <-wait.Error:
				return -1, err
			case <-ctx.Done():
				return -1, ctx.Err()
			}
		},
	}, nil
}
