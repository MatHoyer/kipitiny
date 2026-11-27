package data

import (
	"context"
	"slices"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func (c *Data) serverVersion(ctx context.Context, svc domain.Service, containerID string) (string, error) {
	var out strings.Builder
	err := c.mgr.DockerFor(svc.ServerID).Exec(ctx, containerID, domain.ExecOptions{
		Cmd:    []string{"psql", "-U", svc.Env[PGUser], "-d", svc.Env[PGDatabase], "-tAc", "SHOW server_version"},
		Stdout: &out,
	})
	return strings.TrimSpace(out.String()), err
}

// pgExtensions lists the extensions database db uses, plpgsql aside (every
// server has it).
func (c *Data) pgExtensions(ctx context.Context, svc domain.Service, containerID, db string) ([]string, error) {
	return c.pgList(ctx, svc, containerID, db, "SELECT extname FROM pg_extension WHERE extname <> 'plpgsql' ORDER BY 1")
}

// missingExtensions returns those of exts the server in containerID can't
// create.
func (c *Data) missingExtensions(ctx context.Context, svc domain.Service, containerID string, exts []string) ([]string, error) {
	available, err := c.pgList(ctx, svc, containerID, svc.Env[PGDatabase], "SELECT name FROM pg_available_extensions")
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, e := range exts {
		if !slices.Contains(available, e) {
			missing = append(missing, e)
		}
	}
	return missing, nil
}

// pgList runs a one-column query and returns its rows.
func (c *Data) pgList(ctx context.Context, svc domain.Service, containerID, db, query string) ([]string, error) {
	var out strings.Builder
	err := c.mgr.DockerFor(svc.ServerID).Exec(ctx, containerID, domain.ExecOptions{
		Cmd:    []string{"psql", "-U", svc.Env[PGUser], "-d", db, "-v", "ON_ERROR_STOP=1", "-tAc", query},
		Stdout: &out,
	})
	if err != nil {
		return nil, err
	}
	return strings.Fields(out.String()), nil
}

func pgMajorFromVersion(v string) int {
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}
