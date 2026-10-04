package core

import (
	"math"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestMemoryUsedLeavesOutPageCache(t *testing.T) {
	v2 := container.MemoryStats{Usage: 300, Stats: map[string]uint64{"inactive_file": 100}}
	if got := memoryUsed(v2); got != 200 {
		t.Errorf("cgroup v2: %d", got)
	}
	v1 := container.MemoryStats{Usage: 300, Stats: map[string]uint64{"total_inactive_file": 50, "inactive_file": 100}}
	if got := memoryUsed(v1); got != 250 {
		t.Errorf("cgroup v1: %d", got)
	}
	if got := memoryUsed(container.MemoryStats{Usage: 300}); got != 300 {
		t.Errorf("no stats: %d", got)
	}
}

func TestRecordRatesAndHistory(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	svcs := map[string]store.Service{"s1": {ID: "s1", ProjectID: "p1", ServerID: "local", MemoryMB: 64}}
	owner := map[string]string{"a": "s1", "b": "s1"}
	var st statsState

	// First round: memory only, nothing to compute rates from.
	st.record(t0, map[string]sample{
		"a": {read: t0, cpu: 0, mem: 10 << 20, rx: 1000, tx: 0},
		"b": {read: t0, cpu: 0, mem: 20 << 20},
	}, owner, svcs)
	got := st.service("s1", t0)
	if got.Current == nil || got.Current.Replicas != 2 || got.Current.MemoryBytes != 30<<20 || got.Current.CPU != 0 {
		t.Fatalf("first round: %+v", got.Current)
	}
	if got.Current.MemoryLimitBytes != 128<<20 {
		t.Errorf("limit: %d", got.Current.MemoryLimitBytes)
	}

	// Ten seconds later: a used 5 s of CPU (50 %), b 10 s (100 %); a got
	// 10 kB/s. b's counter reset reads as no traffic, not a huge number.
	t1 := t0.Add(10 * time.Second)
	st.record(t1, map[string]sample{
		"a": {read: t1, cpu: 5e9, mem: 10 << 20, rx: 101000},
		"b": {read: t1, cpu: 10e9, mem: 20 << 20},
	}, owner, svcs)
	got = st.service("s1", t1)
	if c := got.Current; c == nil || math.Abs(c.CPU-150) > 1e-9 || c.NetRx != 10000 || c.NetTx != 0 {
		t.Fatalf("second round: %+v", got.Current)
	}
	if len(got.History) != 2 {
		t.Errorf("history: %d points", len(got.History))
	}
	// Each replica on its own.
	a, b := got.Containers["a"], got.Containers["b"]
	if a.Current == nil || math.Abs(a.Current.CPU-50) > 1e-9 || a.Current.MemoryBytes != 10<<20 || a.Current.NetRx != 10000 || a.Current.MemoryLimitBytes != 64<<20 || len(a.History) != 2 {
		t.Errorf("container a: %+v", a)
	}
	if b.Current == nil || math.Abs(b.Current.CPU-100) > 1e-9 || b.Current.MemoryBytes != 20<<20 {
		t.Errorf("container b: %+v", b)
	}
	if all := st.current(t1); all["s1"].ProjectID != "p1" || all["s1"].ServerID != "local" {
		t.Errorf("current: %+v", all)
	}

	// The history keeps a fixed window.
	at := t1
	for range statsHistory + 5 {
		at = at.Add(statsEvery)
		st.record(at, map[string]sample{"a": {read: at, mem: 1}}, owner, svcs)
	}
	if n := len(st.service("s1", at).History); n != statsHistory {
		t.Errorf("history: %d points, want %d", n, statsHistory)
	}

	// Not running any more: no current value, history kept for a while,
	// then forgotten.
	at = at.Add(statsEvery)
	st.record(at, map[string]sample{}, owner, svcs)
	if got := st.service("s1", at); got.Current != nil || len(got.History) == 0 || got.Containers["a"].Current != nil {
		t.Errorf("stopped: %+v", got)
	}
	if len(st.current(at)) != 0 {
		t.Error("stopped service still has a current value")
	}
	at = at.Add(statsEvery * statsHistory)
	st.record(at, map[string]sample{}, owner, svcs)
	if got := st.service("s1", at); len(got.History) != 0 || len(got.Containers) != 0 {
		t.Errorf("not forgotten: %+v", got)
	}
}
