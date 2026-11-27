package data

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/storage"
)

// Dump backups (kind dump) are made by the database's own tool inside its
// container, like pg_dump: mysqldump (or mariadb-dump) gzipped by the
// manager, or mongodump's gzipped archive. The client always matches the
// server and the manager needs none of their tools.

// mongoSkipped are the databases a MongoDB restore leaves alone: users and
// the server's own state belong to the instance, not to the backup.
var mongoSkipped = []string{"admin", "config", "local"}

// DumpExt is the object key's extension of a dump of kind k.
func DumpExt(k domain.ServiceKind) string {
	if k == domain.ServiceKindMongoDB {
		return ".archive.gz"
	}
	return ".sql.gz"
}

// dumpDatabase streams a dump of svc to storage, filling in size, checksum
// and server version on b.
func (c *Data) dumpDatabase(ctx context.Context, svc domain.Service, containerID string, st storage.Storage, target domain.BackupTarget, b *domain.Backup) error {
	c.steps.Set(backupKey(b.ID), BackupPrepare)
	dk := c.mgr.DockerFor(svc.ServerID)
	version, err := dumpServerVersion(ctx, dk, containerID, svc)
	if err != nil {
		return fmt.Errorf("server version: %w", err)
	}
	b.PGVersion = version
	if svc.Kind == domain.ServiceKindMongoDB {
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
			return fmt.Errorf("%s: %w", MySQLBin(svc.Kind, "mysqldump"), err)
		}
		return zw.Close()
	})
}

// mysqlDumpCmd dumps one database in a consistent snapshot (InnoDB), with
// its routines, triggers and events. Without --databases the dump has no
// CREATE DATABASE or USE, so it loads into any database.
func mysqlDumpCmd(k domain.ServiceKind, db string) []string {
	cmd := []string{
		MySQLBin(k, "mysqldump"), "-uroot",
		"--single-transaction", "--quick", "--routines", "--triggers", "--events",
		"--hex-blob", "--no-tablespaces", "--default-character-set=utf8mb4",
	}
	if k == domain.ServiceKindMySQL {
		// GTID statements only load into an empty server.
		cmd = append(cmd, "--set-gtid-purged=OFF")
	}
	return append(cmd, "--", db)
}

