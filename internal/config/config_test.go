package config

import (
	"slices"
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
