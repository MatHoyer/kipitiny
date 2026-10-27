package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/anonymize"
	"github.com/MatHoyer/kipitiny/internal/compose"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/notify"
	"github.com/MatHoyer/kipitiny/internal/storage"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Previews seeded with anonymized data restore an anonymized copy of the
// parent database's latest backup, never the backup itself. A copy is made
// once per backup (and set of rules) in a throwaway, network-less server:
// the backup is restored there, the database's rules applied, and the
// result dumped next to the backup, as a backup whose SourceID is the
// original. Raw data never reaches a preview container.

// seedable reports whether a backup can seed a preview's database.
func seedable(b store.Backup) bool {
	return b.Kind == store.BackupKindPostgres || b.Kind == store.BackupKindDump
}

// latestSeedable returns the service's latest successful backup a preview
// can be seeded with.
func (c *Core) latestSeedable(ctx context.Context, serviceID string) (store.Backup, bool, error) {
	bs, err := c.store.ListBackups(ctx, store.BackupFilter{ServiceID: serviceID, Status: store.OpSucceeded, Limit: 20})
	if err != nil {
		return store.Backup{}, false, err
	}
	i := slices.IndexFunc(bs, seedable)
	if i < 0 {
		return store.Backup{}, false, nil
	}
	return bs[i], true, nil
}

// anonymizeSalt returns the project's salt, making one on first use.
func (c *Core) anonymizeSalt(ctx context.Context, projectID string) (store.PreviewSettings, error) {
	ps, err := c.previewSettings(ctx, projectID)
	if err != nil || ps.Salt != "" {
		return ps, err
	}
	ps.Salt = anonymize.NewSalt()
	return c.store.SavePreviewSettings(ctx, ps)
}

// refreshAnonymized starts the anonymized copy of each database of a
// project seeding its previews with anonymized data, when its latest
// backup has none made with its current rules. A failed copy is only
// retried with force (or with a new backup or rules).
func (c *Core) refreshAnonymized(ctx context.Context, projectID string, force bool) error {
	ps, err := c.previewSettings(ctx, projectID)
	if err != nil || ps.SeedMode != store.SeedAnonymized {
		return err
	}
	svcs, err := c.store.ListServices(ctx, projectID)
	if err != nil {
		return err
	}
	var errs []error
	for _, s := range svcs {
		if s.Anonymize == nil || !s.Kind.IsDatabase() {
			continue
		}
		if _, err := c.anonymizeLatest(ctx, s, force); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name, err))
		}
	}
	return errors.Join(errs...)
}

// refreshAllAnonymized catches up on copies a restart interrupted or a
// trigger missed.
func (c *Core) refreshAllAnonymized(ctx context.Context) {
	ps, err := c.store.ListProjects(ctx)
	if err != nil {
		return
	}
	for _, p := range ps {
		if p.ParentID == "" {
			c.logAnonymizeErr(c.refreshAnonymized(ctx, p.ID, false))
		}
	}
}

func (c *Core) logAnonymizeErr(err error) {
	if err != nil {
		c.log.Warn("cannot start anonymized copy", "err", err)
	}
}

