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
	if got := resolveEnv(env, project); !maps.Equal(got, want) {
		t.Errorf("resolveEnv = %v, want %v", got, want)
	}
	if got := missingRefs(env, project); !slices.Equal(got, []string{"NOPE"}) {
		t.Errorf("missingRefs = %v", got)
	}
}

func TestMaskEnv(t *testing.T) {
	got := maskEnv(map[string]string{
		"REF":   "{{ project.A }}",
		"REFS":  "{{ project.A }} {{project.B}}",
		"MIXED": "x{{ project.A }}",
		"LIT":   "secret",
		"EMPTY": "",
	})
	want := map[string]string{
		"REF":   "{{ project.A }}",
		"REFS":  "{{ project.A }} {{project.B}}",
		"MIXED": SecretMask,
		"LIT":   SecretMask,
		"EMPTY": SecretMask,
	}
	if !maps.Equal(got, want) {
		t.Errorf("maskEnv = %v, want %v", got, want)
	}
}

func TestSetProjectEnv(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop"})
	if err != nil {
		t.Fatal(err)
	}

	got, err := c.SetProjectEnv(ctx, p.ID, map[string]string{"HOST": "db", "PASS": "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Env["PASS"] != SecretMask {
		t.Errorf("returned env not masked: %v", got.Env)
	}
	if _, err := c.SetProjectEnv(ctx, p.ID, map[string]string{"BAD-KEY": "1"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad key: got %v, want ErrInvalid", err)
	}

	if _, err := c.store.CreateService(ctx, store.Service{
		ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1,
		Env: map[string]string{"DB_HOST": "{{ project.HOST }}"},
	}); err != nil {
		t.Fatal(err)
	}
	// Masked values keep the stored ones; a referenced variable can't go.
	if _, err := c.SetProjectEnv(ctx, p.ID, map[string]string{"PASS": SecretMask}); !errors.Is(err, ErrInvalid) {
		t.Errorf("removing a referenced variable: got %v, want ErrInvalid", err)
	}
	if _, err := c.SetProjectEnv(ctx, p.ID, map[string]string{"HOST": SecretMask}); err != nil {
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
