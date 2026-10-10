package core

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/storage"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Dump backups (kind dump) are made by the database's own tool inside its
// container, like pg_dump: mysqldump (or mariadb-dump) gzipped by the
// manager, or mongodump's gzipped archive. The client always matches the
// server and the manager needs none of their tools.

// mongoSkipped are the databases a MongoDB restore leaves alone: users and
// the server's own state belong to the instance, not to the backup.
var mongoSkipped = []string{"admin", "config", "local"}

// dumpExt is the object key's extension of a dump of kind k.
func dumpExt(k store.ServiceKind) string {
	if k == store.ServiceKindMongoDB {
		return ".archive.gz"
	}
	return ".sql.gz"
}

// dumpDatabase streams a dump of svc to storage, filling in size, checksum
// and server version on b.
func (c *Core) dumpDatabase(ctx context.Context, svc store.Service, containerID string, st storage.Storage, target store.BackupTarget, b *store.Backup) error {
	dk := c.dockerFor(svc.ServerID)
	version, err := dumpServerVersion(ctx, dk, containerID, svc)
	if err != nil {
		return fmt.Errorf("server version: %w", err)
	}
	b.PGVersion = version
	if svc.Kind == store.ServiceKindMongoDB {
		return c.upload(ctx, st, target, b, func(w io.Writer) error {
			err := dk.Exec(ctx, containerID, docker.ExecOptions{
				Cmd:    append([]string{"mongodump", "--quiet", "--archive", "--gzip"}, mongoAuthArgs(svc)...),
				Stdout: w,
			})
			if err != nil {
				return fmt.Errorf("mongodump: %w", err)
			}
			return nil
		})
	}
	return c.upload(ctx, st, target, b, func(w io.Writer) error {
		zw := gzip.NewWriter(w)
		err := dk.Exec(ctx, containerID, docker.ExecOptions{
			Cmd:    mysqlDumpCmd(svc.Kind, svc.Env[mysqlDatabase]),
			Env:    mysqlExecEnv(svc),
			Stdout: zw,
		})
		if err != nil {
			return fmt.Errorf("%s: %w", mysqlBin(svc.Kind, "mysqldump"), err)
		}
		return zw.Close()
	})
}

// mysqlDumpCmd dumps one database in a consistent snapshot (InnoDB), with
// its routines, triggers and events. Without --databases the dump has no
// CREATE DATABASE or USE, so it loads into any database.
func mysqlDumpCmd(k store.ServiceKind, db string) []string {
	cmd := []string{
		mysqlBin(k, "mysqldump"), "-uroot",
		"--single-transaction", "--quick", "--routines", "--triggers", "--events",
		"--hex-blob", "--no-tablespaces", "--default-character-set=utf8mb4",
	}
	if k == store.ServiceKindMySQL {
		// GTID statements only load into an empty server.
		cmd = append(cmd, "--set-gtid-purged=OFF")
	}
	return append(cmd, "--", db)
}

// dumpServerVersion asks the server of a MySQL, MariaDB or MongoDB service
// for its version.
func dumpServerVersion(ctx context.Context, dk *docker.Client, containerID string, svc store.Service) (string, error) {
	opts := docker.ExecOptions{
		Cmd: []string{mysqlBin(svc.Kind, "mysql"), "-uroot", "-NBe", "SELECT VERSION()"},
		Env: mysqlExecEnv(svc),
	}
	if svc.Kind == store.ServiceKindMongoDB {
		opts = docker.ExecOptions{
			Cmd: append([]string{"mongosh", "--quiet", "--eval", "db.version()"}, mongoAuthArgs(svc)...),
		}
	}
	var out strings.Builder
	opts.Stdout = &out
	err := dk.Exec(ctx, containerID, opts)
	return strings.TrimSpace(out.String()), err
}

