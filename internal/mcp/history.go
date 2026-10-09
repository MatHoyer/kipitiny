package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Backup upkeep and the history of deployments and restores.

type backupArg struct {
	BackupID string `json:"backup_id" jsonschema:"the backup (see list_backups)"`
}

func (t *tools) verifyBackup(ctx context.Context, _ *mcp.CallToolRequest, in backupArg) (*mcp.CallToolResult, store.Backup, error) {
	b, err := mutate(ctx, t.c, store.ScopeDeploy, "verify_backup", in.BackupID, func() (store.Backup, error) {
		return t.c.VerifyBackup(ctx, in.BackupID)
	})
	return nil, b, err
}

func (t *tools) deleteBackup(ctx context.Context, _ *mcp.CallToolRequest, in backupArg) (*mcp.CallToolResult, deleted, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "delete_backup", in.BackupID, func() (deleted, error) {
		return deleted{Deleted: in.BackupID}, t.c.DeleteBackup(ctx, in.BackupID)
	})
	return nil, out, err
}

type backupProjectIn struct {
	Project string `json:"project" jsonschema:"the project name"`
	Storage string `json:"storage,omitempty" jsonschema:"storage ID (see list_storage); default local disk"`
}

type backupProjectOut struct {
	Backups []store.Backup `json:"backups"`
	// Error lists the services whose backup could not start.
	Error string `json:"error,omitempty"`
}

// backupProject reports partial failures next to the backups that started,
// as the REST route does.
func (t *tools) backupProject(ctx context.Context, _ *mcp.CallToolRequest, in backupProjectIn) (*mcp.CallToolResult, backupProjectOut, error) {
	out, err := mutate(ctx, t.c, store.ScopeDeploy, "backup_project", in.Project, func() (backupProjectOut, error) {
		p, err := t.findProject(ctx, in.Project)
		if err != nil {
			return backupProjectOut{}, err
		}
		started, err := t.c.BackupProject(ctx, p.ID, in.Storage)
		if err != nil && len(started) == 0 {
			return backupProjectOut{}, err
		}
		out := backupProjectOut{Backups: started}
		if err != nil {
			out.Error = err.Error()
		}
		return out, nil
	})
	return nil, out, err
}

type listRestoresOut struct {
	Restores []store.Restore `json:"restores"`
}

func (t *tools) listRestores(ctx context.Context, _ *mcp.CallToolRequest, in serviceArg) (*mcp.CallToolResult, listRestoresOut, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, listRestoresOut{}, err
	}
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, listRestoresOut{}, friendly(err)
	}
	rs, err := t.c.ListRestores(ctx, svc.ID)
	return nil, listRestoresOut{Restores: rs}, err
}

type listDeploymentsIn struct {
	Service string `json:"service" jsonschema:"the service as project/service, or its ID"`
	Limit   int    `json:"limit,omitempty" jsonschema:"newest first, default 20, max 50"`
}

type listDeploymentsOut struct {
	Deployments []store.Deployment `json:"deployments"`
}

func (t *tools) listDeployments(ctx context.Context, _ *mcp.CallToolRequest, in listDeploymentsIn) (*mcp.CallToolResult, listDeploymentsOut, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, listDeploymentsOut{}, err
	}
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, listDeploymentsOut{}, friendly(err)
	}
	deps, err := t.c.ListDeployments(ctx, svc.ID)
	if err != nil {
		return nil, listDeploymentsOut{}, err
	}
	n := in.Limit
	if n <= 0 {
		n = 20
	}
	return nil, listDeploymentsOut{Deployments: deps[:min(len(deps), n)]}, nil
}
