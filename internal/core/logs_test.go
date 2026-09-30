package core

import (
	"testing"
	"time"
)

func TestParseLogLine(t *testing.T) {
	l := parseLogLine("web-1", "2026-09-30T18:56:55.745123456Z GET / 200")
	want := time.Date(2026, 9, 30, 18, 56, 55, 745123456, time.UTC)
	if !l.Time.Equal(want) || l.Text != "GET / 200" || l.Container != "web-1" {
		t.Errorf("got %+v", l)
	}
	if l := parseLogLine("web-1", "no timestamp here"); !l.Time.IsZero() || l.Text != "no timestamp here" {
		t.Errorf("untimestamped: got %+v", l)
	}
	if l := parseLogLine("web-1", "2026-09-30T18:56:55Z "); l.Text != "" || l.Time.IsZero() {
		t.Errorf("empty line: got %+v", l)
	}
}

func TestSortLinesMergesReplicas(t *testing.T) {
	at := func(s int) time.Time { return time.Unix(1700000000+int64(s), 0) }
	lines := []LogLine{
		{Container: "a", Time: at(1), Text: "a1"},
		{Container: "a", Time: at(3), Text: "a3"},
		{Container: "a", Time: at(3), Text: "a3bis"},
		{Container: "b", Time: at(2), Text: "b2"},
		{Container: "b", Time: at(4), Text: "b4"},
	}
	sortLines(lines)
	var got []string
	for _, l := range lines {
		got = append(got, l.Text)
	}
	want := []string{"a1", "b2", "a3", "a3bis", "b4"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
