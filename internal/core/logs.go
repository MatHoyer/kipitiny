package core

import (
	"bufio"
	"context"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

type LogLine struct {
	Container string `json:"container"`
	Text      string `json:"text"`
}

const maxLogTail = 5000

// StreamLogs follows the logs of every container of the service, calling emit
// for each line (from a single goroutine). It returns when all containers'
// streams end or ctx is cancelled.
func (c *Core) StreamLogs(ctx context.Context, serviceID string, tail int, emit func(LogLine)) error {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return err
	}
	cts, err := c.activeContainers(ctx, svc)
	if err != nil {
		return err
	}
	if tail <= 0 || tail > maxLogTail {
		tail = 200
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines := make(chan LogLine, 64)
	var wg sync.WaitGroup
	for _, ct := range cts {
		name := strings.TrimPrefix(ct.Names[0], "/")
		wg.Go(func() {
			if err := c.followContainer(ctx, ct.ID, name, tail, lines); err != nil && ctx.Err() == nil {
				c.log.Debug("log stream ended", "container", name, "err", err)
			}
		})
	}
	go func() { wg.Wait(); close(lines) }()

	for {
		select {
		case l, ok := <-lines:
			if !ok {
				return nil
			}
			emit(l)
		case <-ctx.Done():
			return nil
		}
	}
}

func (c *Core) followContainer(ctx context.Context, id, name string, tail int, out chan<- LogLine) error {
	rc, err := c.docker.ContainerLogs(ctx, id, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Tail:       strconv.Itoa(tail),
	})
	if err != nil {
		return err
	}
	defer rc.Close()

	// Docker multiplexes stdout/stderr (no TTY); demux into one line stream.
	pr, pw := io.Pipe()
	go func() {
		_, err := stdcopy.StdCopy(pw, pw, rc)
		pw.CloseWithError(err)
	}()
	defer pr.Close()

	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		select {
		case out <- LogLine{Container: name, Text: sc.Text()}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return sc.Err()
}

// RecentLogs returns the last lines of every active container of a service,
// oldest first per container.
func (c *Core) RecentLogs(ctx context.Context, serviceID string, tail int) ([]LogLine, error) {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	cts, err := c.activeContainers(ctx, svc)
	if err != nil {
		return nil, err
	}
	var lines []LogLine
	for _, ct := range cts {
		rc, err := c.docker.ContainerLogs(ctx, ct.ID, client.ContainerLogsOptions{
			ShowStdout: true, ShowStderr: true, Tail: strconv.Itoa(tail),
		})
		if err != nil {
			return nil, err
		}
		pr, pw := io.Pipe()
		go func() {
			_, err := stdcopy.StdCopy(pw, pw, rc)
			pw.CloseWithError(err)
		}()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		name := strings.TrimPrefix(ct.Names[0], "/")
		for sc.Scan() {
			lines = append(lines, LogLine{Container: name, Text: sc.Text()})
		}
		rc.Close()
		pr.Close()
	}
	return lines, nil
}
