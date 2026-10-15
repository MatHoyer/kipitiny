package core

import (
	"testing"
	"time"
)

const hostOut = `== stat
cpu  523158 6039 121440 14835813 15621 0 5117 0 0 0
cpu0 72703 1134 13648 879262 1099 0 709 0 0 0
cpu1 72703 1134 13648 879262 1099 0 709 0 0 0
intr 1 2 3
== meminfo
MemTotal:       31415988 kB
MemFree:         1000000 kB
MemAvailable:   23848264 kB
SwapTotal:       8388604 kB
SwapFree:        8388600 kB
== loadavg
0.38 0.51 0.72 4/2477 49
== uptime
9747.59 148358.15
== df
Filesystem           1024-blocks    Used Available Capacity Mounted on
overlay              1920641288 446811120 1376193204  25% /
`

func TestParseHost(t *testing.T) {
	h, err := parseHost(hostOut)
	if err != nil {
		t.Fatal(err)
	}
	if h.Cores != 2 || h.Load != [3]float64{0.38, 0.51, 0.72} || h.UptimeSec != 9747 {
		t.Errorf("cores, load, uptime: %+v", h.HostUsage)
	}
	if h.MemoryTotalBytes != 31415988<<10 || h.MemoryBytes != (31415988-23848264)<<10 || h.SwapBytes != 4<<10 {
		t.Errorf("memory: %+v", h.HostUsage)
	}
	if h.DiskBytes != 446811120<<10 || h.DiskTotalBytes != (446811120+1376193204)<<10 {
		t.Errorf("disk: %+v", h.HostUsage)
	}
	if h.total != 523158+6039+121440+14835813+15621+5117 || h.busy != 523158+6039+121440+5117 {
		t.Errorf("cpu counters: %d/%d", h.busy, h.total)
	}
	if _, err := parseHost("sh: df: not found\n"); err == nil {
		t.Error("parsed nothing without an error")
	}
}

func TestRecordHosts(t *testing.T) {
	var s statsState
	t0 := time.Now()
	s.last = t0
	s.recordHosts(t0, map[string]hostSample{"local": {busy: 100, total: 1000}})
	s.last = t0.Add(statsEvery)
	s.recordHosts(s.last, map[string]hostSample{"local": {busy: 350, total: 2000}})
	st := s.host("local", s.last)
	if len(st.History) != 2 || st.History[0].CPU != 0 || st.Current == nil || st.Current.CPU != 25 {
		t.Errorf("stats: %+v", st)
	}
	if st := s.host("local", s.last.Add(time.Minute)); st.Current != nil {
		t.Error("stale point is current")
	}
}
