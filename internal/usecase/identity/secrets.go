package identity

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/secrets"
	"github.com/kipit-labs/kipitiny/internal/progress"
	"github.com/kipit-labs/kipitiny/internal/secrets/protonpass"
)

// SecretProviderView is a password manager this build offers, in Settings.
type SecretProviderView struct {
	secrets.Info
	// Available is false when it can't run here (its CLI is missing).
	Available bool `json:"available"`
}

// SecretConnectionView is an account a password manager is logged into.
type SecretConnectionView struct {
	domain.SecretConnection
	// Scheme is what its references start with: the provider's scheme for
	// the default connection (pass), with "+name" for the others (pass+work).
	Scheme       string `json:"scheme"`
	ProviderName string `json:"providerName"`
}

// secretHelperIdle is how long a password manager's helper container stays
// after its last call, and its listings stay cached.
const secretHelperIdle = 5 * time.Minute

// SecretKind is a password manager this build offers. Each connection gets
// its own provider from open (conn is the connection's name): its own
// session and helper.
type SecretKind struct {
	info      secrets.Info
	available bool
	open      func(conn string) secrets.Provider
}

func NewSecretKind(open func(conn string) secrets.Provider) SecretKind {
	p := open("")
	return SecretKind{info: p.Info(), available: p.Available(), open: open}
}

// SecretKinds are the password managers this build offers. Their CLIs run in
// helper containers on the manager's own Docker (none without one, in tests).
func SecretKinds(cfg config.Config, local domain.ContainerRuntime) []SecretKind {
	if local == nil {
		return nil
	}
	return []SecretKind{
		NewSecretKind(func(conn string) secrets.Provider {
			return protonpass.New(protonpass.NewContainerRunner(local, cfg.ProtonPass.Image, conn, secretHelperIdle))
		}),
	}
}

// connNameRe is a connection's name: it goes into references and container
// names.
var connNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

// connScheme is the reference scheme of the connection named name.
func connScheme(scheme, name string) string {
	if name == "" {
		return scheme
	}
	return scheme + "+" + name
}

// secretProvider returns the provider of the connection named name to kind,
// opening it on first use.
func (c *Identity) secretProvider(kind SecretKind, name string) secrets.Provider {
	c.secretsMu.Lock()
	defer c.secretsMu.Unlock()
	key := kind.info.ID + "\x00" + name
	if p, ok := c.secrets[key]; ok {
		return p
	}
	if c.secrets == nil {
		c.secrets = map[string]secrets.Provider{}
	}
	p := kind.open(name)
	c.secrets[key] = p
	return p
}

// dropSecretProvider forgets the provider of a removed connection.
func (c *Identity) dropSecretProvider(kind SecretKind, name string) {
	c.secretsMu.Lock()
	defer c.secretsMu.Unlock()
	delete(c.secrets, kind.info.ID+"\x00"+name)
}

// CloseSecretProviders removes the helper containers of the providers in use.
func (c *Identity) CloseSecretProviders(ctx context.Context) error {
	c.secretsMu.Lock()
	defer c.secretsMu.Unlock()
	var errs []error
	for _, p := range c.secrets {
		if cl, ok := p.(interface{ Close(context.Context) error }); ok {
			errs = append(errs, cl.Close(ctx))
		}
	}
	return errors.Join(errs...)
}

// CleanupSecretHelpers removes the helper containers a crash left, of any
// connection.
func (c *Identity) CleanupSecretHelpers(ctx context.Context, dk domain.ContainerRuntime) error {
	cts, err := dk.ListContainers(ctx, map[string]string{domain.LabelComponent: protonpass.Component})
	if err != nil {
		return err
	}
	for _, ct := range cts {
		if err := dk.RemoveContainer(ctx, ct.ID, 0); err != nil {
			return err
		}
	}
	return nil
}

func (c *Identity) secretKind(id string) (SecretKind, bool) {
	for _, k := range c.secretKinds {
		if k.info.ID == id {
			return k, true
		}
	}
	return SecretKind{}, false
}

func (c *Identity) SecretProviders() []SecretProviderView {
	out := make([]SecretProviderView, len(c.secretKinds))
	for i, k := range c.secretKinds {
		out[i] = SecretProviderView{Info: k.info, Available: k.available}
	}
	return out
}

func (c *Identity) secretConnectionView(conn domain.SecretConnection) SecretConnectionView {
	v := SecretConnectionView{SecretConnection: conn, ProviderName: conn.Kind, Scheme: conn.Name}
	if k, ok := c.secretKind(conn.Kind); ok {
		v.ProviderName, v.Scheme = k.info.Name, connScheme(k.info.Scheme, conn.Name)
	}
	return v
}

