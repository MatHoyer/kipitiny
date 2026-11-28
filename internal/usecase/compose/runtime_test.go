package compose

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestStandardKeys(t *testing.T) {
	data := []byte(`
services:
  web:
    image: shop
    expose: ["3000"]
    healthcheck:
      test: wget -qO- http://localhost:3000/healthz || exit 1
      interval: 30s
      timeout: 5s
      start_period: 1m
      retries: 3
      start_interval: 2s
    environment:
      API_KEY: ${API_KEY}
      LOG_LEVEL: info
  api:
    image: api
    expose: [8080, "9090"]
    x-kipitiny: {port: 9090}
    healthcheck: {disable: true}
    environment:
      - TOKEN
`)
	f, warns, err := Parse(data, Vars{Lookup: func(_ string) (string, bool) { return "v", true }})
	if err != nil {
		t.Fatal(err)
	}
	web, api := f.Services["web"], f.Services["api"]
	if web.X.Port != 3000 || api.X.Port != 9090 {
		t.Errorf("ports = %d, %d", web.X.Port, api.X.Port)
	}
	want := &domain.Healthcheck{Test: []string{"CMD-SHELL", "wget -qO- http://localhost:3000/healthz || exit 1"},
		IntervalSeconds: 30, TimeoutSeconds: 5, StartPeriodSeconds: 60, Retries: 3}
	if !reflect.DeepEqual(web.Healthcheck, want) {
		t.Errorf("healthcheck = %+v", web.Healthcheck)
	}
	if !reflect.DeepEqual(api.Healthcheck, &domain.Healthcheck{Test: []string{"NONE"}}) {
		t.Errorf("disabled healthcheck = %+v", api.Healthcheck)
	}
	if !slices.Equal(web.VarEnv, []string{"API_KEY"}) || !slices.Equal(api.VarEnv, []string{"TOKEN"}) {
		t.Errorf("var env = %v, %v", web.VarEnv, api.VarEnv)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "start_interval") {
		t.Errorf("warnings = %v", warns)
	}

	for _, bad := range []string{
		"services: {web: {image: x, expose: [\"3000-3005\"]}}",
		"services: {web: {image: x, healthcheck: {interval: 5s}}}",
		"services: {web: {image: x, healthcheck: {test: [CMD, x], foo: 1}}}",
	} {
		if _, _, err := Parse([]byte(bad), Vars{}); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
	if _, warns, _ := Parse([]byte("services: {web: {image: x, expose: [80, 81]}}"), Vars{}); len(warns) != 1 {
		t.Errorf("several exposed ports without x-kipitiny.port: warnings %v", warns)
	}
}

func TestRuntimeKeys(t *testing.T) {
	data := []byte(`
services:
  web:
    image: shop
    command: celery -A "shop app" worker --loglevel=$${LEVEL}
    entrypoint: ""
    labels: [com.example.a=1, traefik.enable=true]
    tmpfs: /run
    volumes: [{type: tmpfs, target: /cache, tmpfs: {size: 1000000}}]
    ulimits: {nofile: 65535}
    sysctls: [net.core.somaxconn=1024]
    extra_hosts: {db.local: 10.0.0.2}
  db:
    image: postgres:17
    command: postgres -c max_connections=200
`)
	f, warns, err := Parse(data, Vars{})
	if err != nil {
		t.Fatal(err)
	}
	want := &domain.Runtime{
		Command: []string{"celery", "-A", "shop app", "worker", "--loglevel=${LEVEL}"}, Entrypoint: []string{""},
		Labels: map[string]string{"com.example.a": "1"}, Tmpfs: map[string]string{"/run": "", "/cache": "size=1000000"},
		Ulimits: map[string]domain.Ulimit{"nofile": {Soft: 65535, Hard: 65535}}, Sysctls: map[string]string{"net.core.somaxconn": "1024"},
		ExtraHosts: []string{"db.local:10.0.0.2"},
	}
	if got := f.Services["web"].Runtime; !reflect.DeepEqual(got, want) {
		t.Errorf("runtime = %+v", got)
	}
	if f.Services["db"].Runtime != nil {
		t.Error("a database's command is the manager's")
	}
	joined := strings.Join(warns, "\n")
	if !strings.Contains(joined, "label traefik.enable is ignored") || !strings.Contains(joined, "of a postgres service are ignored") {
		t.Errorf("warnings = %v", warns)
	}
}

func TestSplitCommand(t *testing.T) {
	for in, want := range map[string][]string{
		"":                        {},
		"  a  b ":                 {"a", "b"},
		`sh -c 'echo "$HOME" ok'`: {"sh", "-c", `echo "$HOME" ok`},
		`a "b \"c\" \n" d\ e`:     {"a", `b "c" \n`, "d e"},
		`x''y ""`:                 {"xy", ""},
	} {
		got, err := SplitCommand(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("SplitCommand(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := SplitCommand(`a "b`); err == nil {
		t.Error("want an error for an unterminated quote")
	}
}

func TestPortsAndVolumes(t *testing.T) {
	data := []byte(`
services:
  web:
    image: a
    ports: ["8000-8002:9000-9002/udp", "[::1]:5432:5432"]
    volumes: [/app/node_modules, /app/node-modules, "cache:/cache:ro", shared:/shared]
volumes:
  shared: {external: true}
`)
	f, warns, err := Parse(data, Vars{})
	if err != nil {
		t.Fatal(err)
	}
	s := f.Services["web"]
	want := []domain.PublishedPort{{HostPort: 8000, ContainerPort: 9000, Protocol: "udp"}, {HostPort: 8001, ContainerPort: 9001, Protocol: "udp"},
		{HostPort: 8002, ContainerPort: 9002, Protocol: "udp"}, {HostIP: "::1", HostPort: 5432, ContainerPort: 5432, Protocol: "tcp"}}
	if !slices.Equal(s.Ports, want) {
		t.Errorf("ports = %+v", s.Ports)
	}
	wantVols := []domain.Volume{{Name: "app-node-modules", Path: "/app/node_modules"}, {Name: "app-node-modules-2", Path: "/app/node-modules"},
		{Name: "cache", Path: "/cache", ReadOnly: true}, {Name: "shared", Path: "/shared", External: "shared"}}
	if !reflect.DeepEqual(s.Volumes, wantVols) {
		t.Errorf("volumes = %+v", s.Volumes)
	}
	if len(warns) != 2 || !strings.Contains(warns[0], "anonymous") {
		t.Errorf("warnings = %v", warns)
	}
}
