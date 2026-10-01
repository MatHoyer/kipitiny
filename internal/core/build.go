package core

import (
	"archive/tar"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	buildTimeout = 30 * time.Minute
	// keepBuilds is how many built images per service stay for rollback.
	keepBuilds = 5
)

// buildImage clones the service's repository and builds its Dockerfile with
// BuildKit, through a short-lived docker CLI container that receives the
// source as a tar stream (no git or Docker CLI needed on the host or in the
// manager). Returns the image tag and commit.
func (c *Core) buildImage(ctx context.Context, project store.Project, svc store.Service, dep store.Deployment,
	out io.Writer, logf func(string, ...any)) (string, string, error) {

	ctx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()

	dir := filepath.Join(c.cfg.DataDir, "builds", dep.ID)
	defer os.RemoveAll(dir)

	logf("Cloning %s (branch %s)", redactURL(svc.GitURL), svc.GitBranch)
	opts := &git.CloneOptions{
		URL:           svc.GitURL,
		ReferenceName: plumbing.NewBranchReferenceName(svc.GitBranch),
		SingleBranch:  true,
		Depth:         1,
	}
	if svc.GitToken != "" {
		// Works for GitHub, GitLab and Gitea tokens over HTTPS.
		opts.Auth = &githttp.BasicAuth{Username: "x-access-token", Password: svc.GitToken}
	}
	repo, err := git.PlainCloneContext(ctx, dir, false, opts)
	if err != nil {
		return "", "", fmt.Errorf("clone: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", "", fmt.Errorf("clone: %w", err)
	}
	commit := head.Hash().String()
	logf("Checked out %s", commit[:12])

	contextDir := filepath.Join(dir, filepath.FromSlash(svc.BuildContext))
	if _, err := os.Stat(filepath.Join(contextDir, filepath.FromSlash(svc.Dockerfile))); err != nil {
		return "", "", fmt.Errorf("%s not found in %s", svc.Dockerfile, orDot(svc.BuildContext))
	}

	tag := fmt.Sprintf("kipitiny/%s-%s:%s", project.Name, svc.Name, strings.ToLower(dep.ID))
	server, err := c.store.GetServer(ctx, svc.ServerID)
	if err != nil {
		return "", "", err
	}
	dk := c.dockerFor(server.ID)
	if err := dk.EnsureImage(ctx, c.cfg.BuilderImage, c.registryAuth(ctx, c.cfg.BuilderImage)); err != nil {
		return "", "", fmt.Errorf("pull builder %s: %w", c.cfg.BuilderImage, err)
	}

	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(tarDir(contextDir, pw)) }()
	defer pr.Close()

	logf("Building %s", tag)
	cmd := []string{
		"docker", "build", "--progress=plain",
		"-f", svc.Dockerfile, "-t", tag,
		"--label", docker.LabelManaged + "=true",
		"--label", docker.LabelService + "=" + svc.ID,
		"--label", "org.opencontainers.image.revision=" + commit,
		"-", // context: the tar stream on stdin
	}
	var env []string
	if cfg := c.dockerConfig(ctx); cfg != "" {
		// Base images from private registries: the CLI reads its config file.
		env = []string{"KIPITINY_DOCKER_CONFIG=" + cfg}
		cmd = append([]string{"sh", "-c",
			`mkdir -p "$HOME/.docker" && printf %s "$KIPITINY_DOCKER_CONFIG" > "$HOME/.docker/config.json" && exec "$@"`,
			"sh"}, cmd...)
	}
	code, err := dk.RunAttached(ctx, client.ContainerCreateOptions{
		Name: "kipitiny-build-" + strings.ToLower(dep.ID),
		Config: &container.Config{
			Image:  c.cfg.BuilderImage,
			Cmd:    cmd,
			Env:    env,
			Labels: map[string]string{docker.LabelManaged: "true", docker.LabelComponent: "builder"},
		},
		HostConfig: &container.HostConfig{
			Mounts: []mount.Mount{{Type: mount.TypeBind, Source: socketPath(server, c.cfg), Target: "/var/run/docker.sock"}},
		},
	}, pr, out)
	if err != nil {
		return "", "", fmt.Errorf("build: %w", err)
	}
	if code != 0 {
		return "", "", fmt.Errorf("build failed (exit code %d), see the log", code)
	}
	return tag, commit, nil
}