// anonymizeLatest starts the anonymized copy of svc's latest backup with
// its current rules, unless one exists (a failed one too, without force).
// It returns the copy, made or found.
func (c *Core) anonymizeLatest(ctx context.Context, svc store.Service, force bool) (store.Backup, error) {
	c.anonymizeMu.Lock() // check-then-create: one copy per backup and rules
	defer c.anonymizeMu.Unlock()
	src, ok, err := c.latestSeedable(ctx, svc.ID)
	if err != nil || !ok {
		return store.Backup{}, err
	}
	ps, err := c.anonymizeSalt(ctx, svc.ProjectID)
	if err != nil {
		return store.Backup{}, err
	}
	hash := svc.Anonymize.Hash(ps.Salt)
	copies, err := c.store.ListBackups(ctx, store.BackupFilter{Anonymized: true, SourceID: src.ID})
	if err != nil {
		return store.Backup{}, err
	}
	for _, cp := range copies {
		// A restart's interruption isn't the rules' fault: try again.
		retry := cp.Status == store.OpFailed && (force || strings.HasPrefix(cp.Error, "interrupted by manager"))
		if cp.RulesHash == hash && !retry {
			return cp, nil
		}
	}
	target, err := c.store.GetBackupTarget(ctx, src.TargetID)
	if err != nil {
		return store.Backup{}, err
	}
	st, err := c.openStorage(target)
	if err != nil {
		return store.Backup{}, err
	}
	cp := src
	cp.ID, cp.ScheduleID, cp.Error, cp.Verification = "", "", "", store.Verification{}
	cp.SourceID, cp.RulesHash, cp.Status, cp.CreatedAt, cp.FinishedAt = src.ID, hash, store.OpRunning, time.Time{}, nil
	cp.ObjectKey = anonymizedKey(src, hash, target)
	if cp, err = c.store.CreateBackup(ctx, cp); err != nil {
		return store.Backup{}, err
	}
	rules, mongoDB := svc.Anonymize, svc.Env[mongoDatabase]
	if err := c.goBackground(func() { c.runAnonymize(src, cp, rules, ps.Salt, mongoDB, st, target) }); err != nil {
		cp.Status, cp.Error = store.OpFailed, err.Error()
		_ = c.store.FinishBackup(context.WithoutCancel(ctx), cp)
		return store.Backup{}, err
	}
	return cp, nil
}

// anonymizedKey stores a copy next to its backup:
// <backup key>-<rules hash>.anon<ext>[.age].
func anonymizedKey(src store.Backup, hash string, target store.BackupTarget) string {
	key := strings.TrimSuffix(src.ObjectKey, ".age")
	ext := ".dump"
	if src.Kind == store.BackupKindDump {
		ext = dumpExt(src.ServiceKind)
	}
	key = strings.TrimSuffix(key, ext) + "-" + hash[:8] + ".anon" + ext
	if target.Encrypted() {
		key += ".age"
	}
	return key
}

// runAnonymize makes and records the copy cp of src (mongoDB is the
// database MongoDB rules apply to). Once it succeeded,
// the service's older copies go; a failed one stays to tell why seeding is
// refused, and the previous good copy keeps seeding previews.
func (c *Core) runAnonymize(src, cp store.Backup, rules anonymize.Rules, salt, mongoDB string, st storage.Storage, target store.BackupTarget) {
	defer c.steps.done(backupKey(cp.ID))
	// One throwaway server at a time, restore tests included.
	select {
	case c.verifySem <- struct{}{}:
		defer func() { <-c.verifySem }()
	case <-c.bg.Done():
		return
	}
	ctx, cancel := context.WithTimeout(c.bg, verifyTimeout)
	defer cancel()
	log := c.log.With("backup", src.ID, "copy", cp.ID, "service", src.ServiceName)
	start := time.Now()

	var err error
	if src.Kind == store.BackupKindDump {
		err = c.anonymizeDump(ctx, src, &cp, rules, salt, mongoDB, st, target)
	} else {
		err = c.anonymizePostgres(ctx, src, &cp, rules, salt, st, target)
	}
	cp.DurationMS = time.Since(start).Milliseconds()
	cp.Status = store.OpSucceeded
	if err != nil {
		cp.Status, cp.Error, cp.SizeBytes, cp.SHA256 = store.OpFailed, err.Error(), 0, ""
		if errors.Is(ctx.Err(), context.Canceled) {
			cp.Error = "interrupted by manager shutdown"
		}
		if derr := st.Delete(context.WithoutCancel(ctx), cp.ObjectKey); derr != nil {
			log.Warn("cannot delete partial anonymized copy", "err", derr)
		}
		log.Warn("anonymized copy failed", "err", err)
	} else {
		log.Info("anonymized copy made", "bytes", cp.SizeBytes, "ms", cp.DurationMS)
	}
	if err := c.store.FinishBackup(context.WithoutCancel(ctx), cp); err != nil {
		log.Error("cannot record anonymized copy", "err", err)
		return
	}
	c.pruneAnonymized(context.WithoutCancel(ctx), cp)
	if err != nil {
		c.notify(notify.Event{
			Type:    EventAnonymizeFailed,
			Level:   notify.Error,
			Title:   "Anonymized copy failed for " + src.ProjectName + "/" + src.ServiceName,
			Message: fmt.Sprintf("Previews can't be seeded with the backup of %s: %s", src.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), cp.Error),
		}, "/projects/"+src.ProjectID, "")
	}
}

