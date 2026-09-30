package core

import (
	"bufio"
	"context"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
)

type LogLine struct {
	Container string    `json:"container"`
	Time      time.Time `json:"time"`
	Text      string    `json:"text"`
}

const maxLogTail = 5000

// StreamLogs follows the logs of every container of the service, calling emit
// for each line (from a single goroutine). The backlog comes first, merged
// across replicas in time order, then live lines as they arrive. It returns
// when all containers' streams end or ctx is cancelled.
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
	dk := c.dockerFor(svc.ServerID)

	// Read each backlog to its end, then follow from its last line, so the
	// merged backlog can be sorted before anything is sent.
	var backlog []LogLine
	var follow []container.Summary
	var since []time.Time
	for _, ct := range cts {
		lines, err := containerLogs(ctx, dk, ct, client.ContainerLogsOptions{Tail: strconv.Itoa(tail)})
		if err != nil {
			// Typically removed since it was listed; the others still stream.
			c.log.Debug("log backlog failed", "container", containerName(ct), "err", err)
			continue
		}
		var last time.Time
		if len(lines) > 0 {
			last = lines[len(lines)-1].Time
		}
		follow, since = append(follow, ct), append(since, last)
		backlog = append(backlog, lines...)
	}
	sortLines(backlog)
	for _, l := range backlog {
		emit(l)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines := make(chan LogLine, 64)
	var wg sync.WaitGroup
	for i, ct := range follow {
		wg.Go(func() {
			if err := followContainer(ctx, dk, ct, since[i], lines); err != nil && ctx.Err() == nil {
				c.log.Debug("log stream ended", "container", containerName(ct), "err", err)
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

// followContainer streams lines logged after since (the last backlog line),
// or all of them when the backlog was empty.
func followContainer(ctx context.Context, dk *docker.Client, ct container.Summary, since time.Time, out chan<- LogLine) error {
	opts := client.ContainerLogsOptions{Follow: true}
	if !since.IsZero() {
		// Docker's since is inclusive: the last backlog line comes back.
		opts.Since = since.Format(time.RFC3339Nano)
	}
	return readLogs(ctx, dk, ct, opts, func(l LogLine) bool {
		if !l.Time.After(since) {
			return true
		}
		select {
		case out <- l:
			return true
		case <-ctx.Done():
			return false
		}
	})
}

// RecentLogs returns the last lines of every active container of a service,
// merged across containers in time order.
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
		l, err := containerLogs(ctx, c.dockerFor(svc.ServerID), ct, client.ContainerLogsOptions{Tail: strconv.Itoa(tail)})
		if err != nil {
			return nil, err
		}
		lines = append(lines, l...)
	}
	sortLines(lines)
	return lines, nil
}

func containerLogs(ctx context.Context, dk *docker.Client, ct container.Summary, opts client.ContainerLogsOptions) ([]LogLine, error) {
	var lines []LogLine
	err := readLogs(ctx, dk, ct, opts, func(l LogLine) bool {
		lines = append(lines, l)
		return true
	})
	return lines, err
}

// readLogs calls fn for each timestamped log line until the stream ends or fn
// returns false.
func readLogs(ctx context.Context, dk *docker.Client, ct container.Summary, opts client.ContainerLogsOptions, fn func(LogLine) bool) error {
	opts.ShowStdout, opts.ShowStderr, opts.Timestamps = true, true, true
	rc, err := dk.ContainerLogs(ctx, ct.ID, opts)
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

	name := containerName(ct)
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if !fn(parseLogLine(name, sc.Text())) {
			return ctx.Err()
		}
	}
	return sc.Err()
}

// parseLogLine splits the RFC 3339 timestamp Docker prefixes lines with.
func parseLogLine(container, s string) LogLine {
	ts, text, ok := strings.Cut(s, " ")
	t, err := time.Parse(time.RFC3339Nano, ts)
	if !ok || err != nil {
		return LogLine{Container: container, Text: s}
	}
	return LogLine{Container: container, Time: t, Text: text}
}

// sortLines orders lines by time, keeping each container's own order on ties.
func sortLines(lines []LogLine) {
	slices.SortStableFunc(lines, func(a, b LogLine) int { return a.Time.Compare(b.Time) })
}

func containerName(ct container.Summary) string {
	return strings.TrimPrefix(ct.Names[0], "/")
}
