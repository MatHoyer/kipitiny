package core

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/MatHoyer/kipitiny/internal/notify"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// healthGrace is how long a service may have no healthy replica before it is
// reported: a restart or a slow start shouldn't page anyone.
const healthGrace = 2 * time.Minute

// healthState remembers, per service, since when no replica is healthy. In
// memory: after a restart an ongoing problem is reported again.
type healthState struct {
	mu   sync.Mutex
	svcs map[string]*svcHealth
}

type svcHealth struct {
	serverID string
	badSince time.Time // zero while healthy
	alerted  bool
	detail   string
}

// healthOf judges a deployed, running service from its containers: healthy
// when a replica of the current deployment runs and passes its healthcheck
// (or has none). judged is false when nothing is expected to run.
func healthOf(svc store.Service, cts []container.Summary) (judged, healthy bool, detail string) {
	if svc.CurrentDeploymentID == "" || svc.Stopped {
		return false, false, ""
	}
	var states []string
	for _, ct := range cts {
		if !isActive(ct, svc) {
			continue
		}
		health := container.NoHealthcheck
		if ct.Health != nil && ct.Health.Status != "" {
			health = ct.Health.Status
		}
		if ct.State == container.StateRunning && (health == container.NoHealthcheck || health == container.Healthy) {
			return true, true, ""
		}
		state := string(ct.State)
		if ct.State == container.StateRunning {
			state = string(health)
		} else if ct.Status != "" {
			state = strings.ToLower(ct.Status)
		}
		states = append(states, containerName(ct)+": "+state)
	}
	if len(states) == 0 {
		return true, false, "no replica is running"
	}
	slices.Sort(states)
	return true, false, strings.Join(states, ", ")
}

// observe updates the record with a judgement at now and says what to send.
func (h *svcHealth) observe(healthy bool, detail string, now time.Time) (down, up bool) {
	if healthy {
		up = h.alerted
		h.badSince, h.alerted, h.detail = time.Time{}, false, ""
		return false, up
	}
	if h.badSince.IsZero() {
		h.badSince = now
	}
	h.detail = detail
	if !h.alerted && now.Sub(h.badSince) >= healthGrace {
		h.alerted = true
		return true, false
	}
	return false, false
}

// trackHealth judges a service from the containers the reconciler listed and
// notifies when it has had no healthy replica for healthGrace, then once it
// recovers.
func (c *Core) trackHealth(project store.Project, svc store.Service, cts []container.Summary, now time.Time) {
	// A deploy, restart or restore is working on it: its own outcome is
	// reported, and replicas come and go meanwhile.
	unlock, err := c.lockService(svc.ID)
	if err != nil {
		return
	}
	unlock()

	judged, healthy, detail := healthOf(svc, cts)
	c.health.mu.Lock()
	if c.health.svcs == nil {
		c.health.svcs = map[string]*svcHealth{}
	}
	h := c.health.svcs[svc.ID]
	if h == nil {
		h = &svcHealth{serverID: svc.ServerID}
		c.health.svcs[svc.ID] = h
	}
	if !judged {
		// Stopped on purpose: nothing to report, now or when it comes back.
		*h = svcHealth{serverID: svc.ServerID}
		c.health.mu.Unlock()
		return
	}
	since := h.badSince
	down, up := h.observe(healthy, detail, now)
	c.health.mu.Unlock()

	name := project.Name + "/" + svc.Name
	fields := []notify.Field{{Name: "Project", Value: project.Name}, {Name: "Service", Value: svc.Name}}
	switch {
	case down:
		c.notify(notify.Event{
			Type:    EventServiceUnhealthy,
			Level:   notify.Error,
			Title:   name + " is unhealthy",
			Message: fmt.Sprintf("No healthy replica for %s (%s).", healthGrace, detail),
			Fields:  fields,
		}, "/services/"+svc.ID, "")
	case up:
		c.notify(notify.Event{
			Type:    EventServiceHealthy,
			Level:   notify.Success,
			Title:   name + " is healthy again",
			Message: "Unhealthy for " + now.Sub(since).Round(time.Second).String() + ".",
			Fields:  fields,
		}, "/services/"+svc.ID, "")
	}
}

// forgetHealth drops the records of a server's services that are gone.
func (c *Core) forgetHealth(serverID string, known map[string]bool) {
	c.health.mu.Lock()
	defer c.health.mu.Unlock()
	for id, h := range c.health.svcs {
		if h.serverID == serverID && !known[id] {
			delete(c.health.svcs, id)
		}
	}
}
