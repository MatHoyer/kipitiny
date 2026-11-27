package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/notify"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
)

const resourceAlertsSetting = "resource_alerts"

// alertRecover is how long a resource must stay back on the good side of its
// threshold before the recovery is sent, so a dip doesn't flap.
const alertRecover = time.Minute

func defaultResourceAlerts() domain.ResourceAlerts {
	return domain.ResourceAlerts{MemoryPercent: 90, MemoryMinutes: 5, CPUPercent: 90, CPUMinutes: 10, DiskFreePercent: 10}
}

func (c *Core) resourceAlerts(ctx context.Context) domain.ResourceAlerts {
	a := defaultResourceAlerts()
	if v, err := c.store.GetSetting(ctx, resourceAlertsSetting); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &a)
	}
	return a
}

func (c *Core) SetResourceAlerts(ctx context.Context, a domain.ResourceAlerts) (domain.ResourceAlerts, error) {
	switch {
	case a.MemoryPercent < 1 || a.MemoryPercent > 100, a.CPUPercent < 1 || a.CPUPercent > 100:
		return domain.ResourceAlerts{}, fmt.Errorf("%w: memory and CPU thresholds are 1 to 100%%", ErrInvalid)
	case a.MemoryMinutes < 1 || a.MemoryMinutes > 60, a.CPUMinutes < 1 || a.CPUMinutes > 60:
		return domain.ResourceAlerts{}, fmt.Errorf("%w: durations are 1 to 60 minutes", ErrInvalid)
	case a.DiskFreePercent < 1 || a.DiskFreePercent > 99:
		return domain.ResourceAlerts{}, fmt.Errorf("%w: free disk threshold is 1 to 99%%", ErrInvalid)
	}
	b, err := json.Marshal(a)
	if err != nil {
		return domain.ResourceAlerts{}, err
	}
	return a, c.store.SetSetting(ctx, resourceAlertsSetting, string(b))
}

// alertState debounces one condition: it must hold for a while before it is
// reported, and its absence too before the recovery is.
type alertState struct {
	alerted bool
	since   time.Time // when the reading first disagreed with alerted; zero while it agrees
}

func (a *alertState) observe(over bool, now time.Time, raise, clear time.Duration) (fire, recovered bool) {
	if over == a.alerted {
		a.since = time.Time{}
		return false, false
	}
	if a.since.IsZero() {
		a.since = now
	}
	wait := raise
	if a.alerted {
		wait = clear
	}
	if now.Sub(a.since) < wait {
		return false, false
	}
	a.alerted, a.since = over, time.Time{}
	return over, !over
}

// resourceAlert is a transition to send.
type resourceAlert struct {
	event, serviceID, serverID string
	percent, limit             float64 // limit in bytes for memory, cores for CPU, bytes of disk
	unlimited                  bool
}

// checkResources judges this round's points against the thresholds, under
// the stats lock, and returns what changed. A service or server missing from
// the round starts its wait over.
func (s *statsState) checkResources(now time.Time, th domain.ResourceAlerts, svcs map[string]domain.Service) []resourceAlert {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.alerts == nil {
		s.alerts = map[string]*alertState{}
	}
	seen := map[string]bool{}
	var out []resourceAlert
	judge := func(key string, over bool, raise time.Duration, a resourceAlert, high, ok string) {
		seen[key] = true
		st := s.alerts[key]
		if st == nil {
			st = &alertState{}
			s.alerts[key] = st
		}
		fire, rec := st.observe(over, now, raise, alertRecover)
		switch {
		case fire:
			a.event = high
			out = append(out, a)
		case rec:
			a.event = ok
			out = append(out, a)
		}
	}
	for id, se := range s.series {
		p := se.points[len(se.points)-1]
		if !p.At.Equal(now) {
			continue
		}
		var host *HostUsage
		if h := s.hosts[se.serverID]; h != nil && h.points[len(h.points)-1].At.Equal(now) {
			host = &h.points[len(h.points)-1]
		}
		base := resourceAlert{serviceID: id, serverID: se.serverID}

		mem, unlimited := float64(p.MemoryLimitBytes), p.MemoryLimitBytes == 0
		if unlimited && host != nil {
			mem = float64(host.MemoryTotalBytes)
		}
		if mem > 0 {
			a := base
			a.percent, a.limit, a.unlimited = float64(p.MemoryBytes)/mem*100, mem, unlimited
			judge("mem:"+id, a.percent >= float64(th.MemoryPercent), time.Duration(th.MemoryMinutes)*time.Minute, a, domain.EventMemoryHigh, domain.EventMemoryOK)
		}

		// CPU is in percent of one core.
		cores, unlimited := svcs[id].CPUs*float64(p.Replicas), svcs[id].CPUs == 0
		if unlimited && host != nil {
			cores = float64(host.Cores)
		}
		if cores > 0 {
			a := base
			a.percent, a.limit, a.unlimited = p.CPU/cores, cores, unlimited
			judge("cpu:"+id, a.percent >= float64(th.CPUPercent), time.Duration(th.CPUMinutes)*time.Minute, a, domain.EventCPUHigh, domain.EventCPUOK)
		}
	}
	for id, h := range s.hosts {
		p := h.points[len(h.points)-1]
		if !p.At.Equal(now) || p.DiskTotalBytes == 0 {
			continue
		}
		free := float64(p.DiskTotalBytes-min(p.DiskBytes, p.DiskTotalBytes)) / float64(p.DiskTotalBytes) * 100
		judge("disk:"+id, free < float64(th.DiskFreePercent), alertRecover,
			resourceAlert{serverID: id, percent: free, limit: float64(p.DiskTotalBytes)}, domain.EventDiskLow, domain.EventDiskOK)
	}
	for key, st := range s.alerts {
		if !seen[key] {
			st.since = time.Time{}
			if !st.alerted {
				delete(s.alerts, key)
			}
		}
	}
	// Forget alerted services and servers once their series are gone
	// (deleted, stopped for good, unreachable).
	for key := range s.alerts {
		kind, id, _ := strings.Cut(key, ":")
		if kind == "disk" && s.hosts[id] == nil || kind != "disk" && s.series[id] == nil {
			delete(s.alerts, key)
		}
	}
	return out
}

