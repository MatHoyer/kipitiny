// Package templates is the catalog of one-click apps. A template is a
// compose file (files/<id>.yaml) with a top-level x-template block naming
// it and the inputs its ${NAME} variables come from; installing one applies
// the file with the inputs as its .env. Like compose, it knows nothing about
// Docker or the store.
package templates

import (
	"embed"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

// ExtKey is the top-level block describing a template; compose ignores it.
const ExtKey = "x-template"

//go:embed files/*.yaml
var files embed.FS

// Template is one app of the catalog.
type Template struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Website     string  `json:"website,omitempty"`
	Docs        string  `json:"docs,omitempty"`
	Icon        string  `json:"icon,omitempty"`
	Inputs      []Input `json:"inputs"`
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
	// Type is text, secret, domain or url.
	Type        string `json:"type"`
	Help        string `json:"help,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Default     string `json:"default,omitempty"`
	Required    bool   `json:"required,omitempty"`
	// Generate, when set, is the number of random bytes of a value the
	// manager generates; such an input isn't asked.
	Generate int `json:"generate,omitempty"`
}

var (
	idRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)
	varRe  = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	domain = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$|^localhost$|^([a-z0-9-]+\.)+localhost$`)
	types  = []string{"text", "secret", "domain", "url"}
	load   = sync.OnceValues(loadAll)
)

// List returns every template, by title.
func List() []Template {
	ts, err := load()
	if err != nil {
		panic(err) // the embedded files are checked by tests
	}
	return ts
}

// Get returns the template id; false when there is none.
func Get(id string) (Template, bool) {
	i := slices.IndexFunc(List(), func(t Template) bool { return t.ID == id })
	if i < 0 {
		return Template{}, false
	}
	return List()[i], true
}

func loadAll() ([]Template, error) {
	entries, err := files.ReadDir("files")
	if err != nil {
		return nil, err
	}
	var ts []Template
	for _, e := range entries {
		data, err := files.ReadFile("files/" + e.Name())
		if err != nil {
			return nil, err
		}
		t, err := parse(strings.TrimSuffix(e.Name(), path.Ext(e.Name())), data)
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", e.Name(), err)
		}
		ts = append(ts, t)
	}
	slices.SortFunc(ts, func(a, b Template) int { return strings.Compare(a.Title, b.Title) })
	return ts, nil
}

func parse(id string, data []byte) (Template, error) {
	var doc struct {
		Name string `yaml:"name"`
		X    *struct {
			Title       string  `yaml:"title"`
			Description string  `yaml:"description"`
			Website     string  `yaml:"website"`
			Docs        string  `yaml:"docs"`
			Icon        string  `yaml:"icon"`
			Inputs      []Input `yaml:"inputs"`
		} `yaml:"x-template"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Template{}, err
	}
	if !idRe.MatchString(id) {
		return Template{}, fmt.Errorf("file name must be lowercase letters, digits and dashes")
	}
	if doc.X == nil || doc.X.Title == "" || doc.X.Description == "" {
		return Template{}, fmt.Errorf("%s needs a title and a description", ExtKey)
	}
	t := Template{ID: id, Title: doc.X.Title, Description: doc.X.Description, Website: doc.X.Website, Docs: doc.X.Docs,
		Icon: doc.X.Icon, Inputs: doc.X.Inputs, Project: doc.Name, Compose: string(data)}
	if t.Inputs == nil {
		t.Inputs = []Input{}
	}
	if !idRe.MatchString(t.Project) {
		return Template{}, fmt.Errorf("name must be a valid project name")
	}
	seen := map[string]bool{}
	for i, in := range t.Inputs {
		switch {
		case !varRe.MatchString(in.Name):
			return Template{}, fmt.Errorf("input %q: name must be UPPER_CASE", in.Name)
		case seen[in.Name]:
			return Template{}, fmt.Errorf("input %s is listed twice", in.Name)
		case in.Generate < 0 || in.Generate > 64:
			return Template{}, fmt.Errorf("input %s: generate is a number of bytes, 1 to 64", in.Name)
		case in.Generate == 0 && in.Label == "":
			return Template{}, fmt.Errorf("input %s needs a label", in.Name)
		}
		seen[in.Name] = true
		if in.Type == "" {
			t.Inputs[i].Type = "text"
			if in.Generate > 0 {
				t.Inputs[i].Type = "secret"
			}
		} else if !slices.Contains(types, in.Type) {
			return Template{}, fmt.Errorf("input %s: type is one of %s", in.Name, strings.Join(types, ", "))
		}
	}
	return t, nil
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