// restoreDump loads a dump into svc. The backup is read through first (its
// checksum and compression checked) so a damaged one changes nothing. A
// MySQL dump is then loaded into a scratch database first, and only once
// that succeeded into the emptied live one; a MongoDB archive replaces each
// collection it holds (mongorestore --drop), and collections created since
// the backup stay.
func (c *Core) restoreDump(ctx context.Context, svc store.Service, containerID string, st storage.Storage, target store.BackupTarget, b store.Backup) error {
	if err := checkDump(ctx, st, target, b); err != nil {
		return err
	}
	dk := c.dockerFor(svc.ServerID)
	if svc.Kind == store.ServiceKindMongoDB {
		return c.loadDump(ctx, dk, containerID, svc, "", st, target, b)
	}

	db := svc.Env[mysqlDatabase]
	scratch := db + "__restore"
	admin := func(sql string) error {
		return dk.Exec(ctx, containerID, docker.ExecOptions{
			Cmd: []string{mysqlBin(svc.Kind, "mysql"), "-uroot", "-e", sql},
			Env: mysqlExecEnv(svc),
		})
	}
	dropScratch := func() error { return admin("DROP DATABASE IF EXISTS " + mysqlIdent(scratch)) }
	if err := dropScratch(); err != nil {
		return fmt.Errorf("clean scratch database: %w", err)
	}
	if err := admin("CREATE DATABASE " + mysqlIdent(scratch)); err != nil {
		return fmt.Errorf("create scratch database: %w", err)
	}
	err := c.loadDump(ctx, dk, containerID, svc, scratch, st, target, b)
	if derr := dropScratch(); derr != nil {
		c.log.Warn("cannot drop scratch database", "service", svc.Name, "err", derr)
	}
	if err != nil {
		return err
	}
	// Grants on the database outlive it: the app user keeps its access.
	if err := admin(fmt.Sprintf("DROP DATABASE IF EXISTS %[1]s; CREATE DATABASE %[1]s", mysqlIdent(db))); err != nil {
		return fmt.Errorf("recreate %s: %w", db, err)
	}
	if err := c.loadDump(ctx, dk, containerID, svc, db, st, target, b); err != nil {
		return fmt.Errorf("%w (the scratch load had succeeded; %s may be partly restored)", err, db)
	}
	return nil
}

// checkDump reads a stored dump through: checksum, decryption and gzip.
func checkDump(ctx context.Context, st storage.Storage, target store.BackupTarget, b store.Backup) error {
	rc, err := st.Get(ctx, b.ObjectKey)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer rc.Close()
	return fromStorage(rc, target, b, func(r io.Reader) error {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return fmt.Errorf("read backup: %w", err)
		}
		if _, err := io.Copy(io.Discard, zr); err != nil {
			return fmt.Errorf("read backup: %w", err)
		}
		return nil
	})
}

// loadDump streams a stored dump into the container ct of a server like
// svc: a MySQL dump into database db, a MongoDB archive into the instance.
func (c *Core) loadDump(ctx context.Context, dk *docker.Client, ct string, svc store.Service, db string,
	st storage.Storage, target store.BackupTarget, b store.Backup) error {

	rc, err := st.Get(ctx, b.ObjectKey)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer rc.Close()
	return fromStorage(rc, target, b, func(r io.Reader) error {
		if svc.Kind == store.ServiceKindMongoDB {
			cmd := []string{"mongorestore", "--quiet", "--archive", "--gzip", "--drop"}
			for _, name := range mongoSkipped {
				cmd = append(cmd, "--nsExclude", name+".*")
			}
			if err := dk.Exec(ctx, ct, docker.ExecOptions{Cmd: append(cmd, mongoAuthArgs(svc)...), Stdin: r}); err != nil {
				return fmt.Errorf("mongorestore: %w", err)
			}
			return nil
		}
		zr, err := gzip.NewReader(r)
		if err != nil {
			return fmt.Errorf("read backup: %w", err)
		}
		err = dk.Exec(ctx, ct, docker.ExecOptions{
			Cmd:   []string{mysqlBin(svc.Kind, "mysql"), "-uroot", "--default-character-set=utf8mb4", "--", db},
			Env:   mysqlExecEnv(svc),
			Stdin: zr,
		})
		if err != nil {
			return fmt.Errorf("%s: %w", mysqlBin(svc.Kind, "mysql"), err)
		}
		return nil
	})
}

