package core

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// The data browser runs the database's own client (psql, redis-cli) inside
// its container, like backups: no driver in the manager, no network path to
// the database, and the client always matches the server. Every result is
// capped so a huge table or value never lands in memory.
const (
	// DataPageMax caps the rows (or keys, or members) of one page.
	DataPageMax = 200
	// DataCellMax caps the bytes of one cell or value shown; longer ones are
	// cut and flagged truncated.
	DataCellMax = 4096
	// dataLineMax bounds one line of client output (a row of capped cells).
	dataLineMax = 32 << 20

	dataBrowseTimeout = 5 * time.Second
	dataExportTimeout = 10 * time.Minute
)

// dataTarget is a database's running container, ready for an exec.
type dataTarget struct {
	svc       store.Service
	dk        *docker.Client
	container string
	// db is the database queries run against (postgres, mysql, mongodb).
	db string
}

// dataTarget resolves a database service of one of the given kinds to its
// running container, on its own database. MySQL and MongoDB get the data
// browser's read-only user first.
func (c *Core) dataTarget(ctx context.Context, id string, kinds ...store.ServiceKind) (dataTarget, error) {
	svc, err := c.store.GetService(ctx, id)
	if err != nil {
		return dataTarget{}, err
	}
	if !slices.Contains(kinds, svc.Kind) {
		names := make([]string, len(kinds))
		for i, k := range kinds {
			names[i] = string(k)
		}
		return dataTarget{}, fmt.Errorf("%w: %s is not a %s database", ErrInvalid, svc.Name, strings.Join(names, " or "))
	}
	ct, err := c.runningContainer(ctx, svc)
	if err != nil {
		return dataTarget{}, err
	}
	t := dataTarget{svc: svc, dk: c.dockerFor(svc.ServerID), container: ct}
	switch {
	case svc.Kind == store.ServiceKindPostgres:
		t.db = svc.Env[pgDatabase]
	case svc.Kind.IsMySQL():
		t.db = svc.Env[mysqlDatabase]
	case svc.Kind == store.ServiceKindMongoDB:
		t.db = svc.Env[mongoDatabase]
	}
	if svc.Kind.IsMySQL() || svc.Kind == store.ServiceKindMongoDB {
		if _, ok := c.readUsers.Load(ct); !ok {
			if err := t.ensureReadUser(ctx); err != nil {
				return dataTarget{}, fmt.Errorf("read-only user: %w", err)
			}
			c.readUsers.Store(ct, struct{}{})
		}
	}
	return t, nil
}

// databaseTarget resolves a service with databases (PostgreSQL, MySQL,
// MariaDB, MongoDB) and the one to query: its own when database is empty,
// else one of its instance's. Names are checked against the instance's
// list: clients would also take connection strings or odd names.
func (c *Core) databaseTarget(ctx context.Context, id, database string) (dataTarget, error) {
	t, err := c.dataTarget(ctx, id, store.ServiceKindPostgres, store.ServiceKindMySQL, store.ServiceKindMariaDB, store.ServiceKindMongoDB)
	if err != nil || database == "" || database == t.db {
		return t, err
	}
	if t.svc.Kind == store.ServiceKindPostgres {
		t.db, err = c.pgDatabaseOf(ctx, t.svc, t.container, database)
		return t, err
	}
	dbs, err := t.databases(ctx)
	if err != nil {
		return t, err
	}
	if !slices.ContainsFunc(dbs, func(d PgDatabase) bool { return d.Name == database }) {
		return t, fmt.Errorf("%w: %s has no database %q", ErrInvalid, t.svc.Name, database)
	}
	t.db = database
	return t, nil
}

// databases lists the instance's databases.
func (t dataTarget) databases(ctx context.Context) ([]PgDatabase, error) {
	switch {
	case t.svc.Kind.IsMySQL():
		return t.mysqlDatabases(ctx)
	case t.svc.Kind == store.ServiceKindMongoDB:
		return t.mongoDatabases(ctx)
	}
	return t.pgDatabases(ctx)
}

// readUserPassword is the password of the data browser's read-only user:
// derived from the root password, so nothing more is stored and it never
// changes.
func readUserPassword(root string) string {
	sum := sha256.Sum256([]byte("kipitiny-read\x00" + root))
	return hex.EncodeToString(sum[:16])
}

// readUser is the name of the data browser's read-only user in MySQL,
// MariaDB and MongoDB.
const readUser = "kipitiny_read"

