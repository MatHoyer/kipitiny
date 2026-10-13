package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

type setSleepIn struct {
	Service string `json:"service" jsonschema:"the app as project/service, or its ID"`
	After   string `json:"after" jsonschema:"idle time before the app sleeps, as a duration (30m, 2h; 5 minutes to 30 days); 0 turns sleep mode off"`
}

type setSleepOut struct {
	SleepAfterSeconds int  `json:"sleepAfterSeconds"`
	Sleeping          bool `json:"sleeping"`
}

func (t *tools) setSleep(ctx context.Context, _ *mcp.CallToolRequest, in setSleepIn) (*mcp.CallToolResult, setSleepOut, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "set_sleep", in.Service, func() (setSleepOut, error) {
		svc, err := t.service(ctx, in.Service, core.PermSettings)
		if err != nil {
			return setSleepOut{}, err
		}
		secs := 0
		if in.After != "0" && in.After != "" {
			d, err := time.ParseDuration(in.After)
			if err != nil {
				return setSleepOut{}, err
			}
			secs = int(d.Seconds())
		}
		v, err := t.c.UpdateService(ctx, svc.ID, core.ServicePatch{SleepAfterSeconds: &secs})
		return setSleepOut{SleepAfterSeconds: v.SleepAfterSeconds, Sleeping: v.Sleeping}, err
	})
	return nil, out, err
}
