package identity

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/secrets"
)

// secretListings caches what providers list for the env picker (names and
// references, never values), so browsing doesn't run the CLI every time.
type secretListings struct {
	mu sync.Mutex
	m  map[string]secretListing // connection ID + "\x00" + vault ("" for vaults)
}

type secretListing struct {
	at  time.Time
	val any
}

func (l *secretListings) get(id, vault string) (any, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.m[id+"\x00"+vault]
	if !ok || time.Since(e.at) > secretHelperIdle {
		return nil, false
	}
	return e.val, true
}

func (l *secretListings) put(id, vault string, val any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]secretListing{}
	}
	l.m[id+"\x00"+vault] = secretListing{at: time.Now(), val: val}
}

// forget drops a connection's listings, after it logs in or out.
func (l *secretListings) forget(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k := range l.m {
		if strings.HasPrefix(k, id+"\x00") {
			delete(l.m, k)
		}
	}
}

func (c *Identity) secretBrowser(ctx context.Context, id string) (domain.SecretConnection, secrets.Provider, secrets.Browser, error) {
	conn, p, err := c.secretConnection(ctx, id)
	if err != nil {
		return conn, nil, nil, err
	}
	b, ok := p.(secrets.Browser)
	if !ok {
		return conn, nil, nil, fmt.Errorf("%w: %s can't list its items", domain.ErrInvalid, c.secretConnectionView(conn).ProviderName)
	}
	return conn, p, b, nil
}

// browseSecrets runs list, logging in again with the stored token and
// retrying once if it fails, as resolveSecrets does: the session may have
// expired since the last call.
func browseSecrets[T any](ctx context.Context, conn domain.SecretConnection, p secrets.Provider, list func() (T, error)) (T, error) {
	v, err := list()
	if err == nil {
		return v, nil
	}
	if lerr := p.Connect(ctx, conn.Token); lerr != nil {
		return v, fmt.Errorf("%w: %s (logging in again: %s)", domain.ErrInvalid, err.Error(), lerr.Error())
	}
	if v, err = list(); err != nil {
		return v, fmt.Errorf("%w: %s", domain.ErrInvalid, err.Error())
	}
	return v, nil
}

// SecretVaults lists the vaults the connection's token can read, from a
// listing of the last few minutes unless refresh.
func (c *Identity) SecretVaults(ctx context.Context, id string, refresh bool) ([]string, error) {
	conn, p, b, err := c.secretBrowser(ctx, id)
	if err != nil {
		return nil, err
	}
	if v, ok := c.secretListings.get(id, ""); ok && !refresh {
		return v.([]string), nil
	}
	vs, err := browseSecrets(ctx, conn, p, func() ([]string, error) { return b.Vaults(ctx) })
	if err != nil {
		return nil, err
	}
	c.secretListings.put(id, "", vs)
	return vs, nil
}

// SecretItems lists a vault's items with the fields env can reference
// through the connection, from a listing of the last few minutes unless
// refresh.
func (c *Identity) SecretItems(ctx context.Context, id, vault string, refresh bool) ([]secrets.Item, error) {
	if vault == "" {
		return nil, fmt.Errorf("%w: vault is required", domain.ErrInvalid)
	}
	conn, p, b, err := c.secretBrowser(ctx, id)
	if err != nil {
		return nil, err
	}
	if v, ok := c.secretListings.get(id, vault); ok && !refresh {
		return v.([]secrets.Item), nil
	}
	items, err := browseSecrets(ctx, conn, p, func() ([]secrets.Item, error) { return b.Items(ctx, vault) })
	if err != nil {
		return nil, err
	}
	if scheme := c.secretConnectionView(conn).Scheme; conn.Name != "" {
		for i := range items {
			for j, f := range items[i].Fields {
				_, rest, _ := strings.Cut(f.Ref, "://")
				items[i].Fields[j].Ref = scheme + "://" + rest
			}
		}
	}
	c.secretListings.put(id, vault, items)
	return items, nil
}
