package compose

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestIncludeExtends(t *testing.T) {
	repo := map[string]string{
		"db/compose.yml": `
services:
  cache:
    image: varnish:7
    volumes: ["./redis.conf:/etc/redis.conf"]
x-kipitiny:
  variables: {REGION: eu}
`,
		"db/redis.conf": "maxmemory 1mb",
		"common.yml": `
services:
  base:
    image: node:22
    environment: [A=1, B=1]
    ports: ["3000:3000"]
    volumes: ["./common/app.conf:/etc/app.conf", "data:/data"]
    command: npm start
`,
		"common/app.conf": "x",
	}
	vars := Vars{Files: func(p string) ([]domain.ServiceFile, error) {
		if c, ok := repo[path.Clean(p)]; ok {
			return []domain.ServiceFile{{Content: []byte(c)}}, nil
		}
		return nil, fmt.Errorf("%s not found", p)
	}}
	data := []byte(`
include: [db/compose.yml]
services:
  web:
    extends: {file: common.yml, service: base}
    environment: {B: "2"}
    ports: ["3001:3001"]
    volumes: ["other:/data"]
    command: npm run web
  worker:
    extends: web
    command: npm run worker
`)
	f, warns, err := Parse(data, vars)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) > 0 {
		t.Errorf("warns = %v", warns)
	}
	web, worker := f.Services["web"], f.Services["worker"]
	if web.Image != "node:22" || web.Environment["A"] != "1" || web.Environment["B"] != "2" {
		t.Errorf("web = %+v", web)
	}
	if len(web.Ports) != 2 || len(web.Volumes) != 1 || web.Volumes[0].Name != "other" {
		t.Errorf("web ports %v volumes %v", web.Ports, web.Volumes)
	}
	if len(web.Files) != 1 || web.Files[0].Path != "/etc/app.conf" || web.Files[0].Source != "./common/app.conf" {
		t.Errorf("web files = %+v", web.Files)
	}
	if !slices.Equal(web.Runtime.Command, []string{"npm", "run", "web"}) || !slices.Equal(worker.Runtime.Command, []string{"npm", "run", "worker"}) {
		t.Errorf("commands %v %v", web.Runtime.Command, worker.Runtime.Command)
	}
	if worker.Image != "node:22" || len(worker.Ports) != 2 {
		t.Errorf("worker = %+v", worker)
	}
	cache := f.Services["cache"]
	if cache.Image != "varnish:7" || len(cache.Files) != 1 || string(cache.Files[0].Content) != "maxmemory 1mb" {
		t.Errorf("cache = %+v", cache)
	}
	if f.X.Variables["REGION"] != "eu" {
		t.Errorf("variables = %v", f.X.Variables)
	}

	for in, want := range map[string]string{
		"include: [db/compose.yml]\nservices: {cache: {image: a}}": "service cache is defined in db/compose.yml",
		"services: {a: {extends: b}, b: {extends: a}}":             "a cycle?",
		"services: {a: {extends: nope}}":                           "extends nope: no such service",
		"services: {a: {extends: a}}":                              "no such service",
	} {
		if _, _, err := Parse([]byte(in), vars); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", in, err, want)
		}
	}
	if _, _, err := Parse([]byte("include: [x.yml]\nservices: {a: {image: a}}"), Vars{}); err == nil || !strings.Contains(err.Error(), "need a git project") {
		t.Errorf("include without git: %v", err)
	}
}
