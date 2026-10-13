package upgrade

import (
	"os"
	"slices"
	"strings"
	"testing"
)

const sample = `# Upgrade notices

Intro text, ignored.

## Unreleased
- info: next one

## 0.29.0
- warning: Traefik restarts once on every server:
  apps are unreachable for a few seconds.
- info: Backups move to a new folder.

## 0.28.0
- warning: Postgres images move to 18.
`

func TestParse(t *testing.T) {
	ns, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	want := []Notice{
		{Unreleased, Info, "next one"},
		{"0.29.0", Warning, "Traefik restarts once on every server: apps are unreachable for a few seconds."},
		{"0.29.0", Info, "Backups move to a new folder."},
		{"0.28.0", Warning, "Postgres images move to 18."},
	}
	if !slices.Equal(ns, want) {
		t.Errorf("got %+v", ns)
	}
	for _, bad := range []string{"## v1.0\n", "- warning: before a version\n", "## 1.0.0\n- caution: unknown level\n"} {
		if _, err := Parse(strings.NewReader(bad)); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestBetween(t *testing.T) {
	ns, _ := Parse(strings.NewReader(sample))
	got := Between(ns, "0.27.3", "0.29.0")
	if len(got) != 3 || got[0].Version != "0.29.0" || got[2].Version != "0.28.0" {
		t.Errorf("0.27.3 → 0.29.0: %+v (skipped versions count)", got)
	}
	if got := Between(ns, "0.28.0", "0.28.5"); len(got) != 0 {
		t.Errorf("already past 0.28.0: %+v", got)
	}
}

func TestReleaseNotes(t *testing.T) {
	ns, _ := Parse(strings.NewReader(sample))
	got := ReleaseNotes(ns, "0.29.0")
	if !strings.HasPrefix(got, "## ⚠️ Before upgrading\n") || !strings.Contains(got, "- ⚠️ Traefik") || !strings.Contains(got, "- ℹ️ Backups") {
		t.Errorf("notes:\n%s", got)
	}
	if ReleaseNotes(ns, "0.27.0") != "" {
		t.Error("a release without notices adds nothing")
	}
}

// The repository's own file must parse, or the release fails.
func TestRepositoryFile(t *testing.T) {
	f, err := os.Open("../../UPGRADE.md")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := Parse(f); err != nil {
		t.Fatal(err)
	}
}
