package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
	"github.com/kipit-labs/kipitiny/internal/usecase/servers"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/app"
)

type getServerStatusIn struct {
	Server string `json:"server,omitempty" jsonschema:"only this server, by name; default every server"`
}

// ServerStatus is a server with its host's current use and last security
// check.
type ServerStatus struct {
	servers.ServerView
	// Usage is nil while the host can't be read.
	Usage *servers.HostUsage `json:"usage"`
	// Security is only shown to admins.
	Security *servers.SecurityReport `json:"security,omitempty"`
}

type getServerStatusOut struct {
	Servers []ServerStatus `json:"servers"`
}

func (t *tools) getServerStatus(ctx context.Context, _ *mcp.CallToolRequest, in getServerStatusIn) (*mcp.CallToolResult, getServerStatusOut, error) {
	if err := identity.Authenticated(ctx); err != nil {
		return nil, getServerStatusOut{}, err
	}
	if err := identity.RequireFeature(ctx, identity.FeatureServers); err != nil {
		return nil, getServerStatusOut{}, err
	}
	servers, err := t.c.ListServers(ctx)
	if err != nil {
		return nil, getServerStatusOut{}, friendly(err)
	}
	admin := identity.RequireAdmin(ctx) == nil
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
		return nil, getServerStatusOut{}, friendly(fmt.Errorf("%w: server %s", domain.ErrNotFound, in.Server))
	}
	return nil, out, nil
}

func (t *tools) getCleanup(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, servers.CleanupView, error) {
	if err := identity.RequireAdmin(ctx); err != nil {
		return nil, servers.CleanupView{}, err
	}
	return nil, t.c.Cleanup(ctx), nil
}

func (t *tools) runCleanup(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, servers.CleanupView, error) {
	out, err := mutate(ctx, t.c, "run_cleanup", "cleanup", func() (servers.CleanupView, error) {
		if err := identity.RequireAdmin(ctx); err != nil {
			return servers.CleanupView{}, err
		}
		v, err := t.c.RunCleanup(ctx)
		if errors.Is(err, app.ErrBusy) {
			err = errors.New("a cleanup is already running; get_cleanup shows its step")
		}
		return v, err
	})
	return nil, out, err
}

type installServerUpdatesIn struct {
	Server string `json:"server,omitempty" jsonschema:"the server's name; this one by default"`
	All    bool   `json:"all,omitempty" jsonschema:"every pending update, not only security ones"`
}

func (t *tools) installServerUpdates(ctx context.Context, _ *mcp.CallToolRequest, in installServerUpdatesIn) (*mcp.CallToolResult, servers.SecurityReport, error) {
	out, err := mutate(ctx, t.c, "install_server_updates", in.Server, func() (servers.SecurityReport, error) {
		id, err := t.serverID(ctx, in.Server)
		if err != nil {
			return servers.SecurityReport{}, err
		}
		rep, err := t.c.InstallUpdates(ctx, id, in.All)
		if errors.Is(err, app.ErrBusy) {
			err = errors.New("updates are already being installed on this server; get_server_status shows the install")
		}
		return rep, err
	})
	return nil, out, err
}

type rebootServerIn struct {
	Server  string `json:"server,omitempty" jsonschema:"the server's name; this one by default"`
	Confirm string `json:"confirm" jsonschema:"must equal the server's name"`
}

func (t *tools) rebootServer(ctx context.Context, _ *mcp.CallToolRequest, in rebootServerIn) (*mcp.CallToolResult, servers.SecurityReport, error) {
	out, err := mutate(ctx, t.c, "reboot_server", in.Server, func() (servers.SecurityReport, error) {
		if err := identity.RequireAdmin(ctx); err != nil {
			return servers.SecurityReport{}, err
		}
		id, err := t.serverID(ctx, in.Server)
		if err != nil {
			return servers.SecurityReport{}, err
		}
		names, err := t.serverNames(ctx)
		if err != nil {
			return servers.SecurityReport{}, err
		}
		if in.Confirm == "" || in.Confirm != names[id] {
			return servers.SecurityReport{}, fmt.Errorf("rebooting stops every app on the server: confirm must repeat the server name (%s)", names[id])
		}
		rep, err := t.c.RebootServer(ctx, id)
		if errors.Is(err, app.ErrBusy) {
			err = errors.New(strings.TrimPrefix(err.Error(), app.ErrBusy.Error()+": "))
		}
		return rep, err
	})
	return nil, out, err
}
