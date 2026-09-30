package core

import "testing"

func TestNewer(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1.0.1", "1.0.0", true},
		{"1.10.0", "1.9.9", true}, // numeric, not lexical
		{"2.0.0", "1.99.99", true},
		{"1.0.0", "1.0.0", false},
		{"0.9.0", "1.0.0", false},
		{"1.0.0", "dev", false}, // development builds are never offered updates
		{"v1.0.1", "1.0.0", false},
		{"", "1.0.0", false},
	}
	for _, tt := range tests {
		if got := newer(tt.a, tt.b); got != tt.want {
			t.Errorf("newer(%q, %q) = %v", tt.a, tt.b, got)
		}
	}
}

func TestLatestVersion(t *testing.T) {
	tags := []string{"latest", "1.2.0", "1.10.0", "sha-abc", "1.9.3", "2.0.0-rc1", "v3.0.0"}
	if got := latestVersion(tags); got != "1.10.0" {
		t.Errorf("latest = %q", got)
	}
	if got := latestVersion([]string{"latest"}); got != "" {
		t.Errorf("no version tags: %q", got)
	}
}
