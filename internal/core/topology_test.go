package core

import (
	"slices"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestUsedDatabases(t *testing.T) {
	main := store.Service{ID: "db1", Name: "main", Kind: store.ServiceKindPostgres}
	cache := store.Service{ID: "db2", Name: "cache", Kind: store.ServiceKindPostgres}
	dbs := databasesOf([]store.Service{main, cache})
	p := store.Project{Env: map[string]string{"SHARED": "{{ db.cache.URL }}", "PLAIN": "x"}}

	app := store.Service{ID: "app", Env: map[string]string{
		"DATABASE_URL": "{{ db.main.URL }}",
		"PGHOST":       "{{db.main.HOST}}",
		"CACHE":        "{{ project.SHARED }}",
		"OTHER":        "{{ project.PLAIN }} {{ db.gone.URL }}",
	}}
	if got := usedDatabases(app, p, dbs); !slices.Equal(got, []string{"db1", "db2"}) {
		t.Errorf("usedDatabases = %v", got)
	}
	if got := usedDatabases(store.Service{ID: "x"}, p, dbs); len(got) != 0 {
		t.Errorf("no env: %v", got)
	}
	self := store.Service{ID: "db1", Env: map[string]string{"A": "{{ db.main.URL }}"}}
	if got := usedDatabases(self, p, dbs); len(got) != 0 {
		t.Errorf("self reference: %v", got)
	}
}
