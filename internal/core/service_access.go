package core

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

// checkServiceChange checks what a change from old to svc needs beyond the
// settings permission: env and domains have their own, and host access
// (the host's network or Docker socket) is root on the host, so only
// admins give it.
func checkServiceChange(ctx context.Context, old, svc domain.Service) error {
	a, ok := identity.ActorFrom(ctx)
	if !ok || a.Admin() {
		return nil
	}
	if (svc.HostNetwork && !old.HostNetwork) || (svc.DockerSocket != "" && svc.DockerSocket != old.DockerSocket) {
		return fmt.Errorf("%w: only admins give an app the host's network or Docker socket", identity.ErrForbidden)
	}
	if svc.Runtime.HostAccess() && !reflect.DeepEqual(runtimeHostAccess(svc.Runtime), runtimeHostAccess(old.Runtime)) {
		return fmt.Errorf("%w: only admins give an app capabilities, devices, privileged mode, security options or host paths", identity.ErrForbidden)
	}
	if hv := volumesHostAccess(svc.Volumes); len(hv) > 0 && !reflect.DeepEqual(hv, volumesHostAccess(old.Volumes)) {
		return fmt.Errorf("%w: only admins mount external volumes or set volume driver options", identity.ErrForbidden)
	}
	for _, v := range svc.Volumes {
		if i := slices.IndexFunc(old.Volumes, func(o domain.Volume) bool { return o.Name == v.Name }); v.HostAccess() && !v.ReadOnly && i >= 0 && old.Volumes[i].ReadOnly {
			return fmt.Errorf("%w: only admins make volume %s writable: it reaches outside the project", identity.ErrForbidden, v.Name)
		}
	}
	// Secret files resolve references like env vars do.
	if env, oldEnv := refEnv(svc), refEnv(old); !maps.Equal(env, oldEnv) || !slices.Equal(svc.Secrets, old.Secrets) {
		if err := identity.Authorize(ctx, svc.ProjectID, identity.PermEnv); err != nil {
			return err
		}
		if err := checkSecretRefs(ctx, oldEnv, env); err != nil {
			return err
		}
	}
	if svc.Domain != old.Domain || !reflect.DeepEqual(svc.Middlewares, old.Middlewares) || !network.SameRoutes(svc.Routes, old.Routes) {
		if err := identity.Authorize(ctx, svc.ProjectID, identity.PermDomains); err != nil {
			return err
		}
		if err := checkSecretRefs(ctx, basicAuthRefs(old.Middlewares), basicAuthRefs(svc.Middlewares)); err != nil {
			return err
		}
	}
	return nil
}

// basicAuthRefs holds m's basic auth password references, in the shape
// checkSecretRefs reads.
func basicAuthRefs(m domain.Middlewares) map[string]string {
	out := map[string]string{}
	for _, u := range m.BasicAuth {
		if u.Ref != "" {
			out[u.Name] = u.Ref
		}
	}
	return out
}

// checkSecretRefs fails when a member references a password manager item
// that wasn't referenced before without the secret-providers feature: the
// app would read it.
func checkSecretRefs(ctx context.Context, old, env map[string]string) error {
	a, ok := identity.ActorFrom(ctx)
	if !ok || a.Admin() {
		return nil
	}
	had := secretRefs(old, nil)
	for _, ref := range secretRefs(env, nil) {
		if !slices.Contains(had, ref) {
			return identity.RequireFeature(ctx, identity.FeatureSecretProviders)
		}
	}
	return nil
}
