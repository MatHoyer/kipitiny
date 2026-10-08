package gitprovider

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// fakeForge serves handler over TLS and points the package's client at it.
func fakeForge(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	old := client
	client = srv.Client()
	t.Cleanup(func() { client = old })
	return srv.URL
}

func TestAPIURL(t *testing.T) {
	for _, tc := range []struct {
		kind       store.GitProviderKind
		base, want string
	}{
		{store.GitHub, "https://github.com", "https://api.github.com"},
		{store.GitHub, "https://ghe.example.com", "https://ghe.example.com/api/v3"},
		{store.GitLab, "https://gitlab.com", "https://gitlab.com/api/v4"},
		{store.Gitea, "https://git.example.com", "https://git.example.com/api/v1"},
	} {
		if got := APIURL(tc.kind, tc.base); got != tc.want {
			t.Errorf("%s %s: %s", tc.kind, tc.base, got)
		}
	}
}

func TestReposPaginates(t *testing.T) {
	base := fakeForge(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/user/repos" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, `{"message":"nope"}`, http.StatusUnauthorized)
			return
		}
		n := 50 // Gitea's page size
		if r.URL.Query().Get("page") == "2" {
			n = 3
		}
		repos := make([]map[string]any, n)
		for i := range repos {
			repos[i] = map[string]any{"full_name": fmt.Sprintf("me/r%d", i), "clone_url": "https://x/me.git", "default_branch": "main", "private": true}
		}
		_ = json.NewEncoder(w).Encode(repos)
	})
	repos, err := Repos(context.Background(), store.Gitea, base, "tok")
	if err != nil || len(repos) != 53 || !repos[0].Private || repos[0].DefaultBranch != "main" {
		t.Fatalf("repos = %d, %v", len(repos), err)
	}
	_, err = Repos(context.Background(), store.Gitea, base, "wrong")
	if !Unauthorized(err) || !strings.Contains(err.Error(), "nope") {
		t.Errorf("refused: %v", err)
	}
}

func TestGitLabRepos(t *testing.T) {
	base := fakeForge(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects" || r.URL.Query().Get("membership") != "true" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"path_with_namespace":"grp/sub/app","http_url_to_repo":"https://gl/grp/sub/app.git","default_branch":"main","visibility":"private"}]`))
	})
	repos, err := Repos(context.Background(), store.GitLab, base, "tok")
	if err != nil || len(repos) != 1 || repos[0].FullName != "grp/sub/app" || !repos[0].Private {
		t.Fatalf("repos = %+v, %v", repos, err)
	}
}

func TestGitHubApp(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	base := fakeForge(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/app-manifests/c0de/conversions":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "slug": "kipitiny-x", "pem": pemKey, "webhook_secret": "wh", "owner": map[string]string{"login": "me"}})
		case "/api/v3/app/installations/9/access_tokens":
			claims := jwt.RegisteredClaims{}
			_, err := jwt.ParseWithClaims(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), &claims,
				func(*jwt.Token) (any, error) { return &key.PublicKey, nil })
			if err != nil || claims.Issuer != "7" || r.Method != http.MethodPost {
				http.Error(w, `{"message":"bad jwt"}`, http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"token":"ghs_x","expires_at":"2030-01-01T00:00:00Z"}`))
		default:
			http.NotFound(w, r)
		}
	})
	app, err := ConvertManifest(context.Background(), base, "c0de")
	if err != nil || app.ID != 7 || app.Slug != "kipitiny-x" || app.WebhookSecret != "wh" || app.Owner.Login != "me" {
		t.Fatalf("app = %+v, %v", app, err)
	}
	p := store.GitProvider{Kind: store.GitHub, BaseURL: base, AppID: app.ID, PrivateKey: app.PEM, InstallationID: 9}
	tok, exp, err := InstallationToken(context.Background(), p)
	if err != nil || tok != "ghs_x" || exp.Year() != 2030 {
		t.Fatalf("token = %q %v %v", tok, exp, err)
	}
}

func TestManifest(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal([]byte(Manifest(ManifestInput{Name: "k", WebhookURL: "https://k.example.com/h"})), &m); err != nil {
		t.Fatal(err)
	}
	perms, _ := m["default_permissions"].(map[string]any)
	if perms["contents"] != "read" || m["public"] != false || m["hook_attributes"] == nil {
		t.Errorf("manifest = %v", m)
	}
	var noHook map[string]any
	if err := json.Unmarshal([]byte(Manifest(ManifestInput{Name: "k"})), &noHook); err != nil {
		t.Fatal(err)
	}
	if noHook["hook_attributes"] != nil || noHook["default_events"] != nil {
		t.Errorf("manifest without webhook = %v", noHook)
	}
}
