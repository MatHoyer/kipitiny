package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// dumpTestDB runs a database service of kind against the local Docker
// daemon with the spec a deploy gives it, and waits for its healthcheck.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run DumpDocker
func dumpTestDB(t *testing.T, kind store.ServiceKind) (*Core, context.Context, store.Service, func(string) string) {
	t.Helper()
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	ctx := context.Background()
	dk, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	c := newTestCore(t, config.Config{DataDir: t.TempDir()})
	c.pool = docker.NewPool(dk, c.connectServer)
	if _, err := c.EncryptBackupTarget(ctx, store.LocalTargetID); err != nil {
		t.Fatal(err)
	}
	p, err := c.store.CreateProject(ctx, store.Project{Name: "dumptest", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := c.serviceFromInput(p.ID, ServiceInput{Name: "db", Kind: kind})
	if err != nil {
		t.Fatal(err)
	}
	svc.ServerID = store.LocalServerID
	if svc, err = c.store.CreateService(ctx, svc); err != nil {
		t.Fatal(err)
	}
	if err := c.store.SetCurrentDeployment(ctx, svc.ID, "D1"); err != nil {
		t.Fatal(err)
	}
	if err := dk.EnsureImage(ctx, svc.Image, ""); err != nil {
		t.Fatal(err)
	}
	cfg := &container.Config{Image: svc.Image, Labels: map[string]string{}}
	for k, v := range svc.Env {
		cfg.Env = append(cfg.Env, k+"="+v)
	}
	host := &container.HostConfig{Resources: container.Resources{Memory: int64(svc.MemoryMB) << 20}}
	applyDatabaseSpec(cfg, host, svc)
	// Not kipitiny.managed: a manager running on this daemon would remove
	// it as an orphan.
	cfg.Labels = map[string]string{docker.LabelProject: p.ID, docker.LabelService: svc.ID, docker.LabelDeploy: "D1"}
	id, err := dk.Run(ctx, client.ContainerCreateOptions{Name: "kipitiny-test-" + strings.ToLower(svc.ID), Config: cfg, HostConfig: host})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = dk.RemoveContainerAndVolumes(context.Background(), id)
		_ = removeServiceVolumes(context.Background(), dk, svc)
	})
	if err := dk.WaitHealthy(ctx, id, databaseReadyTimeout); err != nil {
		t.Fatal(err)
	}
	exec := func(cmd string) string {
		t.Helper()
		var out strings.Builder
		if err := dk.Exec(ctx, id, docker.ExecOptions{Cmd: []string{"sh", "-c", cmd}, Stdout: &out}); err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		return strings.TrimSpace(out.String())
	}
	return c, ctx, svc, exec
}

// dumpRoundTrip backs up, verifies and restores svc: seed writes the data,
// change alters it and read shows it.
func dumpRoundTrip(t *testing.T, c *Core, ctx context.Context, svc store.Service, seed, change, read func() string, tables int, rows int64) {
	t.Helper()
	before := seed()
	b, err := c.BackupService(ctx, svc.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	b = waitBackup(t, c, b.ID, func(b store.Backup) bool { return b.Status != store.OpRunning })
	if b.Status != store.OpSucceeded || b.Kind != store.BackupKindDump || b.ServiceKind != svc.Kind || !b.Encrypted || b.PGVersion == "" ||
		!strings.HasSuffix(b.ObjectKey, dumpExt(svc.Kind)+".age") {
		t.Fatalf("backup = %+v", b)
	}

	if _, err := c.VerifyBackup(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	b = waitBackup(t, c, b.ID, func(b store.Backup) bool { return b.VerifyStatus != store.OpRunning })
	if b.VerifyStatus != store.OpSucceeded || b.VerifyDetails.Tables != tables || b.VerifyDetails.Rows != rows || b.VerifyDetails.DBBytes == 0 {
		t.Fatalf("verification = %+v", b.Verification)
	}

	if got := change(); got == before {
		t.Fatalf("change did nothing: %q", got)
	}
	r, err := c.RestoreBackup(ctx, b.ID, "", svc.Name)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		rs, err := c.ListRestores(ctx, svc.ID)
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
	if got := read(); got != before {
		t.Fatalf("after restore = %q, want %q", got, before)
	}
}

func TestDumpDockerMySQL(t *testing.T) {
	for _, kind := range []store.ServiceKind{store.ServiceKindMySQL, store.ServiceKindMariaDB} {
		t.Run(string(kind), func(t *testing.T) {
			c, ctx, svc, exec := dumpTestDB(t, kind)
			client := mysqlBin(kind, "mysql")
			// As the app connects: its user, its database.
			app := func(sql string) string {
				return exec(`MYSQL_PWD="$MYSQL_PASSWORD" ` + client + ` -h 127.0.0.1 -u"$MYSQL_USER" -NB "$MYSQL_DATABASE" -e "` + strings.ReplaceAll(sql, "`", "\\`") + `"`)
			}
			read := func() string {
				return app("SELECT GROUP_CONCAT(v ORDER BY id) FROM items; SELECT COUNT(*) FROM v_items; SHOW TABLES")
			}
			dumpRoundTrip(t, c, ctx, svc,
				func() string {
					app(`CREATE TABLE items (id INT PRIMARY KEY, v TEXT, b BLOB);
INSERT INTO items VALUES (1, 'one', 0xdead), (2, 'two\nlines', NULL), (3, 'é', NULL);
CREATE TABLE ` + "`odd name`" + ` (x INT); INSERT INTO ` + "`odd name`" + ` VALUES (1), (2);
CREATE VIEW v_items AS SELECT id FROM items`)
					return read()
				},
				func() string {
					app("UPDATE items SET v = 'changed'; DELETE FROM items WHERE id = 3; CREATE TABLE extra (x INT)")
					return read()
				},
				read, 2, 5)
			if got := exec(`MYSQL_PWD="$MYSQL_ROOT_PASSWORD" ` + client + ` -uroot -NB -e "SHOW DATABASES LIKE '%restore%'"`); got != "" {
				t.Errorf("leftover databases: %s", got)
			}
		})
	}
}

func TestDumpDockerMongo(t *testing.T) {
	c, ctx, svc, exec := dumpTestDB(t, store.ServiceKindMongoDB)
	mongosh := func(js string) string {
		return exec(`mongosh --quiet "mongodb://$MONGO_INITDB_ROOT_USERNAME:$MONGO_INITDB_ROOT_PASSWORD@127.0.0.1/app?authSource=admin" --eval '` + js + `'`)
	}
	read := func() string {
		return mongosh(`print(JSON.stringify(db.items.find({}, { _id: 0 }).sort({ n: 1 }).toArray()), db.getSiblingDB("other").logs.countDocuments())`)
	}
	dumpRoundTrip(t, c, ctx, svc,
		func() string {
			mongosh(`db.items.insertMany([{ n: 1, v: "one" }, { n: 2, v: "two" }]); db.getSiblingDB("other").logs.insertMany([{ a: 1 }, { a: 2 }, { a: 3 }])`)
			return read()
		},
		func() string {
			mongosh(`db.items.updateMany({}, { $set: { v: "changed" } }); db.getSiblingDB("other").logs.deleteMany({})`)
			return read()
		},
		read, 2, 5)
}