// dumpServerVersion asks the server of a MySQL, MariaDB or MongoDB service
// for its version.
func dumpServerVersion(ctx context.Context, dk *docker.Client, containerID string, svc domain.Service) (string, error) {
	opts := docker.ExecOptions{
		Cmd: []string{MySQLBin(svc.Kind, "mysql"), "-uroot", "-NBe", "SELECT VERSION()"},
		Env: mysqlExecEnv(svc),
	}
	if svc.Kind == domain.ServiceKindMongoDB {
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
// key names the restore, for its steps.
func (c *Data) restoreDump(ctx context.Context, svc domain.Service, containerID string, st storage.Storage, target domain.BackupTarget, b domain.Backup, key string) error {
	c.steps.Set(key, RestoreCheck)
	if err := c.checkDump(ctx, st, target, b); err != nil {
		return err
	}
	dk := c.mgr.DockerFor(svc.ServerID)
	if svc.Kind == domain.ServiceKindMongoDB {
		c.steps.Set(key, RestoreLoad)
		return c.loadDump(ctx, dk, containerID, svc, "", st, target, b)
	}

	db := svc.Env[mysqlDatabase]
	scratch := db + "__restore"
	admin := func(sql string) error {
		return dk.Exec(ctx, containerID, docker.ExecOptions{
			Cmd: []string{MySQLBin(svc.Kind, "mysql"), "-uroot", "-e", sql},
			Env: mysqlExecEnv(svc),
		})
	}
	dropScratch := func() error { return admin("DROP DATABASE IF EXISTS " + mysqlIdent(scratch)) }
	if err := dropScratch(); err != nil {
		return fmt.Errorf("clean scratch database: %w", err)
	}
	c.steps.Set(key, RestoreScratch)
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
	c.steps.Set(key, RestoreLoad)
	if err := admin(fmt.Sprintf("DROP DATABASE IF EXISTS %[1]s; CREATE DATABASE %[1]s", mysqlIdent(db))); err != nil {
		return fmt.Errorf("recreate %s: %w", db, err)
	}
	if err := c.loadDump(ctx, dk, containerID, svc, db, st, target, b); err != nil {
		return fmt.Errorf("%w (the scratch load had succeeded; %s may be partly restored)", err, db)
	}
	return nil
}

// checkDump reads a stored dump through: checksum, decryption and gzip.
func (c *Data) checkDump(ctx context.Context, st storage.Storage, target domain.BackupTarget, b domain.Backup) error {
	rc, err := st.Get(ctx, b.ObjectKey)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer rc.Close()
	return c.fromStorage(rc, target, b, func(r io.Reader) error {
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
func (c *Data) loadDump(ctx context.Context, dk *docker.Client, ct string, svc domain.Service, db string,
	st storage.Storage, target domain.BackupTarget, b domain.Backup) error {

	rc, err := st.Get(ctx, b.ObjectKey)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer rc.Close()
	return c.fromStorage(rc, target, b, func(r io.Reader) error {
		if svc.Kind == domain.ServiceKindMongoDB {
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
			Cmd:   []string{MySQLBin(svc.Kind, "mysql"), "-uroot", "--default-character-set=utf8mb4", "--", db},
			Env:   mysqlExecEnv(svc),
			Stdin: zr,
		})
		if err != nil {
			return fmt.Errorf("%s: %w", MySQLBin(svc.Kind, "mysql"), err)
		}
		return nil
	})
}

// verifyDump restores a dump into a throwaway, network-less server of the
// backed-up service's kind and counts what came back.
func (c *Data) verifyDump(ctx context.Context, b domain.Backup) (domain.VerificationDetails, error) {
	var d domain.VerificationDetails
	target, err := c.store.GetBackupTarget(ctx, b.TargetID)
	if err != nil {
		return d, err
	}
	st, err := c.openStorage(target)
	if err != nil {
		return d, err
	}
	svc, dk, id, err := c.scratchDump(ctx, b, "kipitiny-verify-"+strings.ToLower(b.ID), func(s string) { c.steps.Set(verifyKey(b.ID), s) })
	if err != nil {
		return d, err
	}
	defer c.removeScratch(ctx, dk, id)
	c.steps.Set(verifyKey(b.ID), VerifyRestore)
	if err := c.loadDump(ctx, dk, id, svc, svc.Env[mysqlDatabase], st, target, b); err != nil {
		return d, err
	}

	c.steps.Set(verifyKey(b.ID), VerifyCheck)
	var out strings.Builder
	if svc.Kind == domain.ServiceKindMongoDB {
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

// scratchDump starts a throwaway, network-less server of b's kind (MySQL,
// MariaDB, MongoDB) able to load it, and waits until it's ready; step
// reports where it's at. svc describes it: kind, image, credentials. The
// caller removes it with removeScratch.
func (c *Data) scratchDump(ctx context.Context, b domain.Backup, name string, step func(string)) (domain.Service, *docker.Client, string, error) {
	// The throwaway server is a service of its own: fresh credentials.
	svc := domain.Service{Kind: b.ServiceKind, ServerID: domain.LocalServerID, MemoryMB: verifyMemoryMB}
	svc.Image = MySQLImageFor(svc.Kind)
	svc.Env = NewMySQLEnv()
	if svc.Kind == domain.ServiceKindMongoDB {
		svc.Image, svc.Env = DefaultMongoImage, NewMongoEnv()
	}
	if live, err := c.store.GetService(ctx, b.ServiceID); err == nil && live.Kind == svc.Kind {
		// Its own image: the server that made the dump, or a later one.
		svc.Image, svc.ServerID = live.Image, live.ServerID
	} else if b.Image != "" {
		svc.Image = b.Image
	}
	dk := c.mgr.DockerFor(svc.ServerID)
	step(VerifyImage)
	if err := dk.EnsureImage(ctx, svc.Image, c.mgr.RegistryAuth(ctx, svc.Image), ""); err != nil {
		return svc, nil, "", fmt.Errorf("pull %s: %w", svc.Image, err)
	}
	step(VerifyServer)
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
	if svc.Kind == domain.ServiceKindMongoDB {
		cfg.Cmd, cfg.Healthcheck = mongoArgs(verifyMemoryMB), mongoHealthcheck()
	} else {
		cfg.Cmd, cfg.Healthcheck = mysqlArgs(verifyMemoryMB), mysqlHealthcheck(svc.Kind)
	}
	id, err := dk.RunMoby(ctx, client.ContainerCreateOptions{Name: name, Config: cfg, HostConfig: host})
	if err != nil {
		return svc, nil, "", err
	}
	if err := dk.WaitHealthy(ctx, id, DatabaseReadyTimeout); err != nil {
		c.removeScratch(ctx, dk, id)
		return svc, nil, "", fmt.Errorf("throwaway server: %w", err)
	}
	return svc, dk, id, nil
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
func mysqlStats(ctx context.Context, dk *docker.Client, ct string, svc domain.Service, out io.Writer) error {
	mysql := func(sql string, w io.Writer) error {
		return dk.Exec(ctx, ct, docker.ExecOptions{
			Cmd:    []string{MySQLBin(svc.Kind, "mysql"), "-uroot", "-NB", "-e", sql, "--", svc.Env[mysqlDatabase]},
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
