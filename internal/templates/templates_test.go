package templates

import (
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

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
	if in.Default != "" {
		return in.Default
	}
	switch in.Type {
	case "domain":
		return "app.example.com"
	case "url":
		return "https://app.example.com"
	case "select":
		return in.Options[0]
	case "checkbox":
		return "true"
	}
	return "value"
}

func TestRender(t *testing.T) {
	tpl := Template{ID: "t", Inputs: []Input{
		{Name: "DOMAIN", Label: "Domain", Type: "domain", Required: true},
		{Name: "MODE", Label: "Mode", Type: "text", Default: "prod"},
		{Name: "URL", Label: "URL", Type: "url"},
		{Name: "SECRET", Type: "secret", Generate: 8},
		{Name: "LEVEL", Label: "Level", Type: "select", Options: []string{"low", "high"}, Default: "low"},
		{Name: "AGREE", Label: "Agree", Type: "checkbox", Required: true},
		{Name: "EXTRA", Label: "Extra", Type: "checkbox"},
	}}
	gen := func(n int) string { return strings.Repeat("g", n) }

	env, err := tpl.Render(map[string]string{"DOMAIN": " app.example.com ", "AGREE": "true"}, gen)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"DOMAIN": "app.example.com", "MODE": "prod", "URL": "", "SECRET": "gggggggg", "LEVEL": "low", "AGREE": "true", "EXTRA": "false"}
	for k, v := range want {
		if got, ok := env[k]; !ok || got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}

	for name, values := range map[string]map[string]string{
		"required":  {},
		"domain":    {"DOMAIN": "not a domain", "AGREE": "true"},
		"url":       {"DOMAIN": "app.example.com", "AGREE": "true", "URL": "ftp://x"},
		"unknown":   {"DOMAIN": "app.example.com", "AGREE": "true", "OTHER": "x"},
		"generated": {"DOMAIN": "app.example.com", "AGREE": "true", "SECRET": "mine"},
		"multiline": {"DOMAIN": "app.example.com", "AGREE": "true", "MODE": "a\nb"},
		"option":    {"DOMAIN": "app.example.com", "AGREE": "true", "LEVEL": "max"},
		"unchecked": {"DOMAIN": "app.example.com"},
		"not bool":  {"DOMAIN": "app.example.com", "AGREE": "yes"},
	} {
		if _, err := tpl.Render(values, gen); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestParseRefuses(t *testing.T) {
	const head = "name: app\nx-template: {title: T, description: D, category: games"
	for name, src := range map[string]string{
		"no header":    "name: app\nservices: {web: {image: nginx:1}}\n",
		"no category":  "name: app\nx-template: {title: T, description: D}\n",
		"bad category": "name: app\nx-template: {title: T, description: D, category: misc}\n",
		"bad input":    head + ", inputs: [{name: lower, label: L}]}\n",
		"no label":     head + ", inputs: [{name: A}]}\n",
		"bad type":     head + ", inputs: [{name: A, label: L, type: number}]}\n",
		"twice":        head + ", inputs: [{name: A, label: L}, {name: A, label: L}]}\n",
		"no project":   "x-template: {title: T, description: D, category: games}\n",
		"no options":   head + ", inputs: [{name: A, label: L, type: select}]}\n",
		"bad default":  head + ", inputs: [{name: A, label: L, type: select, options: [x], default: y}]}\n",
	} {
		if _, _, err := parse("app", []byte(src), nil); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

const okSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><path d="M0 0h1v1z"/></svg>`

func TestLogos(t *testing.T) {
	const withLogo = "name: app\nx-template: {title: T, description: D, category: games, icon: app, logo: {label: App, color: \"#112233\"}}\n"
	_, logo, err := parse("app", []byte(withLogo), []byte(okSVG))
	if err != nil || logo == nil || logo.Name != "app" || logo.Color != "#112233" {
		t.Fatalf("logo = %+v, %v", logo, err)
	}
	for name, svg := range map[string]string{
		"script":   `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"handler":  `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`,
		"external": `<svg xmlns="http://www.w3.org/2000/svg"><image href="https://x.example/a.png"/></svg>`,
		"foreign":  `<svg xmlns="http://www.w3.org/2000/svg"><foreignObject/></svg>`,
		"not svg":  `<html></html>`,
		"no ns":    `<svg viewBox="0 0 1 1"></svg>`,
	} {
		if _, _, err := parse("app", []byte(withLogo), []byte(svg)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, _, err := parse("app", []byte(withLogo), nil); err == nil {
		t.Error("logo without logo.svg accepted")
	}
	if _, _, err := parse("app", []byte("name: app\nx-template: {title: T, description: D, category: games}\n"), []byte(okSVG)); err == nil {
		t.Error("logo.svg without x-template.logo accepted")
	}

	// Across templates: an icon needs a logo (or a built-in one), brought once.
	tpl := func(icon, logo string) string {
		return "name: app\nx-template: {title: T, description: D, category: games, icon: " + icon + logo + "}\nservices: {web: {image: nginx:1}}\n"
	}
	logoBlock := `, logo: {label: L, color: "#000000"}`
	for name, files := range map[string]fstest.MapFS{
		"missing": {"files/a/compose.yaml": {Data: []byte(tpl("nope", ""))}},
		"twice": {
			"files/a/compose.yaml": {Data: []byte(tpl("x", logoBlock))}, "files/a/logo.svg": {Data: []byte(okSVG)},
			"files/b/compose.yaml": {Data: []byte(tpl("x", logoBlock))}, "files/b/logo.svg": {Data: []byte(okSVG)},
		},
		"loose file": {"files/a.yaml": {Data: []byte(tpl("postgres", ""))}},
	} {
		if _, err := loadFS(files); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
	if _, err := loadFS(fstest.MapFS{"files/a/compose.yaml": {Data: []byte(tpl("postgres", ""))}}); err != nil {
		t.Errorf("built-in logo: %v", err)
	}
}
