// Package protonpass resolves pass:// references through the official Proton
// Pass CLI (pass-cli), logged in with a personal access token. Proton has no
// public API: the CLI does the SRP login and item decryption.
package protonpass

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/MatHoyer/kipitiny/internal/secrets"
)

const Scheme = "pass"

// Client runs pass-cli with its session in its own directory.
type Client struct {
	bin string
	dir string
	// self is the command `pass-cli run` starts to print the resolved values
	// (secrets.PrintEnv).
	self []string
	mu   sync.Mutex // one CLI at a time: they share the session file
}

// New runs bin (a path or a name on PATH) with its session kept in dir and
// self as the command that prints resolved references.
func New(bin, dir string, self []string) *Client {
	return &Client{bin: bin, dir: dir, self: self}
}

func (c *Client) Info() secrets.Info {
	return secrets.Info{
		ID:         "protonpass",
		Name:       "Proton Pass",
		Scheme:     Scheme,
		TokenLabel: "Personal access token",
		Example:    "pass://Vault/Item/password",
		Help: "Create a token with `pass-cli personal-access-token create --name kipitiny --expiration 1y`, " +
			"then give it the vaults it may read: `pass-cli personal-access-token access grant " +
			"--personal-access-token-name kipitiny --vault-name <vault>`. kipitiny only sees what the token is granted.",
	}
}

func (c *Client) Available() bool {
	_, err := exec.LookPath(c.bin)
	return err == nil
}

// tokenRe is the format pass-cli expects: pst_<token>::<key>.
var tokenRe = regexp.MustCompile(`^pst_\S+::\S+$`)

func (c *Client) Connect(ctx context.Context, token string) error {
	if !tokenRe.MatchString(token) {
		return errors.New("expected a personal access token like pst_…::…")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.run(ctx, nil, "logout", "--force")
	if _, err := c.run(ctx, []string{"PROTON_PASS_PERSONAL_ACCESS_TOKEN=" + token}, "login"); err != nil {
		return err
	}
	_, err := c.run(ctx, nil, "info")
	return err
}

func (c *Client) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.run(ctx, nil, "logout", "--force")
	return err
}

// Resolve starts self under `pass-cli run`, which replaces each reference in
// its environment by the value.
func (c *Client) Resolve(ctx context.Context, refs []string) (map[string]string, error) {
	if len(refs) == 0 {
		return map[string]string{}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	args := append([]string{"run", "--no-masking", "--"}, c.self...)
	out, err := c.run(ctx, secrets.RefEnv(refs), args...)
	if err != nil {
		return nil, err
	}
	vals, err := secrets.ParseEnv(out, refs)
	if err != nil {
		return nil, err
	}
	for _, r := range refs {
		if vals[r] == r {
			return nil, fmt.Errorf("%s was not resolved", r)
		}
	}
	return vals, nil
}

// run starts pass-cli with only its own settings and extra in the
// environment, so the manager's variables never reach it.
func (c *Client) run(ctx context.Context, extra []string, args ...string) ([]byte, error) {
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Env = append([]string{
		"HOME=" + c.dir,
		"PROTON_PASS_SESSION_DIR=" + c.dir,
		// The key that encrypts the session sits next to it: there is no
		// keyring in a container, and the data dir is the trust boundary.
		"PROTON_PASS_KEY_PROVIDER=fs",
		"PROTON_PASS_NO_UPDATE_CHECK=1",
		"PASS_LOG_LEVEL=off",
		"NO_COLOR=1",
	}, extra...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := cliError(stderr.String()); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, fmt.Errorf("pass-cli %s: %w", args[0], err)
	}
	return stdout.Bytes(), nil
}

var causeRe = regexp.MustCompile(`^\d+:\s*`)

// cliError flattens pass-cli's "Error: …\n\nCaused by:\n  0: …" output to
// one line.
func cliError(stderr string) string {
	var parts []string
	for _, l := range strings.Split(stderr, "\n") {
		l = strings.TrimSpace(l)
		l = strings.TrimPrefix(l, "Error: ")
		l = causeRe.ReplaceAllString(l, "")
		if l == "" || l == "Caused by:" {
			continue
		}
		parts = append(parts, l)
	}
	return strings.Join(parts, ": ")
}
