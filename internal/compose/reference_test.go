package compose

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// The reference (site/docs/compose.md) must document every key the parser
// reads or drops.
func TestReferenceCoversKeys(t *testing.T) {
	data, err := os.ReadFile("../../site/docs/compose.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	keys := append(append(append([]string{}, supportedServiceKeys...), ignoredServiceKeys...), ignoredTopKeys...)
	for _, typ := range []any{Ext{}, FileExt{}, Middlewares{}, BasicAuthUser{}, RateLimit{}} {
		rt := reflect.TypeOf(typ)
		for i := range rt.NumField() {
			keys = append(keys, strings.Split(rt.Field(i).Tag.Get("yaml"), ",")[0])
		}
	}
	for _, k := range keys {
		if !strings.Contains(doc, "`"+k+"`") {
			t.Errorf("site/docs/compose.md doesn't mention `%s`", k)
		}
	}
}

// The reference's examples are valid files.
func TestReferenceExamples(t *testing.T) {
	data, err := os.ReadFile("../../site/docs/compose.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks := strings.Split(string(data), "```yaml\n")[1:]
	if len(blocks) == 0 {
		t.Fatal("no yaml examples")
	}
	for i, b := range blocks {
		src := b[:strings.Index(b, "```")]
		if !strings.Contains(src, "services:") {
			continue // a fragment
		}
		vars := Vars{Ref: func(_, _, n string) string { return "{{ project." + n + " }}" }}
		if _, warns, err := Parse([]byte(src), vars); err != nil || len(warns) > 0 {
			t.Errorf("example %d: %v %v", i+1, err, warns)
		}
	}
}
