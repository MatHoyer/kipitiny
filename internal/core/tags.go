package core

import (
	"context"
	"fmt"
	"hash/fnv"
	"regexp"
	"slices"
	"strings"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// Tags group projects and services in the UI. They are free-form names;
// a tag's color is the one a user picked, else one derived from its name.

const (
	maxTags   = 10
	maxTagLen = 32
)

var tagRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ._/:-]*$`)

// TagColors is the palette tags take their color from, named as the UI's
// color scale so both themes have a shade of each.
var TagColors = []string{
	"red", "orange", "amber", "yellow", "lime", "green", "emerald", "teal", "cyan",
	"sky", "blue", "indigo", "violet", "purple", "fuchsia", "pink", "rose", "gray",
}

// Tag is a tag in use, with its color and how many projects and services
// the caller sees carry it.
type Tag struct {
	Name  string `json:"name"`
	Color string `json:"color"`
	// Custom is set when a user picked the color.
	Custom   bool `json:"custom"`
	Projects int  `json:"projects"`
	Services int  `json:"services"`
}

// AutoTagColor is the color of a tag nobody picked one for. The UI derives
// the same one (web/src/lib/tags.ts): FNV-1a of the name over the palette
// without gray.
func AutoTagColor(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	return TagColors[h.Sum32()%uint32(len(TagColors)-1)]
}

// normalizeTag lowercases a tag and collapses its spaces.
func normalizeTag(t string) string {
	return strings.ToLower(strings.Join(strings.Fields(t), " "))
}

// normalizeTags returns the valid, distinct tags, sorted.
func normalizeTags(tags []string) ([]string, error) {
	out := []string{}
	for _, t := range tags {
		t = normalizeTag(t)
		if t == "" || slices.Contains(out, t) {
			continue
		}
		if len([]rune(t)) > maxTagLen || !tagRe.MatchString(t) {
			return nil, fmt.Errorf("%w: tag %q must be up to %d letters, digits, spaces and . _ / : -", ErrInvalid, t, maxTagLen)
		}
		out = append(out, t)
	}
	if len(out) > maxTags {
		return nil, fmt.Errorf("%w: up to %d tags", ErrInvalid, maxTags)
	}
	slices.Sort(out)
	return out, nil
}

// SetProjectTags replaces a project's tags.
func (c *Core) SetProjectTags(ctx context.Context, projectID string, tags []string) (store.Project, error) {
	tags, err := normalizeTags(tags)
	if err != nil {
		return store.Project{}, err
	}
	if err := c.store.SetProjectTags(ctx, projectID, tags); err != nil {
		return store.Project{}, err
	}
	return c.GetProject(ctx, projectID)
}

// SetServiceTags replaces a service's tags. A git-linked project's compose
// file owns its services' tags (x-kipitiny.tags).
func (c *Core) SetServiceTags(ctx context.Context, serviceID string, tags []string) (ServiceView, error) {
	tags, err := normalizeTags(tags)
	if err != nil {
		return ServiceView{}, err
	}
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return ServiceView{}, err
	}
	if err := c.checkGitOwned(ctx, svc.ProjectID); err != nil {
		return ServiceView{}, err
	}
	if err := c.store.SetServiceTags(ctx, svc.ID, tags); err != nil {
		return ServiceView{}, err
	}
	svc.Tags = tags
	return c.view(ctx, svc)
}

// ListTags returns the tags of the projects and services the caller sees,
// by name.
func (c *Core) ListTags(ctx context.Context) ([]Tag, error) {
	byName, err := c.tagUse(ctx, func(string) bool { return true })
	if err != nil {
		return nil, err
	}
	colors, err := c.store.ListTagColors(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Tag, 0, len(byName))
	for name, t := range byName {
		t.Name, t.Color = name, AutoTagColor(name)
		if i := slices.IndexFunc(colors, func(tc store.TagColor) bool { return tc.Name == name }); i >= 0 {
			t.Color, t.Custom = colors[i].Color, true
		}
		out = append(out, *t)
	}
	slices.SortFunc(out, func(a, b Tag) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// SetTagColor sets a tag's color, one of TagColors; empty goes back to
// the one derived from its name. Admins may color any tag, members those
// on a project they may change the settings of.
func (c *Core) SetTagColor(ctx context.Context, name, color string) (Tag, error) {
	name = normalizeTag(name)
	if color != "" && !slices.Contains(TagColors, color) {
		return Tag{}, fmt.Errorf("%w: color must be one of %s", ErrInvalid, strings.Join(TagColors, ", "))
	}
	a, ok := ActorFrom(ctx)
	if ok && !a.Admin() {
		used, err := c.tagUse(ctx, func(projectID string) bool { return a.Can(projectID, PermSettings) })
		if err != nil {
			return Tag{}, err
		}
		if used[name] == nil {
			return Tag{}, fmt.Errorf("%w: needs the settings permission on a project using the tag", ErrForbidden)
		}
	}
	if err := c.store.SetTagColor(ctx, name, color); err != nil {
		return Tag{}, err
	}
	tags, err := c.ListTags(ctx)
	if err != nil {
		return Tag{}, err
	}
	if i := slices.IndexFunc(tags, func(t Tag) bool { return t.Name == name }); i >= 0 {
		return tags[i], nil
	}
	t := Tag{Name: name, Color: color, Custom: color != ""}
	if color == "" {
		t.Color = AutoTagColor(name)
	}
	return t, nil
}

// tagUse counts the tags of the visible projects that pass keep, and of
// their services.
func (c *Core) tagUse(ctx context.Context, keep func(projectID string) bool) (map[string]*Tag, error) {
	ps, err := c.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return nil, err
	}
	ok := func(projectID string) bool { return visible(ctx, projectID) && keep(projectID) }
	byName := map[string]*Tag{}
	get := func(name string) *Tag {
		if byName[name] == nil {
			byName[name] = &Tag{}
		}
		return byName[name]
	}
	for _, p := range ps {
		if ok(p.ID) {
			for _, t := range p.Tags {
				get(t).Projects++
			}
		}
	}
	for _, s := range svcs {
		if ok(s.ProjectID) {
			for _, t := range s.Tags {
				get(t).Services++
			}
		}
	}
	return byName, nil
}
