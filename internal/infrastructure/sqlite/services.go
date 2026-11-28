package sqlite

import (
	"context"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/anonymize"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

func (s *Store) CreateService(ctx context.Context, svc domain.Service) (domain.Service, error) {
	now := now()
	svc.ID, svc.CreatedAt, svc.UpdatedAt = ids.New(), now, now
	if svc.Secrets == nil {
		svc.Secrets = []string{}
	}
	if svc.Volumes == nil {
		svc.Volumes = []domain.Volume{}
	}
	if svc.PublishedPorts == nil {
		svc.PublishedPorts = []domain.PublishedPort{}
	}
	if svc.Routes == nil {
		svc.Routes = []domain.Route{}
	}
	if svc.Files == nil {
		svc.Files = []domain.ServiceFile{}
	}
	if svc.DependsOn == nil {
		svc.DependsOn = []domain.Dependency{}
	}
	if svc.ProjectNetworks == nil {
		svc.ProjectNetworks = []string{}
	}
	if svc.Networks == nil {
		svc.Networks = []string{}
	}
	if svc.Tags == nil {
		svc.Tags = []string{}
	}
	// Services always run on their project's server.
	err := s.db.NewSelect().Model((*domain.Project)(nil)).Column("server_id").
		Where("id = ?", svc.ProjectID).Scan(ctx, &svc.ServerID)
	if err != nil {
		return domain.Service{}, mapErr(err)
	}
	if _, err := s.db.NewInsert().Model(&svc).Exec(ctx); err != nil {
		return domain.Service{}, mapErr(err)
	}
	return svc, nil
}

func (s *Store) GetService(ctx context.Context, id string) (domain.Service, error) {
	var svc domain.Service
	err := s.db.NewSelect().Model(&svc).Where("id = ?", id).Scan(ctx)
	return svc, mapErr(err)
}

func (s *Store) ListServices(ctx context.Context, projectID string) ([]domain.Service, error) {
	svcs := []domain.Service{}
	err := s.db.NewSelect().Model(&svcs).
		Where("project_id = ?", projectID).Order("name").Scan(ctx)
	return svcs, mapErr(err)
}

func (s *Store) UpdateService(ctx context.Context, svc domain.Service) (domain.Service, error) {
	svc.UpdatedAt = now()
	if svc.Routes == nil {
		svc.Routes = []domain.Route{}
	}
	if svc.Tags == nil {
		svc.Tags = []string{}
	}
	if svc.Files == nil {
		svc.Files = []domain.ServiceFile{}
	}
	if svc.DependsOn == nil {
		svc.DependsOn = []domain.Dependency{}
	}
	if svc.ProjectNetworks == nil {
		svc.ProjectNetworks = []string{}
	}
	res, err := s.db.NewUpdate().Model(&svc).
		Column("image", "icon", "tags", "anonymize", "replicas", "port", "domain", "analytics", "analytics_since", "analytics_script", "analytics_hit", "errors", "errors_project", "errors_key", "env", "secrets", "memory_mb", "cpus", "health_path", "healthcheck", "pre_deploy", "volumes", "middlewares", "pre_backup", "published_ports", "routes", "stop_grace_seconds", "host_network", "docker_socket", "runtime", "files", "depends_on", "project_networks", "preview", "updated_at").
		WherePK().Exec(ctx)
	if err != nil {
		return domain.Service{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Service{}, domain.ErrNotFound
	}
	return svc, nil
}

func (s *Store) RenameService(ctx context.Context, id, name string) error {
	res, err := s.db.NewUpdate().Model((*domain.Service)(nil)).
		Set("name = ?", name).Set("updated_at = ?", now()).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetServiceAnalyticsShare(ctx context.Context, id, hash string) error {
	svc := domain.Service{ID: id, AnalyticsShare: hash, UpdatedAt: now()}
	if hash != "" {
		svc.AnalyticsSharedAt = now()
	}
	res, err := s.db.NewUpdate().Model(&svc).Column("analytics_share", "analytics_shared_at", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) GetServiceByAnalyticsShare(ctx context.Context, hash string) (domain.Service, error) {
	var svc domain.Service
	if hash == "" {
		return svc, domain.ErrNotFound
	}
	err := s.db.NewSelect().Model(&svc).Where("analytics_share = ?", hash).Scan(ctx)
	return svc, mapErr(err)
}

func (s *Store) SetServiceTags(ctx context.Context, id string, tags []string) error {
	if tags == nil {
		tags = []string{}
	}
	svc := domain.Service{ID: id, Tags: tags, UpdatedAt: now()}
	res, err := s.db.NewUpdate().Model(&svc).Column("tags", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetServiceAnonymize(ctx context.Context, id string, rules anonymize.Rules) error {
	svc := domain.Service{ID: id, Anonymize: rules, UpdatedAt: now()}
	res, err := s.db.NewUpdate().Model(&svc).Column("anonymize", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetCurrentDeployment(ctx context.Context, serviceID, deploymentID string) error {
	res, err := s.db.NewUpdate().Model((*domain.Service)(nil)).
		Set("current_deployment_id = ?", deploymentID).Where("id = ?", serviceID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetDeploymentImage(ctx context.Context, id, image string, config domain.Service) error {
	d := domain.Deployment{ID: id, Image: image, Config: config}
	res, err := s.db.NewUpdate().Model(&d).Column("image", "config").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetServiceStopped(ctx context.Context, serviceID string, stopped bool) error {
	res, err := s.db.NewUpdate().Model((*domain.Service)(nil)).
		Set("stopped = ?", stopped).Set("sleeping = ?", false).Where("id = ?", serviceID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetServiceImage(ctx context.Context, serviceID, image string) error {
	res, err := s.db.NewUpdate().Model((*domain.Service)(nil)).
		Set("image = ?", image).Set("updated_at = ?", now()).Where("id = ?", serviceID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) ListAllServices(ctx context.Context) ([]domain.Service, error) {
	svcs := []domain.Service{}
	err := s.db.NewSelect().Model(&svcs).Order("project_id", "name").Scan(ctx)
	return svcs, mapErr(err)
}

func (s *Store) DeleteService(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "services", id)
}

func (s *Store) SetServiceGitState(ctx context.Context, serviceID, specHash string, orphaned bool) error {
	res, err := s.db.NewUpdate().Model((*domain.Service)(nil)).
		Set("git_spec_hash = ?", specHash).Set("orphaned = ?", orphaned).Where("id = ?", serviceID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetServiceNetworks(ctx context.Context, serviceID string, networkIDs []string) error {
	if networkIDs == nil {
		networkIDs = []string{}
	}
	res, err := s.db.NewUpdate().Model(&domain.Service{ID: serviceID, Networks: networkIDs}).
		Column("networks").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
