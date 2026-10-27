package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/anonymize"
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
	for _, v := range []struct {
		schema *any
		t      reflect.Type
	}{{&t.InputSchema, reflect.TypeFor[In]()}, {&t.OutputSchema, reflect.TypeFor[Out]()}} {
		if *v.schema != nil || v.t == reflect.TypeFor[any]() {
			continue
		}
		if v.t.Kind() == reflect.Pointer {
			v.t = v.t.Elem()
		}
		sc, err := jsonschema.ForType(v.t, &jsonschema.ForOptions{TypeSchemas: typeSchemas})
		if err != nil {
			panic(fmt.Errorf("tool %s: %w", t.Name, err))
		}
		*v.schema = sc
	}
	mcp.AddTool(s.server, t, h)
}

// typeSchemas are the schemas of types whose JSON reflection can't infer.
var typeSchemas = func() map[reflect.Type]*jsonschema.Schema {
	// A table's rule: truncate, exclude, or rules by column.
	table := &jsonschema.Schema{Types: []string{"string", "object"}, AdditionalProperties: &jsonschema.Schema{Type: "string"}}
	return map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[anonymize.Table](): table,
		reflect.TypeFor[anonymize.Rules](): {Types: []string{"null", "object"}, AdditionalProperties: table},
	}
}()

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
