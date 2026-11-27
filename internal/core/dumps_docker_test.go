package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
)

// dumpTestDB runs a database service of kind against the local Docker
// daemon with the spec a deploy gives it, and waits for its healthcheck.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run DumpDocker
func dumpTestDB(t *testing.T, kind domain.ServiceKind) (*Core, context.Context, domain.Service, func(string) string) {
	t.Helper()
	c, ctx, projectID := dumpTestCore(t)
	svc, exec := startTestDB(ctx, t, c, projectID, ServiceInput{Name: "db", Kind: kind})
	return c, ctx, svc, exec
}

// dumpTestCore is a core on the local Docker daemon with a dumptest project.
func dumpTestCore(t *testing.T) (*Core, context.Context, string) {
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
	if _, err := c.EncryptBackupTarget(ctx, domain.LocalTargetID, ""); err != nil {
		t.Fatal(err)
	}
	p, err := c.store.CreateProject(ctx, domain.Project{Name: "dumptest", ServerID: domain.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	return c, ctx, p.ID
}

// startTestDB creates the database service in in project projectID and
// runs it like a deploy would; exec runs a shell command in its container.
func startTestDB(ctx context.Context, t *testing.T, c *Core, projectID string, in ServiceInput) (domain.Service, func(string) string) {
	t.Helper()
	dk := c.dockerFor(domain.LocalServerID)
	svc, err := c.serviceFromInput(projectID, in)
	if err != nil {
		t.Fatal(err)
	}
	svc.ServerID = domain.LocalServerID
	if svc, err = c.store.CreateService(ctx, svc); err != nil {
		t.Fatal(err)
	}
	if err := c.store.SetCurrentDeployment(ctx, svc.ID, "D1"); err != nil {
		t.Fatal(err)
	}
	if err := dk.EnsureImage(ctx, svc.Image, "", ""); err != nil {
		t.Fatal(err)
	}
	cfg := &container.Config{Image: svc.Image, Labels: map[string]string{}}
	for k, v := range svc.Env {
		cfg.Env = append(cfg.Env, k+"="+v)
	}
	host := &container.HostConfig{Resources: container.Resources{Memory: int64(svc.MemoryMB) << 20}}
	data.ApplyDatabaseSpec(cfg, host, svc)
	// Not kipitiny.managed: a manager running on this daemon would remove
	// it as an orphan.
	cfg.Labels = map[string]string{docker.LabelProject: projectID, docker.LabelService: svc.ID, docker.LabelDeploy: "D1"}
	id, err := dk.Run(ctx, client.ContainerCreateOptions{Name: "kipitiny-test-" + strings.ToLower(svc.ID), Config: cfg, HostConfig: host})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = dk.RemoveContainerAndVolumes(context.Background(), id)
		_ = data.RemoveServiceVolumes(context.Background(), dk, svc)
	})
	if err := dk.WaitHealthy(ctx, id, data.DatabaseReadyTimeout); err != nil {
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
	return svc, exec
}

// dumpRoundTrip backs up, verifies and restores svc: seed writes the data,
// change alters it and read shows it.
func dumpRoundTrip(ctx context.Context, t *testing.T, c *Core, svc domain.Service, seed, change, read func() string, tables int, rows int64) {
	t.Helper()
	before := seed()
	b, err := c.BackupService(ctx, svc.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	b = waitBackup(t, c, b.ID, func(b domain.Backup) bool { return b.Status != domain.OpRunning })
	if b.Status != domain.OpSucceeded || b.Kind != domain.BackupKindDump || b.ServiceKind != svc.Kind || !b.Encrypted || b.PGVersion == "" ||
		!strings.HasSuffix(b.ObjectKey, data.DumpExt(svc.Kind)+".age") {
		t.Fatalf("backup = %+v", b)
	}

	if _, err := c.VerifyBackup(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	b = waitBackup(t, c, b.ID, func(b domain.Backup) bool { return b.VerifyStatus != domain.OpRunning })
	if b.VerifyStatus != domain.OpSucceeded || b.VerifyDetails.Tables != tables || b.VerifyDetails.Rows != rows || b.VerifyDetails.DBBytes == 0 {
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
		if rs[0].ID == r.ID && rs[0].Status != domain.OpRunning {
			if rs[0].Status != domain.OpSucceeded {
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
	for _, kind := range []domain.ServiceKind{domain.ServiceKindMySQL, domain.ServiceKindMariaDB} {
		t.Run(string(kind), func(t *testing.T) {
			c, ctx, svc, exec := dumpTestDB(t, kind)
			client := data.MySQLBin(kind, "mysql")
			// As the app connects: its user, its database.
			app := func(sql string) string {
				return exec(`MYSQL_PWD="$MYSQL_PASSWORD" ` + client + ` -h 127.0.0.1 -u"$MYSQL_USER" -NB "$MYSQL_DATABASE" -e "` + strings.ReplaceAll(sql, "`", "\\`") + `"`)
			}
			read := func() string {
				return app("SELECT GROUP_CONCAT(v ORDER BY id) FROM items; SELECT COUNT(*) FROM v_items; SHOW TABLES")
			}
			dumpRoundTrip(ctx, t, c, svc,
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
	c, ctx, svc, exec := dumpTestDB(t, domain.ServiceKindMongoDB)
	mongosh := func(js string) string {
		return exec(`mongosh --quiet "mongodb://$MONGO_INITDB_ROOT_USERNAME:$MONGO_INITDB_ROOT_PASSWORD@127.0.0.1/app?authSource=admin" --eval '` + js + `'`)
	}
	read := func() string {
		return mongosh(`print(JSON.stringify(db.items.find({}, { _id: 0 }).sort({ n: 1 }).toArray()), db.getSiblingDB("other").logs.countDocuments())`)
	}
	dumpRoundTrip(ctx, t, c, svc,
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

// A password starting with "-" (even "--") must not reach a database tool as an option
// (mongosh: "unrecognized option"): the backup, the data browser and the
// healthcheck all authenticate with it.
func TestDashPasswordDocker(t *testing.T) {
	for _, kind := range []domain.ServiceKind{domain.ServiceKindPostgres, domain.ServiceKindRedis, domain.ServiceKindMySQL,
		domain.ServiceKindMariaDB, domain.ServiceKindMongoDB} {
		t.Run(string(kind), func(t *testing.T) {
			c, ctx, projectID := dumpTestCore(t)
			ctx = identity.WithActor(ctx, identity.Actor{Kind: "user", Name: "test", Role: domain.RoleAdmin})
			svc, _ := startTestDB(ctx, t, c, projectID, ServiceInput{Name: "db", Kind: kind, Password: "--dash" + identity.RandomToken(12)})
			if kind == domain.ServiceKindRedis {
				if _, err := c.RedisScan(ctx, svc.ID, "0", "*", 10); err != nil {
					t.Fatalf("scan: %v", err)
				}
				return
			}
			if _, err := c.PgDatabases(ctx, svc.ID); err != nil {
				t.Fatalf("databases: %v", err)
			}
			b, err := c.BackupService(ctx, svc.ID, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if b = waitBackup(t, c, b.ID, func(b domain.Backup) bool { return b.Status != domain.OpRunning }); b.Status != domain.OpSucceeded {
				t.Fatalf("backup = %+v", b)
			}
		})
	}
}
