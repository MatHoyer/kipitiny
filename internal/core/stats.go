package core

import (
	"context"
	"encoding/json"
	"maps"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	statsEvery = 10 * time.Second
	// statsHistory is how many samples a service keeps: five minutes.
	statsHistory = 30
	// statsWorkers bounds the stats calls in flight on one server.
	statsWorkers = 4
)

// Usage is a service's resource use at one moment, summed over its running
// replicas.
type Usage struct {
	At       time.Time `json:"at"`
	Replicas int       `json:"replicas"`
	// CPU is in percent of one core (200 = two busy cores), like docker stats.
	CPU         float64 `json:"cpu"`
	MemoryBytes uint64  `json:"memoryBytes"`
	// MemoryLimitBytes is the limit of all those replicas; 0 when unlimited.
	MemoryLimitBytes uint64 `json:"memoryLimitBytes,omitempty"`
	// NetRx and NetTx are bytes per second received and sent.
	NetRx float64 `json:"netRx"`
	NetTx float64 `json:"netTx"`
}

type ServiceStats struct {
	// Current is nil while the service runs no replica (or none was sampled yet).
	Current *Usage  `json:"current"`
	History []Usage `json:"history"`
	// Containers is the same for each replica, by short container ID.
	Containers map[string]ContainerStats `json:"containers"`
}

type ContainerStats struct {
	Current *Usage  `json:"current"`
	History []Usage `json:"history"`
}

// ServiceUsage is the current usage of one service, with what it belongs to
// so lists can add it up per project or server.
type ServiceUsage struct {
	ProjectID string `json:"projectId"`
	ServerID  string `json:"serverId"`
	Usage
}

// statsState holds recent samples in memory only: they are worth nothing
// after a restart.
type statsState struct {
	mu         sync.Mutex
	prev       map[string]sample // container ID -> last sample, for rates
	series     map[string]*series
	containers map[string]*series // container ID ->
	last       time.Time          // of the last round
}

type series struct {
	projectID, serverID string
	serviceID           string // of a container's series
	points              []Usage
}

// add appends p, keeping statsHistory points.
func (se *series) add(p Usage) {
	se.points = append(se.points, p)
	if len(se.points) > statsHistory {
		se.points = append(se.points[:0], se.points[len(se.points)-statsHistory:]...)
	}
}

// view is the series as the API shows it.
func (se *series) view(fresh func(Usage) bool) ContainerStats {
	st := ContainerStats{History: append([]Usage{}, se.points...)}
	if last := se.points[len(se.points)-1]; fresh(last) {
		st.Current = &last
	}
	return st
}

// sample is one container's cumulative counters.
type sample struct {
	read   time.Time
	cpu    uint64 // CPU time used, in ns
	mem    uint64
	rx, tx uint64
}

// StartStats samples the resource use of every running replica, on every
// server, every ten seconds.
func (c *Core) StartStats() error {
	return c.goLoop(func() {
		tick := time.NewTicker(statsEvery)
		defer tick.Stop()
		for {
			c.sampleStats(c.bg)
			select {
			case <-c.bg.Done():
				return
			case <-tick.C:
			}
		}
	})
}

func (c *Core) sampleStats(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, statsEvery)
	defer cancel()
	servers, err := c.store.ListServers(ctx)
	if err != nil {
		return
	}
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return
	}
	byID := make(map[string]store.Service, len(svcs))
	for _, s := range svcs {
		byID[s.ID] = s
	}
	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		got   = map[string]sample{}
		owner = map[string]string{} // container ID -> service ID
	)
	for _, sv := range servers {
		wg.Go(func() {
			s, o, err := sampleServer(ctx, c.dockerFor(sv.ID), byID)
			if err != nil {
				c.log.Debug("container stats", "server", sv.Name, "err", err)
				return
			}
			mu.Lock()
			maps.Copy(got, s)
			maps.Copy(owner, o)
			mu.Unlock()
		})
	}
	wg.Wait()
	c.stats.record(time.Now(), got, owner, byID)
}

// sampleServer reads the counters of the server's running replicas that
// serve traffic (not the previous deployment's during a rollout).
func sampleServer(ctx context.Context, dk *docker.Client, svcs map[string]store.Service) (map[string]sample, map[string]string, error) {
	f := make(client.Filters)
	f.Add("label", docker.LabelManaged+"=true")
	f.Add("label", docker.LabelService)
	res, err := dk.ContainerList(ctx, client.ContainerListOptions{Filters: f})
	if err != nil {
		return nil, nil, err
	}
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		sem     = make(chan struct{}, statsWorkers)
		samples = map[string]sample{}
		owner   = map[string]string{}
	)
	for _, ct := range res.Items {
		svc, ok := svcs[ct.Labels[docker.LabelService]]
		if !ok || !isActive(ct, svc) {
			continue
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			s, err := containerSample(ctx, dk, ct.ID)
			if err != nil {
				return // typically removed since it was listed
			}
			mu.Lock()
			samples[ct.ID], owner[ct.ID] = s, svc.ID
			mu.Unlock()
		})
	}
	wg.Wait()
	return samples, owner, nil
}

