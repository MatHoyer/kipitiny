// Package templates is the catalog of one-click apps. A template is a
// folder (files/<id>/) holding a compose file whose top-level x-template
// block names it and the inputs its ${NAME} variables come from, and
// optionally the logo it brings (logo.svg); installing one applies the file
// with the inputs as its .env. Like compose, it knows nothing about Docker
// or the store.
package templates

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

//go:generate go run ./gen ../../web/src/demo/catalog.json

// ExtKey is the top-level block describing a template; compose ignores it.
const ExtKey = "x-template"

//go:embed files
var files embed.FS

// Template is one app of the catalog.
type Template struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Website     string `json:"website,omitempty"`
	Docs        string `json:"docs,omitempty"`
	Icon        string `json:"icon,omitempty"`
	// Category is one of Categories; Tags are extra words to search by.
	Category string   `json:"category"`
	Tags     []string `json:"tags"`
	Inputs   []Input  `json:"inputs"`
	// Project is the compose file's name, the default project name.
	Project string `json:"project"`
	// Compose is the file as shipped, x-template included.
	Compose string `json:"compose"`
}

// Input is a value asked from the user, or generated, given to the file as
// the variable Name.
type Input struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	// Type is text, secret, domain, url, select (one of Options) or
	// checkbox ("true" or "false"; required means it must be checked).
	Type        string   `json:"type"`
	Options     []string `json:"options,omitempty"`
	Help        string   `json:"help,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Default     string   `json:"default,omitempty"`
	Required    bool     `json:"required,omitempty"`
	// Generate, when set, is the number of random bytes of a value the
	// manager generates; such an input isn't asked.
	Generate int `json:"generate,omitempty"`
}

// Logo is a service logo a template brings: the UI shows it for services
// whose icon is Name, or whose image's base name is one of Images.
type Logo struct {
	Name   string   `json:"name"`
	Label  string   `json:"label"`
	Color  string   `json:"color"`
	Images []string `json:"images"`
	SVG    string   `json:"svg"`
}

// Categories group the gallery.
var Categories = []string{"monitoring", "analytics", "automation", "development", "storage", "media", "communication", "productivity", "security", "games"}

// builtinLogos are the logos the UI draws itself
// (web/src/components/service-icon.tsx): a template may use them without
// bringing a logo.
var builtinLogos = []string{"postgres", "redis", "nginx", "node"}

var (
	idRe    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)
	varRe   = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	colorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	domain  = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$|^localhost$|^([a-z0-9-]+\.)+localhost$`)
	types   = []string{"text", "secret", "domain", "url", "select", "checkbox"}
	load    = sync.OnceValues(func() (catalog, error) { return loadFS(files) })
)

type catalog struct {
	templates []Template
	logos     []Logo
}

func mustLoad() catalog {
	c, err := load()
	if err != nil {
		panic(err) // the embedded files are checked by tests
	}
	return c
}

// List returns every template, by title.
func List() []Template { return mustLoad().templates }

// Logos returns the logos the templates bring, by name.
func Logos() []Logo { return mustLoad().logos }

// Get returns the template id; false when there is none.
func Get(id string) (Template, bool) {
	ts := List()
	i := slices.IndexFunc(ts, func(t Template) bool { return t.ID == id })
	if i < 0 {
		return Template{}, false
	}
	return ts[i], true
}

// loadFS reads the catalog from a files/ tree: one folder per template.
func loadFS(fsys fs.FS) (catalog, error) {
	entries, err := fs.ReadDir(fsys, "files")
	if err != nil {
		return catalog{}, err
	}
	var c catalog
	for _, e := range entries {
		if !e.IsDir() {
			return catalog{}, fmt.Errorf("files/%s: each template is a folder", e.Name())
		}
		dir := "files/" + e.Name() + "/"
		data, err := fs.ReadFile(fsys, dir+"compose.yaml")
		if err != nil {
			return catalog{}, fmt.Errorf("template %s: %w", e.Name(), err)
		}
		svg, err := fs.ReadFile(fsys, dir+"logo.svg")
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return catalog{}, err
		}
		t, logo, err := parse(e.Name(), data, svg)
		if err != nil {
			return catalog{}, fmt.Errorf("template %s: %w", e.Name(), err)
		}
		c.templates = append(c.templates, t)
		if logo != nil {
			if slices.ContainsFunc(c.logos, func(l Logo) bool { return l.Name == logo.Name }) {
				return catalog{}, fmt.Errorf("template %s: logo %s is already brought by another template", e.Name(), logo.Name)
			}
			c.logos = append(c.logos, *logo)
		}
	}
	for _, t := range c.templates {
		if t.Icon != "" && !slices.Contains(builtinLogos, t.Icon) && !slices.ContainsFunc(c.logos, func(l Logo) bool { return l.Name == t.Icon }) {
			return catalog{}, fmt.Errorf("template %s: no template brings the logo %s (add logo.svg and x-template.logo)", t.ID, t.Icon)
		}
	}
	slices.SortFunc(c.templates, func(a, b Template) int { return strings.Compare(a.Title, b.Title) })
	slices.SortFunc(c.logos, func(a, b Logo) int { return strings.Compare(a.Name, b.Name) })
	return c, nil
}

