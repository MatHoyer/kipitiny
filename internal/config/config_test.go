package config

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestPrefixes(t *testing.T) {
	got := prefixes(" 10.0.0.0/8, 192.168.1.4 ,nope, 172.16.5.9/12,, ::1")
	var s []string
	for _, p := range got {
		s = append(s, p.String())
	}
	want := []string{"10.0.0.0/8", "192.168.1.4/32", "172.16.0.0/12", "::1/128"}
	if !slices.Equal(s, want) {
		t.Errorf("prefixes = %v, want %v", s, want)
	}
}

// Traefik and cloudflared are pulled only when missing and recreated only
// when their image string changes: a floating tag like traefik:v3.7 leaves
// running servers on whatever patch they first pulled, security fixes
// included. Defaults must name an exact version.
func TestDefaultProxyImagesPinned(t *testing.T) {
	t.Setenv("KIPITINY_TRAEFIK_IMAGE", "")
	t.Setenv("KIPITINY_CLOUDFLARED_IMAGE", "")
	cfg := Load()
	for name, img := range map[string]string{"traefik": cfg.Traefik.Image, "cloudflared": cfg.Tunnel.Image} {
		_, tag, _ := strings.Cut(img, ":")
		if !pinnedTag.MatchString(tag) {
			t.Errorf("default %s image %q is not pinned to an exact version", name, img)
		}
	}
}

var pinnedTag = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)
