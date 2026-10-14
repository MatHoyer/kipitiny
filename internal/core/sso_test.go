package core

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"golang.org/x/crypto/bcrypt"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// fakeIssuer is an OpenID provider: discovery, keys, and a token endpoint
// answering the codes set in codes with an ID token for their claims.
type fakeIssuer struct {
	*httptest.Server
	key   *rsa.PrivateKey
	mu    sync.Mutex
	codes map[string]map[string]any // code -> ID token claims besides iss, aud, exp, iat
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIssuer{key: key, codes: map[string]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.URL,
			"authorization_endpoint":                f.URL + "/authorize",
			"token_endpoint":                        f.URL + "/token",
			"jwks_uri":                              f.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		claims, ok := f.codes[r.Form.Get("code")]
		delete(f.codes, r.Form.Get("code"))
		f.mu.Unlock()
		if !ok || r.Form.Get("code_verifier") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": f.sign(t, claims)})
	})
	f.Server = httptest.NewTLSServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeIssuer) sign(t *testing.T, claims map[string]any) string {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: f.key, KeyID: "k"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	full := map[string]any{"iss": f.URL, "aud": "client", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}
	for k, v := range claims {
		full[k] = v
	}
	b, _ := json.Marshal(full)
	jws, err := signer.Sign(b)
	if err != nil {
		t.Fatal(err)
	}
	s, err := jws.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// code makes the token endpoint answer code with these claims.
func (f *fakeIssuer) code(code string, claims map[string]any) {
	f.mu.Lock()
	f.codes[code] = claims
	f.mu.Unlock()
}

func asAdmin(ctx context.Context) context.Context {
	return WithActor(ctx, Actor{Kind: "user", Name: "admin", Scope: store.ScopeAdmin, Role: store.RoleAdmin})
}

func TestSSOProviders(t *testing.T) {
	ctx := asAdmin(context.Background())
	c := newTestCore(t, config.Config{})
	idp := newFakeIssuer(t)
	c.ssoHTTP = idp.Client()

	in := SSOProviderInput{Name: " Authentik ", Issuer: idp.URL, ClientID: "client", ClientSecret: "secret"}
	for name, bad := range map[string]SSOProviderInput{
		"no name":      {Issuer: idp.URL, ClientID: "client", ClientSecret: "secret"},
		"http issuer":  {Name: "x", Issuer: "http://auth.example.com", ClientID: "client", ClientSecret: "secret"},
		"no client":    {Name: "x", Issuer: idp.URL, ClientSecret: "secret"},
		"no secret":    {Name: "x", Issuer: idp.URL, ClientID: "client"},
		"wrong issuer": {Name: "x", Issuer: idp.URL + "/other", ClientID: "client", ClientSecret: "secret"},
	} {
		if _, err := c.CreateSSOProvider(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	member := WithActor(context.Background(), Actor{Kind: "user", Scope: store.ScopeAdmin, Role: store.RoleMember})
	if _, err := c.CreateSSOProvider(member, in); !errors.Is(err, ErrForbidden) {
		t.Errorf("member: %v", err)
	}

	p, err := c.CreateSSOProvider(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Authentik" || p.ClientSecret != SecretMask {
		t.Errorf("created = %+v", p)
	}
	if _, err := c.CreateSSOProvider(ctx, in); !errors.Is(err, ErrInvalid) {
		t.Errorf("same name: %v", err)
	}

	in.Name, in.ClientSecret = "Company", SecretMask
	if _, err := c.UpdateSSOProvider(ctx, p.ID, in); err != nil {
		t.Fatal(err)
	}
	stored, _ := c.store.GetSSOProvider(ctx, p.ID)
	if stored.Name != "Company" || stored.ClientSecret != "secret" {
		t.Errorf("updated = %+v", stored)
	}
	if bs := c.SSOButtons(context.Background()); len(bs) != 1 || bs[0] != (SSOButton{ID: p.ID, Name: "Company"}) {
		t.Errorf("buttons = %+v", bs)
	}

	if err := c.DeleteSSOProvider(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if ps, _ := c.ListSSOProviders(ctx); len(ps) != 0 {
		t.Errorf("left = %+v", ps)
	}
}

// answer sets the fake issuer to answer the flow behind start with
// subject sub, and returns the flow's state.
func (f *fakeIssuer) answer(t *testing.T, start SSOStart, code, sub string) string {
	t.Helper()
	u, err := url.Parse(start.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if !strings.HasPrefix(start.URL, f.URL+"/authorize?") || q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != "https://kipitiny.example.com"+SSOCallbackPath {
		t.Fatalf("authorize URL = %s", start.URL)
	}
	f.code(code, map[string]any{"sub": sub, "email": sub + "@example.com", "nonce": q.Get("nonce")})
	return q.Get("state")
}

func TestSSOSignIn(t *testing.T) {
	ctx := asAdmin(context.Background())
	c := newTestCore(t, config.Config{})
	idp := newFakeIssuer(t)
	c.ssoHTTP = idp.Client()
	p, err := c.CreateSSOProvider(ctx, SSOProviderInput{Name: "Company", Issuer: idp.URL, ClientID: "client", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	const origin = "https://kipitiny.example.com"
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct horse"), bcrypt.MinCost)
	alice, err := c.store.CreateUser(ctx, store.User{Username: "alice", PasswordHash: string(hash), Role: store.RoleMember})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := c.store.CreateUser(ctx, store.User{Username: "bob", PasswordHash: string(hash), Role: store.RoleMember})
	if err != nil {
		t.Fatal(err)
	}
	as := func(u store.User) context.Context {
		return WithActor(context.Background(), Actor{Kind: "user", Name: u.Username, UserID: u.ID, Role: u.Role, Scope: store.ScopeAdmin})
	}

	// Nobody is linked yet.
	start, err := c.BeginSSO(context.Background(), p.ID, origin, "/projects/x")
	if err != nil {
		t.Fatal(err)
	}
	state := idp.answer(t, start, "c0", "alice-sub")
	if res, err := c.FinishSSO(context.Background(), state, state, "c0"); !errors.Is(err, ErrUnauthorized) || !strings.Contains(err.Error(), "alice-sub@example.com") || res.Purpose != ssoLogin {
		t.Fatalf("unlinked: %+v %v", res, err)
	}

	// Linking asks for the password, then links the identity.
	if _, err := c.BeginSSOConnect(as(alice), p.ID, origin, "wrong"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	start, err = c.BeginSSOConnect(as(alice), p.ID, origin, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	state = idp.answer(t, start, "c1", "alice-sub")
	if _, err := c.FinishSSO(context.Background(), state, "other", "c1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("other browser: %v", err)
	}
	if _, err := c.FinishSSO(context.Background(), state, state, "c1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("state reused: %v", err)
	}
	start, _ = c.BeginSSOConnect(as(alice), p.ID, origin, "correct horse")
	state = idp.answer(t, start, "c2", "alice-sub")
	res, err := c.FinishSSO(context.Background(), state, state, "c2")
	if err != nil || res.Purpose != ssoConnect || res.User.ID != alice.ID {
		t.Fatalf("connect: %+v %v", res, err)
	}
	acc, _ := c.Account(as(alice))
	if len(acc.SSO) != 1 || acc.SSO[0].ProviderName != "Company" || acc.SSO[0].Email != "alice-sub@example.com" {
		t.Fatalf("account = %+v", acc.SSO)
	}

	// The same identity can't be linked to bob.
	start, _ = c.BeginSSOConnect(as(bob), p.ID, origin, "correct horse")
	state = idp.answer(t, start, "c3", "alice-sub")
	if _, err := c.FinishSSO(context.Background(), state, state, "c3"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("linked twice: %v", err)
	}

	// Signing in lands where it started, with a session.
	start, _ = c.BeginSSO(context.Background(), p.ID, origin, "/projects/x")
	state = idp.answer(t, start, "c4", "alice-sub")
	res, err = c.FinishSSO(context.Background(), state, state, "c4")
	if err != nil || res.Session == "" || res.User.ID != alice.ID || res.ReturnTo != "/projects/x" {
		t.Fatalf("sign in: %+v %v", res, err)
	}
	if u, err := c.Authenticate(context.Background(), res.Session); err != nil || u.ID != alice.ID {
		t.Fatalf("session: %v", err)
	}

	// A nonce from another flow is refused.
	start, _ = c.BeginSSO(context.Background(), p.ID, origin, "/")
	u, _ := url.Parse(start.URL)
	state = u.Query().Get("state")
	idp.code("c5", map[string]any{"sub": "alice-sub", "nonce": "other"})
	if _, err := c.FinishSSO(context.Background(), state, state, "c5"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nonce: %v", err)
	}

	// With TOTP on, the provider stands in for the password only.
	if err := c.store.SetTOTP(ctx, alice.ID, newTOTPSecret(), nil); err != nil {
		t.Fatal(err)
	}
	start, _ = c.BeginSSO(context.Background(), p.ID, origin, "/")
	state = idp.answer(t, start, "c6", "alice-sub")
	res, err = c.FinishSSO(context.Background(), state, state, "c6")
	if err != nil || res.Session != "" || res.MFATicket == "" {
		t.Fatalf("with totp: %+v %v", res, err)
	}

	// Unlinked, the identity no longer signs in.
	if err := c.UnlinkSSOIdentity(as(alice), acc.SSO[0].ID); err != nil {
		t.Fatal(err)
	}
	start, _ = c.BeginSSO(context.Background(), p.ID, origin, "/")
	state = idp.answer(t, start, "c7", "alice-sub")
	if _, err := c.FinishSSO(context.Background(), state, state, "c7"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unlinked: %v", err)
	}
}

func TestLocalPath(t *testing.T) {
	for in, want := range map[string]string{"/projects/x?a=1": "/projects/x?a=1", "": "/", "//evil.com": "/", "https://evil.com": "/", "/\\evil.com": "/"} {
		if got := localPath(in); got != want {
			t.Errorf("localPath(%q) = %q", in, got)
		}
	}
}