// pruneAnonymized deletes the copies of cp's service that cp replaces: all
// the others once it succeeded, the other failed ones otherwise.
func (c *Core) pruneAnonymized(ctx context.Context, cp store.Backup) {
	others, err := c.store.ListBackups(ctx, store.BackupFilter{Anonymized: true, ServiceID: cp.ServiceID})
	if err != nil {
		return
	}
	for _, o := range others {
		if o.ID == cp.ID || o.Status == store.OpRunning || (cp.Status == store.OpFailed && o.Status == store.OpSucceeded) {
			continue
		}
		if err := c.DeleteBackup(ctx, o.ID); err != nil {
			c.log.Warn("cannot delete old anonymized copy", "copy", o.ID, "err", err)
		}
	}
}

// anonymizePostgres restores src into a throwaway server, applies the
// rules in one transaction and dumps the result as cp.
func (c *Core) anonymizePostgres(ctx context.Context, src store.Backup, cp *store.Backup, rules anonymize.Rules, salt string,
	st storage.Storage, target store.BackupTarget) error {

	script, err := anonymize.Postgres(rules, salt)
	if err != nil {
		return err
	}
	key := backupKey(cp.ID)
	const user, db = "app", "app"
	dk, id, err := c.scratchPostgres(ctx, src, "kipitiny-anonymize-"+strings.ToLower(cp.ID), user, db, func(s string) { c.steps.set(key, s) })
	if err != nil {
		return err
	}
	defer c.removeScratch(ctx, dk, id)
	// An empty database, as a restore does (the image may fill db).
	const anon = "anonymized"
	if err := dk.Exec(ctx, id, docker.ExecOptions{
		Cmd: []string{"psql", "-U", user, "-d", db, "-v", "ON_ERROR_STOP=1", "-qc", "CREATE DATABASE " + anon + " TEMPLATE template0"},
	}); err != nil {
		return fmt.Errorf("create database: %w", err)
	}
	c.steps.set(key, VerifyRestore)
	rc, err := st.Get(ctx, src.ObjectKey)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer rc.Close()
	if err := c.pgRestore(ctx, dk, id, user, anon, rc, target, src); err != nil {
		return err
	}
	c.steps.set(key, AnonymizeRules)
	if err := dk.Exec(ctx, id, docker.ExecOptions{
		Cmd:    []string{"psql", "-X", "-q", "-1", "-v", "ON_ERROR_STOP=1", "-U", user, "-d", anon},
		Stdin:  strings.NewReader(script),
		Stdout: io.Discard,
	}); err != nil {
		return fmt.Errorf("apply rules: %w", err)
	}
	return c.upload(ctx, st, target, cp, func(w io.Writer) error {
		if err := dk.Exec(ctx, id, docker.ExecOptions{Cmd: []string{"pg_dump", "-U", user, "-d", anon, "-Fc"}, Stdout: w}); err != nil {
			return fmt.Errorf("pg_dump: %w", err)
		}
		return nil
	})
}

