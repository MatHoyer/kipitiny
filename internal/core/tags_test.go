package core

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestNormalizeTags(t *testing.T) {
	got, err := normalizeTags([]string{" Prod ", "prod", "", "client  A", "v1.2/eu:west_x-y"})
	if err != nil || !slices.Equal(got, []string{"client a", "prod", "v1.2/eu:west_x-y"}) {
		t.Errorf("normalizeTags = %q, %v", got, err)
	}
	if got, err := normalizeTags(nil); err != nil || got == nil || len(got) != 0 {
		t.Errorf("no tags = %#v, %v", got, err)
	}
	for _, bad := range [][]string{{"-x"}, {"a,b"}, {strings.Repeat("a", maxTagLen+1)}, {"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}} {
		if _, err := normalizeTags(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("normalizeTags(%q) = %v, want ErrInvalid", bad, err)
		}
	}
	// web/src/lib/tags.ts derives the same colors.
	for name, want := range map[string]string{"prod": "green", "staging": "amber", "client a": "rose", "été": "red"} {
		if got := AutoTagColor(name); got != want {
			t.Errorf("AutoTagColor(%q) = %s, want %s", name, got, want)
		}
	}
}

func TestTags(t *testing.T) {
	c := newTestCore(t, config.Config{})
	ctx := context.Background()
	admin := WithActor(ctx, Actor{Kind: "user", Name: "admin", Role: store.RoleAdmin})
	shop, _ := c.store.CreateProject(ctx, store.Project{Name: "shop"})
	blog, _ := c.store.CreateProject(ctx, store.Project{Name: "blog"})
	web, err := c.store.CreateService(ctx, store.Service{ProjectID: shop.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}

	if p, err := c.SetProjectTags(admin, shop.ID, []string{"Prod", "client a"}); err != nil || !slices.Equal(p.Tags, []string{"client a", "prod"}) {
		t.Fatalf("SetProjectTags = %v, %v", p.Tags, err)
	}
	if _, err := c.SetProjectTags(admin, blog.ID, []string{"prod", "blog"}); err != nil {
		t.Fatal(err)
	}
	if err := c.store.SetServiceTags(ctx, web.ID, []string{"front"}); err != nil {
		t.Fatal(err)
	}

	tags, err := c.ListTags(admin)
	if err != nil || len(tags) != 4 {
		t.Fatalf("ListTags = %+v, %v", tags, err)
	}
	if prod := tags[slices.IndexFunc(tags, func(t Tag) bool { return t.Name == "prod" })]; prod.Projects != 2 || prod.Color != AutoTagColor("prod") || prod.Custom {
		t.Errorf("prod = %+v", prod)
	}

	// A member sees the tags of their projects, and colors those of the
	// projects they may change.
	member := WithActor(ctx, Actor{Kind: "user", Name: "alice", Role: store.RoleMember,
		Grants: map[string][]string{shop.ID: {string(PermSettings)}, blog.ID: {string(PermView)}}})
	viewer := WithActor(ctx, Actor{Kind: "user", Name: "bob", Role: store.RoleMember, Grants: map[string][]string{blog.ID: {string(PermView)}}})
	if tags, _ := c.ListTags(viewer); len(tags) != 2 || tags[0].Name != "blog" || tags[1].Projects != 1 {
		t.Errorf("viewer's tags = %+v", tags)
	}
	if _, err := c.SetTagColor(viewer, "prod", "red"); !errors.Is(err, ErrForbidden) {
		t.Errorf("viewer colors a tag: %v", err)
	}
	if _, err := c.SetTagColor(member, "blog", "red"); !errors.Is(err, ErrForbidden) {
		t.Errorf("member colors a tag of a project they only see: %v", err)
	}
	if tag, err := c.SetTagColor(member, " Front ", "red"); err != nil || tag != (Tag{Name: "front", Color: "red", Custom: true, Services: 1}) {
		t.Errorf("member colors a service's tag = %+v, %v", tag, err)
	}
	if _, err := c.SetTagColor(admin, "front", "chartreuse"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown color: %v", err)
	}
	if tag, err := c.SetTagColor(admin, "front", ""); err != nil || tag.Color != AutoTagColor("front") || tag.Custom {
		t.Errorf("color reset = %+v, %v", tag, err)
	}

	// A git-linked project's file owns its services' tags.
	if _, err := c.store.SaveProjectGit(ctx, store.ProjectGit{ProjectID: shop.ID, RepoURL: "https://example.com/shop.git", Branch: "main", Path: "compose.yaml"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetServiceTags(admin, web.ID, []string{"x"}); !errors.Is(err, ErrGitManaged) {
		t.Errorf("SetServiceTags in a git project: %v", err)
	}
	if _, err := c.SetProjectTags(admin, shop.ID, nil); err != nil {
		t.Errorf("SetProjectTags in a git project: %v", err)
	}
}
