package compose

import (
	"fmt"
	"path"
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
				Image:       "ghcr.io/me/shop:1.4.2",
				Environment: map[string]string{"DATABASE_URL": "{{ db.db.URL }}", "API_KEY": "${WEB_API_KEY}", "PRICE": "$$5"},
				Ports: []store.PublishedPort{{HostPort: 2525, ContainerPort: 25, Protocol: "tcp"}, {HostPort: 9000, ContainerPort: 9000, Protocol: "udp"},
					{HostIP: "127.0.0.1", HostPort: 5432, ContainerPort: 5432, Protocol: "tcp"}, {HostIP: "::1", HostPort: 6379, ContainerPort: 6379, Protocol: "tcp"}},
				Volumes: []store.Volume{{Name: "uploads", Path: "/app/uploads"}, {Name: "config", Path: "/config", ReadOnly: true},
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
				Healthcheck: &store.Healthcheck{Test: []string{"CMD", "wget", "-qO-", "http://localhost:8080/healthz"}, IntervalSeconds: 30, StartPeriodSeconds: 90, Retries: 3},
				Runtime: &store.Runtime{
					Command: []string{"worker", "--queue", "a b"}, Entrypoint: []string{""}, User: "1000:1000", WorkingDir: "/app",
					Labels: map[string]string{"com.example.team": "shop"}, Tmpfs: map[string]string{"/tmp": "size=64m", "/run": ""},
					ShmSizeMB: 256, Ulimits: map[string]store.Ulimit{"nofile": {Soft: 1024, Hard: 4096}, "nproc": {Soft: 512, Hard: 512}},
					Sysctls: map[string]string{"net.core.somaxconn": "1024"}, ReadOnly: true, Init: true, StopSignal: "SIGINT",
					TTY: true, StdinOpen: true, ExtraHosts: []string{"host.docker.internal:host-gateway"},
					CapAdd: []string{"NET_ADMIN"}, CapDrop: []string{"MKNOD"}, Devices: []string{"/dev/net/tun"}, Privileged: true,
					SecurityOpt: []string{"no-new-privileges:true"},
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
	if len(web.Ports) != 1 || web.Ports[0] != (store.PublishedPort{HostIP: "127.0.0.1", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"}) {
		t.Errorf("ports = %+v", web.Ports)
	}
	if !slices.Equal(web.DependsOn, []store.Dependency{{Service: "db"}}) || web.Runtime != nil {
		t.Errorf("depends_on = %v, runtime %v", web.DependsOn, web.Runtime)
	}
	db := f.Services["db"]
	if db.X.Kind != "postgres" || db.Environment != nil || db.Volumes != nil {
		t.Errorf("db = %+v", db)
	}
	for _, w := range []string{"version is ignored", "build is ignored", "isn't published", "environment of a postgres"} {
		if !strings.Contains(strings.Join(warns, "\n"), w) {
			t.Errorf("missing warning %q in %v", w, warns)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"build only":   {"services:\n  web:\n    build: .\n", "kipitiny doesn't build"},
		"cgroup":       {"services:\n  web:\n    image: a\n    cgroup: host\n", "cgroup is not supported"},
		"cap string":   {"services:\n  web:\n    image: a\n    cap_add: NET_ADMIN\n", "cap_add must be a list"},
		"bad quote":    {"services:\n  web:\n    image: a\n    command: echo 'hi\n", "unterminated quote"},
		"bad bool":     {"services:\n  web:\n    image: a\n    init: maybe\n", "not true or false"},
		"bind mount":   {"services:\n  web:\n    image: a\n    volumes: [./data:/data]\n", "need a git project"},
		"nfs plugin":   {"services:\n  web:\n    image: a\n    volumes: [d:/d]\nvolumes: {d: {driver: rexray}}\n", "driver rexray"},
		"missing var":  {"services:\n  web:\n    image: a:${TAG}\n", "variables not set: TAG"},
		"required var": {"services:\n  web:\n    image: a:${TAG:?set a tag}\n", "set a tag"},
		"bad ext":      {"services:\n  web:\n    image: a\n    x-kipitiny:\n      domian: x\n", "domian"},
		"plain auth":   {"services:\n  web:\n    image: a\n    x-kipitiny:\n      middlewares:\n        basic_auth: [{name: a}]\n", "bcrypt hash"},
		"top unknown":  {"services:\n  web:\n    image: a\nmodels: {}\n", "top-level models"},
		"no secret":    {"services:\n  web:\n    image: a\n    secrets: [pw]\n", "secret pw is not defined"},
		"two sources":  {"services:\n  web:\n    image: a\nconfigs: {c: {content: x, file: ./c}}\n", "one of content"},
		"env file":     {"services:\n  web:\n    image: a\n    env_file: .env\n", "needs a git project"},
		"no services":  {"name: x\n", "no services"},
		"port range":   {"services:\n  web:\n    image: a\n    ports: [\"8000-8001:80\"]\n", "ranges of the same length"},
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
	if s.Ports[0] != (store.PublishedPort{HostPort: 5353, ContainerPort: 53, Protocol: "udp"}) || !reflect.DeepEqual(s.Volumes[0], store.Volume{Name: "data", Path: "/data"}) {
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
	want := &store.Runtime{
		Command: []string{"celery", "-A", "shop app", "worker", "--loglevel=${LEVEL}"}, Entrypoint: []string{""},
		Labels: map[string]string{"com.example.a": "1"}, Tmpfs: map[string]string{"/run": "", "/cache": "size=1000000"},
		Ulimits: map[string]store.Ulimit{"nofile": {Soft: 65535, Hard: 65535}}, Sysctls: map[string]string{"net.core.somaxconn": "1024"},
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
	want := []store.PublishedPort{{HostPort: 8000, ContainerPort: 9000, Protocol: "udp"}, {HostPort: 8001, ContainerPort: 9001, Protocol: "udp"},
		{HostPort: 8002, ContainerPort: 9002, Protocol: "udp"}, {HostIP: "::1", HostPort: 5432, ContainerPort: 5432, Protocol: "tcp"}}
	if !slices.Equal(s.Ports, want) {
		t.Errorf("ports = %+v", s.Ports)
	}
	wantVols := []store.Volume{{Name: "app-node-modules", Path: "/app/node_modules"}, {Name: "app-node-modules-2", Path: "/app/node-modules"},
		{Name: "cache", Path: "/cache", ReadOnly: true}, {Name: "shared", Path: "/shared", External: "shared"}}
	if !reflect.DeepEqual(s.Volumes, wantVols) {
		t.Errorf("volumes = %+v", s.Volumes)
	}
	if len(warns) != 2 || !strings.Contains(warns[0], "anonymous") {
		t.Errorf("warnings = %v", warns)
	}
}

func TestFiles(t *testing.T) {
	repo := map[string][]store.ServiceFile{
		"./nginx.conf": {{Content: []byte("server {}"), Mode: 0o644}},
		"./conf":       {{Path: "a.yml", Content: []byte("a: 1")}, {Path: "sub/run.sh", Content: []byte("#!/bin/sh"), Mode: 0o755}},
		"app.env":      {{Content: []byte("A=from-file\nB=from-file\n")}},
		"./key.pem":    {{Content: []byte("KEY")}},
	}
	vars := Vars{
		Lookup: func(n string) (string, bool) { return "pw-value", n == "DB_PW" },
		Ref:    func(_, _, n string) string { return "{{ project." + n + " }}" },
		Files: func(p string) ([]store.ServiceFile, error) {
			if f, ok := repo[p]; ok {
				return slices.Clone(f), nil
			}
			return nil, fmt.Errorf("not found")
		},
	}
	data := []byte(`
services:
  web:
    image: a
    env_file: [app.env, {path: missing.env, required: false}]
    environment: {B: inline}
    volumes: ["./nginx.conf:/etc/nginx/nginx.conf", "./conf:/etc/app", "/srv/media:/media:ro"]
    configs: [app, {source: app, target: /etc/app2.conf, mode: 0440}]
    secrets: [db_pw, {source: token, target: api-token}, tls]
configs:
  app: {content: "port=$${PORT}"}
secrets:
  db_pw: {environment: DB_PW}
  token: {environment: API_TOKEN}
  tls: {file: ./key.pem}
`)
	f, warns, err := Parse(data, vars)
	if err != nil {
		t.Fatal(err)
	}
	s := f.Services["web"]
	if s.Environment["A"] != "from-file" || s.Environment["B"] != "inline" {
		t.Errorf("env = %v", s.Environment)
	}
	got := map[string]string{}
	for _, file := range s.Files {
		got[file.Path] = fmt.Sprintf("%s|%o|%v|%s", file.Content, file.Mode, file.Secret, file.Source)
	}
	want := map[string]string{
		"/etc/nginx/nginx.conf":  "server {}|644|false|./nginx.conf",
		"/etc/app/a.yml":         "a: 1|0|false|./conf",
		"/etc/app/sub/run.sh":    "#!/bin/sh|755|false|./conf",
		"/app":                   "port=${PORT}|0|false|config:app",
		"/etc/app2.conf":         "port=${PORT}|440|false|config:app",
		"/run/secrets/db_pw":     "pw-value|0|true|secret:db_pw",
		"/run/secrets/api-token": "{{ project.API_TOKEN }}|0|true|secret:token",
		"/run/secrets/tls":       "KEY|0|true|secret:tls",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("files = %v", got)
	}
	if s.Runtime == nil || !slices.Equal(s.Runtime.Binds, []string{"/srv/media:/media:ro"}) {
		t.Errorf("binds = %+v", s.Runtime)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}

	// Exported, the files are configs (and secrets, by variable) a file
	// without a repository reads back.
	out := s
	out.Environment = nil
	out.Files = nil
	for _, file := range s.Files {
		if file.Secret {
			file.Content = []byte("WEB_" + strings.ToUpper(strings.TrimPrefix(file.Source, "secret:")))
		} else {
			file.Content = []byte(Escape(string(file.Content)))
		}
		out.Files = append(out.Files, file)
	}
	written, err := Marshal(File{Services: map[string]Service{"web": out}})
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := Parse(written, Vars{Lookup: func(n string) (string, bool) { return "v-" + n, true }})
	if err != nil {
		t.Fatalf("%v\n%s", err, written)
	}
	if b := back.Services["web"]; len(b.Files) != len(s.Files) || !slices.Equal(b.Runtime.Binds, s.Runtime.Binds) {
		t.Errorf("read back:\n%+v\n%s", b, written)
	}
	for _, file := range back.Services["web"].Files {
		if file.Path == "/etc/app2.conf" && (string(file.Content) != "port=${PORT}" || file.Mode != 0o440) {
			t.Errorf("config read back = %+v\n%s", file, written)
		}
		if file.Path == "/run/secrets/tls" && string(file.Content) != "v-WEB_TLS" {
			t.Errorf("secret read back = %+v", file)
		}
	}

	// A config's variable is a compose variable: only secrets fall back to
	// a project reference, since only they are resolved at deploy.
	_, _, err = Parse([]byte("services:\n  web:\n    image: a\n    configs: [c]\nconfigs:\n  c: {environment: GREETING}\n"), vars)
	if err == nil || !strings.Contains(err.Error(), "variables not set: GREETING") {
		t.Errorf("config from a missing variable: err = %v", err)
	}
}

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
	vars := Vars{Files: func(p string) ([]store.ServiceFile, error) {
		if c, ok := repo[path.Clean(p)]; ok {
			return []store.ServiceFile{{Content: []byte(c)}}, nil
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

func TestDependsOnRestart(t *testing.T) {
	f, warns, err := Parse([]byte(`
services:
  web:
    image: a
    depends_on:
      migrate: {condition: service_completed_successfully}
      api: {condition: service_healthy, required: true}
  api:
    image: a
    depends_on: [db]
    restart: on-failure:3
  migrate:
    image: a
    restart: "no"
  db:
    image: postgres:16
    restart: always
`), Vars{})
	if err != nil {
		t.Fatal(err)
	}
	web := f.Services["web"]
	if !slices.Equal(web.DependsOn, []store.Dependency{{Service: "api", Condition: "healthy"}, {Service: "migrate", Condition: "completed"}}) {
		t.Errorf("web depends_on = %v", web.DependsOn)
	}
	if f.Services["migrate"].Runtime.Restart != "no" || f.Services["api"].Runtime.Restart != "on-failure:3" || f.Services["db"].Runtime != nil {
		t.Errorf("restart: %+v %+v", f.Services["migrate"].Runtime, f.Services["api"].Runtime)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "required is ignored") {
		t.Errorf("warns = %v", warns)
	}
	data, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"restart: \"no\"", "depends_on:\n      - db", "condition: service_completed_successfully", "restart: on-failure:3"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("marshal lacks %q:\n%s", want, data)
		}
	}
	back, _, err := Parse(data, Vars{})
	if err != nil || !slices.Equal(back.Services["web"].DependsOn, web.DependsOn) {
		t.Errorf("round trip: %v %v", err, back.Services["web"].DependsOn)
	}

	for in, want := range map[string]string{
		"services: {a: {image: a, depends_on: [b]}}":                                   "depends_on b: no such service",
		"services: {a: {image: a, depends_on: [b]}, b: {image: a, depends_on: [a]}}":   "depends_on cycle: a → b → a",
		"services: {a: {image: a, depends_on: {b: {condition: nope}}}, b: {image: a}}": "condition nope is not supported",
		"services: {a: {image: a, restart: sometimes}}":                                "restart sometimes is not supported",
	} {
		if _, _, err := Parse([]byte(in), Vars{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", in, err, want)
		}
	}
}

func TestNetworks(t *testing.T) {
	f, warns, err := Parse([]byte(`
services:
  proxy:
    image: a
    networks: [front, shared]
  api:
    image: a
    networks:
      front:
      back: {aliases: [api.internal]}
  db:
    image: postgres:16
    networks: [back]
  plain:
    image: a
networks:
  front:
  back: {internal: true}
  shared: {external: true, name: kipitiny-shared}
`), Vars{})
	if err != nil {
		t.Fatal(err)
	}
	if s := f.Services["proxy"]; !slices.Equal(s.Networks, []string{"front"}) || !slices.Equal(s.ExternalNetworks, []string{"kipitiny-shared"}) {
		t.Errorf("proxy = %v %v", s.Networks, s.ExternalNetworks)
	}
	if s := f.Services["api"]; !slices.Equal(s.Networks, []string{"back", "front"}) || s.ExternalNetworks != nil {
		t.Errorf("api = %v %v", s.Networks, s.ExternalNetworks)
	}
	if s := f.Services["db"]; !slices.Equal(s.Networks, []string{"back"}) {
		t.Errorf("db = %v", s.Networks)
	}
	if s := f.Services["plain"]; s.Networks != nil {
		t.Errorf("plain = %v", s.Networks)
	}
	if w := strings.Join(warns, "\n"); !strings.Contains(w, "internal is ignored") || !strings.Contains(w, "aliases") {
		t.Errorf("warns = %v", warns)
	}
	data, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := Parse(data, Vars{})
	if err != nil {
		t.Fatal(err)
	}
	if s := back.Services["proxy"]; !slices.Equal(s.Networks, []string{"front"}) || !slices.Equal(s.ExternalNetworks, []string{"kipitiny-shared"}) {
		t.Errorf("round trip proxy = %v %v\n%s", s.Networks, s.ExternalNetworks, data)
	}
	if back.Services["plain"].Networks != nil {
		t.Errorf("round trip plain = %v", back.Services["plain"].Networks)
	}
	for in, want := range map[string]string{
		"services: {a: {image: a, networks: [nope]}}":                               "network nope is not defined",
		"services: {a: {image: a, network_mode: host, networks: [default]}}":        "host network can't join",
		"services: {a: {image: a}}\nnetworks: {default: {external: true, name: x}}": "default network can't be external",
	} {
		if _, _, err := Parse([]byte(in), Vars{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", in, err, want)
		}
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