// containerSample takes a single reading, without the daemon's one-second
// wait for a previous one: rates come from our own previous sample.
func containerSample(ctx context.Context, dk *docker.Client, id string) (sample, error) {
	res, err := dk.ContainerStats(ctx, id, client.ContainerStatsOptions{})
	if err != nil {
		return sample{}, err
	}
	defer res.Body.Close()
	var st container.StatsResponse
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		return sample{}, err
	}
	return toSample(st, time.Now()), nil
}

func toSample(st container.StatsResponse, now time.Time) sample {
	s := sample{read: st.Read, cpu: st.CPUStats.CPUUsage.TotalUsage, mem: memoryUsed(st.MemoryStats)}
	if s.read.IsZero() {
		s.read = now
	}
	for _, n := range st.Networks {
		s.rx += n.RxBytes
		s.tx += n.TxBytes
	}
	return s
}

// memoryUsed leaves out the page cache the kernel can reclaim, like docker
// stats does.
func memoryUsed(m container.MemoryStats) uint64 {
	if v, ok := m.Stats["total_inactive_file"]; ok && v < m.Usage { // cgroup v1
		return m.Usage - v
	}
	if v := m.Stats["inactive_file"]; v < m.Usage {
		return m.Usage - v
	}
	return m.Usage
}

// rates turns two samples of a container into CPU percent and bytes/s.
func rates(prev, cur sample) (cpu, rx, tx float64) {
	dt := cur.read.Sub(prev.read).Seconds()
	if dt <= 0 {
		return 0, 0, 0
	}
	return float64(grew(prev.cpu, cur.cpu)) / 1e9 / dt * 100,
		float64(grew(prev.rx, cur.rx)) / dt,
		float64(grew(prev.tx, cur.tx)) / dt
}

// grew is b-a for a counter, 0 if it was reset.
func grew(a, b uint64) uint64 {
	if b < a {
		return 0
	}
	return b - a
}

// record adds one point per service from this round's container samples.
// A container seen for the first time counts for memory only.
func (s *statsState) record(now time.Time, got map[string]sample, owner map[string]string, svcs map[string]store.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.series == nil {
		s.series, s.containers = map[string]*series{}, map[string]*series{}
	}
	points := map[string]*Usage{}
	for id, cur := range got {
		svc := svcs[owner[id]]
		one := Usage{At: now, Replicas: 1, MemoryBytes: cur.mem, MemoryLimitBytes: uint64(svc.MemoryMB) << 20}
		if prev, ok := s.prev[id]; ok {
			one.CPU, one.NetRx, one.NetTx = rates(prev, cur)
		}
		ce := s.containers[id]
		if ce == nil {
			ce = &series{serviceID: svc.ID}
			s.containers[id] = ce
		}
		ce.add(one)

		p := points[svc.ID]
		if p == nil {
			p = &Usage{At: now}
			points[svc.ID] = p
		}
		p.Replicas++
		p.CPU += one.CPU
		p.MemoryBytes += one.MemoryBytes
		p.MemoryLimitBytes += one.MemoryLimitBytes
		p.NetRx += one.NetRx
		p.NetTx += one.NetTx
	}
	s.prev, s.last = got, now
	for sid, p := range points {
		svc := svcs[sid]
		se := s.series[sid]
		if se == nil {
			se = &series{}
			s.series[sid] = se
		}
		se.projectID, se.serverID = svc.ProjectID, svc.ServerID
		se.add(*p)
	}
	// Forget what went quiet for the whole window: stopped, deleted, replaced,
	// or on a server that can't be reached.
	for _, m := range []map[string]*series{s.series, s.containers} {
		for id, se := range m {
			if now.Sub(se.points[len(se.points)-1].At) > statsEvery*statsHistory {
				delete(m, id)
			}
		}
	}
}

// fresh reports whether a point still describes the present: it is from the
// last sampling round, and that round is recent.
func (s *statsState) fresh(p Usage, now time.Time) bool {
	return p.At.Equal(s.last) && now.Sub(s.last) < 3*statsEvery
}

func (s *statsState) service(id string, now time.Time) ServiceStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	fresh := func(p Usage) bool { return s.fresh(p, now) }
	st := ServiceStats{History: []Usage{}, Containers: map[string]ContainerStats{}}
	if se := s.series[id]; se != nil {
		v := se.view(fresh)
		st.Current, st.History = v.Current, v.History
	}
	for cid, ce := range s.containers {
		if ce.serviceID == id {
			st.Containers[cid[:min(len(cid), 12)]] = ce.view(fresh)
		}
	}
	return st
}

func (s *statsState) current(now time.Time) map[string]ServiceUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]ServiceUsage{}
	for id, se := range s.series {
		if last := se.points[len(se.points)-1]; s.fresh(last, now) {
			out[id] = ServiceUsage{ProjectID: se.projectID, ServerID: se.serverID, Usage: last}
		}
	}
	return out
}

// ServiceStats returns a service's current resource use and the last five
// minutes of it.
func (c *Core) ServiceStats(ctx context.Context, id string) (ServiceStats, error) {
	if _, err := c.store.GetService(ctx, id); err != nil {
		return ServiceStats{}, err
	}
	return c.stats.service(id, time.Now()), nil
}

// CurrentUsage returns the current resource use of every running service,
// by service ID.
func (c *Core) CurrentUsage() map[string]ServiceUsage {
	return c.stats.current(time.Now())
}
