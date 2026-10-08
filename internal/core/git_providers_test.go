package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestPublicURL(t *testing.T) {
	for u, want := range map[string]bool{
		"https://kipitiny.example.com": true,
		"https://203.0.113.10:3000":    true,
		"http://localhost:3000":        false,
		"http://127.0.0.1:3000":        false,
		"http://192.168.1.20:3000":     false,
		"http://kipitiny.local":        false,
		"http://box:3000":              false,
	} {
		if got := publicURL(u); got != want {
			t.Errorf("%s: %v", u, got)
		}
	}
}

func TestNormalizeForgeURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                           "https://gitlab.com",
		"git.example.com/":           "https://git.example.com",
		"https://Git.Example.com/gl": "https://git.example.com/gl",
	} {
		if got, err := normalizeForgeURL(store.GitLab, in); err != nil || got != want {
			t.Errorf("%q: %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"http://git.example.com", "https://u:p@git.example.com"} {
		if _, err := normalizeForgeURL(store.GitLab, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", in, err)
		}
	}
	if _, err := normalizeForgeURL(store.Gitea, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("gitea without URL: %v", err)
	}
}

func TestCheckProviderRepo(t *testing.T) {
	p := store.GitProvider{Name: "gh", BaseURL: "https://github.com"}
	if err := checkProviderRepo(p, "https://github.com/me/infra.git"); err != nil {
		t.Error(err)
	}
	for _, u := range []string{"https://evil.example.com/me/infra.git", "http://github.com/me/infra.git"} {
		if err := checkProviderRepo(p, u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", u, err)
		}
	}
}

func TestGitProviders(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	if _, err := c.CreateGitProvider(ctx, GitProviderInput{Kind: store.GitHub, Name: "gh"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("github created directly: %v", err)
	}
	p, err := c.CreateGitProvider(ctx, GitProviderInput{Kind: store.GitLab, Name: "gl", ClientID: "id", ClientSecret: "s"})
	if err != nil || p.BaseURL != "https://gitlab.com" || p.ClientSecret != SecretMask || p.Connected {
		t.Fatalf("create = %+v, %v", p, err)
	}
	if _, err := c.CreateGitProvider(ctx, GitProviderInput{Kind: store.Gitea, Name: "gl", BaseURL: "git.example.com", ClientID: "id", ClientSecret: "s"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("duplicate name: %v", err)
	}

	// An authorization is dropped when the application changes, kept otherwise.
	sp, _ := c.store.GetGitProvider(ctx, p.ID)
	sp.AccessToken, sp.Account = "tok", "me"
	if _, err := c.store.UpdateGitProvider(ctx, sp); err != nil {
		t.Fatal(err)
	}
	if v, err := c.UpdateGitProvider(ctx, p.ID, GitProviderInput{Name: "GitLab", ClientID: "id", ClientSecret: SecretMask}); err != nil || !v.Connected || v.Name != "GitLab" {
		t.Errorf("rename = %+v, %v", v, err)
	}
	if v, err := c.UpdateGitProvider(ctx, p.ID, GitProviderInput{Name: "GitLab", ClientID: "other", ClientSecret: SecretMask}); err != nil || v.Connected || v.Account != "" {
		t.Errorf("new application = %+v, %v", v, err)
	}
	sp, _ = c.store.GetGitProvider(ctx, p.ID)
	if sp.ClientSecret != "s" {
		t.Errorf("secret = %q", sp.ClientSecret)
	}

	// A project's repository must be on the provider's host, and the
	// provider can't go while a project uses it.
	proj, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.gitFromInput(ctx, proj.ID, GitInput{RepoURL: "https://github.com/me/r.git", ProviderID: p.ID}); !errors.Is(err, ErrInvalid) {
		t.Errorf("other host: %v", err)
	}
	g, err := c.gitFromInput(ctx, proj.ID, GitInput{RepoURL: "https://gitlab.com/me/r.git", ProviderID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.SaveProjectGit(ctx, g); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteGitProvider(ctx, p.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("delete while used: %v", err)
	}
	if err := c.store.DeleteProjectGit(ctx, proj.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteGitProvider(ctx, p.ID); err != nil {
		t.Error(err)
	}
}

func TestGitHubAppFlowState(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	start, err := c.StartGitHubApp(ctx, "http://localhost:3000", GitHubAppInput{Org: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://github.com/organizations/acme/settings/apps/new?state="; start.URL[:len(want)] != want {
		t.Errorf("url = %s", start.URL)
	}
	// Not reachable by GitHub: no webhook.
	if strings.Contains(start.Manifest, "hook_attributes") {
		t.Errorf("manifest = %s", start.Manifest)
	}
	if _, err := c.FinishGitHubApp(ctx, "code", "forged"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown state: %v", err)
	}
	if _, err := c.GitHubAppInstalled(ctx, 42, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown installation: %v", err)
	}
	pub, err := c.StartGitHubApp(ctx, "https://kipitiny.example.com", GitHubAppInput{Name: "Work"})
	if err != nil || !strings.Contains(pub.Manifest, `"url":"https://kipitiny.example.com/api/hooks/git-providers/`) {
		t.Errorf("public manifest = %s, %v", pub.Manifest, err)
	}
}

func TestGitProviderWebhook(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateGitProvider(ctx, store.GitProvider{Kind: store.GitHub, Name: "gh", BaseURL: "https://github.com", WebhookSecret: "s3cret", InstallationID: 1})
	if err != nil {
		t.Fatal(err)
	}
	proj, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.SaveProjectGit(ctx, store.ProjectGit{ProjectID: proj.ID, ProviderID: p.ID, RepoURL: "https://github.com/me/infra.git",
		Branch: "main", Path: "compose.yaml", PollSeconds: 300, WebhookSecret: "w"}); err != nil {
		t.Fatal(err)
	}
	send := func(event, body string, secret string) error {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(body))
		h := http.Header{"X-Hub-Signature-256": {"sha256=" + hex.EncodeToString(mac.Sum(nil))}, "X-Github-Event": {event}}
		return c.HandleGitProviderWebhook(ctx, p.ID, h.Get, []byte(body))
	}
	push := `{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/Me/infra.git"}}`
	if err := send("push", push, "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("bad signature: %v", err)
	}
	if err := send("installation", `{}`, "s3cret"); !errors.Is(err, ErrIgnored) {
		t.Errorf("other event: %v", err)
	}
	if err := send("push", `{"ref":"refs/heads/dev","repository":{"clone_url":"https://github.com/me/infra.git"}}`, "s3cret"); !errors.Is(err, ErrIgnored) {
		t.Errorf("other branch: %v", err)
	}
	if err := send("push", push, "s3cret"); err != nil {
		t.Fatal(err)
	}
	if got := <-c.gitKick; got != proj.ID {
		t.Errorf("kicked %s", got)
	}
}
