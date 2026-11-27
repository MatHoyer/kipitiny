package core

import (
	"strings"
	"testing"
)

// A generated database password never starts with "-" (1 in 64 base64url
// tokens do): tools would read it as an option.
func TestRandomPasswordNoLeadingDash(t *testing.T) {
	for range 2000 {
		if p := randomPassword(); strings.HasPrefix(p, "-") || len(p) != 32 {
			t.Fatalf("password %q", p)
		}
	}
}
