package core

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestResolveEnv(t *testing.T) {
	project := map[string]string{"HOST": "db.internal", "PASS": "s3cret"}
	env := map[string]string{
		"A": "{{ project.HOST }}",
		"B": "postgres://app:{{project.PASS}}@{{  project.HOST  }}/app",
		"C": "{{ project.NOPE }}",
		"D": "{{ other.HOST }}",
	}
	want := map[string]string{
		"A": "db.internal",
		"B": "postgres://app:s3cret@db.internal/app",
		"C": "",
		"D": "{{ other.HOST }}",
	}
	src := envSources{project: project}
	if got := resolveEnv(env, src); !maps.Equal(got, want) {
		t.Errorf("resolveEnv = %v, want %v", got, want)
	}
	if got := unresolved(env, src); !slices.Equal(got, []string{"project.NOPE"}) {
		t.Errorf("unresolved = %v", got)
	}
}

func TestResolveDatabaseRefs(t *testing.T) {
	db := pgService()
	src := envSources{dbs: map[string]store.Service{db.Name: db}}
	env := map[string]string{
		"DATABASE_URL": "{{ db." + db.Name + ".URL }}",
		"PGHOST":       "{{ db." + db.Name + ".HOST }}",
		"PGPORT":       "{{db." + db.Name + ".PORT}}",
		"PGUSER":       "{{ db." + db.Name + ".USER }}",
		"PGPASSWORD":   "{{ db." + db.Name + ".PASSWORD }}",
		"PGDATABASE":   "{{ db." + db.Name + ".DATABASE }}",
		"BAD_FIELD":    "{{ db." + db.Name + ".NOPE }}",
		"BAD_DB":       "{{ db.gone.URL }}",
	}
	got := resolveEnv(env, src)
	want := map[string]string{
		"DATABASE_URL": DatabaseURL(db), "PGHOST": db.Name, "PGPORT": "5432",
		"PGUSER": db.Env[pgUser], "PGPASSWORD": db.Env[pgPassword], "PGDATABASE": db.Env[pgDatabase],
		"BAD_FIELD": "", "BAD_DB": "",
	}
	if !maps.Equal(got, want) {
		t.Errorf("resolveEnv = %v, want %v", got, want)
	}
	if got, want := unresolved(env, src), []string{"db." + db.Name + ".NOPE", "db.gone.URL"}; !slices.Equal(got, want) {
		t.Errorf("unresolved = %v, want %v", got, want)
	}
	if !usesDatabase(env, db.Name) || usesDatabase(map[string]string{"A": "{{ project.X }}"}, db.Name) {
		t.Error("usesDatabase")
	}
	if !soleRefRe.MatchString(env["PGHOST"]) {
		t.Error("a database reference must count as a sole reference (not masked)")
	}
}

func TestMaskEnv(t *testing.T) {
	got := maskEnv(map[string]string{"VAR": "v", "KEY": "k", "REF": "{{ project.KEY }}", "MIX": "x{{ project.KEY }}"},
		[]string{"KEY", "REF", "MIX"})
	want := map[string]string{"VAR": "v", "KEY": SecretMask, "REF": "{{ project.KEY }}", "MIX": SecretMask}
	if !maps.Equal(got, want) {
		t.Errorf("maskEnv = %v, want %v", got, want)
	}
}

