package templates

import (
	"regexp"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/compose"
)

var useRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)`)

// Every shipped template loads, parses as a compose file with its inputs
// filled in, and uses exactly the variables it declares.
func TestCatalog(t *testing.T) {
	ts := List()
	if len(ts) == 0 {
		t.Fatal("no templates")
	}
	for _, tpl := range ts {
		t.Run(tpl.ID, func(t *testing.T) {
			values := map[string]string{}
			for _, in := range tpl.Inputs {
				if in.Generate == 0 {
					values[in.Name] = sample(in)
				}
			}
			env, err := tpl.Render(values, func(n int) string { return strings.Repeat("x", n) })
			if err != nil {
				t.Fatal(err)
			}
			_, warns, err := compose.Parse([]byte(tpl.Compose), compose.Vars{Lookup: func(n string) (string, bool) { v, ok := env[n]; return v, ok }})
			if err != nil || len(warns) > 0 {
				t.Fatalf("compose: %v %v", err, warns)
			}
			used := map[string]bool{}
			for _, m := range useRe.FindAllStringSubmatch(tpl.Compose, -1) {
				used[m[1]] = true
				if _, ok := env[m[1]]; !ok {
					t.Errorf("${%s} is not an input", m[1])
				}
			}
			for _, in := range tpl.Inputs {
				if !used[in.Name] {
					t.Errorf("input %s is never used", in.Name)
				}
			}
		})
	}
}

func sample(in Input) string {
	switch in.Type {
	case "domain":
		return "app.example.com"
	case "url":
		return "https://app.example.com"
	}
	return "value"
}

func TestRender(t *testing.T) {
	tpl := Template{ID: "t", Inputs: []Input{
		{Name: "DOMAIN", Label: "Domain", Type: "domain", Required: true},
		{Name: "MODE", Label: "Mode", Type: "text", Default: "prod"},
		{Name: "URL", Label: "URL", Type: "url"},
		{Name: "SECRET", Type: "secret", Generate: 8},
	}}
	gen := func(n int) string { return strings.Repeat("g", n) }

	env, err := tpl.Render(map[string]string{"DOMAIN": " app.example.com "}, gen)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"DOMAIN": "app.example.com", "MODE": "prod", "URL": "", "SECRET": "gggggggg"}
	for k, v := range want {
		if got, ok := env[k]; !ok || got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}

	for name, values := range map[string]map[string]string{
		"required":  {},
		"domain":    {"DOMAIN": "not a domain"},
		"url":       {"DOMAIN": "app.example.com", "URL": "ftp://x"},
		"unknown":   {"DOMAIN": "app.example.com", "OTHER": "x"},
		"generated": {"DOMAIN": "app.example.com", "SECRET": "mine"},
		"multiline": {"DOMAIN": "app.example.com", "MODE": "a\nb"},
	} {
		if _, err := tpl.Render(values, gen); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestParseRefuses(t *testing.T) {
	for name, src := range map[string]string{
		"no header":  "name: app\nservices: {web: {image: nginx:1}}\n",
		"bad input":  "name: app\nx-template: {title: T, description: D, inputs: [{name: lower, label: L}]}\n",
		"no label":   "name: app\nx-template: {title: T, description: D, inputs: [{name: A}]}\n",
		"bad type":   "name: app\nx-template: {title: T, description: D, inputs: [{name: A, label: L, type: number}]}\n",
		"twice":      "name: app\nx-template: {title: T, description: D, inputs: [{name: A, label: L}, {name: A, label: L}]}\n",
		"no project": "x-template: {title: T, description: D}\n",
	} {
		if _, err := parse("app", []byte(src)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
