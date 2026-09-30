package core

import (
	"fmt"
	"time"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// retain returns the IDs to keep among a schedule's successful backups
// (newest first), using grandfather-father-son rules. Periods are in UTC.
// With no rule set, everything is kept.
func retain(backups []store.Backup, sc store.BackupSchedule) map[string]bool {
	keep := map[string]bool{}
	if sc.KeepLast == 0 && sc.KeepDaily == 0 && sc.KeepWeekly == 0 && sc.KeepMonthly == 0 {
		for _, b := range backups {
			keep[b.ID] = true
		}
		return keep
	}
	for i, b := range backups {
		if i < sc.KeepLast {
			keep[b.ID] = true
		}
	}
	rules := []struct {
		n      int
		period func(time.Time) string
	}{
		{sc.KeepDaily, func(t time.Time) string { return t.Format(time.DateOnly) }},
		{sc.KeepWeekly, func(t time.Time) string { y, w := t.ISOWeek(); return fmt.Sprintf("%d-W%02d", y, w) }},
		{sc.KeepMonthly, func(t time.Time) string { return t.Format("2006-01") }},
	}
	for _, r := range rules {
		seen := map[string]bool{}
		for _, b := range backups { // newest first: the first of a period is its newest
			p := r.period(b.CreatedAt.UTC())
			if seen[p] {
				continue
			}
			if len(seen) == r.n {
				break
			}
			seen[p] = true
			keep[b.ID] = true
		}
	}
	return keep
}
