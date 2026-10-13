// Command notes prints UPGRADE.md's notices for the release workflow:
// `label` the image label's JSON (released versions), `release X.Y.Z` the
// Markdown heading that release's notes.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/MatHoyer/kipitiny/internal/upgrade"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "notes:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	f, err := os.Open("UPGRADE.md")
	if err != nil {
		return err
	}
	defer f.Close()
	ns, err := upgrade.Parse(f)
	if err != nil {
		return fmt.Errorf("UPGRADE.md: %w", err)
	}
	switch {
	case len(args) == 1 && args[0] == "label":
		released := []upgrade.Notice{}
		for _, n := range ns {
			if n.Version != upgrade.Unreleased {
				released = append(released, n)
			}
		}
		b, err := json.Marshal(released)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
	case len(args) == 2 && args[0] == "release":
		for _, n := range ns {
			if n.Version == upgrade.Unreleased {
				return fmt.Errorf("UPGRADE.md still has an %s section: name it %s", upgrade.Unreleased, args[1])
			}
		}
		fmt.Print(upgrade.ReleaseNotes(ns, args[1]))
	default:
		return fmt.Errorf("usage: notes label | notes release X.Y.Z")
	}
	return nil
}