// tarDir writes a directory as a tar stream, skipping .git.
func tarDir(root string, w io.Writer) error {
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == "." {
			return err
		}
		if d.Name() == ".git" && d.IsDir() {
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if d.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

// pruneBuilds removes images of this service's older builds, keeping the
// current one and the last few successful ones for rollback.
func (c *Core) pruneBuilds(ctx context.Context, svc store.Service) {
	deps, err := c.store.ListDeployments(ctx, svc.ID, 50)
	if err != nil {
		return
	}
	kept := 0
	for _, d := range deps {
		if d.GitCommit == "" || !strings.HasPrefix(d.Image, "kipitiny/") {
			continue
		}
		if (d.Status == store.DeploymentSucceeded && kept < keepBuilds) || d.ID == svc.CurrentDeploymentID {
			kept++
			continue
		}
		if _, err := c.dockerFor(svc.ServerID).ImageRemove(ctx, d.Image, client.ImageRemoveOptions{PruneChildren: true}); err == nil {
			c.log.Info("removed old build", "image", d.Image)
		}
	}
}

func orDot(s string) string {
	if s == "" {
		return "the repository root"
	}
	return s
}

// redactURL hides credentials embedded in a clone URL.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.User("***")
	return u.String()
}

// validateGit checks the source fields of a git service.
func validateGit(s store.Service) error {
	u, err := url.Parse(s.GitURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%w: repository must be an http(s) URL", ErrInvalid)
	}
	if s.GitBranch == "" || strings.ContainsAny(s.GitBranch, " ~^:?*[\\\\") {
		return fmt.Errorf("%w: invalid branch", ErrInvalid)
	}
	for name, p := range map[string]string{"Dockerfile": s.Dockerfile, "build context": s.BuildContext} {
		if p != "" && (path.IsAbs(p) || strings.HasPrefix(path.Clean(p), "..")) {
			return fmt.Errorf("%w: %s must be a path inside the repository", ErrInvalid, name)
		}
	}
	return nil
}

// Webhook is what a push webhook needs to be configured.
type Webhook struct {
	URL    string `json:"url"`
	Secret string `json:"secret"`
}

func (c *Core) ServiceWebhook(ctx context.Context, id string) (Webhook, error) {
	svc, err := c.store.GetService(ctx, id)
	if err != nil {
		return Webhook{}, err
	}
	if svc.Source != store.SourceGit {
		return Webhook{}, fmt.Errorf("%w: only git services have a webhook", ErrInvalid)
	}
	return Webhook{URL: "/api/hooks/" + svc.ID, Secret: svc.WebhookSecret}, nil
}

// ErrIgnored is returned for webhooks that are valid but trigger nothing.
var ErrIgnored = errors.New("ignored")

// HandleWebhook authenticates a push webhook and deploys when it concerns the
// service's branch. It accepts GitHub (X-Hub-Signature-256 HMAC), GitLab
// (X-Gitlab-Token) and generic (Bearer token) deliveries.
func (c *Core) HandleWebhook(ctx context.Context, serviceID string, header func(string) string, body []byte) (store.Deployment, error) {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil || svc.Source != store.SourceGit || svc.WebhookSecret == "" {
		return store.Deployment{}, ErrUnauthorized // don't reveal which IDs exist
	}
	if !webhookAuthorized(svc.WebhookSecret, header, body) {
		return store.Deployment{}, ErrUnauthorized
	}
	if event := header("X-GitHub-Event"); event == "ping" {
		return store.Deployment{}, ErrIgnored
	}
	var payload struct {
		Ref string `json:"ref"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			return store.Deployment{}, fmt.Errorf("%w: invalid JSON payload", ErrInvalid)
		}
	}
	if payload.Ref != "" && payload.Ref != "refs/heads/"+svc.GitBranch {
		return store.Deployment{}, ErrIgnored
	}
	return c.Deploy(ctx, svc.ID)
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
