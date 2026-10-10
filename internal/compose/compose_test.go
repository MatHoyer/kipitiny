package compose

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestRoundTrip(t *testing.T) {
	in := File{
		Name: "shop",
		Services: map[string]Service{
			"web": {
				Image:            "ghcr.io/me/shop:1.4.2",
				Environment:      map[string]string{"DATABASE_URL": "{{ db.db.URL }}", "API_KEY": "${WEB_API_KEY}", "PRICE": "$$5"},
				Ports:            []store.PublishedPort{{HostPort: 2525, ContainerPort: 25, Protocol: "tcp"}, {HostPort: 9000, ContainerPort: 9000, Protocol: "udp"}},
				Volumes:          []store.Volume{{Name: "uploads", Path: "/app/uploads"}},
				Replicas:         2,
				MemoryMB:         256,
				CPUs:             0.5,
				StopGraceSeconds: 90,
				X: Ext{
					Domain: "shop.example.com", Port: 3000, PreDeploy: "./migrate up",
					Secrets: []string{"API_KEY"},
					Uptime:  &Uptime{Path: "/health", Interval: 90, Timeout: 5, ExpectedStatus: 204, Paused: true},
					Middlewares: &Middlewares{
						BasicAuth:   []BasicAuthUser{{Name: "admin", Hash: "$2a$10$abc"}, {Name: "ops", Ref: "{{ pass://V/I/p }}"}},
						IPAllowList: []string{"1.2.3.0/24"},
						RateLimit:   &RateLimit{Average: 50, Burst: 100},
						Headers:     map[string]string{"X-Frame-Options": "DENY"},
					},
				},
			},
			"worker": {
				Image:       "ghcr.io/me/shop-worker:1.4.2",
				Healthcheck: &store.Healthcheck{Test: []string{"CMD", "wget", "-qO-", "http://localhost:8080/healthz"}, IntervalSeconds: 30, StartPeriodSeconds: 90, Retries: 3},
			},
			"db": {Image: "postgres:17-alpine", MemoryMB: 512, X: Ext{Kind: "postgres", Password: "${DB_PASSWORD}"}},
		},
		X: FileExt{Variables: map[string]string{"REGION": "eu", "TOKEN": "${PROJECT_TOKEN}"}, Secrets: []string{"TOKEN"}},
	}
	data, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	vars := Vars{Lookup: func(n string) (string, bool) { return "secret-" + n, n == "WEB_API_KEY" }}
	got, warns, err := Parse(data, vars)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, data)
	}
	if len(warns) > 0 {
		t.Errorf("warnings: %v", warns)
	}
	want := in
	web := want.Services["web"]
	web.Environment = map[string]string{"DATABASE_URL": "{{ db.db.URL }}", "API_KEY": "secret-WEB_API_KEY", "PRICE": "$5"}
	web.VarEnv = []string{"API_KEY"}
	want.Services["web"] = web
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip:\n got %+v\nwant %+v\n%s", got, want, data)
	}
}

