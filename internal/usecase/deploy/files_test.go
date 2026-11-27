package deploy

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestServiceFiles(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, domain.Project{Name: "shop", ServerID: domain.LocalServerID, Env: map[string]string{"TOKEN": "t0k"}})
	if err != nil {
		t.Fatal(err)
	}
	src := `
services:
  web:
    image: nginx
    configs: [{source: site, target: /etc/nginx/conf.d/site.conf, mode: 0640}]
    secrets: [token]
configs:
  site: {content: "listen 80; # $$host"}
secrets:
  token: {environment: TOKEN}
`
	if _, err := c.ApplyCompose(ctx, p.ID, []byte(src), ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	web := servicesByName(t, c, p.ID)["web"]
	if len(web.Files) != 2 || string(web.Files[0].Content) != "listen 80; # $host" || web.Files[1].Path != "/run/secrets/token" ||
		string(web.Files[1].Content) != "{{ project.TOKEN }}" || web.Files[0].Size != 18 {
		t.Fatalf("files = %+v", web.Files)
	}
	got := serviceFiles(web, envSources{project: p.Env})
	if string(got[1].Content) != "t0k" || got[0].Mode != 0o640 {
		t.Errorf("container files = %+v", got)
	}

	// API responses leave the content out; sent back like that, it's kept.
	view := masked(web)
	if view.Files[0].Content != nil || view.Files[0].Size != 18 || web.Files[0].Content == nil {
		t.Fatalf("view files = %+v", view.Files)
	}
	if _, err := c.updateService(ctx, web.ID, ServicePatch{Files: view.Files}); err != nil {
		t.Fatal(err)
	}
	if again := servicesByName(t, c, p.ID)["web"]; !sameFiles(again.Files, web.Files) {
		t.Errorf("files after a round trip through the API = %+v", again.Files)
	}

	out, err := c.ExportCompose(ctx, p.ID, ExportOptions{Secrets: true})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := c.ApplyCompose(ctx, p.ID, out.Compose, ApplyOptions{DryRun: true})
	if err != nil || !slices.Equal(plan.Unchanged, []string{"web"}) {
		t.Errorf("own export re-applied: %+v, %v\n%s", plan, err, out.Compose)
	}

	for name, s := range map[string]domain.Service{
		"read-only root": {Runtime: domain.Runtime{ReadOnly: true}, Files: []domain.ServiceFile{{Path: "/a"}}},
		"in a volume":    {Volumes: []domain.Volume{{Name: "d", Path: "/data"}}, Files: []domain.ServiceFile{{Path: "/data/x"}}},
		"relative":       {Files: []domain.ServiceFile{{Path: "etc/x"}}},
		"twice":          {Files: []domain.ServiceFile{{Path: "/a"}, {Path: "/a"}}},
	} {
		s.Kind, s.Image, s.Replicas = domain.ServiceKindApp, "x", 1
		if err := validateService(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}
