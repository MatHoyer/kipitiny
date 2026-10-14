package core

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

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
