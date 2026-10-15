package core

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/docker"
)

// HostUsage is a server's resource use at one moment.
type HostUsage struct {
	At time.Time `json:"at"`
	// CPU is in percent of the whole machine (100 = every core busy).
	CPU   float64 `json:"cpu"`
	Cores int     `json:"cores"`
	// Load is the 1, 5 and 15 minute load average.
	Load             [3]float64 `json:"load"`
	MemoryBytes      uint64     `json:"memoryBytes"`
	MemoryTotalBytes uint64     `json:"memoryTotalBytes"`
	SwapBytes        uint64     `json:"swapBytes"`
	SwapTotalBytes   uint64     `json:"swapTotalBytes"`
	// Disk is the filesystem holding Docker's data (images, containers,
	// volumes by default).
	DiskBytes      uint64 `json:"diskBytes"`
	DiskTotalBytes uint64 `json:"diskTotalBytes"`
	UptimeSec      int64  `json:"uptimeSec"`
}

type ServerStats struct {
	// Current is nil while the server can't be read.
	Current *HostUsage  `json:"current"`
	History []HostUsage `json:"history"`
}

// hostSeries is one server's recent points and the CPU counters of the last
// reading, for the rate.
type hostSeries struct {
	busy, total uint64
	points      []HostUsage
}

// hostScript prints the host files a reading needs. /proc isn't namespaced
// for these, and the root of a container is on Docker's data filesystem.
const hostScript = `for f in stat meminfo loadavg uptime; do echo "== $f"; cat /proc/$f; done; echo "== df"; df -kP /`

// sampleHost reads a server's host counters from inside its Traefik
// container, which runs on every server.
// shortcut: needs cat and df in the Traefik image (alpine based); use a
// helper container if it ever ships without a shell.
func sampleHost(ctx context.Context, dk *docker.Client) (hostSample, error) {
	var out bytes.Buffer
	if err := dk.Exec(ctx, traefikName, docker.ExecOptions{Cmd: []string{"sh", "-c", hostScript}, Stdout: &out}); err != nil {
		return hostSample{}, err
	}
	return parseHost(out.String())
}

// hostSample is one reading: the usage without CPU, and the CPU counters.
type hostSample struct {
	HostUsage
	busy, total uint64
}

func parseHost(out string) (hostSample, error) {
	var (
		h       hostSample
		section string
		mem     = map[string]uint64{}
		seen    = map[string]bool{}
	)
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if name, ok := strings.CutPrefix(line, "== "); ok {
			section = name
			continue
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch section {
		case "stat":
			if f[0] == "cpu" && len(f) >= 9 {
				// user nice system idle iowait irq softirq steal; guest is
				// already in user.
				for i, v := range f[1:9] {
					n, _ := strconv.ParseUint(v, 10, 64)
					h.total += n
					if i != 3 && i != 4 {
						h.busy += n
					}
				}
				seen["stat"] = true
			} else if strings.HasPrefix(f[0], "cpu") {
				h.Cores++
			}
		case "meminfo":
			if len(f) >= 2 {
				n, _ := strconv.ParseUint(f[1], 10, 64)
				mem[strings.TrimSuffix(f[0], ":")] = n << 10
				seen["meminfo"] = true
			}
		case "loadavg":
			for i := range min(3, len(f)) {
				h.Load[i], _ = strconv.ParseFloat(f[i], 64)
			}
		case "uptime":
			up, _ := strconv.ParseFloat(f[0], 64)
			h.UptimeSec = int64(up)
		case "df":
			if f[0] == "Filesystem" || len(f) < 4 {
				continue
			}
			used, err1 := strconv.ParseUint(f[2], 10, 64)
			avail, err2 := strconv.ParseUint(f[3], 10, 64)
			if err1 == nil && err2 == nil {
				// used+available, like df's percentage: the blocks kept for
				// root aren't usable.
				h.DiskBytes, h.DiskTotalBytes = used<<10, (used+avail)<<10
				seen["df"] = true
			}
		}
	}
	for _, s := range []string{"stat", "meminfo", "df"} {
		if !seen[s] {
			return hostSample{}, fmt.Errorf("host stats: no %s in the output", s)
		}
	}
	h.MemoryTotalBytes = mem["MemTotal"]
	if avail := mem["MemAvailable"]; avail <= h.MemoryTotalBytes {
		h.MemoryBytes = h.MemoryTotalBytes - avail
	}
	h.SwapTotalBytes = mem["SwapTotal"]
	if free := mem["SwapFree"]; free <= h.SwapTotalBytes {
		h.SwapBytes = h.SwapTotalBytes - free
	}
	return h, nil
}

// recordHosts adds a point per server read this round. The first reading of
// a server has no CPU rate yet.
func (s *statsState) recordHosts(now time.Time, got map[string]hostSample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hosts == nil {
		s.hosts = map[string]*hostSeries{}
	}
	for id, h := range got {
		se := s.hosts[id]
		if se == nil {
			se = &hostSeries{}
			s.hosts[id] = se
		}
		p := h.HostUsage
		p.At = now
		if dt := grew(se.total, h.total); se.total > 0 && dt > 0 {
			p.CPU = float64(grew(se.busy, h.busy)) / float64(dt) * 100
		}
		se.busy, se.total = h.busy, h.total
		se.points = append(se.points, p)
		if len(se.points) > statsHistory {
			se.points = append(se.points[:0], se.points[len(se.points)-statsHistory:]...)
		}
	}
	for id, se := range s.hosts {
		if now.Sub(se.points[len(se.points)-1].At) > statsEvery*statsHistory {
			delete(s.hosts, id)
		}
	}
}

func (s *statsState) host(id string, now time.Time) ServerStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := ServerStats{History: []HostUsage{}}
	if se := s.hosts[id]; se != nil {
		st.History = append(st.History, se.points...)
		if last := se.points[len(se.points)-1]; last.At.Equal(s.last) && now.Sub(s.last) < 3*statsEvery {
			st.Current = &last
		}
	}
	return st
}

// ServerStats returns a server's current host resource use and the last
// five minutes of it.
func (c *Core) ServerStats(ctx context.Context, id string) (ServerStats, error) {
	if _, err := c.store.GetServer(ctx, id); err != nil {
		return ServerStats{}, err
	}
	return c.stats.host(id, time.Now()), nil
}
