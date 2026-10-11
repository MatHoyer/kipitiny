package core

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// TestPostGISDocker backs up a PostGIS database and checks that restore tests
// and restores need a server with its extensions.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run PostGISDocker
func TestPostGISDocker(t *testing.T) {
	const image = "postgis/postgis:17-3.6-alpine"
	// dumpTestDB's database runs the default image, without PostGIS.
	c, ctx, plain, _ := dumpTestDB(t, store.ServiceKindPostgres)
	gis, gisExec := startTestDB(t, c, ctx, plain.ProjectID, ServiceInput{Name: "gis", Kind: store.ServiceKindPostgres, Image: image})

	psql := func(sql string) string {
		return gisExec("psql -U " + gis.Env[pgUser] + " -d " + gis.Env[pgDatabase] + " -tAc \"" + sql + "\"")
	}
	psql("CREATE TABLE places (id int PRIMARY KEY, geom geometry(Point, 4326)); " +
		"INSERT INTO places VALUES (1, ST_SetSRID(ST_MakePoint(2.35, 48.85), 4326))")
	read := func() string { return psql("SELECT coalesce(string_agg(ST_AsText(geom), ','), '') FROM places") }
	if got := read(); got != "POINT(2.35 48.85)" {
		t.Fatalf("seeded = %q", got)
	}

	b, err := c.BackupService(ctx, gis.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	b = waitBackup(t, c, b.ID, func(b store.Backup) bool { return b.Status != store.OpRunning })
	if b.Status != store.OpSucceeded || b.Image != image || !slices.Contains(b.Extensions, "postgis") || slices.Contains(b.Extensions, "plpgsql") {
		t.Fatalf("backup = %+v", b)
	}

	verify := func() {
		t.Helper()
		if _, err := c.VerifyBackup(ctx, b.ID); err != nil {
			t.Fatal(err)
		}
		v := waitBackup(t, c, b.ID, func(b store.Backup) bool { return b.VerifyStatus != store.OpRunning })
		if v.VerifyStatus != store.OpSucceeded || v.VerifyDetails.Rows < 1 {
			t.Fatalf("verification = %+v", v.Verification)
		}
	}
	// The image creates PostGIS's extensions in its database: the restore
	// test must not trip on them.
	verify()

	// Into a server without PostGIS: refused before anything starts.
	if _, err := c.RestoreBackup(ctx, b.ID, plain.ID, plain.Name); err == nil || !strings.Contains(err.Error(), "postgis") {
		t.Fatalf("restore into plain postgres: %v", err)
	}

	psql("DELETE FROM places")
	r, err := c.RestoreBackup(ctx, b.ID, "", gis.Name)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		rs, err := c.ListRestores(ctx, gis.ID)
		if err != nil {
			t.Fatal(err)
		}
		if rs[0].ID == r.ID && rs[0].Status != store.OpRunning {
			if rs[0].Status != store.OpSucceeded {
				t.Fatalf("restore = %+v", rs[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restore did not finish")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if got := read(); got != "POINT(2.35 48.85)" {
		t.Fatalf("after restore = %q", got)
	}

	// Service gone: the restore test runs the image that made the backup,
	// not postgres:17-alpine.
	if err := c.store.DeleteService(ctx, gis.ID); err != nil {
		t.Fatal(err)
	}
	verify()
}
