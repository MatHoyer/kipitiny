package core

import (
	"context"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

// The thresholds servers saves are the ones monitoring shows.
func TestResourceAlertsInNotifications(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	ok := domain.ResourceAlerts{MemoryPercent: 80, MemoryMinutes: 2, CPUPercent: 95, CPUMinutes: 15, DiskFreePercent: 20}
	if _, err := c.SetResourceAlerts(ctx, ok); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Notifications(ctx); v.Thresholds != ok {
		t.Errorf("thresholds = %+v", v.Thresholds)
	}
}
