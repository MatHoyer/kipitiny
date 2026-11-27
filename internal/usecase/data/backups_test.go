package data

import (
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestIconHint(t *testing.T) {
	for _, tc := range []struct {
		svc  domain.Service
		want string
	}{
		{domain.Service{Image: "postgres:17"}, "postgres"},
		{domain.Service{Image: "ghcr.io/n8n-io/n8n:1.0@sha256:" + strings.Repeat("0", 64)}, "n8n"},
		{domain.Service{Image: "bitnami/redis"}, "redis"},
		{domain.Service{Image: "ghcr.io/org/blog:v2", Icon: "ghost"}, "ghost"},
		{domain.Service{Image: "Not An Image"}, ""},
	} {
		if got := IconHint(tc.svc); got != tc.want {
			t.Errorf("IconHint(%+v) = %q, want %q", tc.svc, got, tc.want)
		}
	}
}
