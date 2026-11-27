package mcp

import (
	"context"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kipit-labs/kipitiny/internal/core"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

// backup checks p on a backup's project; the manager's own are admin only.
func (t *tools) backup(ctx context.Context, id string, p identity.Perm) error {
	projectID, err := t.c.ProjectOf(ctx, identity.IDBackup, id)
	if err != nil {
		return err
	}
	if projectID == "" {
		return identity.RequireAdmin(ctx)
	}
	return identity.Authorize(ctx, projectID, p)
}

type backupIn struct {
	Database string `json:"database" jsonschema:"the service to back up (a database or an app with volumes) as project/service, or its ID"`
	Storage  string `json:"storage,omitempty" jsonschema:"storage ID (see list_storage); default local disk"`
	// The service is "database" for compatibility; this picks inside it.
	DatabaseName string `json:"database_name,omitempty" jsonschema:"for PostgreSQL, which of the instance's databases to dump; default the service's own"`
}

func (t *tools) backupDatabase(ctx context.Context, _ *mcp.CallToolRequest, in backupIn) (*mcp.CallToolResult, domain.Backup, error) {
	b, err := mutate(ctx, t.c, "backup_database", in.Database, func() (domain.Backup, error) {
		svc, err := t.service(ctx, in.Database, identity.PermBackups)
		if err != nil {
			return domain.Backup{}, err
		}
		return t.c.BackupService(ctx, svc.ID, in.Storage, in.DatabaseName)
	})
	return nil, b, err
}

// StorageSummary is a storage as agents see it: no endpoints or credentials.
type StorageSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Encrypted bool   `json:"encrypted"`
}

type listStorageOut struct {
	Storage []StorageSummary `json:"storage"`
}

func (t *tools) listStorage(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listStorageOut, error) {
	if err := identity.Authenticated(ctx); err != nil {
		return nil, listStorageOut{}, err
	}
	if err := identity.RequireFeature(ctx, identity.FeatureStorage); err != nil {
		return nil, listStorageOut{}, err
	}
	ts, err := t.c.ListBackupTargets(ctx)
	if err != nil {
		return nil, listStorageOut{}, err
	}
	out := listStorageOut{Storage: make([]StorageSummary, len(ts))}
	for i, s := range ts {
		out.Storage[i] = StorageSummary{ID: s.ID, Name: s.Name, Kind: string(s.Kind), Encrypted: s.Encrypted()}
	}
	return nil, out, nil
}

type scanStorageIn struct {
	Storage string `json:"storage" jsonschema:"storage ID (see list_storage)"`
}

func (t *tools) scanStorage(ctx context.Context, _ *mcp.CallToolRequest, in scanStorageIn) (*mcp.CallToolResult, core.ScanResult, error) {
	out, err := mutate(ctx, t.c, "scan_storage", in.Storage, func() (core.ScanResult, error) {
		if err := identity.RequireAdmin(ctx); err != nil {
			return core.ScanResult{}, err
		}
		return t.c.ScanBackupTarget(ctx, in.Storage)
	})
	return nil, out, err
}

type listBackupsIn struct {
	Database string `json:"database" jsonschema:"the service as project/service, or its ID"`
}

type listBackupsOut struct {
	Backups []domain.Backup `json:"backups"`
}

func (t *tools) listBackups(ctx context.Context, _ *mcp.CallToolRequest, in listBackupsIn) (*mcp.CallToolResult, listBackupsOut, error) {
	if err := identity.Authenticated(ctx); err != nil {
		return nil, listBackupsOut{}, err
	}
	svc, err := t.service(ctx, in.Database, identity.PermView)
	if err != nil {
		return nil, listBackupsOut{}, friendly(err)
	}
	bs, err := t.c.ListBackups(ctx, domain.BackupFilter{ServiceID: svc.ID, Limit: 50})
	return nil, listBackupsOut{Backups: bs}, err
}

type restoreIn struct {
	Database string `json:"database" jsonschema:"the service to overwrite, as project/service or ID"`
	BackupID string `json:"backup_id" jsonschema:"backup to restore (see list_backups)"`
	Confirm  string `json:"confirm" jsonschema:"must equal the service name to confirm the data will be replaced"`
}

func (t *tools) restoreDatabase(ctx context.Context, _ *mcp.CallToolRequest, in restoreIn) (*mcp.CallToolResult, domain.Restore, error) {
	r, err := mutate(ctx, t.c, "restore_database", in.Database, func() (domain.Restore, error) {
		svc, err := t.service(ctx, in.Database, identity.PermRestore)
		if err != nil {
			return domain.Restore{}, err
		}
		return t.c.RestoreBackup(ctx, in.BackupID, svc.ID, in.Confirm)
	})
	return nil, r, err
}
