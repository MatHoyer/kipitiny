package storage

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// remote is the name of the one remote in the generated rclone config.
const remote = "kipitiny"

// Rclone stores objects on a remote rclone reaches, e.g. Google Drive. Each command gets a config file written from the target's
// options; options rclone changes (refreshed OAuth tokens, a Proton session)
// are read back and handed to save, so the database stays the only copy.
type Rclone struct {
	bin     string
	work    string // directory for per-command config and spool files
	id      string // target id; "" for a target not saved yet
	backend string // rclone backend type, e.g. "drive"
	prefix  string
	save    func(ctx context.Context, id string, config map[string]string) error

	mu     sync.Mutex // guards config
	config map[string]string
}

// targetLocks serialises CLI commands per target (rclone, proton-drive):
// concurrent runs could each refresh a rotating token and lose the other's.
var targetLocks sync.Map // id -> *sync.Mutex

// lockTarget takes a target's lock; a target not saved yet needs none.
func lockTarget(id string) func() {
	if id == "" {
		return func() {}
	}
	m, _ := targetLocks.LoadOrStore(id, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// Config is the remote's options, as rclone left them after the last command.
func (r *Rclone) Config() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.config)
}

func (r *Rclone) path(key string) (string, error) {
	clean := path.Clean("/" + key)[1:]
	if clean == "" || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid key %q", key)
	}
	return remote + ":" + path.Join(r.prefix, clean), nil
}

// session is one locked use of the remote: its config file and spool dir.
type session struct {
	r      *Rclone
	dir    string
	conf   string
	before map[string]string
	unlock func()
}

func (r *Rclone) open() (*session, error) {
	unlock := lockTarget(r.id)
	s := &session{r: r, unlock: unlock, before: r.Config()}
	var err error
	if err = os.MkdirAll(r.work, 0o700); err == nil {
		s.dir, err = os.MkdirTemp(r.work, "run-*")
	}
	if err == nil {
		s.conf = filepath.Join(s.dir, "rclone.conf")
		err = os.WriteFile(s.conf, renderConfig(r.backend, s.before), 0o600)
	}
	if err != nil {
		s.cleanup()
		return nil, err
	}
	return s, nil
}

func (s *session) cleanup() {
	if s.dir != "" {
		os.RemoveAll(s.dir)
	}
	s.unlock()
}

// close keeps what rclone changed in the config, then cleans up.
func (s *session) close(ctx context.Context) error {
	defer s.cleanup()
	data, err := os.ReadFile(s.conf)
	if err != nil {
		return nil // nothing to keep
	}
	after := parseConfig(data)
	delete(after, "type")
	if len(after) == 0 || maps.Equal(after, s.before) {
		return nil
	}
	s.r.mu.Lock()
	s.r.config = after
	s.r.mu.Unlock()
	if s.r.save == nil || s.r.id == "" {
		return nil
	}
	return s.r.save(context.WithoutCancel(ctx), s.r.id, after)
}

// cmd runs rclone with only its own settings in the environment, so the
// manager's variables never reach it.
func (s *session) cmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, s.r.bin, append([]string{
		"--config", s.conf,
		"--ask-password=false",
		"--auto-confirm",
		"--log-level", "ERROR",
	}, args...)...)
	cmd.Env = []string{"HOME=" + s.dir, "TMPDIR=" + s.dir, "RCLONE_CONFIG=" + s.conf}
	return cmd
}