// alertResources sends the transitions of this round.
func (c *Core) alertResources(ctx context.Context, now time.Time, svcs map[string]domain.Service, servers []domain.Server) {
	th := c.resourceAlerts(ctx)
	for _, a := range c.stats.checkResources(now, th, svcs) {
		if a.serviceID == "" {
			name := a.serverID
			for _, sv := range servers {
				if sv.ID == a.serverID {
					name = sv.Name
				}
			}
			fields := []notify.Field{{Name: "Server", Value: name}, {Name: "Free", Value: fmt.Sprintf("%.1f%% of %s", a.percent, bytesIEC(uint64(a.limit)))}}
			e := notify.Event{Type: a.event, Level: notify.Error, Title: name + " is running out of disk space",
				Message: fmt.Sprintf("Less than %d%% free on the filesystem holding Docker's data. Clean up in System › Cleanup, or grow the disk.", th.DiskFreePercent), Fields: fields}
			if a.event == domain.EventDiskOK {
				e.Level, e.Title, e.Message = notify.Success, name+" has disk space again", fmt.Sprintf("Back above %d%% free.", th.DiskFreePercent)
			}
			c.Notify(e, "/servers/"+a.serverID, "")
			continue
		}
		svc := svcs[a.serviceID]
		project, err := c.store.GetProject(ctx, svc.ProjectID)
		if err != nil {
			continue
		}
		name := project.Name + "/" + svc.Name
		of := ""
		switch {
		case a.event == domain.EventMemoryHigh || a.event == domain.EventMemoryOK:
			of = "of its memory limit (" + bytesIEC(uint64(a.limit)) + ")"
			if a.unlimited {
				of = "of the server's memory (" + bytesIEC(uint64(a.limit)) + ", no limit set)"
			}
		case a.unlimited:
			of = fmt.Sprintf("of the server's %g cores (no limit set)", a.limit)
		default:
			of = fmt.Sprintf("of its CPU limit (%g cores)", a.limit)
		}
		fields := []notify.Field{{Name: "Project", Value: project.Name}, {Name: "Service", Value: svc.Name}, {Name: "Usage", Value: fmt.Sprintf("%.0f%% %s", a.percent, of)}}
		var e notify.Event
		switch a.event {
		case domain.EventMemoryHigh:
			e = notify.Event{Level: notify.Warning, Title: name + " is using a lot of memory",
				Message: fmt.Sprintf("Memory at %d%% or more %s for %d min.", th.MemoryPercent, of, th.MemoryMinutes)}
			if !a.unlimited {
				e.Message += " Past its limit it is killed."
			}
		case domain.EventMemoryOK:
			e = notify.Event{Level: notify.Success, Title: name + " memory is back to normal", Message: fmt.Sprintf("Below %d%% %s.", th.MemoryPercent, of)}
		case domain.EventCPUHigh:
			e = notify.Event{Level: notify.Warning, Title: name + " is using a lot of CPU",
				Message: fmt.Sprintf("CPU at %d%% or more %s for %d min.", th.CPUPercent, of, th.CPUMinutes)}
		case domain.EventCPUOK:
			e = notify.Event{Level: notify.Success, Title: name + " CPU is back to normal", Message: fmt.Sprintf("Below %d%% %s.", th.CPUPercent, of)}
		}
		e.Type, e.Fields = a.event, fields
		c.Notify(e, "/services/"+svc.ID, "")
	}
}

// notifyOOM reports a replica the kernel killed for memory. Keyed: a service
// stuck in a loop of kills is reported once per monitoring.NotifyCooldown.
func (c *Core) notifyOOM(ctx context.Context, serviceID, replica string) {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return
	}
	project, err := c.store.GetProject(ctx, svc.ProjectID)
	if err != nil {
		return
	}
	limit := "no memory limit is set, so the server itself ran out of memory"
	if svc.MemoryMB > 0 {
		limit = fmt.Sprintf("its memory limit is %d MiB", svc.MemoryMB)
	}
	c.Notify(notify.Event{
		Type:    domain.EventServiceOOMKilled,
		Level:   notify.Error,
		Title:   fmt.Sprintf("%s/%s ran out of memory", project.Name, svc.Name),
		Message: fmt.Sprintf("A process of replica %s was killed for using too much memory (%s). Further kills are not reported for %s.", replica, limit, monitoring.NotifyCooldown),
		Fields:  []notify.Field{{Name: "Project", Value: project.Name}, {Name: "Service", Value: svc.Name}},
	}, "/services/"+svc.ID, "oom:"+svc.ID)
}

// bytesIEC formats n like 1.5 GiB.
func bytesIEC(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
