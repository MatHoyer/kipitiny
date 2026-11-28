package compose

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/anonymize"
)

func TestRoundTrip(t *testing.T) {
	in := File{
		Name: "shop",
		Services: map[string]Service{
			"web": {
				Image:       "ghcr.io/me/shop:1.4.2",
				Environment: map[string]string{"DATABASE_URL": "{{ db.db.URL }}", "API_KEY": "${WEB_API_KEY}", "PRICE": "$$5"},
				Ports: []domain.PublishedPort{{HostPort: 2525, ContainerPort: 25, Protocol: "tcp"}, {HostPort: 9000, ContainerPort: 9000, Protocol: "udp"},
					{HostIP: "127.0.0.1", HostPort: 5432, ContainerPort: 5432, Protocol: "tcp"}, {HostIP: "::1", HostPort: 6379, ContainerPort: 6379, Protocol: "tcp"}},
				Volumes: []domain.Volume{{Name: "uploads", Path: "/app/uploads"}, {Name: "config", Path: "/config", ReadOnly: true},
					{Name: "media", Path: "/media", External: "nas-media"}, {Name: "nfs", Path: "/nfs", DriverOpts: map[string]string{"type": "nfs", "o": "addr=10.0.0.5", "device": ":/export"}}},
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
				Healthcheck: &domain.Healthcheck{Test: []string{"CMD", "wget", "-qO-", "http://localhost:8080/healthz"}, IntervalSeconds: 30, StartPeriodSeconds: 90, Retries: 3},
				Runtime: &domain.Runtime{
					Command: []string{"worker", "--queue", "a b"}, Entrypoint: []string{""}, User: "1000:1000", WorkingDir: "/app",
					Labels: map[string]string{"com.example.team": "shop"}, Tmpfs: map[string]string{"/tmp": "size=64m", "/run": ""},
					ShmSizeMB: 256, Ulimits: map[string]domain.Ulimit{"nofile": {Soft: 1024, Hard: 4096}, "nproc": {Soft: 512, Hard: 512}},
					Sysctls: map[string]string{"net.core.somaxconn": "1024"}, ReadOnly: true, Init: true, StopSignal: "SIGINT",
					TTY: true, StdinOpen: true, ExtraHosts: []string{"host.docker.internal:host-gateway"},
					CapAdd: []string{"NET_ADMIN"}, CapDrop: []string{"MKNOD"}, Devices: []string{"/dev/net/tun"}, Privileged: true,
					SecurityOpt: []string{"no-new-privileges:true"}, Platform: "linux/arm64",
				},
			},
			"db": {Image: "postgres:17-alpine", MemoryMB: 512, X: Ext{Kind: "postgres", Password: "${DB_PASSWORD}", Anonymize: &anonymize.Rules{
				"users":    {Columns: map[string]string{"email": "fake:email", "pw": "fixed:$2b$10$x"}},
				"sessions": {Action: anonymize.Truncate},
			}}},
			"cache": {Image: "mongo:8.2", MemoryMB: 512, X: Ext{Kind: "mongodb", Anonymize: &anonymize.Rules{}}},
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
	if len(web.Ports) != 1 || web.Ports[0] != (domain.PublishedPort{HostIP: "127.0.0.1", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"}) {
		t.Errorf("ports = %+v", web.Ports)
	}
	if !slices.Equal(web.DependsOn, []domain.Dependency{{Service: "db"}}) || web.Runtime != nil {
		t.Errorf("depends_on = %v, runtime %v", web.DependsOn, web.Runtime)
	}
	db := f.Services["db"]
	if db.X.Kind != "postgres" || db.Environment != nil || db.Volumes != nil || db.X.Password != "x" {
		t.Errorf("db = %+v", db)
	}
	for _, w := range []string{"version is ignored", "build is ignored", "isn't published"} {
		if !strings.Contains(strings.Join(warns, "\n"), w) {
			t.Errorf("missing warning %q in %v", w, warns)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"build only":     {"services:\n  web:\n    build: .\n", "kipitiny doesn't build"},
		"cgroup":         {"services:\n  web:\n    image: a\n    cgroup: host\n", "cgroup is not supported"},
		"cap string":     {"services:\n  web:\n    image: a\n    cap_add: NET_ADMIN\n", "cap_add must be a list"},
		"bad quote":      {"services:\n  web:\n    image: a\n    command: echo 'hi\n", "unterminated quote"},
		"bad bool":       {"services:\n  web:\n    image: a\n    init: maybe\n", "not true or false"},
		"bind mount":     {"services:\n  web:\n    image: a\n    volumes: [./data:/data]\n", "need a git project"},
		"nfs plugin":     {"services:\n  web:\n    image: a\n    volumes: [d:/d]\nvolumes: {d: {driver: rexray}}\n", "driver rexray"},
		"missing var":    {"services:\n  web:\n    image: a:${TAG}\n", "variables not set: TAG"},
		"required var":   {"services:\n  web:\n    image: a:${TAG:?set a tag}\n", "set a tag"},
		"bad ext":        {"services:\n  web:\n    image: a\n    x-kipitiny:\n      domian: x\n", "domian"},
		"plain auth":     {"services:\n  web:\n    image: a\n    x-kipitiny:\n      middlewares:\n        basic_auth: [{name: a}]\n", "bcrypt hash"},
		"top unknown":    {"services:\n  web:\n    image: a\nmodels: {}\n", "top-level models"},
		"no secret":      {"services:\n  web:\n    image: a\n    secrets: [pw]\n", "secret pw is not defined"},
		"two sources":    {"services:\n  web:\n    image: a\nconfigs: {c: {content: x, file: ./c}}\n", "one of content"},
		"env file":       {"services:\n  web:\n    image: a\n    env_file: .env\n", "needs a git project"},
		"no services":    {"name: x\n", "no services"},
		"port range":     {"services:\n  web:\n    image: a\n    ports: [\"8000-8001:80\"]\n", "ranges of the same length"},
		"anonymize app":  {"services:\n  web:\n    image: a\n    x-kipitiny:\n      anonymize: {}\n", "only PostgreSQL"},
		"anonymize rule": {"services:\n  db:\n    image: postgres:17\n    x-kipitiny:\n      anonymize: {users: {email: fake:iban}}\n", "users.email"},
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
	if s.Ports[0] != (domain.PublishedPort{HostPort: 5353, ContainerPort: 53, Protocol: "udp"}) || !reflect.DeepEqual(s.Volumes[0], domain.Volume{Name: "data", Path: "/data"}) {
		t.Errorf("ports/volumes = %+v %+v", s.Ports, s.Volumes)
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
	if !s.HostNetwork || s.DockerSocket != "ro" || len(s.Volumes) != 1 || s.Volumes[0].Name != "agent-data" || len(warns) != 0 {
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
		"home bind":    "services: {a: {image: x, volumes: ['~/etc:/etc']}}",
		"socket moved": "services: {a: {image: x, volumes: ['/var/run/docker.sock:/docker.sock']}}",
		"bridge":       "services: {a: {image: x, network_mode: bridge}}",
		"service mode": "services: {a: {image: x, network_mode: 'service:b'}}",
		"database":     "services: {db: {image: 'postgres:17', network_mode: host}}",
	} {
		if _, _, err := Parse([]byte(file), Vars{}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, _, err := Parse([]byte("services: {a: {image: x, network_mode: 'service:vpn'}}"), Vars{}); err == nil || !strings.Contains(err.Error(), "proxy") {
		t.Errorf("service mode error doesn't explain: %v", err)
	}
}

func TestSleepAfterMoved(t *testing.T) {
	data := []byte("services:\n  web:\n    image: nginx\n    x-kipitiny:\n      domain: shop.example.com\n      port: 80\n      sleep_after: 30m\n")
	f, warns, err := Parse(data, Vars{})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "sleep_after is ignored") || f.Services["web"].X.Domain != "shop.example.com" {
		t.Fatalf("warnings %v, web %+v", warns, f.Services["web"].X)
	}
}

func TestProfiles(t *testing.T) {
	f, warns, err := Parse([]byte(`
services:
  web: {image: a}
  mailpit: {image: axllent/mailpit, profiles: [preview]}
  mailer: {image: a, x-kipitiny: {preview: skip}}
  debug: {image: a, profiles: [debug]}
`), Vars{})
	if err != nil {
		t.Fatal(err)
	}
	if f.Services["mailpit"].X.Preview != "only" || f.Services["mailer"].X.Preview != "skip" || f.Services["web"].X.Preview != "" {
		t.Errorf("services = %+v", f.Services)
	}
	if _, ok := f.Services["debug"]; ok || len(warns) != 1 || !strings.Contains(warns[0], "service debug: skipped") {
		t.Errorf("debug kept or warns = %v", warns)
	}
	data, _ := Marshal(f)
	if !strings.Contains(string(data), "preview: only") {
		t.Errorf("marshal:\n%s", data)
	}
	for in, want := range map[string]string{
		"services: {a: {image: a, x-kipitiny: {preview: maybe}}}":                     "preview must be only or skip",
		"services: {a: {image: a, profiles: [preview], x-kipitiny: {preview: skip}}}": "contradict",
	} {
		if _, _, err := Parse([]byte(in), Vars{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", in, err, want)
		}
	}
}

func TestAnalyticsRoundTrip(t *testing.T) {
	src := "services:\n  web:\n    image: nginx\n    x-kipitiny: {domain: shop.example.com, port: 80, analytics: true}\n"
	f, _, err := Parse([]byte(src), Vars{})
	if err != nil {
		t.Fatal(err)
	}
	if !f.Services["web"].X.Analytics {
		t.Fatal("analytics not read")
	}
	out, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "analytics: true") {
		t.Errorf("not written:\n%s", out)
	}
}
