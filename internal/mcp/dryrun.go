package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolset is the server tools are added to, and whether it is in dry-run
// mode.
type toolset struct {
	server *mcp.Server
	dryRun bool
}

// addTool adds a tool. In dry-run mode a tool that changes something
// doesn't run: one taking dry_run returns its plan, query_database runs when
// read-only, and the others answer what they would have been called with.
func addTool[In, Out any](s toolset, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	if s.dryRun && (t.Annotations == nil || !t.Annotations.ReadOnlyHint) {
		h = dryRun(t.Name, h)
	}
	mcp.AddTool(s.server, t, h)
}

func dryRun[In, Out any](name string, h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		v := reflect.ValueOf(&in).Elem()
		if v.Kind() == reflect.Struct {
			if f := v.FieldByName("DryRun"); f.Kind() == reflect.Bool {
				f.SetBool(true)
				return h(ctx, req, in)
			}
			if f := v.FieldByName("Write"); f.Kind() == reflect.Bool && !f.Bool() {
				return h(ctx, req, in) // query_database without write is read-only
			}
		}
		// An error result: a success would need the tool's output, which
		// there is none of.
		args, _ := json.Marshal(in)
		var zero Out
		return nil, zero, fmt.Errorf("dry run: %s was not run, nothing changed. It would have been called with %s. "+
			"Permissions and whether the targets exist are not checked; connect without dry_run to apply it", name, args)
	}
}