func TestMergeEnv(t *testing.T) {
	old := map[string]string{"VAR": "v", "KEY": "k", "TOKEN": "t"}
	oldSecrets := []string{"KEY", "TOKEN"}

	env, secrets, err := mergeEnv(map[string]string{"VAR": "v2", "KEY": SecretMask, "TOKEN": "new", "NEW": "n"},
		[]string{"KEY", "NEW"}, old, oldSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"VAR": "v2", "KEY": "k", "TOKEN": "new", "NEW": "n"}; !maps.Equal(env, want) {
		t.Errorf("env = %v, want %v", env, want)
	}
	if want := []string{"KEY", "NEW"}; !slices.Equal(secrets, want) {
		t.Errorf("secrets = %v, want %v (TOKEN became a variable with a new value)", secrets, want)
	}

	// A masked secret can't become a variable: that would reveal it.
	if _, _, err := mergeEnv(map[string]string{"KEY": SecretMask}, []string{}, old, oldSecrets); !errors.Is(err, ErrInvalid) {
		t.Errorf("unmasking a secret: got %v, want ErrInvalid", err)
	}

	// Without flags, stored ones are kept and new entries are secrets.
	_, secrets, err = mergeEnv(map[string]string{"VAR": "v", "KEY": SecretMask, "NEW": "n"}, nil, old, oldSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"KEY", "NEW"}; !slices.Equal(secrets, want) {
		t.Errorf("default secrets = %v, want %v", secrets, want)
	}
}

func TestSetProjectEnv(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop"})
	if err != nil {
		t.Fatal(err)
	}

	got, err := c.SetProjectEnv(ctx, p.ID, map[string]string{"HOST": "db", "PASS": "s3cret"}, []string{"PASS"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Env["PASS"] != SecretMask || got.Env["HOST"] != "db" {
		t.Errorf("returned env: %v, want the secret masked and the variable readable", got.Env)
	}
	if _, err := c.SetProjectEnv(ctx, p.ID, map[string]string{"BAD-KEY": "1"}, nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad key: got %v, want ErrInvalid", err)
	}

	if _, err := c.store.CreateService(ctx, store.Service{
		ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1,
		Env: map[string]string{"DB_HOST": "{{ project.HOST }}"},
	}); err != nil {
		t.Fatal(err)
	}
	// A variable referenced by a service can't go.
	if _, err := c.SetProjectEnv(ctx, p.ID, map[string]string{"PASS": SecretMask}, []string{"PASS"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("removing a referenced variable: got %v, want ErrInvalid", err)
	}
	if _, err := c.SetProjectEnv(ctx, p.ID, map[string]string{"HOST": "db"}, []string{}); err != nil {
		t.Fatal(err)
	}
	stored, _ := c.store.GetProject(ctx, p.ID)
	if !maps.Equal(stored.Env, map[string]string{"HOST": "db"}) {
		t.Errorf("stored env = %v", stored.Env)
	}

	svc := store.Service{ProjectID: p.ID, Image: "nginx", Replicas: 1, Env: map[string]string{"X": "{{ project.MISSING }}"}}
	if err := c.validate(ctx, svc); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown reference: got %v, want ErrInvalid", err)
	}
}

func TestSecretRefs(t *testing.T) {
	project := map[string]string{"TOKEN": "{{ pass://Work/API/token }}", "PLAIN": "p"}
	env := map[string]string{
		"A": "{{ pass://My Vault/My Item/password }}",
		"B": "user:{{pass://Work/DB/password}}@{{ project.PLAIN }}",
		"C": "{{ project.TOKEN }}",
	}
	want := []string{"pass://My Vault/My Item/password", "pass://Work/API/token", "pass://Work/DB/password"}
	if got := secretRefs(env, project); !slices.Equal(got, want) {
		t.Errorf("secretRefs = %v, want %v", got, want)
	}

	// Checking references doesn't fetch secrets: they count as resolvable.
	if got := unresolved(env, envSources{project: project}); len(got) != 0 {
		t.Errorf("unresolved = %v", got)
	}

	src := envSources{project: project, secrets: map[string]string{
		want[0]: "s3cret", want[1]: "tok", want[2]: "dbpw",
	}}
	wantEnv := map[string]string{"A": "s3cret", "B": "user:dbpw@p", "C": "tok"}
	if got := resolveEnv(env, src); !maps.Equal(got, wantEnv) {
		t.Errorf("resolveEnv = %v, want %v", got, wantEnv)
	}

	// A secret that only references a password manager stays readable.
	if got := maskEnv(env, []string{"A", "B"}); got["A"] != env["A"] || got["B"] != SecretMask {
		t.Errorf("maskEnv = %v", got)
	}
}
