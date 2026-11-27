package deploy

import (
	"slices"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestUsedDatabases(t *testing.T) {
	main := domain.Service{ID: "db1", Name: "main", Kind: domain.ServiceKindPostgres}
	cache := domain.Service{ID: "db2", Name: "cache", Kind: domain.ServiceKindPostgres}
	dbs := databasesOf([]domain.Service{main, cache})
	p := domain.Project{Env: map[string]string{"SHARED": "{{ db.cache.URL }}", "PLAIN": "x"}}

	app := domain.Service{ID: "app", Env: map[string]string{
		"DATABASE_URL": "{{ db.main.URL }}",
		"PGHOST":       "{{db.main.HOST}}",
		"CACHE":        "{{ project.SHARED }}",
		"OTHER":        "{{ project.PLAIN }} {{ db.gone.URL }}",
	}}
	if got := usedDatabases(app, p, dbs); !slices.Equal(got, []string{"db1", "db2"}) {
		t.Errorf("usedDatabases = %v", got)
	}
	if got := usedDatabases(domain.Service{ID: "x"}, p, dbs); len(got) != 0 {
		t.Errorf("no env: %v", got)
	}
	self := domain.Service{ID: "db1", Env: map[string]string{"A": "{{ db.main.URL }}"}}
	if got := usedDatabases(self, p, dbs); len(got) != 0 {
		t.Errorf("self reference: %v", got)
	}
}