func (c *Identity) SecretConnections(ctx context.Context) ([]SecretConnectionView, error) {
	conns, err := c.store.ListSecretConnections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SecretConnectionView, len(conns))
	for i, conn := range conns {
		out[i] = c.secretConnectionView(conn)
	}
	return out, nil
}

// secretConnection returns a connection with its provider.
func (c *Identity) secretConnection(ctx context.Context, id string) (domain.SecretConnection, secrets.Provider, error) {
	conn, err := c.store.GetSecretConnection(ctx, id)
	if err != nil {
		return conn, nil, err
	}
	k, ok := c.secretKind(conn.Kind)
	if !ok {
		return conn, nil, fmt.Errorf("%w: this build has no %s password manager", domain.ErrInvalid, conn.Kind)
	}
	return conn, c.secretProvider(k, conn.Name), nil
}

// CreateSecretConnection logs into a password manager of kind with token
// and keeps the token. name is "" for the default connection of the kind.
func (c *Identity) CreateSecretConnection(ctx context.Context, kind, name, token string) (SecretConnectionView, error) {
	k, ok := c.secretKind(kind)
	if !ok {
		return SecretConnectionView{}, domain.ErrNotFound
	}
	if !k.available {
		return SecretConnectionView{}, fmt.Errorf("%w: %s isn't installed on the manager", domain.ErrInvalid, k.info.Name)
	}
	name = strings.TrimSpace(name)
	if name != "" && !connNameRe.MatchString(name) {
		return SecretConnectionView{}, fmt.Errorf("%w: name must be lowercase letters, digits and dashes, starting with a letter", domain.ErrInvalid)
	}
	conns, err := c.store.ListSecretConnections(ctx)
	if err != nil {
		return SecretConnectionView{}, err
	}
	for _, conn := range conns {
		if conn.Kind == kind && conn.Name == name {
			if name == "" {
				return SecretConnectionView{}, fmt.Errorf("%w: %s already has a default connection: name this one", domain.ErrInvalid, k.info.Name)
			}
			return SecretConnectionView{}, fmt.Errorf("%w: %s already has a connection named %s", domain.ErrInvalid, k.info.Name, name)
		}
	}
	token = strings.TrimSpace(token)
	if err := c.secretProvider(k, name).Connect(ctx, token); err != nil {
		return SecretConnectionView{}, fmt.Errorf("%w: %s", domain.ErrInvalid, err.Error())
	}
	conn, err := c.store.CreateSecretConnection(ctx, domain.SecretConnection{Kind: kind, Name: name, Token: token})
	if err != nil {
		return SecretConnectionView{}, err
	}
	return c.secretConnectionView(conn), nil
}

// UpdateSecretConnection logs a connection in again with a new token.
func (c *Identity) UpdateSecretConnection(ctx context.Context, id, token string) (SecretConnectionView, error) {
	conn, p, err := c.secretConnection(ctx, id)
	if err != nil {
		return SecretConnectionView{}, err
	}
	token = strings.TrimSpace(token)
	if err := p.Connect(ctx, token); err != nil {
		return SecretConnectionView{}, fmt.Errorf("%w: %s", domain.ErrInvalid, err.Error())
	}
	if err := c.store.SetSecretConnectionToken(ctx, id, token); err != nil {
		return SecretConnectionView{}, err
	}
	c.secretListings.forget(id)
	conn.Token = token
	return c.secretConnectionView(conn), nil
}

// TestSecretConnection checks the session still works, logging in again
// with the saved token if it doesn't, as a deploy would.
func (c *Identity) TestSecretConnection(ctx context.Context, id string) error {
	conn, p, err := c.secretConnection(ctx, id)
	if err != nil {
		return err
	}
	name := c.secretConnectionView(conn).label()
	// Long enough to pull a helper image on first use.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if w, ok := p.(interface{ Warm(context.Context) error }); ok {
		if err := w.Warm(ctx); err != nil {
			return fmt.Errorf("%w: %s: %s", domain.ErrInvalid, name, err.Error())
		}
	}
	b, ok := p.(secrets.Browser)
	if ok {
		progress.Report(ctx, secrets.StepSession)
		if _, err := b.Vaults(ctx); err == nil {
			return nil
		}
	}
	if err := p.Connect(ctx, conn.Token); err != nil {
		return fmt.Errorf("%w: %s refused the saved token: %s", domain.ErrInvalid, name, err.Error())
	}
	if ok {
		progress.Report(ctx, secrets.StepVaults)
		if _, err := b.Vaults(ctx); err != nil {
			return fmt.Errorf("%w: %s: %s", domain.ErrInvalid, name, err.Error())
		}
	}
	return nil
}

