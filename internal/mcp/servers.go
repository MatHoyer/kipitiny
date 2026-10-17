package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

type getServerStatusIn struct {
	Server string `json:"server,omitempty" jsonschema:"only this server, by name; default every server"`
}

// ServerStatus is a server with its host's current use and last security
// check.
type ServerStatus struct {
	core.ServerView
	// Usage is nil while the host can't be read.
	Usage *core.HostUsage `json:"usage"`
	// Security is only shown to admins.
	Security *core.SecurityReport `json:"security,omitempty"`
}

type getServerStatusOut struct {
	Servers []ServerStatus `json:"servers"`
}

func (t *tools) getServerStatus(ctx context.Context, _ *mcp.CallToolRequest, in getServerStatusIn) (*mcp.CallToolResult, getServerStatusOut, error) {
	if err := core.Authenticated(ctx); err != nil {
		return nil, getServerStatusOut{}, err
	}
	if err := core.RequireFeature(ctx, core.FeatureServers); err != nil {
		return nil, getServerStatusOut{}, err
	}
	servers, err := t.c.ListServers(ctx)
	if err != nil {
		return nil, getServerStatusOut{}, friendly(err)
	}
	admin := core.RequireAdmin(ctx) == nil
	out := getServerStatusOut{Servers: []ServerStatus{}}
	for _, sv := range servers {
		if in.Server != "" && sv.Name != in.Server {
			continue
		}
		s := ServerStatus{ServerView: sv}
		st, err := t.c.ServerStats(ctx, sv.ID)
		if err != nil {
			return nil, getServerStatusOut{}, friendly(err)
		}
		s.Usage = st.Current
		if admin {
			rep, err := t.c.ServerSecurity(ctx, sv.ID)
			if err != nil {
				return nil, getServerStatusOut{}, friendly(err)
			}
			s.Security = &rep
		}
		out.Servers = append(out.Servers, s)
	}
	if in.Server != "" && len(out.Servers) == 0 {
		return nil, getServerStatusOut{}, friendly(fmt.Errorf("%w: server %s", store.ErrNotFound, in.Server))
	}
	return nil, out, nil
}
