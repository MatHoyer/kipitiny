package data

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/anonymize"
	"github.com/kipit-labs/kipitiny/internal/compose"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

// AnonymizeRules: an anonymized copy is applying the database's rules.
const AnonymizeRules = "anonymize"

// SeedDatabase is a database of a project, as previews are seeded with it.
type SeedDatabase struct {
	ServiceID string             `json:"serviceId"`
	Name      string             `json:"name"`
	Kind      domain.ServiceKind `json:"kind"`
	// Rules are its anonymize rules; nil has none.
	Rules anonymize.Rules `json:"rules"`
	// Backup is its latest backup a preview can be seeded with.
	Backup *domain.Backup `json:"backup,omitempty"`
	// Copy is the latest anonymized copy made with the current rules,
	// whatever its status; Seeds the one previews get, the latest good one.
	Copy  *domain.Backup `json:"copy,omitempty"`
	Seeds *domain.Backup `json:"seeds,omitempty"`
}

// PreviewSeed lists the databases of a project, as previews are seeded
// with them (Redis aside: previews start without its data).
func (c *Data) PreviewSeed(ctx context.Context, projectID string) ([]SeedDatabase, error) {
	if _, err := c.store.GetProject(ctx, projectID); err != nil {
		return nil, err
	}
	ps, err := c.mgr.PreviewSettings(ctx, projectID)
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

func anonymizable(k domain.ServiceKind) bool {
	return k.IsDatabase() && k != domain.ServiceKindRedis
}

func (c *Data) seedDatabase(ctx context.Context, s domain.Service, salt string) (SeedDatabase, error) {
	d := SeedDatabase{ServiceID: s.ID, Name: s.Name, Kind: s.Kind, Rules: s.Anonymize}
	if b, ok, err := c.LatestSeedable(ctx, s.ID); err != nil {
		return d, err
	} else if ok {
		d.Backup = &b
	}
	if s.Anonymize == nil || salt == "" {
		return d, nil
	}
	copies, err := c.store.ListBackups(ctx, domain.BackupFilter{Anonymized: true, ServiceID: s.ID})
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
		if cp.Status == domain.OpSucceeded {
			d.Seeds = &cp
			break
		}
	}
	return d, nil
}

// SetAnonymizeRules sets the anonymize rules of one of a project's
// databases (nil removes them), unless they come from its git repository.
func (c *Data) SetAnonymizeRules(ctx context.Context, projectID, service string, rules anonymize.Rules) (SeedDatabase, error) {
	p, err := c.store.GetProject(ctx, projectID)
	if err != nil {
		return SeedDatabase{}, err
	}
	if p.ParentID != "" {
		return SeedDatabase{}, fmt.Errorf("%w: a preview's databases aren't anonymized", ErrInvalid)
	}
	if g, err := c.store.GetProjectGit(ctx, projectID); err == nil {
		return SeedDatabase{}, fmt.Errorf("%w: %s follows git: set the rules in x-kipitiny.anonymize of %s", ErrInvalid, p.Name, g.Path)
	} else if !errors.Is(err, domain.ErrNotFound) {
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
		return SeedDatabase{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	if err := c.store.SetServiceAnonymize(ctx, svc.ID, rules); err != nil {
		return SeedDatabase{}, err
	}
	svc.Anonymize = rules
	c.LogAnonymizeErr(c.RefreshAnonymized(ctx, projectID, false))
	ps, err := c.mgr.PreviewSettings(ctx, projectID)
	if err != nil {
		return SeedDatabase{}, err
	}
	return c.seedDatabase(ctx, svc, ps.Salt)
}

// AnonymizeNow makes the anonymized copy of a database's latest backup
// again, after a failure.
func (c *Data) AnonymizeNow(ctx context.Context, projectID, service string) (domain.Backup, error) {
	ps, err := c.mgr.PreviewSettings(ctx, projectID)
	if err != nil {
		return domain.Backup{}, err
	}
	if ps.SeedMode != domain.SeedAnonymized {
		return domain.Backup{}, fmt.Errorf("%w: previews aren't seeded with anonymized data", ErrInvalid)
	}
	svc, err := c.serviceNamed(ctx, projectID, service)
	if err != nil {
		return domain.Backup{}, err
	}
	if svc.Anonymize == nil {
		return domain.Backup{}, fmt.Errorf("%w: %s has no anonymize rules", ErrInvalid, svc.Name)
	}
	cp, err := c.anonymizeLatest(ctx, svc, true)
	if err == nil && cp.ID == "" {
		err = fmt.Errorf("%w: %s has no backup to anonymize yet", ErrInvalid, svc.Name)
	}
	return cp, err
}

func (c *Data) serviceNamed(ctx context.Context, projectID, name string) (domain.Service, error) {
	svcs, err := c.store.ListServices(ctx, projectID)
	if err != nil {
		return domain.Service{}, err
	}
	if i := slices.IndexFunc(svcs, func(s domain.Service) bool { return s.Name == name }); i >= 0 {
		return svcs[i], nil
	}
	return domain.Service{}, fmt.Errorf("%w: service %q", domain.ErrNotFound, name)
}

// SeedAnonymized picks the anonymized copy each parent database the
// preview has seeds it with, refusing when one has no rules or no good
// copy made with them: production data is never the fallback.
func (c *Data) SeedAnonymized(ctx context.Context, parent domain.Project, f compose.File, parentSvcs []domain.Service) (map[string]string, error) {
	ps, err := c.mgr.PreviewSettings(ctx, parent.ID)
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
		case d.Copy != nil && d.Copy.Status == domain.OpRunning:
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