// DeleteSecretConnection logs out and forgets the connection. Services that
// reference it can't deploy until it's connected again.
func (c *Identity) DeleteSecretConnection(ctx context.Context, id string) error {
	conn, p, err := c.secretConnection(ctx, id)
	if err != nil && !errors.Is(err, domain.ErrInvalid) {
		return err
	}
	if p != nil {
		if err := p.Disconnect(ctx); err != nil {
			c.log.Warn("secret provider logout failed", "provider", conn.Kind, "connection", conn.Name, "err", err)
		}
		k, _ := c.secretKind(conn.Kind)
		c.dropSecretProvider(k, conn.Name)
	}
	c.secretListings.forget(id)
	return c.store.DeleteSecretConnection(ctx, id)
}

// label names a connection in messages: "Proton Pass" for the default one,
// "Proton Pass (work)" for the others.
func (v SecretConnectionView) label() string {
	if v.Name == "" {
		return v.ProviderName
	}
	return v.ProviderName + " (" + v.Name + ")"
}

// connectionFor returns the kind of password manager a reference scheme
// names (pass, pass+work) and its connection, if any.
func (c *Identity) connectionFor(ctx context.Context, scheme string) (SecretKind, *domain.SecretConnection, error) {
	base, name, _ := strings.Cut(scheme, "+")
	var kind SecretKind
	found := false
	for _, k := range c.secretKinds {
		if k.info.Scheme == base {
			kind, found = k, true
		}
	}
	if !found {
		return kind, nil, fmt.Errorf("no password manager handles %s://", scheme)
	}
	conns, err := c.store.ListSecretConnections(ctx)
	if err != nil {
		return kind, nil, err
	}
	for _, conn := range conns {
		if conn.Kind == kind.info.ID && conn.Name == name {
			return kind, &conn, nil
		}
	}
	return kind, nil, nil
}

// CheckSecretScheme refuses a reference no password manager handles, or
// that names a connection that doesn't exist. The default connection may
// come later: its references were accepted before it was connected.
func (c *Identity) CheckSecretScheme(ctx context.Context, ref string) error {
	scheme := secrets.SchemeOf(ref)
	_, conn, err := c.connectionFor(ctx, scheme)
	if err != nil {
		return err
	}
	if conn == nil && strings.Contains(scheme, "+") {
		return fmt.Errorf("no password manager connection is named %s", scheme[strings.Index(scheme, "+")+1:])
	}
	return nil
}

// baseRef is ref with the provider's own scheme, as its CLI reads it:
// pass+work://V/I/f becomes pass://V/I/f.
func baseRef(ref string) string {
	scheme, rest, _ := strings.Cut(ref, "://")
	base, _, _ := strings.Cut(scheme, "+")
	return base + "://" + rest
}

// ResolveSecrets returns the value of each reference from its connection. A
// failure is retried once after logging in again with the stored token, in
// case the session expired or the data dir was restored without it.
func (c *Identity) ResolveSecrets(ctx context.Context, refs []string) (map[string]string, error) {
	byScheme := map[string][]string{}
	for _, r := range refs {
		s := secrets.SchemeOf(r)
		byScheme[s] = append(byScheme[s], r)
	}
	out := make(map[string]string, len(refs))
	for scheme, rs := range byScheme {
		k, conn, err := c.connectionFor(ctx, scheme)
		if err != nil {
			return nil, err
		}
		if conn == nil {
			if _, name, ok := strings.Cut(scheme, "+"); ok {
				return nil, fmt.Errorf("%s has no connection named %s (Integrations › Password managers)", k.info.Name, name)
			}
			return nil, fmt.Errorf("%s isn't connected (Integrations › Password managers)", k.info.Name)
		}
		name := c.secretConnectionView(*conn).label()
		p := c.secretProvider(k, conn.Name)
		base := make([]string, len(rs))
		for i, r := range rs {
			base[i] = baseRef(r)
		}
		vals, err := p.Resolve(ctx, base)
		if err != nil {
			if lerr := p.Connect(ctx, conn.Token); lerr != nil {
				return nil, fmt.Errorf("%s: %w (logging in again: %w)", name, err, lerr)
			}
			if vals, err = p.Resolve(ctx, base); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
		}
		for i, r := range rs {
			out[r] = vals[base[i]]
		}
	}
	return out, nil
}

// SecretMask replaces env values in read responses. Sending it back in an
// update keeps the stored value.
const SecretMask = "********"
