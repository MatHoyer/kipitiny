// Package upgrade reads the upgrade notices of UPGRADE.md: what a release
// does to running servers that its users should know before upgrading
// (a Traefik restart, a manual step). The release workflow puts them on the
// GitHub release and in an image label, which managers read before they
// offer the update.
package upgrade

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Label is the image label holding every release's notices, as JSON.
const Label = "io.kipitiny.upgrade-notices"

// Unreleased is the section the next release's notices are added to.
const Unreleased = "Unreleased"

type Level string

const (
	Info    Level = "info"
	Warning Level = "warning"
)

// Notice is one thing to know before upgrading to Version.
type Notice struct {
	Version string `json:"version"`
	Level   Level  `json:"level"`
	Text    string `json:"text"`
}

var (
	headingRe = regexp.MustCompile(`^##\s+(\S+)\s*$`)
	itemRe    = regexp.MustCompile(`^-\s+(info|warning):\s*(.+)$`)
	versionRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)
)

// Parse reads UPGRADE.md: "## X.Y.Z" (or "## Unreleased") sections of
// "- warning: …" or "- info: …" items; an item continues on the indented
// lines below it.
func Parse(r io.Reader) ([]Notice, error) {
	var out []Notice
	version := ""
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if m := headingRe.FindStringSubmatch(line); m != nil {
			version = m[1]
			if version != Unreleased && !versionRe.MatchString(version) {
				return nil, fmt.Errorf("line %d: %q is neither X.Y.Z nor %s", n, version, Unreleased)
			}
			continue
		}
		if m := itemRe.FindStringSubmatch(line); m != nil {
			if version == "" {
				return nil, fmt.Errorf("line %d: a notice before any ## version heading", n)
			}
			out = append(out, Notice{Version: version, Level: Level(m[1]), Text: m[2]})
			continue
		}
		if strings.HasPrefix(line, "  ") && strings.TrimSpace(line) != "" && len(out) > 0 && out[len(out)-1].Version == version {
			out[len(out)-1].Text += " " + strings.TrimSpace(line)
			continue
		}
		if strings.HasPrefix(line, "- ") && version != "" {
			return nil, fmt.Errorf("line %d: a notice starts with \"- warning:\" or \"- info:\"", n)
		}
	}
	return out, sc.Err()
}

// Between keeps the notices of the versions above from, up to to: what an
// upgrade from from to to goes through, skipped versions included.
func Between(ns []Notice, from, to string) []Notice {
	var out []Notice
	for _, n := range ns {
		if Newer(n.Version, from) && !Newer(n.Version, to) {
			out = append(out, n)
		}
	}
	return out
}

// Newer reports whether version a is strictly above b (both X.Y.Z).
func Newer(a, b string) bool {
	va, ok1 := parse(a)
	vb, ok2 := parse(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range va {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	m := versionRe.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}

// FromLabel decodes the label of an image; empty or invalid gives none.
func FromLabel(v string) []Notice {
	var ns []Notice
	if json.Unmarshal([]byte(v), &ns) != nil {
		return nil
	}
	return ns
}

// ReleaseNotes is the Markdown heading a release's notes, empty without
// notices.
func ReleaseNotes(ns []Notice, version string) string {
	var b strings.Builder
	for _, n := range ns {
		if n.Version != version {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("## ⚠️ Before upgrading\n\n")
		}
		mark := "ℹ️"
		if n.Level == Warning {
			mark = "⚠️"
		}
		fmt.Fprintf(&b, "- %s %s\n", mark, n.Text)
	}
	return b.String()
}