func parse(id string, data, svg []byte) (Template, *Logo, error) {
	var doc struct {
		Name string `yaml:"name"`
		X    *struct {
			Title       string   `yaml:"title"`
			Description string   `yaml:"description"`
			Website     string   `yaml:"website"`
			Docs        string   `yaml:"docs"`
			Icon        string   `yaml:"icon"`
			Category    string   `yaml:"category"`
			Tags        []string `yaml:"tags"`
			Logo        *struct {
				Label  string   `yaml:"label"`
				Color  string   `yaml:"color"`
				Images []string `yaml:"images"`
			} `yaml:"logo"`
			Inputs []Input `yaml:"inputs"`
		} `yaml:"x-template"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Template{}, nil, err
	}
	if !idRe.MatchString(id) {
		return Template{}, nil, fmt.Errorf("folder name must be lowercase letters, digits and dashes")
	}
	x := doc.X
	if x == nil || x.Title == "" || x.Description == "" {
		return Template{}, nil, fmt.Errorf("%s needs a title and a description", ExtKey)
	}
	if !slices.Contains(Categories, x.Category) {
		return Template{}, nil, fmt.Errorf("category is one of %s", strings.Join(Categories, ", "))
	}
	t := Template{ID: id, Title: x.Title, Description: x.Description, Website: x.Website, Docs: x.Docs,
		Icon: x.Icon, Category: x.Category, Tags: x.Tags, Inputs: x.Inputs, Project: doc.Name, Compose: string(data)}
	if t.Inputs == nil {
		t.Inputs = []Input{}
	}
	if t.Tags == nil {
		t.Tags = []string{}
	}
	if !idRe.MatchString(t.Project) {
		return Template{}, nil, fmt.Errorf("name must be a valid project name")
	}
	if err := checkInputs(t.Inputs); err != nil {
		return Template{}, nil, err
	}

	var logo *Logo
	switch {
	case x.Logo == nil && svg != nil:
		return Template{}, nil, fmt.Errorf("logo.svg needs x-template.logo (label, color)")
	case x.Logo != nil && svg == nil:
		return Template{}, nil, fmt.Errorf("x-template.logo needs logo.svg")
	case x.Logo != nil:
		if !idRe.MatchString(t.Icon) {
			return Template{}, nil, fmt.Errorf("a template bringing a logo names it with icon")
		}
		if x.Logo.Label == "" || !colorRe.MatchString(x.Logo.Color) {
			return Template{}, nil, fmt.Errorf("logo needs a label and a #rrggbb color")
		}
		if err := checkSVG(svg); err != nil {
			return Template{}, nil, fmt.Errorf("logo.svg: %w", err)
		}
		logo = &Logo{Name: t.Icon, Label: x.Logo.Label, Color: x.Logo.Color, Images: x.Logo.Images, SVG: strings.TrimSpace(string(svg))}
		if logo.Images == nil {
			logo.Images = []string{}
		}
	}
	return t, logo, nil
}

func checkInputs(inputs []Input) error {
	seen := map[string]bool{}
	for i, in := range inputs {
		switch {
		case !varRe.MatchString(in.Name):
			return fmt.Errorf("input %q: name must be UPPER_CASE", in.Name)
		case seen[in.Name]:
			return fmt.Errorf("input %s is listed twice", in.Name)
		case in.Generate < 0 || in.Generate > 64:
			return fmt.Errorf("input %s: generate is a number of bytes, 1 to 64", in.Name)
		case in.Generate == 0 && in.Label == "":
			return fmt.Errorf("input %s needs a label", in.Name)
		}
		seen[in.Name] = true
		if (in.Type == "select") != (len(in.Options) > 0) {
			return fmt.Errorf("input %s: options go with type select", in.Name)
		}
		if in.Type == "select" && in.Default != "" && !slices.Contains(in.Options, in.Default) {
			return fmt.Errorf("input %s: default %q is not an option", in.Name, in.Default)
		}
		if in.Type == "" {
			inputs[i].Type = "text"
			if in.Generate > 0 {
				inputs[i].Type = "secret"
			}
		} else if !slices.Contains(types, in.Type) {
			return fmt.Errorf("input %s: type is one of %s", in.Name, strings.Join(types, ", "))
		}
	}
	return nil
}

var (
	svgHandlerRe = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	svgHrefRe    = regexp.MustCompile(`(?i)href\s*=\s*["']([^"']*)`)
)

// checkSVG keeps logos to plain drawings: the UI shows them as images,
// which never run scripts, but they're also served by the API.
func checkSVG(svg []byte) error {
	s := strings.TrimSpace(string(svg))
	switch {
	case len(s) > 16<<10:
		return fmt.Errorf("larger than 16 KB")
	case !strings.HasPrefix(s, "<svg") || !strings.HasSuffix(s, "</svg>"):
		return fmt.Errorf("must be a single <svg> element")
	case !strings.Contains(s, `xmlns="http://www.w3.org/2000/svg"`):
		return fmt.Errorf(`needs xmlns="http://www.w3.org/2000/svg"`)
	case strings.Contains(strings.ToLower(s), "<script"), strings.Contains(strings.ToLower(s), "<foreignobject"), svgHandlerRe.MatchString(s):
		return fmt.Errorf("no scripts, event handlers or foreignObject")
	}
	for _, m := range svgHrefRe.FindAllStringSubmatch(s, -1) {
		if !strings.HasPrefix(m[1], "#") {
			return fmt.Errorf("links only within the file (href=\"#…\")")
		}
	}
	return nil
}

// Render checks the user's values against the inputs and returns the .env
// the file is applied with. gen makes a random value of n bytes for the
// generated inputs. Every input is in the .env, unset ones as "", so none
// becomes a project variable reference.
func (t Template) Render(values map[string]string, gen func(n int) string) (map[string]string, error) {
	env := map[string]string{}
	var errs []string
	for _, in := range t.Inputs {
		if in.Generate > 0 {
			env[in.Name] = gen(in.Generate)
			continue
		}
		v := strings.TrimSpace(values[in.Name])
		if v == "" {
			v = in.Default
		}
		if v == "" && in.Type == "checkbox" {
			v = "false"
		}
		if err := in.check(v); err != nil {
			errs = append(errs, err.Error())
		}
		env[in.Name] = v
	}
	for k := range values {
		if !slices.ContainsFunc(t.Inputs, func(in Input) bool { return in.Name == k && in.Generate == 0 }) {
			errs = append(errs, fmt.Sprintf("%s is not an input of %s", k, t.ID))
		}
	}
	if len(errs) > 0 {
		slices.Sort(errs)
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return env, nil
}

func (in Input) check(v string) error {
	if in.Type == "checkbox" {
		if v != "true" && v != "false" {
			return fmt.Errorf("%s: %q is not true or false", in.Label, v)
		}
		if in.Required && v != "true" {
			return fmt.Errorf("%s must be checked", in.Label)
		}
		return nil
	}
	if v == "" {
		if in.Required {
			return fmt.Errorf("%s is required", in.Label)
		}
		return nil
	}
	switch in.Type {
	case "domain":
		if !domain.MatchString(v) {
			return fmt.Errorf("%s: %q is not a hostname", in.Label, v)
		}
	case "select":
		if !slices.Contains(in.Options, v) {
			return fmt.Errorf("%s: %q is not one of %s", in.Label, v, strings.Join(in.Options, ", "))
		}
	case "url":
		if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%s: %q is not an http(s) URL", in.Label, v)
		}
	}
	if strings.ContainsAny(v, "\n\r") {
		return fmt.Errorf("%s: one line only", in.Label)
	}
	return nil
}
