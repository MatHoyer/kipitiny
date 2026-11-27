package deploy

import (
	"context"
	"errors"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestResolveSecrets(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	connect := useSecretProvider(t, c, &fakeProvider{})

	if _, err := c.ResolveSecrets(ctx, []string{"fake://a"}); err == nil {
		t.Errorf("not connected: %v", err)
	}
	if _, err := c.ResolveSecrets(ctx, []string{"op://x/y/z"}); err == nil {
		t.Errorf("unknown scheme: %v", err)
	}
	connect("")

	if err := c.checkSecretSchemes(ctx, map[string]string{"A": "{{ fake://x }}"}); err != nil {
		t.Error(err)
	}
	for _, ref := range []string{"op://x/y/z", "fake+work://x"} {
		if err := c.checkSecretSchemes(ctx, map[string]string{"A": "{{ " + ref + " }}"}); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s accepted: %v", ref, err)
		}
	}
	connect("work")
	if err := c.checkSecretSchemes(ctx, map[string]string{"A": "{{ fake+work://x }}"}); err != nil {
		t.Error(err)
	}
}
