package data

import (
	"slices"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// backupsEvery makes n backups, newest first, spaced by step from start.
func backupsEvery(start time.Time, step time.Duration, n int) []domain.Backup {
	bs := make([]domain.Backup, n)
	for i := range n {
		t := start.Add(-time.Duration(i) * step)
		bs[i] = domain.Backup{ID: t.Format(time.RFC3339), CreatedAt: t}
	}
	return bs
}

func kept(bs []domain.Backup, sc domain.BackupSchedule) []string {
	keep := retain(bs, sc)
	var ids []string
	for _, b := range bs {
		if keep[b.ID] {
			ids = append(ids, b.ID)
		}
	}
	return ids
}

func TestRetain(t *testing.T) {
	// Every 6 hours for 120 days, newest 2026-09-30 18:00 UTC (a Wednesday).
	now := time.Date(2026, 9, 30, 18, 0, 0, 0, time.UTC)
	bs := backupsEvery(now, 6*time.Hour, 4*120)

	if got := kept(bs, domain.BackupSchedule{}); len(got) != len(bs) {
		t.Errorf("no rules should keep everything, kept %d/%d", len(got), len(bs))
	}

	got := kept(bs, domain.BackupSchedule{KeepLast: 3})
	if !slices.Equal(got, []string{"2026-09-30T18:00:00Z", "2026-09-30T12:00:00Z", "2026-09-30T06:00:00Z"}) {
		t.Errorf("keep last 3: %v", got)
	}

	got = kept(bs, domain.BackupSchedule{KeepDaily: 3})
	if !slices.Equal(got, []string{"2026-09-30T18:00:00Z", "2026-09-29T18:00:00Z", "2026-09-28T18:00:00Z"}) {
		t.Errorf("keep daily 3 (newest of each day): %v", got)
	}

	// ISO weeks: 2026-09-28 is a Monday, so the current week has 3 days.
	got = kept(bs, domain.BackupSchedule{KeepWeekly: 2})
	if !slices.Equal(got, []string{"2026-09-30T18:00:00Z", "2026-09-27T18:00:00Z"}) {
		t.Errorf("keep weekly 2: %v", got)
	}

	got = kept(bs, domain.BackupSchedule{KeepMonthly: 3})
	if !slices.Equal(got, []string{"2026-09-30T18:00:00Z", "2026-08-31T18:00:00Z", "2026-07-31T18:00:00Z"}) {
		t.Errorf("keep monthly 3: %v", got)
	}

	// Rules combine; overlaps count once.
	// last 2 + daily adds 29..24 + weekly adds 20, 13 (27 is already daily)
	// + monthly adds Aug, Jul, Jun (the data starts in June).
	got = kept(bs, domain.BackupSchedule{KeepLast: 2, KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6})
	want := []string{
		"2026-09-30T18:00:00Z", "2026-09-30T12:00:00Z",
		"2026-09-29T18:00:00Z", "2026-09-28T18:00:00Z", "2026-09-27T18:00:00Z",
		"2026-09-26T18:00:00Z", "2026-09-25T18:00:00Z", "2026-09-24T18:00:00Z",
		"2026-09-20T18:00:00Z", "2026-09-13T18:00:00Z",
		"2026-08-31T18:00:00Z", "2026-07-31T18:00:00Z", "2026-06-30T18:00:00Z",
	}
	if !slices.Equal(got, want) {
		t.Errorf("combined rules:\n got %v\nwant %v", got, want)
	}
	// Months older than the data are simply absent.
	got = kept(bs[:4], domain.BackupSchedule{KeepMonthly: 12})
	if len(got) != 1 {
		t.Errorf("single month of data: %v", got)
	}
}
