package servers

import (
	"context"
	"os"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
)

type Status struct {
	Version     string             `json:"version"`
	Update      UpdateInfo         `json:"update"`
	Docker      *domain.DockerInfo `json:"docker"`
	DockerError string             `json:"dockerError,omitempty"`
	// Database is the size of the manager's SQLite files, absent when they
	// can't be read.
	Database *DatabaseSize `json:"database,omitempty"`
}

type DatabaseSize struct {
	SizeBytes int64 `json:"sizeBytes"`
	// WALBytes is the write-ahead log not yet checkpointed into the file.
	WALBytes int64 `json:"walBytes"`
}

func (c *Servers) databaseSize() *DatabaseSize {
	fi, err := os.Stat(c.cfg.DBPath())
	if err != nil {
		return nil
	}
	d := &DatabaseSize{SizeBytes: fi.Size()}
	if wal, err := os.Stat(c.cfg.DBPath() + "-wal"); err == nil {
		d.WALBytes = wal.Size()
	}
	return d
}

func (c *Servers) Status(ctx context.Context) Status {
	if a, ok := identity.ActorFrom(ctx); ok && !a.Admin() {
		return Status{Version: c.cfg.Version} // the rest is about the server
	}
	st := Status{Version: c.cfg.Version, Update: c.Update(), Database: c.databaseSize()}
	info, err := c.DockerFor(domain.LocalServerID).Info(ctx)
	if err != nil {
		st.DockerError = err.Error()
		return st
	}
	st.Docker = &info
	return st
}