func TestParse(t *testing.T) {
	const src = `
version: "3.8"
services:
  web:
    image: app:${TAG:-latest}
    build: .
    restart: always
    depends_on: [db]
    environment:
      - PLAIN=1
      - FROM_PROJECT
    ports: ["80", "127.0.0.1:8080:80"]
    mem_limit: 1g
  db:
    image: postgres:16
    environment:
      POSTGRES_PASSWORD: x
    volumes: [pgdata:/var/lib/postgresql/data]
volumes:
  pgdata:
    driver: local
`
	f, warns, err := Parse([]byte(src), Vars{Ref: func(_, _, n string) string { return "{{ project." + n + " }}" }})
	if err != nil {
		t.Fatal(err)
	}
	web := f.Services["web"]
	if web.Image != "app:latest" || web.MemoryMB != 1024 || web.Environment["FROM_PROJECT"] != "{{ project.FROM_PROJECT }}" || web.Environment["PLAIN"] != "1" {
		t.Errorf("web = %+v", web)
	}
	if len(web.Ports) != 1 || web.Ports[0] != (store.PublishedPort{HostPort: 8080, ContainerPort: 80, Protocol: "tcp"}) {
		t.Errorf("ports = %+v", web.Ports)
	}
	db := f.Services["db"]
	if db.X.Kind != "postgres" || db.Environment != nil || db.Volumes != nil {
		t.Errorf("db = %+v", db)
	}
	for _, w := range []string{"version is ignored", "build is ignored", "restart is ignored", "depends_on is ignored", "host IP is ignored", "isn't published", "driver is ignored", "environment of a postgres"} {
		if !strings.Contains(strings.Join(warns, "\n"), w) {
			t.Errorf("missing warning %q in %v", w, warns)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"build only":   {"services:\n  web:\n    build: .\n", "kipitiny doesn't build"},
		"command":      {"services:\n  web:\n    image: a\n    command: run\n", "command is not supported"},
		"bind mount":   {"services:\n  web:\n    image: a\n    volumes: [./data:/data]\n", "bind mounts"},
		"anonymous":    {"services:\n  web:\n    image: a\n    volumes: [/data]\n", "anonymous"},
		"missing var":  {"services:\n  web:\n    image: a:${TAG}\n", "variables not set: TAG"},
		"required var": {"services:\n  web:\n    image: a:${TAG:?set a tag}\n", "set a tag"},
		"bad ext":      {"services:\n  web:\n    image: a\n    x-kipitiny:\n      domian: x\n", "domian"},
		"plain auth":   {"services:\n  web:\n    image: a\n    x-kipitiny:\n      middlewares:\n        basic_auth: [{name: a}]\n", "bcrypt hash"},
		"top secrets":  {"services:\n  web:\n    image: a\nsecrets: {}\n", "top-level secrets"},
		"no services":  {"name: x\n", "no services"},
		"port range":   {"services:\n  web:\n    image: a\n    ports: [\"8000-8001:80\"]\n", "ranges"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := Parse([]byte(tc.src), Vars{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestParseValues(t *testing.T) {
	src := `
services:
  web:
    image: a
    stop_grace_period: 1m30s
    deploy:
      replicas: 3
      resources: {limits: {memory: 1536k, cpus: "1.5"}}
    ports: [{target: 53, published: 5353, protocol: udp}]
    volumes: [{type: volume, source: data, target: /data}]
    x-kipitiny:
      password: ${PW}
      kind: app
`
	f, _, err := Parse([]byte(src), Vars{})
	if err != nil {
		t.Fatal(err)
	}
	s := f.Services["web"]
	if s.StopGraceSeconds != 90 || s.Replicas != 3 || s.MemoryMB != 2 || s.CPUs != 1.5 || s.X.Password != "${PW}" {
		t.Errorf("service = %+v", s)
	}
	if s.Ports[0] != (store.PublishedPort{HostPort: 5353, ContainerPort: 53, Protocol: "udp"}) || s.Volumes[0] != (store.Volume{Name: "data", Path: "/data"}) {
		t.Errorf("ports/volumes = %+v %+v", s.Ports, s.Volumes)
	}
}

func TestEnvRoundTrip(t *testing.T) {
	in := map[string]string{"A": "plain", "B": "it's $x", "C": "line\nbreak \\ \"q\"", "D": ""}
	got, err := ParseEnv(MarshalEnv(in))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("got %q, want %q", got, in)
	}
	got, err = ParseEnv([]byte("# c\nexport X=1 # note\nY=\"a\\tb\"\n"))
	if err != nil || got["X"] != "1" || got["Y"] != "a\tb" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := ParseEnv([]byte("nope\n")); err == nil {
		t.Error("want error for a line without =")
	}
}

func TestHostAccess(t *testing.T) {
	// Beszel's agent, as its docs give it.
	f, warns, err := Parse([]byte(`services:
  beszel-agent:
    image: henrygd/beszel-agent:0.12
    network_mode: host
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - agent-data:/var/lib/beszel-agent
    environment:
      LISTEN: 45876
`), Vars{})
	if err != nil {
		t.Fatal(err)
	}
	s := f.Services["beszel-agent"]
	if !s.HostNetwork || s.DockerSocket != "ro" || len(s.Volumes) != 1 || s.Volumes[0].Name != "agent-data" || len(warns) != 1 {
		t.Fatalf("service = %+v, warnings %v", s, warns)
	}
	data, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := Parse(data, Vars{})
	if err != nil || !reflect.DeepEqual(back.Services["beszel-agent"], s) {
		t.Fatalf("round trip = %+v, %v\n%s", back.Services["beszel-agent"], err, data)
	}

	long, _, err := Parse([]byte(`services:
  a:
    image: x
    volumes: [{type: bind, source: /var/run/docker.sock, target: /var/run/docker.sock}]
`), Vars{})
	if err != nil || long.Services["a"].DockerSocket != "rw" {
		t.Errorf("long form = %+v, %v", long.Services["a"], err)
	}

	for name, file := range map[string]string{
		"other bind":   "services: {a: {image: x, volumes: ['/etc:/etc']}}",
		"socket moved": "services: {a: {image: x, volumes: ['/var/run/docker.sock:/docker.sock']}}",
		"bridge":       "services: {a: {image: x, network_mode: bridge}}",
		"service mode": "services: {a: {image: x, network_mode: 'service:b'}}",
		"database":     "services: {db: {image: 'postgres:17', network_mode: host}}",
	} {
		if _, _, err := Parse([]byte(file), Vars{}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

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
	f, warns, err := Parse(data, Vars{Lookup: func(n string) (string, bool) { return "v", true }})
	if err != nil {
		t.Fatal(err)
	}
	web, api := f.Services["web"], f.Services["api"]
	if web.X.Port != 3000 || api.X.Port != 9090 {
		t.Errorf("ports = %d, %d", web.X.Port, api.X.Port)
	}
	want := &store.Healthcheck{Test: []string{"CMD-SHELL", "wget -qO- http://localhost:3000/healthz || exit 1"},
		IntervalSeconds: 30, TimeoutSeconds: 5, StartPeriodSeconds: 60, Retries: 3}
	if !reflect.DeepEqual(web.Healthcheck, want) {
		t.Errorf("healthcheck = %+v", web.Healthcheck)
	}
	if !reflect.DeepEqual(api.Healthcheck, &store.Healthcheck{Test: []string{"NONE"}}) {
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
