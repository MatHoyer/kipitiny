// Package protonpass resolves pass:// references through the official Proton
// Pass CLI (pass-cli), logged in with a personal access token. Proton has no
// public API: the CLI does the SRP login and item decryption. The CLI isn't
// in the manager's image: a Runner starts it elsewhere (ContainerRunner runs
// the kipitiny-protonpass image through Docker).
package protonpass

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/MatHoyer/kipitiny/internal/secrets"
)

const Scheme = "pass"

// Runner runs a command next to pass-cli and its session. stdin may be nil.
// A non-zero exit returns an error along with what the command wrote.
type Runner interface {
	Run(ctx context.Context, cmd, env []string, stdin io.Reader) (stdout, stderr []byte, err error)
}

// Client runs pass-cli through a Runner.
type Client struct {
	runner Runner
	mu     sync.Mutex // one CLI at a time: they share the session file
}

func New(r Runner) *Client {
	return &Client{runner: r}
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
			"--personal-access-token-name kipitiny --vault-name <vault>`. kipitiny only sees what the token is granted. " +
			"The first connection downloads the Proton Pass CLI (about 60MB), run in a container only while it's used.",
	}
}

// Available is always true: the CLI is fetched on first use.
func (c *Client) Available() bool { return true }

// tokenRe is the format pass-cli expects: pst_<token>::<key>.
var tokenRe = regexp.MustCompile(`^pst_\S+::\S+$`)

// loginCmd reads the token from stdin, so it never shows in the process or
// container configuration.
var loginCmd = []string{"sh", "-c", `IFS= read -r PROTON_PASS_PERSONAL_ACCESS_TOKEN && export PROTON_PASS_PERSONAL_ACCESS_TOKEN && exec pass-cli login`}

func (c *Client) Connect(ctx context.Context, token string) error {
	if !tokenRe.MatchString(token) {
		return errors.New("expected a personal access token like pst_…::…")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.run(ctx, nil, "logout", "--force")
	if _, err := c.exec(ctx, loginCmd, nil, strings.NewReader(token+"\n")); err != nil {
		return err
	}
	_, err := c.run(ctx, nil, "info")
	return err
}

// Close stops whatever the runner keeps running between calls.
func (c *Client) Close(ctx context.Context) error {
	if r, ok := c.runner.(interface{ Close(context.Context) error }); ok {
		return r.Close(ctx)
	}
	return nil
}

// Disconnect logs out, then drops the session entirely when the runner can
// (ContainerRunner removes its volume).
func (c *Client) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.run(ctx, nil, "logout", "--force")
	if r, ok := c.runner.(interface{ Reset(context.Context) error }); ok {
		err = errors.Join(err, r.Reset(ctx))
	}
	return err
}

// envPrefix names the variables `pass-cli run` resolves: it replaces each
// reference found in the environment of the command it starts by its value.
const envPrefix = "KIPITINY_SECRET_"

// Resolve has `pass-cli run` print the resolved environment with env -0.
func (c *Client) Resolve(ctx context.Context, refs []string) (map[string]string, error) {
	if len(refs) == 0 {
		return map[string]string{}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out, err := c.run(ctx, refEnv(refs), "run", "--no-masking", "--", "env", "-0")
	if err != nil {
		return nil, err
	}
	vals, err := parseEnv0(out, refs)
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

// refEnv lists refs as envPrefix variables, in order.
func refEnv(refs []string) []string {
	env := make([]string, len(refs))
	for i, r := range refs {
		env[i] = envPrefix + strconv.Itoa(i) + "=" + r
	}
	return env
}

// parseEnv0 maps refs to the values of their refEnv variables in the output
// of env -0 (NUL-separated, so values may hold newlines).
func parseEnv0(out []byte, refs []string) (map[string]string, error) {
	m := make(map[string]string, len(refs))
	for _, kv := range strings.Split(string(out), "\x00") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, envPrefix) {
			continue
		}
		i, err := strconv.Atoi(k[len(envPrefix):])
		if err != nil || i < 0 || i >= len(refs) {
			continue
		}
		m[refs[i]] = v
	}
	if len(m) != len(refs) {
		return nil, errors.New("resolved values don't match the references")
	}
	return m, nil
}

// run starts pass-cli with args.
func (c *Client) run(ctx context.Context, env []string, args ...string) ([]byte, error) {
	return c.exec(ctx, append([]string{"pass-cli"}, args...), env, nil)
}

func (c *Client) exec(ctx context.Context, cmd, env []string, stdin io.Reader) ([]byte, error) {
	stdout, stderr, err := c.runner.Run(ctx, cmd, env, stdin)
	if err != nil {
		if msg := cliError(string(stderr)); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, fmt.Errorf("pass-cli: %w", err)
	}
	return stdout, nil
}

var (
	causeRe = regexp.MustCompile(`^\d+:\s*`)
	// logRe matches the log lines pass-cli prints whatever PASS_LOG_LEVEL:
	// "2026-10-09T09:51:15.411250Z ERROR pass-cli/src/main.rs:335: …".
	logRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\S+\s+[A-Z]+\s`)
)

// cliError flattens pass-cli's "Error: …\n\nCaused by:\n  0: …" output to
// one line.
func cliError(stderr string) string {
	var parts []string
	for _, l := range strings.Split(stderr, "\n") {
		l = strings.TrimSpace(l)
		if logRe.MatchString(l) {
			continue
		}
		l = strings.TrimPrefix(l, "Error: ")
		l = causeRe.ReplaceAllString(l, "")
		if l == "" || l == "Caused by:" {
			continue
		}
		parts = append(parts, l)
	}
	return strings.Join(parts, ": ")
}