// verifyDump restores a dump into a throwaway, network-less server of the
// backed-up service's kind and counts what came back.
func (c *Core) verifyDump(ctx context.Context, b store.Backup) (store.VerificationDetails, error) {
	var d store.VerificationDetails
	target, err := c.store.GetBackupTarget(ctx, b.TargetID)
	if err != nil {
		return d, err
	}
	st, err := c.openStorage(target)
	if err != nil {
		return d, err
	}
	// The throwaway server is a service of its own: fresh credentials.
	svc := store.Service{Kind: b.ServiceKind, ServerID: store.LocalServerID, MemoryMB: verifyMemoryMB}
	svc.Image = defaultMySQLImage(svc.Kind)
	svc.Env = newMySQLEnv()
	if svc.Kind == store.ServiceKindMongoDB {
		svc.Image, svc.Env = DefaultMongoImage, newMongoEnv()
	}
	if live, err := c.store.GetService(ctx, b.ServiceID); err == nil && live.Kind == svc.Kind {
		// Its own image: the server that made the dump, or a later one.
		svc.Image, svc.ServerID = live.Image, live.ServerID
	}
	dk := c.dockerFor(svc.ServerID)
	if err := dk.EnsureImage(ctx, svc.Image, c.registryAuth(ctx, svc.Image)); err != nil {
		return d, fmt.Errorf("pull %s: %w", svc.Image, err)
	}
	cfg := &container.Config{
		Image:  svc.Image,
		Labels: map[string]string{docker.LabelManaged: "true", docker.LabelComponent: verifyComponent},
	}
	host := &container.HostConfig{
		// Isolated: the restore arrives over exec, nothing needs a network.
		NetworkMode: "none",
		Resources:   container.Resources{Memory: verifyMemoryMB << 20},
		LogConfig:   docker.DefaultLogConfig(),
	}
	for k, v := range svc.Env {
		cfg.Env = append(cfg.Env, k+"="+v)
	}
	if svc.Kind == store.ServiceKindMongoDB {
		cfg.Cmd, cfg.Healthcheck = mongoArgs(verifyMemoryMB), mongoHealthcheck()
	} else {
		cfg.Cmd, cfg.Healthcheck = mysqlArgs(verifyMemoryMB), mysqlHealthcheck(svc.Kind)
	}
	id, err := dk.Run(ctx, client.ContainerCreateOptions{Name: "kipitiny-verify-" + strings.ToLower(b.ID), Config: cfg, HostConfig: host})
	if err != nil {
		return d, err
	}
	defer func() {
		// The data directory is an anonymous volume: remove it too.
		if err := dk.RemoveContainerAndVolumes(context.WithoutCancel(ctx), id); err != nil {
			c.log.Warn("cannot remove restore test container", "id", id[:12], "err", err)
		}
	}()
	if err := dk.WaitHealthy(ctx, id, databaseReadyTimeout); err != nil {
		return d, fmt.Errorf("throwaway server: %w", err)
	}
	if err := c.loadDump(ctx, dk, id, svc, svc.Env[mysqlDatabase], st, target, b); err != nil {
		return d, err
	}

	var out strings.Builder
	if svc.Kind == store.ServiceKindMongoDB {
		err = dk.Exec(ctx, id, docker.ExecOptions{
			Cmd:    append([]string{"mongosh", "--quiet", "--eval", mongoStatsJS}, mongoAuthArgs(svc)...),
			Stdout: &out,
		})
	} else {
		err = mysqlStats(ctx, dk, id, svc, &out)
	}
	if err != nil {
		return d, fmt.Errorf("sanity query: %w", err)
	}
	return parseVerifyOutput(out.String())
}

// mongoStatsJS prints "collections|documents|bytes" of the user databases.
const mongoStatsJS = `let t = 0, r = 0, b = 0;
for (const d of db.adminCommand({ listDatabases: 1 }).databases) {
  if (["admin", "config", "local"].includes(d.name)) continue;
  const x = db.getSiblingDB(d.name);
  for (const c of x.getCollectionInfos({ type: "collection" })) {
    if (c.name.startsWith("system.")) continue;
    t++;
    r += x.getCollection(c.name).estimatedDocumentCount();
  }
  b += Number(x.stats().dataSize);
}
print(t + "|" + r + "|" + b);`

// mysqlStats writes "tables|rows|bytes" of the service's database to out:
// rows are counted exactly (InnoDB's estimates are rough).
func mysqlStats(ctx context.Context, dk *docker.Client, ct string, svc store.Service, out io.Writer) error {
	mysql := func(sql string, w io.Writer) error {
		return dk.Exec(ctx, ct, docker.ExecOptions{
			Cmd:    []string{mysqlBin(svc.Kind, "mysql"), "-uroot", "-NB", "-e", sql, "--", svc.Env[mysqlDatabase]},
			Env:    mysqlExecEnv(svc),
			Stdout: w,
		})
	}
	var names strings.Builder
	if err := mysql("SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'", &names); err != nil {
		return err
	}
	// One name per line (batch mode escapes newlines in values).
	tables := strings.FieldsFunc(names.String(), func(r rune) bool { return r == '\n' })
	rows := "0"
	for _, t := range tables {
		rows += fmt.Sprintf(" + (SELECT COUNT(*) FROM %s)", mysqlIdent(t))
	}
	return mysql(fmt.Sprintf(`SELECT CONCAT(%d, '|', %s, '|', (SELECT COALESCE(SUM(DATA_LENGTH + INDEX_LENGTH), 0)
FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()))`, len(tables), rows), out)
}
