package compose

import (
	"reflect"
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
					Domain: "shop.example.com", Port: 3000, HealthPath: "/healthz", PreDeploy: "./migrate up",
					Secrets: []string{"API_KEY"},
					Middlewares: &Middlewares{
						BasicAuth:   []BasicAuthUser{{Name: "admin", Hash: "$2a$10$abc"}, {Name: "ops", Ref: "{{ pass://V/I/p }}"}},
						IPAllowList: []string{"1.2.3.0/24"},
						RateLimit:   &RateLimit{Average: 50, Burst: 100},
						Headers:     map[string]string{"X-Frame-Options": "DENY"},
					},
				},
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