// anonymizeDump loads a MySQL, MariaDB or MongoDB dump into a throwaway
// server, applies the rules and dumps the result as cp.
func (c *Core) anonymizeDump(ctx context.Context, src store.Backup, cp *store.Backup, rules anonymize.Rules, salt, mongoDB string,
	st storage.Storage, target store.BackupTarget) error {

	key := backupKey(cp.ID)
	svc, dk, id, err := c.scratchDump(ctx, src, "kipitiny-anonymize-"+strings.ToLower(cp.ID), func(s string) { c.steps.set(key, s) })
	if err != nil {
		return err
	}
	defer c.removeScratch(ctx, dk, id)
	c.steps.set(key, VerifyRestore)
	if err := c.loadDump(ctx, dk, id, svc, svc.Env[mysqlDatabase], st, target, src); err != nil {
		return err
	}
	c.steps.set(key, AnonymizeRules)
	opts := docker.ExecOptions{Stdout: io.Discard}
	if svc.Kind == store.ServiceKindMongoDB {
		script, env, err := anonymize.Mongo(rules, salt, mongoDB)
		if err != nil {
			return err
		}
		opts.Cmd, opts.Env = append([]string{"mongosh", "--quiet", "--eval", script}, mongoAuthArgs(svc)...), env
	} else {
		script, err := anonymize.MySQL(rules, salt)
		if err != nil {
			return err
		}
		opts.Cmd = []string{mysqlBin(svc.Kind, "mysql"), "-uroot", "--default-character-set=utf8mb4", "--", svc.Env[mysqlDatabase]}
		opts.Env, opts.Stdin = mysqlExecEnv(svc), strings.NewReader(script)
	}
	if err := dk.Exec(ctx, id, opts); err != nil {
		return fmt.Errorf("apply rules: %w", err)
	}
	return c.dumpDatabase(ctx, svc, id, st, target, cp)
}

// AnonymizeRules: an anonymized copy is applying the database's rules.
const AnonymizeRules = "anonymize"

// SeedDatabase is a database of a project, as previews are seeded with it.
type SeedDatabase struct {
	ServiceID string            `json:"serviceId"`
	Name      string            `json:"name"`
	Kind      store.ServiceKind `json:"kind"`
	// Rules are its anonymize rules; nil has none.
	Rules anonymize.Rules `json:"rules"`
	// Backup is its latest backup a preview can be seeded with.
	Backup *store.Backup `json:"backup,omitempty"`
	// Copy is the latest anonymized copy made with the current rules,
	// whatever its status; Seeds the one previews get, the latest good one.
	Copy  *store.Backup `json:"copy,omitempty"`
	Seeds *store.Backup `json:"seeds,omitempty"`
}

