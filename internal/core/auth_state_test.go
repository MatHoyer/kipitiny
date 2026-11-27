package core

import (
	"context"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestAuthStateSSORequired(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	if _, err := c.store.CreateUser(ctx, domain.User{Username: "admin", Role: domain.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	if st, err := c.AuthState(ctx, ""); err != nil || st.SetupRequired || st.SSORequired {
		t.Fatalf("before: %+v %v", st, err)
	}
	if err := c.store.SetSetting(ctx, "sso_required", "true"); err != nil {
		t.Fatal(err)
	}
	if st, _ := c.AuthState(ctx, ""); !st.SSORequired || st.SSO == nil {
		t.Errorf("signed out, SSO required: %+v", st)
	}
}
