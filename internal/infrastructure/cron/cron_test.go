package cron

import (
	"testing"
	"time"
)

func TestCronParser(t *testing.T) {
	for expr, ok := range map[string]bool{
		"0 3 * * *":                      true,
		"@daily":                         true,
		"@every 6h":                      true,
		"CRON_TZ=Europe/Paris 0 3 * * *": true,
		"every day":                      false,
		"0 3 * *":                        false,
		"* * * * * *":                    false, // no seconds field
	} {
		_, err := Cron{}.Next(expr, time.Now())
		if (err == nil) != ok {
			t.Errorf("Parse(%q) err = %v, want ok=%v", expr, err, ok)
		}
	}
}

func TestSchedulerAdd(t *testing.T) {
	s := Cron{}.NewScheduler()
	if _, err := s.Add("every day", func() {}, false); err == nil {
		t.Error("an invalid schedule was added")
	}
	a, err := s.Add("@daily", func() {}, true)
	if err != nil || a == 0 {
		t.Fatalf("add: %d, %v", a, err)
	}
	if b, err := s.Add("0 3 * * *", func() {}, false); err != nil || b == a {
		t.Fatalf("second entry: %d, %v", b, err)
	}
	s.Remove(a)
	if next := s.Next(a); !next.IsZero() {
		t.Errorf("removed entry runs at %s", next)
	}
}
