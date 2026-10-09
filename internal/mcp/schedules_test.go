package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestScheduleTools(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p, _ := e.st.CreateProject(ctx, store.Project{Name: "shop"})
	db, err := e.st.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "db", Kind: store.ServiceKindPostgres, Image: "postgres:17", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "cache", Kind: store.ServiceKindRedis, Image: "redis:7", Replicas: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "worker", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1}); err != nil {
		t.Fatal(err)
	}
	read, admin := e.session(store.ScopeRead), e.session(store.ScopeAdmin)

	for name, args := range map[string]map[string]any{
		"set_backup_schedule":    {"service": "shop/db", "cron": "@daily"},
		"delete_backup_schedule": {"service": "shop/db", "schedule_id": "x"},
		"set_uptime_check":       {"service": "shop/worker"},
		"delete_uptime_check":    {"service": "shop/worker"},
	} {
		if out, isErr := e.call(read, name, args); !isErr || !strings.Contains(out, "needs the admin scope") {
			t.Errorf("%s with a read token: %s", name, out)
		}
	}

	if out, isErr := e.call(admin, "set_backup_schedule", map[string]any{"service": "shop/db"}); !isErr || !strings.Contains(out, "needs a cron") {
		t.Errorf("schedule without cron: %s", out)
	}
	out, isErr := e.call(admin, "set_backup_schedule", map[string]any{"service": "shop/db", "cron": "0 3 * * *"})
	var sc core.ScheduleView
	if err := json.Unmarshal([]byte(out), &sc); isErr || err != nil || sc.KeepDaily != 7 || sc.KeepMonthly != 6 || !sc.Enabled || sc.TargetID != store.LocalTargetID {
		t.Fatalf("new schedule: %s", out)
	}
	// An update keeps what it doesn't set.
	out, isErr = e.call(admin, "set_backup_schedule", map[string]any{"service": "shop/db", "schedule_id": sc.ID, "keep_daily": 3, "enabled": false})
	if isErr || !strings.Contains(out, `"cron":"0 3 * * *"`) || !strings.Contains(out, `"keepDaily":3`) || !strings.Contains(out, `"enabled":false`) || !strings.Contains(out, `"keepWeekly":4`) {
		t.Errorf("updated schedule: %s", out)
	}
	if out, isErr = e.call(read, "list_backup_schedules", map[string]any{"service": "shop/db"}); isErr || !strings.Contains(out, sc.ID) {
		t.Errorf("list_backup_schedules: %s", out)
	}
	// A schedule ID only reaches its own service's schedules.
	for _, name := range []string{"set_backup_schedule", "delete_backup_schedule"} {
		if out, isErr = e.call(admin, name, map[string]any{"service": "shop/cache", "schedule_id": sc.ID}); !isErr || !strings.Contains(out, "not found") {
			t.Errorf("%s through another service: %s", name, out)
		}
	}
	if out, isErr = e.call(admin, "delete_backup_schedule", map[string]any{"service": "shop/db", "schedule_id": sc.ID}); isErr {
		t.Errorf("delete_backup_schedule: %s", out)
	}
	if scs, _ := e.c.ListBackupSchedules(ctx, db.ID); len(scs) != 0 {
		t.Errorf("schedule left: %+v", scs)
	}

	if out, isErr = e.call(admin, "set_uptime_check", map[string]any{"service": "shop/worker"}); !isErr || !strings.Contains(out, "public domain") {
		t.Errorf("uptime on a private app: %s", out)
	}
	if _, err := e.st.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1,
		Domain: "shop.example.com", Port: 80}); err != nil {
		t.Fatal(err)
	}
	if out, isErr = e.call(admin, "set_uptime_check", map[string]any{"service": "shop/web", "path": "/health", "interval_sec": 10}); !isErr || !strings.Contains(out, "interval") {
		t.Errorf("uptime with a short interval: %s", out)
	}
	if out, isErr = e.call(admin, "set_uptime_check", map[string]any{"service": "shop/web", "path": "/health"}); isErr || !strings.Contains(out, `"path":"/health"`) || !strings.Contains(out, `"intervalSec":60`) {
		t.Errorf("set_uptime_check: %s", out)
	}
	if out, isErr = e.call(admin, "delete_uptime_check", map[string]any{"service": "shop/web"}); isErr {
		t.Errorf("delete_uptime_check: %s", out)
	}
}
