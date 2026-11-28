package analytics

import (
	"crypto/rand"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Kinds of Tracker.
const (
	// KindPixel is a 1x1 GIF at /p/<slug>.gif: each load is a view.
	KindPixel = "pixel"
	// KindLink redirects /l/<slug> to its target: each visit is a click.
	KindLink = "link"
)

// Tracker is a pixel or a tracked link.
type Tracker struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Target    string `json:"target,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	// Hits counts its views or clicks of the last 30 days.
	Hits int `json:"hits" bun:"-"`
	// URL is set by the manager, which knows the analytics domain.
	URL string `json:"url,omitempty" bun:"-"`
}

// Host is the pseudo-host its hits are recorded under.
func (t Tracker) Host() string { return t.Kind + ":" + t.ID }

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func randomSlug() string {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789" // no 0/o, 1/l
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// Check validates t, giving a pixel no target and a missing slug a random one.
func (t *Tracker) Check() error {
	t.Name, t.Slug, t.Target = strings.TrimSpace(t.Name), strings.ToLower(strings.TrimSpace(t.Slug)), strings.TrimSpace(t.Target)
	if t.Kind != KindPixel && t.Kind != KindLink {
		return fmt.Errorf("%w: kind is pixel or link", ErrInvalid)
	}
	if t.Name == "" || len(t.Name) > 100 {
		return fmt.Errorf("%w: a name of 1 to 100 characters", ErrInvalid)
	}
	if t.Slug == "" {
		t.Slug = randomSlug()
	}
	if !slugRe.MatchString(t.Slug) {
		return fmt.Errorf("%w: slug %q: lowercase letters, digits and -, up to 63", ErrInvalid, t.Slug)
	}
	if t.Kind == KindPixel {
		t.Target = ""
		return nil
	}
	u, err := url.Parse(t.Target)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(t.Target) > 2048 {
		return fmt.Errorf("%w: a link's target is an http(s) URL", ErrInvalid)
	}
	return nil
}
