package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MatHoyer/kipitiny/internal/anonymize"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// anonymizeRoundTrip backs svc up (seed writes its data), turns on
// anonymized seeds with rules, waits for the copy, restores it into into
// (as a preview's database) and compares read with want, given the
// project's salt.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run AnonymizeDocker
func anonymizeRoundTrip(t *testing.T, c *Core, ctx context.Context, svc, into store.Service, rules anonymize.Rules, seed func(), read func() string,
	want func(salt string) string) {

	t.Helper()
	seed()
	b, err := c.BackupService(ctx, svc.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if b = waitBackup(t, c, b.ID, func(b store.Backup) bool { return b.Status != store.OpRunning }); b.Status != store.OpSucceeded {
		t.Fatalf("backup = %+v", b)
	}
	if err := c.store.SetServiceAnonymize(ctx, svc.ID, rules); err != nil {
		t.Fatal(err)
	}
	// Turning anonymized seeds on makes the copy.
	if _, err := c.SetPreviewSettings(ctx, svc.ProjectID, PreviewSettingsInput{Seed: store.SeedAnonymized}); err != nil {
		t.Fatal(err)
	}
	cp := waitCopy(t, c, b.ID, "")
	if cp.Status != store.OpSucceeded || !cp.Encrypted || !strings.Contains(cp.ObjectKey, ".anon") || cp.SizeBytes == 0 {
		t.Fatalf("copy = %+v", cp)
	}
	if bs, _ := c.store.ListBackups(ctx, store.BackupFilter{ServiceID: svc.ID}); len(bs) != 1 {
		t.Fatalf("the copy shows among backups: %+v", bs)
	}
	// Up to date: nothing new.
	if err := c.refreshAnonymized(ctx, svc.ProjectID, false); err != nil {
		t.Fatal(err)
	}
	if again, _ := c.store.ListBackups(ctx, store.BackupFilter{Anonymized: true, SourceID: b.ID}); len(again) != 1 {
		t.Fatalf("copies = %+v", again)
	}

	restoreAndWait(t, c, ctx, cp.ID, into)
	ps, _ := c.store.GetPreviewSettings(ctx, svc.ProjectID)
	if got := read(); got != want(ps.Salt) {
		t.Fatalf("anonymized data = %q, want %q", got, want(ps.Salt))
	}

	// A rule that can't apply fails the new copy; the good one stays.
	if err := c.store.SetServiceAnonymize(ctx, svc.ID, anonymize.Rules{"nope": {Action: anonymize.Truncate}}); err != nil {
		t.Fatal(err)
	}
	if err := c.refreshAnonymized(ctx, svc.ProjectID, false); err != nil {
		t.Fatal(err)
	}
	bad := waitCopy(t, c, b.ID, cp.ID)
	if bad.Status != store.OpFailed || !strings.Contains(bad.Error, "nope") {
		t.Fatalf("bad copy = %+v", bad)
	}
	if _, err := c.store.GetBackup(ctx, cp.ID); err != nil {
		t.Fatalf("the good copy went: %v", err)
	}

	// Deleting the backup deletes its copies.
	if err := c.DeleteBackup(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := c.store.ListBackups(ctx, store.BackupFilter{Anonymized: true, ServiceID: svc.ID}); len(left) != 0 {
		t.Fatalf("copies left: %+v", left)
	}
}

// waitCopy waits for a finished copy of backup src other than skip.
func waitCopy(t *testing.T, c *Core, src, skip string) store.Backup {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		cps, err := c.store.ListBackups(context.Background(), store.BackupFilter{Anonymized: true, SourceID: src})
		if err != nil {
			t.Fatal(err)
		}
		for _, cp := range cps {
			if cp.ID != skip && cp.Status != store.OpRunning {
				return cp
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("no anonymized copy")
	return store.Backup{}
}

func restoreAndWait(t *testing.T, c *Core, ctx context.Context, backupID string, svc store.Service) {
	t.Helper()
	r, err := c.RestoreBackup(ctx, backupID, svc.ID, svc.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.waitRestore(ctx, svc.ID, r.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
}

// anonymized is what rule makes of v (null stays null).
func anonymized(rule, salt, v string) string {
	if out, ok := anonymize.Value(rule, salt, v); ok {
		return out
	}
	return v
}

func TestAnonymizeDockerPostgres(t *testing.T) {
	c, ctx, svc, exec := dumpTestDB(t, store.ServiceKindPostgres)
	psql := func(sql string) string {
		return exec(`psql -X -q -tA -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -c "` + sql + `"`)
	}
	rules := anonymize.Rules{
		"users":         {Columns: map[string]string{"email": "fake:email", "name": "fake:name", "phone": "fake:phone", "pw": "fixed:x", "note": "null", "ext": "hash"}},
		"sessions":      {Action: anonymize.Truncate},
		"billing.audit": {Action: anonymize.Exclude},
	}
	anonymizeRoundTrip(t, c, ctx, svc, svc, rules,
		func() {
			psql(`CREATE TABLE users (id int PRIMARY KEY, email text UNIQUE, name text, phone text, pw text, note text, ext uuid);
INSERT INTO users VALUES (1, 'michelle@mail.com', 'michelle', '06 01 01 01 01', 'secret', 'n', gen_random_uuid()), (2, 'b@x.com', NULL, NULL, NULL, NULL, NULL);
CREATE TABLE sessions (id int, user_id int REFERENCES users);
INSERT INTO sessions VALUES (1, 1);
CREATE SCHEMA billing; CREATE TABLE billing.audit (user_id int REFERENCES users); INSERT INTO billing.audit VALUES (1);
CREATE FUNCTION touch() RETURNS trigger LANGUAGE plpgsql AS \$\$ BEGIN RAISE EXCEPTION 'trigger ran'; END \$\$;
CREATE TRIGGER no_update BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION touch();`)
		},
		func() string {
			return psql(`SELECT concat_ws('|', email, name, phone, pw, note, length(ext::text)) FROM users ORDER BY id;
SELECT count(*) FROM sessions; SELECT count(*) FROM pg_tables WHERE schemaname = 'billing'; SELECT count(*) FROM pg_proc WHERE proname LIKE 'kp\_%'`)
		},
		func(salt string) string {
			return strings.Join([]string{
				strings.Join([]string{anonymized("fake:email", salt, "michelle@mail.com"), anonymized("fake:name", salt, "michelle"),
					anonymized("fake:phone", salt, "06 01 01 01 01"), "x", "36"}, "|"),
				anonymized("fake:email", salt, "b@x.com") + "|x",
				"0", "0", "0",
			}, "\n")
		})
}

func TestAnonymizeDockerMySQL(t *testing.T) {
	for _, kind := range []store.ServiceKind{store.ServiceKindMySQL, store.ServiceKindMariaDB} {
		t.Run(string(kind), func(t *testing.T) {
			c, ctx, svc, exec := dumpTestDB(t, kind)
			app := func(sql string) string {
				return exec(`MYSQL_PWD="$MYSQL_PASSWORD" ` + mysqlBin(kind, "mysql") + ` -h 127.0.0.1 -u"$MYSQL_USER" -NB "$MYSQL_DATABASE" -e "` + sql + `"`)
			}
			rules := anonymize.Rules{
				"users":    {Columns: map[string]string{"email": "fake:email", "name": "fake:name", "phone": "fake:phone", "pw": "fixed:o'k", "note": "null"}},
				"sessions": {Action: anonymize.Truncate},
				"audit":    {Action: anonymize.Exclude},
			}
			anonymizeRoundTrip(t, c, ctx, svc, svc, rules,
				func() {
					app(`CREATE TABLE users (id INT PRIMARY KEY, email VARCHAR(100) UNIQUE, name TEXT, phone TEXT, pw TEXT, note TEXT) DEFAULT CHARSET=latin1;
INSERT INTO users VALUES (1, 'michelle@mail.com', 'michelle', '06 01 01 01 01', 'secret', 'n'), (2, 'b@x.com', NULL, NULL, NULL, NULL);
CREATE TABLE sessions (id INT, user_id INT, FOREIGN KEY (user_id) REFERENCES users (id)); INSERT INTO sessions VALUES (1, 1);
CREATE TABLE audit (user_id INT, FOREIGN KEY (user_id) REFERENCES users (id)); INSERT INTO audit VALUES (1)`)
				},
				func() string {
					return app(`SELECT CONCAT_WS('|', email, name, phone, pw, note) FROM users ORDER BY id; SELECT COUNT(*) FROM sessions;
SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'audit';
SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE()`)
				},
				func(salt string) string {
					return strings.Join([]string{
						strings.Join([]string{anonymized("fake:email", salt, "michelle@mail.com"), anonymized("fake:name", salt, "michelle"),
							anonymized("fake:phone", salt, "06 01 01 01 01"), "o'k"}, "|"),
						anonymized("fake:email", salt, "b@x.com") + "|o'k",
						"0", "0", "0",
					}, "\n")
				})
		})
	}
}

func TestAnonymizeDockerMongo(t *testing.T) {
	c, ctx, svc, prodExec := dumpTestDB(t, store.ServiceKindMongoDB)
	// A restore keeps the collections an archive lacks: into a fresh
	// database, as a preview's.
	fresh, exec := startTestDB(t, c, ctx, svc.ProjectID, ServiceInput{Name: "fresh", Kind: store.ServiceKindMongoDB})
	mongo := func(exec func(string) string, js string) string {
		return exec(`mongosh --quiet "mongodb://$MONGO_INITDB_ROOT_USERNAME:$MONGO_INITDB_ROOT_PASSWORD@127.0.0.1/app?authSource=admin" --eval '` + js + `'`)
	}
	rules := anonymize.Rules{
		"users":    {Columns: map[string]string{"email": "fake:email", "contact.phone": "fake:phone", "note": "null"}},
		"sessions": {Action: anonymize.Truncate},
		"audit":    {Action: anonymize.Exclude},
	}
	anonymizeRoundTrip(t, c, ctx, svc, fresh, rules,
		func() {
			mongo(prodExec, `db.users.insertMany([{ n: 1, email: "michelle@mail.com", contact: { phone: "06 01 01 01 01" }, note: "n" }, { n: 2, email: "b@x.com" }]);
db.sessions.insertOne({ a: 1 }); db.audit.insertOne({ a: 1 })`)
		},
		func() string {
			return mongo(exec, `db.users.find({}, { _id: 0 }).sort({ n: 1 }).forEach((u) => print([u.email, u.contact && u.contact.phone, u.note].join("|")));
print(db.sessions.countDocuments(), db.getCollectionNames().includes("audit"))`)
		},
		func(salt string) string {
			return strings.Join([]string{
				anonymized("fake:email", salt, "michelle@mail.com") + "|" + anonymized("fake:phone", salt, "06 01 01 01 01") + "|",
				anonymized("fake:email", salt, "b@x.com") + "||",
				"0 false",
			}, "\n")
		})
}