// PreviewSeed lists the databases of a project, as previews are seeded
// with them (Redis aside: previews start without its data).
func (c *Core) PreviewSeed(ctx context.Context, projectID string) ([]SeedDatabase, error) {
	if _, err := c.store.GetProject(ctx, projectID); err != nil {
		return nil, err
	}
	ps, err := c.previewSettings(ctx, projectID)
	if err != nil {
		return nil, err
	}
	svcs, err := c.store.ListServices(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := []SeedDatabase{}
	for _, s := range svcs {
		if !anonymizable(s.Kind) {
			continue
		}
		d, err := c.seedDatabase(ctx, s, ps.Salt)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b SeedDatabase) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func anonymizable(k store.ServiceKind) bool {
	return k.IsDatabase() && k != store.ServiceKindRedis
}

func (c *Core) seedDatabase(ctx context.Context, s store.Service, salt string) (SeedDatabase, error) {
	d := SeedDatabase{ServiceID: s.ID, Name: s.Name, Kind: s.Kind, Rules: s.Anonymize}
	if b, ok, err := c.latestSeedable(ctx, s.ID); err != nil {
		return d, err
	} else if ok {
		d.Backup = &b
	}
	if s.Anonymize == nil || salt == "" {
		return d, nil
	}
	copies, err := c.store.ListBackups(ctx, store.BackupFilter{Anonymized: true, ServiceID: s.ID})
	if err != nil {
		return d, err
	}
	hash := s.Anonymize.Hash(salt)
	for _, cp := range copies { // newest first
		if cp.RulesHash != hash {
			continue
		}
		if d.Copy == nil {
			d.Copy = &cp
		}
		if cp.Status == store.OpSucceeded {
			d.Seeds = &cp
			break
		}
	}
	return d, nil
}

// SetAnonymizeRules sets the anonymize rules of one of a project's
// databases (nil removes them), unless they come from its git repository.
func (c *Core) SetAnonymizeRules(ctx context.Context, projectID, service string, rules anonymize.Rules) (SeedDatabase, error) {
	p, err := c.store.GetProject(ctx, projectID)
	if err != nil {
		return SeedDatabase{}, err
	}
	if p.ParentID != "" {
		return SeedDatabase{}, fmt.Errorf("%w: a preview's databases aren't anonymized", ErrInvalid)
	}
	if g, err := c.store.GetProjectGit(ctx, projectID); err == nil {
		return SeedDatabase{}, fmt.Errorf("%w: %s follows git: set the rules in x-kipitiny.anonymize of %s", ErrInvalid, p.Name, g.Path)
	} else if !errors.Is(err, store.ErrNotFound) {
		return SeedDatabase{}, err
	}
	svc, err := c.serviceNamed(ctx, projectID, service)
	if err != nil {
		return SeedDatabase{}, err
	}
	if !anonymizable(svc.Kind) {
		return SeedDatabase{}, fmt.Errorf("%w: only PostgreSQL, MySQL, MariaDB and MongoDB databases are anonymized", ErrInvalid)
	}
	if err := rules.Validate(); err != nil {
		return SeedDatabase{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := c.store.SetServiceAnonymize(ctx, svc.ID, rules); err != nil {
		return SeedDatabase{}, err
	}
	svc.Anonymize = rules
	c.logAnonymizeErr(c.refreshAnonymized(ctx, projectID, false))
	ps, err := c.previewSettings(ctx, projectID)
	if err != nil {
		return SeedDatabase{}, err
	}
	return c.seedDatabase(ctx, svc, ps.Salt)
}

// AnonymizeNow makes the anonymized copy of a database's latest backup
// again, after a failure.
func (c *Core) AnonymizeNow(ctx context.Context, projectID, service string) (store.Backup, error) {
	ps, err := c.previewSettings(ctx, projectID)
	if err != nil {
		return store.Backup{}, err
	}
	if ps.SeedMode != store.SeedAnonymized {
		return store.Backup{}, fmt.Errorf("%w: previews aren't seeded with anonymized data", ErrInvalid)
	}
	svc, err := c.serviceNamed(ctx, projectID, service)
	if err != nil {
		return store.Backup{}, err
	}
	if svc.Anonymize == nil {
		return store.Backup{}, fmt.Errorf("%w: %s has no anonymize rules", ErrInvalid, svc.Name)
	}
	cp, err := c.anonymizeLatest(ctx, svc, true)
	if err == nil && cp.ID == "" {
		err = fmt.Errorf("%w: %s has no backup to anonymize yet", ErrInvalid, svc.Name)
	}
	return cp, err
}

func (c *Core) serviceNamed(ctx context.Context, projectID, name string) (store.Service, error) {
	svcs, err := c.store.ListServices(ctx, projectID)
	if err != nil {
		return store.Service{}, err
	}
	if i := slices.IndexFunc(svcs, func(s store.Service) bool { return s.Name == name }); i >= 0 {
		return svcs[i], nil
	}
	return store.Service{}, fmt.Errorf("%w: service %q", store.ErrNotFound, name)
}

// seedAnonymized picks the anonymized copy each parent database the
// preview has seeds it with, refusing when one has no rules or no good
// copy made with them: production data is never the fallback.
func (c *Core) seedAnonymized(ctx context.Context, parent store.Project, f compose.File, parentSvcs []store.Service) (map[string]string, error) {
	ps, err := c.previewSettings(ctx, parent.ID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	var problems []string
	for _, s := range parentSvcs {
		if _, listed := f.Services[s.Name]; !listed || !anonymizable(s.Kind) {
			continue
		}
		if s.Anonymize == nil {
			problems = append(problems, s.Name+" has no anonymize rules (an empty set says it holds nothing personal)")
			continue
		}
		d, err := c.seedDatabase(ctx, s, ps.Salt)
		if err != nil {
			return nil, err
		}
		switch {
		case d.Seeds != nil:
			out[s.Name] = d.Seeds.ID
		case d.Backup == nil:
			problems = append(problems, "no backup of "+s.Name+" to anonymize")
		case d.Copy != nil && d.Copy.Status == store.OpRunning:
			problems = append(problems, s.Name+"'s anonymized copy is still being made")
		case d.Copy != nil:
			problems = append(problems, s.Name+"'s anonymized copy failed: "+d.Copy.Error)
		default:
			problems = append(problems, s.Name+" has no anonymized copy yet")
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%w: can't seed the preview with anonymized data: %s", ErrInvalid, strings.Join(problems, "; "))
	}
	return out, nil
}
