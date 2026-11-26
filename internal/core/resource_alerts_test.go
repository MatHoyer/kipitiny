package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/store"
)

func TestAlertStateDebounce(t *testing.T) {
	var a alertState
	t0 := time.Unix(0, 0)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	steps := []struct {
		sec        int
		over       bool
		fire, recv bool
	}{
		{0, true, false, false},
		{200, true, false, false},
		{250, false, false, false}, // a dip restarts the wait
		{260, true, false, false},
		{559, true, false, false},
		{560, true, true, false}, // 5 min over
		{600, true, false, false},
		{610, false, false, false},
		{650, true, false, false}, // a spike back up keeps it alerted
		{660, false, false, false},
		{720, false, false, true}, // 1 min back below
		{730, false, false, false},
	}
	for _, s := range steps {
		fire, recv := a.observe(s.over, at(s.sec), 5*time.Minute, time.Minute)
		if fire != s.fire || recv != s.recv {
			t.Fatalf("at %ds over=%v: fire=%v recovered=%v, want %v %v", s.sec, s.over, fire, recv, s.fire, s.recv)
		}
	}
}

func TestCheckResources(t *testing.T) {
	th := defaultResourceAlerts()
	svcs := map[string]store.Service{
		"limited":   {ID: "limited", ServerID: "s1", MemoryMB: 100, CPUs: 0.5},
		"unlimited": {ID: "unlimited", ServerID: "s1"},
	}
	var s statsState
	round := func(now time.Time, limitedMem, unlimitedCPU float64, diskUsed uint64) []resourceAlert {
		s.series = map[string]*series{
			"limited":   {serverID: "s1", points: []Usage{{At: now, Replicas: 1, MemoryBytes: uint64(limitedMem * (1 << 20)), MemoryLimitBytes: 100 << 20, CPU: 10}}},
			"unlimited": {serverID: "s1", points: []Usage{{At: now, Replicas: 2, MemoryBytes: 1 << 30, CPU: unlimitedCPU}}},
		}
		s.hosts = map[string]*hostSeries{"s1": {points: []HostUsage{{At: now, Cores: 4, MemoryTotalBytes: 8 << 30, DiskBytes: diskUsed, DiskTotalBytes: 100}}}}
		return s.checkResources(now, th, svcs)
	}
	events := func(as []resourceAlert) map[string]resourceAlert {
		m := map[string]resourceAlert{}
		for _, a := range as {
			m[a.event] = a
		}
		return m
	}
	t0 := time.Unix(1000, 0)
	// 95 MiB of 100, 380% of 4 cores, 95% of the disk used.
	for i := 0; i < 30; i++ {
		if got := round(t0.Add(time.Duration(i)*10*time.Second), 95, 380, 95); len(got) != 0 && i < 6 {
			t.Fatalf("round %d: %+v before any wait is over", i, got)
		}
	}
	got := events(round(t0.Add(300*time.Second), 95, 380, 95))
	if a, ok := got[EventMemoryHigh]; !ok || a.unlimited || a.percent < 94 || a.limit != 100<<20 {
		t.Errorf("memory_high after 5 min: %+v", got)
	}
	if _, ok := got[EventCPUHigh]; ok {
		t.Errorf("cpu_high before 10 min: %+v", got)
	}
	for i := 31; i < 60; i++ {
		round(t0.Add(time.Duration(i)*10*time.Second), 95, 380, 95)
	}
	got = events(round(t0.Add(600*time.Second), 95, 380, 95))
	if a, ok := got[EventCPUHigh]; !ok || !a.unlimited || a.limit != 4 || a.percent != 95 {
		t.Errorf("cpu_high of the host's cores after 10 min: %+v", got)
	}
	// Back to normal: recoveries after a minute.
	for i := 61; i < 67; i++ {
		if got := round(t0.Add(time.Duration(i)*10*time.Second), 10, 10, 10); len(got) != 0 {
			t.Fatalf("round %d: %+v before a minute below", i, got)
		}
	}
	got = events(round(t0.Add(670*time.Second), 10, 10, 10))
	for _, e := range []string{EventMemoryOK, EventCPUOK, EventDiskOK} {
		if _, ok := got[e]; !ok {
			t.Errorf("no %s: %+v", e, got)
		}
	}
	// The disk_low was sent a minute in.
	s = statsState{}
	round(t0, 0, 0, 95)
	if got := events(round(t0.Add(time.Minute), 0, 0, 95)); got[EventDiskLow].percent != 5 {
		t.Errorf("disk_low: %+v", got)
	}
	// Unlimited memory is judged against the host: 1 GiB of 8 isn't high.
	if _, ok := s.alerts["mem:unlimited"]; ok && s.alerts["mem:unlimited"].alerted {
		t.Error("unlimited memory alerted")
	}
	// A service gone from the stats is forgotten.
	delete(s.series, "limited")
	s.checkResources(t0.Add(2*time.Minute), th, svcs)
	if _, ok := s.alerts["mem:limited"]; ok {
		t.Error("state of a vanished service kept")
	}
}

func TestSetResourceAlerts(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	if got := c.resourceAlerts(ctx); got != defaultResourceAlerts() {
		t.Errorf("defaults = %+v", got)
	}
	ok := ResourceAlerts{MemoryPercent: 80, MemoryMinutes: 2, CPUPercent: 95, CPUMinutes: 15, DiskFreePercent: 20}
	for i, bad := range []func(*ResourceAlerts){
		func(a *ResourceAlerts) { a.MemoryPercent = 0 },
		func(a *ResourceAlerts) { a.CPUPercent = 101 },
		func(a *ResourceAlerts) { a.MemoryMinutes = 0 },
		func(a *ResourceAlerts) { a.CPUMinutes = 61 },
		func(a *ResourceAlerts) { a.DiskFreePercent = 100 },
	} {
		a := ok
		bad(&a)
		if _, err := c.SetResourceAlerts(ctx, a); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: %v", i, err)
		}
	}
	if _, err := c.SetResourceAlerts(ctx, ok); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Notifications(ctx); v.Thresholds != ok {
		t.Errorf("thresholds = %+v", v.Thresholds)
	}
}
