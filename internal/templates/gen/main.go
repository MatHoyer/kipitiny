// Command gen writes the catalog the UI's demo build serves
// (web/src/demo/catalog.json), so the demo shows the real templates: run
// `go generate ./internal/templates` after changing one. A test fails when
// the file is stale. Images are written without their tag and the compose
// file is left out, so bumping an image's version (Dependabot) doesn't
// change it.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/MatHoyer/kipitiny/internal/compose"
	"github.com/MatHoyer/kipitiny/internal/store"
	"github.com/MatHoyer/kipitiny/internal/templates"
)

// demoTemplate is a template plus the services the demo creates for it.
type demoTemplate struct {
	templates.Template
	Services []demoService `json:"services"`
}

// demoService is what the demo needs of a service the template creates.
// DomainInput names the input its domain comes from.
type demoService struct {
	Name           string                `json:"name"`
	Kind           string                `json:"kind"`
	Image          string                `json:"image"`
	Icon           string                `json:"icon,omitempty"`
	Port           int                   `json:"port,omitempty"`
	MemoryMB       int                   `json:"memoryMb,omitempty"`
	PublishedPorts []store.PublishedPort `json:"publishedPorts,omitempty"`
	Volumes        []store.Volume        `json:"volumes,omitempty"`
	HostNetwork    bool                  `json:"hostNetwork,omitempty"`
	DockerSocket   string                `json:"dockerSocket,omitempty"`
	DomainInput    string                `json:"domainInput,omitempty"`
}

type demoCatalog struct {
	Templates []demoTemplate   `json:"templates"`
	Logos     []templates.Logo `json:"logos"`
}

func build() ([]byte, error) {
	c := demoCatalog{Templates: []demoTemplate{}, Logos: templates.Logos()}
	for _, t := range templates.List() {
		env := map[string]string{}
		for _, in := range t.Inputs {
			env[in.Name] = sample(in)
		}
		f, _, err := compose.Parse([]byte(t.Compose), compose.Vars{Lookup: func(n string) (string, bool) { v, ok := env[n]; return v, ok }})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.ID, err)
		}
		t.Compose = ""
		dt := demoTemplate{Template: t, Services: []demoService{}}
		for _, name := range f.ServiceNames() {
			s := f.Services[name]
			ds := demoService{Name: name, Kind: s.X.Kind, Image: untagged(s.Image), Icon: s.X.Icon, Port: s.X.Port, MemoryMB: s.MemoryMB,
				PublishedPorts: s.Ports, Volumes: s.Volumes, HostNetwork: s.HostNetwork, DockerSocket: s.DockerSocket}
			if ds.Kind == "" {
				ds.Kind = string(store.ServiceKindApp)
			}
			if v, ok := compose.SoleVar(s.X.Domain); ok {
				ds.DomainInput = v
			}
			dt.Services = append(dt.Services, ds)
		}
		c.Templates = append(c.Templates, dt)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// untagged drops an image's tag (ghcr.io/a/b:1.2 → ghcr.io/a/b).
func untagged(image string) string {
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		return image[:i]
	}
	return image
}

// sample gives an input a value that parses: its default, else one of its type.
func sample(in templates.Input) string {
	switch {
	case in.Default != "":
		return in.Default
	case in.Type == "select":
		return in.Options[0]
	case in.Type == "checkbox":
		return "true"
	case in.Type == "domain":
		return "app.example.com"
	case in.Type == "url":
		return "https://app.example.com"
	}
	return "x"
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gen <output.json>")
		os.Exit(2)
	}
	data, err := build()
	if err == nil {
		err = os.WriteFile(os.Args[1], data, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
