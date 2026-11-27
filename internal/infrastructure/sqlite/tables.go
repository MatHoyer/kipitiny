package sqlite

import (
	"github.com/jinzhu/inflection"
	"github.com/uptrace/bun/schema"
)

// tables names the tables whose name isn't the plural of their entity's:
// domain types carry no bun.BaseModel, so bun derives every other name
// (domain.User is users, domain.APIToken api_tokens).
var tables = map[string]string{
	"project_git": "project_git",
	"audit_entry": "audit_log",
}

func init() {
	schema.SetTableNameInflector(func(model string) string {
		if t, ok := tables[model]; ok {
			return t
		}
		return inflection.Plural(model)
	})
}
