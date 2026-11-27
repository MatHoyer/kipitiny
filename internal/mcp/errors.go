package mcp

import (
	"context"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kipit-labs/kipitiny/internal/analytics"
	"github.com/kipit-labs/kipitiny/internal/core"
)

type listIssuesIn struct {
	Service string `json:"service" jsonschema:"project/service of an app with error tracking on"`
	Status  string `json:"status,omitempty" jsonschema:"open (default), resolved, ignored or all"`
	Search  string `json:"search,omitempty" jsonschema:"text in the title or the culprit (function and file)"`
	Sort    string `json:"sort,omitempty" jsonschema:"lastSeen (default), firstSeen, events or users"`
}

func (t *tools) listIssues(ctx context.Context, _ *mcp.CallToolRequest, in listIssuesIn) (*mcp.CallToolResult, core.ErrorsView, error) {
	svc, err := t.service(ctx, in.Service, identity.PermView)
	if err != nil {
		return nil, core.ErrorsView{}, friendly(err)
	}
	status := in.Status
	switch status {
	case "":
		status = analytics.IssueOpen
	case "all":
		status = ""
	}
	out, err := t.c.ServiceErrors(ctx, svc.ID, core.ErrorsQuery{Status: status, Search: in.Search, Sort: in.Sort, Limit: 50})
	return nil, out, friendly(err)
}

type getIssueIn struct {
	Service string `json:"service" jsonschema:"project/service of an app with error tracking on"`
	Issue   int64  `json:"issue" jsonschema:"the issue's number, from list_issues"`
	Event   string `json:"event,omitempty" jsonschema:"one of its kept events (older/newer of a previous answer); default the latest"`
}

type getIssueOut struct {
	Issue analytics.Issue      `json:"issue"`
	Event analytics.IssueEvent `json:"event"`
}

func (t *tools) getIssue(ctx context.Context, _ *mcp.CallToolRequest, in getIssueIn) (*mcp.CallToolResult, getIssueOut, error) {
	svc, err := t.service(ctx, in.Service, identity.PermView)
	if err != nil {
		return nil, getIssueOut{}, friendly(err)
	}
	is, err := t.c.ServiceIssue(ctx, svc.ID, in.Issue)
	if err != nil {
		return nil, getIssueOut{}, friendly(err)
	}
	ev, err := t.c.ServiceIssueEvent(ctx, svc.ID, in.Issue, in.Event)
	return nil, getIssueOut{Issue: is, Event: ev}, friendly(err)
}

type setIssueStatusIn struct {
	Service string `json:"service" jsonschema:"project/service of an app with error tracking on"`
	Issue   int64  `json:"issue" jsonschema:"the issue's number"`
	Status  string `json:"status" jsonschema:"resolved (reopens if it happens again), ignored, or open"`
}

func (t *tools) setIssueStatus(ctx context.Context, _ *mcp.CallToolRequest, in setIssueStatusIn) (*mcp.CallToolResult, analytics.Issue, error) {
	svc, err := t.service(ctx, in.Service, identity.PermDeploy)
	if err != nil {
		return nil, analytics.Issue{}, friendly(err)
	}
	out, err := t.c.SetServiceIssueStatus(ctx, svc.ID, in.Issue, in.Status)
	return nil, out, friendly(err)
}