// run runs one command to completion and returns its stdout.
func (s *session) run(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := s.cmd(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, rcloneError(args[0], err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func (r *Rclone) do(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	s, err := r.open()
	if err != nil {
		return nil, err
	}
	out, err := s.run(ctx, stdin, args...)
	if cerr := s.close(ctx); err == nil {
		err = cerr
	}
	return out, err
}

func (r *Rclone) Put(ctx context.Context, key string, rd io.Reader) error {
	p, err := r.path(key)
	if err != nil {
		return err
	}
	s, err := r.open()
	if err != nil {
		return err
	}
	// rcat streams when the backend can, else spools to TMPDIR (the session dir).
	_, err = s.run(ctx, readerWithContext(ctx, rd), "rcat", p)
	if err != nil {
		// Drive uploads only appear once complete; clean up regardless.
		_, _ = s.run(context.WithoutCancel(ctx), nil, "deletefile", p)
	}
	if cerr := s.close(ctx); err == nil {
		err = cerr
	}
	return err
}

func (r *Rclone) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	p, err := r.path(key)
	if err != nil {
		return nil, err
	}
	s, err := r.open()
	if err != nil {
		return nil, err
	}
	// cat of a missing file fails only once read: check first, as S3 does.
	if _, err := s.run(ctx, nil, "lsf", p); err != nil {
		_ = s.close(ctx)
		return nil, err
	}
	cmd := s.cmd(ctx, "cat", p)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		_ = s.close(ctx)
		return nil, err
	}
	return &catReader{ReadCloser: out, cmd: cmd, stderr: &stderr, s: s, ctx: ctx}, nil
}

// catReader streams `rclone cat`; Close waits for it and ends the session.
type catReader struct {
	io.ReadCloser
	cmd    *exec.Cmd
	stderr *bytes.Buffer
	s      *session
	ctx    context.Context
	once   sync.Once
	err    error
}

func (c *catReader) Close() error {
	c.once.Do(func() {
		c.ReadCloser.Close()
		if err := c.cmd.Wait(); err != nil {
			c.err = rcloneError("cat", err, c.stderr.String())
		}
		if err := c.s.close(c.ctx); c.err == nil {
			c.err = err
		}
	})
	return c.err
}

func (r *Rclone) Delete(ctx context.Context, key string) error {
	p, err := r.path(key)
	if err != nil {
		return err
	}
	_, err = r.do(ctx, nil, "deletefile", p)
	if errors.Is(err, errNotFound) {
		return nil
	}
	return err
}

func (r *Rclone) Check(ctx context.Context) error {
	if r.prefix != "" {
		if _, err := r.do(ctx, nil, "mkdir", remote+":"+r.prefix); err != nil {
			return err
		}
	}
	if err := r.Put(ctx, ".kipitiny-check", strings.NewReader("ok")); err != nil {
		return fmt.Errorf("write test file: %w", err)
	}
	return r.Delete(ctx, ".kipitiny-check")
}

// RcloneAvailable reports whether bin can be run.
func RcloneAvailable(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// renderConfig writes the remote's section. Keys are sorted so unchanged
// options read back equal.
func renderConfig(backend string, opts map[string]string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "[%s]\ntype = %s\n", remote, backend)
	for _, k := range slices.Sorted(maps.Keys(opts)) {
		if k == "type" {
			continue
		}
		// One option per line: a newline would inject another.
		v := strings.NewReplacer("\r", "", "\n", "").Replace(opts[k])
		fmt.Fprintf(&b, "%s = %s\n", k, v)
	}
	return b.Bytes()
}

// parseConfig reads the remote's section back.
func parseConfig(data []byte) map[string]string {
	opts := map[string]string{}
	in := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			in = line == "["+remote+"]"
			continue
		}
		if !in || line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			opts[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return opts
}

var errNotFound = errors.New("not found")

// logPrefix is rclone's "2026/10/01 12:00:00 ERROR : " line prefix.
var logPrefix = regexp.MustCompile(`^\d{4}/\d\d/\d\d \d\d:\d\d:\d\d\s+(?:[A-Z]+\s*:\s*)?`)

// rcloneError keeps rclone's last log line; exit codes 3 and 4 (directory or
// file not found) wrap errNotFound.
func rcloneError(op string, err error, stderr string) error {
	msg := ""
	for _, l := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if l = strings.TrimSpace(logPrefix.ReplaceAllString(l, "")); l != "" {
			msg = l
		}
	}
	if msg == "" {
		msg = err.Error()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && (exit.ExitCode() == 3 || exit.ExitCode() == 4) {
		return fmt.Errorf("rclone %s: %s: %w", op, msg, errNotFound)
	}
	return fmt.Errorf("rclone %s: %s", op, msg)
}
