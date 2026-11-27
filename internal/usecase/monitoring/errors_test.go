package monitoring

import "testing"

func TestImageRelease(t *testing.T) {
	for in, want := range map[string]string{
		"ghcr.io/org/shop:sha-a1b2c3":            "sha-a1b2c3",
		"registry.local:5000/shop:1.2.0":         "1.2.0",
		"registry.local:5000/shop":               "latest",
		"nginx":                                  "latest",
		"shop@sha256:0123456789abcdef0123":       "0123456789ab",
		"shop:2.0@sha256:0123456789abcdef0123ff": "2.0",
	} {
		if got := ImageRelease(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}
