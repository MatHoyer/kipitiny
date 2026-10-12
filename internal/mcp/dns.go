package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

type syncDNSOut struct {
	Cloudflare core.CloudflareView `json:"cloudflare"`
	// Records is each managed hostname's state: synced, warning, conflict or error.
	Records map[string]core.DNSStatus `json:"records"`
}

func (t *tools) syncDNS(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, syncDNSOut, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "sync_dns", "cloudflare", func() (syncDNSOut, error) {
		if err := core.RequireAdmin(ctx); err != nil {
			return syncDNSOut{}, err
		}
		v, err := t.c.SyncDNS(ctx)
		if err != nil {
			return syncDNSOut{}, err
		}
		return syncDNSOut{Cloudflare: v, Records: t.c.DNSRecords(ctx)}, nil
	})
	return nil, out, err
}
