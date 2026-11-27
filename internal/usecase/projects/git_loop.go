package projects

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// StartGitSync polls the branches of the projects with auto sync, each on
// its interval, and syncs projects kicked by a webhook or a new link.
func (c *Projects) StartGitSync() error {
	return c.mgr.GoLoop(func() {
		checked := map[string]time.Time{}
		ticker := time.NewTicker(gitLoopEvery)
		defer ticker.Stop()
		for {
			select {
			case <-c.bg.Done():
				return
			case id := <-c.gitKick:
				checked[id] = time.Now()
				c.runGitSync(id, true)
				continue
			case <-ticker.C:
			}
			links, err := c.store.ListProjectGit(c.bg)
			if err != nil {
				c.log.Warn("git sync", "err", err)
				continue
			}
			for _, g := range links {
				if !g.AutoSync || time.Since(checked[g.ProjectID]) < time.Duration(g.PollSeconds)*time.Second {
					continue
				}
				checked[g.ProjectID] = time.Now()
				c.syncLink(g, false)
			}
		}
	})
}

func (c *Projects) runGitSync(projectID string, force bool) {
	g, err := c.store.GetProjectGit(c.bg, projectID)
	if err != nil {
		return
	}
	c.syncLink(g, force)
}

func (c *Projects) syncLink(g domain.ProjectGit, force bool) {
	if _, err := c.syncGit(c.bg, g, force, false); err != nil && c.bg.Err() == nil {
		c.log.Warn("git sync failed", "project", g.ProjectID, "err", err)
	}
}

// kickGit syncs a project soon.
func (c *Projects) kickGit(projectID string) {
	select {
	case c.gitKick <- projectID:
	default:
	}
}

// HandleGitWebhook authenticates a push webhook and syncs the project when
// it concerns its branch. It accepts GitHub/Gitea (X-Hub-Signature-256
// HMAC), GitLab (X-Gitlab-Token) and generic (Bearer token) deliveries.
// The body is only read once the project is known and the delivery carries
// a signature or token, so unknown callers can't make the manager buffer
// payloads.
func (c *Projects) HandleGitWebhook(ctx context.Context, projectID string, header func(string) string, readBody func() ([]byte, error)) error {
	g, err := c.store.GetProjectGit(ctx, projectID)
	if err != nil || g.WebhookSecret == "" {
		return identity.ErrUnauthorized // don't reveal which IDs exist
	}
	if header("X-Hub-Signature-256") == "" && header("X-Gitlab-Token") == "" && !strings.HasPrefix(header("Authorization"), "Bearer ") {
		return identity.ErrUnauthorized
	}
	body, err := readBody()
	if err != nil {
		return err
	}
	if !webhookAuthorized(g.WebhookSecret, header, body) {
		return identity.ErrUnauthorized
	}
	if header("X-GitHub-Event") == "ping" {
		return ErrIgnored
	}
	var payload struct {
		Ref string `json:"ref"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			return fmt.Errorf("%w: invalid JSON payload", ErrInvalid)
		}
	}
	if payload.Ref != "" && payload.Ref != "refs/heads/"+g.Branch {
		return ErrIgnored
	}
	c.kickGit(projectID)
	return nil
}

func webhookAuthorized(secret string, header func(string) string, body []byte) bool {
	if sig, ok := strings.CutPrefix(header("X-Hub-Signature-256"), "sha256="); ok {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		return hmac.Equal([]byte(sig), []byte(want))
	}
	for _, got := range []string{header("X-Gitlab-Token"), strings.TrimPrefix(header("Authorization"), "Bearer ")} {
		if got != "" && hmac.Equal([]byte(got), []byte(secret)) {
			return true
		}
	}
	return false
}