// ensureReadUser creates (or resets) the read-only user, as root.
func (t dataTarget) ensureReadUser(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, dataConsoleTimeout)
	defer cancel()
	if t.svc.Kind == store.ServiceKindMongoDB {
		return clientError(t.dk.Exec(ctx, t.container, docker.ExecOptions{
			Cmd: append([]string{"mongosh", "--quiet", "--norc", "--eval", `const a = db.getSiblingDB("admin"), u = process.env.KP_USER, pwd = process.env.KP_PASSWORD;
if (a.getUser(u)) a.updateUser(u, { pwd, roles: ["readAnyDatabase"] });
else a.createUser({ user: u, pwd, roles: ["readAnyDatabase"] });`}, mongoAuthArgs(t.svc)...),
			Env: []string{"KP_USER=" + readUser, "KP_PASSWORD=" + readUserPassword(t.svc.Env[mongoPassword])},
		}))
	}
	user := fmt.Sprintf("'%s'@'localhost'", readUser)
	pw := readUserPassword(t.svc.Env[mysqlRootPassword])
	return clientError(t.dk.Exec(ctx, t.container, docker.ExecOptions{
		Cmd: []string{mysqlBin(t.svc.Kind, "mysql"), "-uroot", "-e", fmt.Sprintf(
			"CREATE USER IF NOT EXISTS %[1]s IDENTIFIED BY '%[2]s'; ALTER USER %[1]s IDENTIFIED BY '%[2]s'; GRANT SELECT, SHOW VIEW ON *.* TO %[1]s",
			user, pw)},
		Env: mysqlExecEnv(t.svc),
	}))
}

// execLines runs opts in the target's container and hands each stdout line
// to fn. Returning errStopLines from fn ends the read early (the exec is
// cancelled and its error ignored).
func (t dataTarget) execLines(ctx context.Context, opts docker.ExecOptions, fn func([]byte) error) error {
	return t.execStream(ctx, opts, func(r io.Reader) error {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64<<10), dataLineMax)
		for sc.Scan() {
			if err := fn(sc.Bytes()); err != nil {
				return err
			}
		}
		return sc.Err()
	})
}

// execStream runs opts in the target's container and hands its stdout to
// read. If read fails, the exec is cancelled; errStopLines from read means it
// stopped on purpose and is not an error.
func (t dataTarget) execStream(ctx context.Context, opts docker.ExecOptions, read func(io.Reader) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	opts.Stdout = pw
	done := make(chan error, 1)
	go func() {
		// The reader sees EOF either way; the exit status comes from done.
		done <- t.dk.Exec(ctx, t.container, opts)
		pw.Close()
	}()
	if err := read(pr); err != nil {
		cancel()
		pr.CloseWithError(err)
		<-done
		if errors.Is(err, errStopLines) {
			return nil
		}
		return err
	}
	_, _ = io.Copy(io.Discard, pr)
	return clientError(<-done)
}

var errStopLines = errors.New("stop reading")

// clientError turns a failed client run into a message for the caller: the
// database's own error ("relation does not exist", "WRONGTYPE ...") is the
// useful part.
func clientError(err error) error {
	var ee *docker.ExecError
	if !errors.As(err, &ee) {
		return err
	}
	msg := strings.TrimSpace(ee.Stderr)
	if msg == "" {
		msg = ee.Error()
	}
	// psql prefixes "psql:<stdin>:3: ERROR:  ", redis-cli "(error) ", mysql
	// "ERROR 1146 (42S02) at line 1: ".
	if m := mysqlErrorRe.FindStringIndex(msg); m != nil {
		msg = strings.TrimSpace(msg[m[1]:])
	} else {
		for _, p := range []string{"ERROR:", "(error)"} {
			if i := strings.Index(msg, p); i >= 0 {
				msg = strings.TrimSpace(msg[i+len(p):])
				break
			}
		}
	}
	return fmt.Errorf("%w: %s", ErrInvalid, msg)
}

var mysqlErrorRe = regexp.MustCompile(`ERROR \d+ \([0-9A-Z]+\)( at line \d+)?: `)

// truncate caps s at DataCellMax bytes without splitting a UTF-8 sequence.
func truncate(s string) (string, bool) {
	if len(s) <= DataCellMax {
		return s, false
	}
	cut := DataCellMax
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut], true
}
