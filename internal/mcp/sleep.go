package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
)

type setSleepIn struct {
	Project string `json:"project" jsonschema:"the project's name or ID"`
	After   string `json:"after" jsonschema:"idle time before the project sleeps, as a duration (30m, 2h; 5 minutes to 30 days); 0 turns sleep mode off"`
}

type setSleepOut struct {
	SleepAfterSeconds int `json:"sleepAfterSeconds"`
}

func (t *tools) setSleep(ctx context.Context, _ *mcp.CallToolRequest, in setSleepIn) (*mcp.CallToolResult, setSleepOut, error) {
	out, err := mutate(ctx, t.c, "set_sleep", in.Project, func() (setSleepOut, error) {
		p, err := t.project(ctx, in.Project, core.PermSettings)
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
		v, err := t.c.SetProjectSleep(ctx, p.ID, secs)
		return setSleepOut{SleepAfterSeconds: v.SleepAfterSeconds}, err
	})
	return nil, out, err
}
