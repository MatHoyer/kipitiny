package compose

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestFiles(t *testing.T) {
	repo := map[string][]domain.ServiceFile{
		"./nginx.conf": {{Content: []byte("server {}"), Mode: 0o644}},
		"./conf":       {{Path: "a.yml", Content: []byte("a: 1")}, {Path: "sub/run.sh", Content: []byte("#!/bin/sh"), Mode: 0o755}},
		"app.env":      {{Content: []byte("A=from-file\nB=from-file\n")}},
		"./key.pem":    {{Content: []byte("KEY")}},
	}
	vars := Vars{
		Lookup: func(n string) (string, bool) { return "pw-value", n == "DB_PW" },
		Ref:    func(_, _, n string) string { return "{{ project." + n + " }}" },
		Files: func(p string) ([]domain.ServiceFile, error) {
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
