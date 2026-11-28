package compose

import (
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestDatabaseEnv(t *testing.T) {
	repo := map[string]string{"./init.sql": "CREATE TABLE t();"}
	vars := Vars{Files: func(p string) ([]domain.ServiceFile, error) {
		if c, ok := repo[p]; ok {
			return []domain.ServiceFile{{Content: []byte(c)}}, nil
		}
		return nil, fmt.Errorf("%s not found", p)
	}}
	f, warns, err := Parse([]byte(`
services:
  db:
    image: postgres:17
    environment: {POSTGRES_USER: shop, POSTGRES_DB: shop, POSTGRES_PASSWORD: s3cret, PGDATA: /x}
    volumes: ["./init.sql:/docker-entrypoint-initdb.d/init.sql", "./init.sql:/etc/other.sql"]
  maria:
    image: mariadb:11
    x-kipitiny: {kind: mariadb}
    environment: [MARIADB_USER=wp, MARIADB_DATABASE=wp]
  web:
    image: a
    deploy:
      restart_policy: {condition: on-failure, max_attempts: 3, delay: 5s}
      update_config: {order: start-first}
      resources: {reservations: {memory: 256m, cpus: "0.5"}}
`), vars)
	if err != nil {
		t.Fatal(err)
	}
	db := f.Services["db"]
	if !maps.Equal(db.Environment, map[string]string{"POSTGRES_USER": "shop", "POSTGRES_DB": "shop"}) || db.X.Password != "s3cret" {
		t.Errorf("db = %v %q", db.Environment, db.X.Password)
	}
	if len(db.Files) != 1 || db.Files[0].Path != "/docker-entrypoint-initdb.d/init.sql" {
		t.Errorf("db files = %+v", db.Files)
	}
	if m := f.Services["maria"]; !maps.Equal(m.Environment, map[string]string{"MYSQL_USER": "wp", "MYSQL_DATABASE": "wp"}) {
		t.Errorf("maria = %v", m.Environment)
	}
	if r := f.Services["web"].Runtime; r == nil || r.Restart != "on-failure:3" || r.MemoryReservationMB != 256 {
		t.Errorf("web runtime = %+v", r)
	}
	w := strings.Join(warns, "\n")
	for _, want := range []string{"PGDATA of a postgres service is ignored", "/etc/other.sql of a postgres service is ignored", "restart_policy.delay is ignored", "update_config is ignored", "reservations.cpus is ignored"} {
		if !strings.Contains(w, want) {
			t.Errorf("warnings lack %q: %v", want, warns)
		}
	}

	// The reservation survives an export.
	data, err := Marshal(File{Services: map[string]Service{"web": f.Services["web"]}})
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := Parse(data, Vars{})
	if err != nil || back.Services["web"].Runtime == nil || back.Services["web"].Runtime.MemoryReservationMB != 256 {
		t.Errorf("read back: %v\n%s", err, data)
	}
}
