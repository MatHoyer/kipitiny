package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Files of a proton-drive session, kept in the target's config under these
// keys: the signed-in session (tokens and the key password) and the client
// id it was created with.
const (
	ProtonSessionFile = "auth-session.json"
	ProtonClientFile  = "clientUid.json"
)

// ProtonDrive stores objects in Proton Drive through the official CLI
// (proton-drive). Each command runs in a fresh directory holding the session
// files from the target's config; the CLI rewrites them (refreshed tokens),
// and what changed is handed to save, so the database stays the only copy.
//
// The CLI has no stdin/stdout transfers: uploads and downloads go through a
// file in that directory.
type ProtonDrive struct {
	bin    string
	work   string
	id     string
	prefix string
	save   func(ctx context.Context, id string, config map[string]string) error

	mu     sync.Mutex
	config map[string]string
}

// ProtonEnv is the environment proton-drive runs with: its session as plain
// files in dir (no keyring in a container; the data dir is the trust
// boundary), and nothing of the manager's.
func ProtonEnv(dir string) []string {
	return []string{
		"HOME=" + dir,
		"LANG=C.UTF-8",
		"PROTON_DRIVE_CREDENTIALS_STORE=unsafe_file",
		"PROTON_DRIVE_CACHE_DIR=" + dir,
	}
}

func (p *ProtonDrive) Config() map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return maps.Clone(p.config)
}

// remotePath is the object's path in the drive: under My files.
func (p *ProtonDrive) remotePath(key string) (string, error) {
	clean := path.Clean("/" + key)[1:]
	if clean == "" || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid key %q", key)
	}
	return path.Join("/my-files", p.prefix, clean), nil
}

type protonSession struct {
	p      *ProtonDrive
	dir    string
	before map[string]string
	unlock func()
}

func (p *ProtonDrive) open() (*protonSession, error) {
	unlock := lockTarget(p.id)
	s := &protonSession{p: p, unlock: unlock, before: p.Config()}
	var err error
	if err = os.MkdirAll(p.work, 0o700); err == nil {
		s.dir, err = os.MkdirTemp(p.work, "run-*")
	}
	for _, f := range []string{ProtonSessionFile, ProtonClientFile} {
		if err == nil && s.before[f] != "" {
			err = os.WriteFile(filepath.Join(s.dir, f), []byte(s.before[f]), 0o600)
		}
	}
	if err == nil && s.before[ProtonSessionFile] == "" {
		err = errors.New("not signed in to Proton")
	}
	if err != nil {
		s.cleanup()
		return nil, err
	}
	return s, nil
}

func (s *protonSession) cleanup() {
	if s.dir != "" {
		os.RemoveAll(s.dir)
	}
	s.unlock()
}

// close keeps the session files the CLI rewrote, then cleans up.
func (s *protonSession) close(ctx context.Context) error {
	defer s.cleanup()
	after := maps.Clone(s.before)
	for _, f := range []string{ProtonSessionFile, ProtonClientFile} {
		if data, err := os.ReadFile(filepath.Join(s.dir, f)); err == nil && json.Valid(data) {
			after[f] = string(data)
		}
	}
	if maps.Equal(after, s.before) {
		return nil
	}
	s.p.mu.Lock()
	s.p.config = after
	s.p.mu.Unlock()
	if s.p.save == nil || s.p.id == "" {
		return nil
	}
	return s.p.save(context.WithoutCancel(ctx), s.p.id, after)
}

// run runs one command to completion and returns its stdout.
func (s *protonSession) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, s.p.bin, args...)
	cmd.Env = ProtonEnv(s.dir)
	cmd.Dir = s.dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, protonError(args, err, stderr.String()+"\n"+stdout.String())
	}
	return stdout.Bytes(), nil
}

func (p *ProtonDrive) do(ctx context.Context, args ...string) ([]byte, error) {
	s, err := p.open()
	if err != nil {
		return nil, err
	}
	out, err := s.run(ctx, args...)
	if cerr := s.close(ctx); err == nil {
		err = cerr
	}
	return out, err
}

func (p *ProtonDrive) Put(ctx context.Context, key string, r io.Reader) error {
	remote, err := p.remotePath(key)
	if err != nil {
		return err
	}
	s, err := p.open()
	if err != nil {
		return err
	}
	err = s.put(ctx, remote, r)
	if cerr := s.close(ctx); err == nil {
		err = cerr
	}
	return err
}

// put stages the object as a local tree mirroring its folders and uploads the
// top folder into My files with folders merged: missing ones are created.
func (s *protonSession) put(ctx context.Context, remote string, r io.Reader) error {
	rel := strings.TrimPrefix(remote, "/my-files/")
	stage := filepath.Join(s.dir, "upload")
	local := filepath.Join(stage, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(local, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, readerWithContext(ctx, r))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	top := strings.SplitN(rel, "/", 2)[0]
	_, err = s.run(ctx, "filesystem", "upload", "--skip-thumbnails",
		"--file-conflict-strategy", "replace", "--folder-conflict-strategy", "merge",
		filepath.Join(stage, top), "/my-files")
	return err
}

func (p *ProtonDrive) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	remote, err := p.remotePath(key)
	if err != nil {
		return nil, err
	}
	s, err := p.open()
	if err != nil {
		return nil, err
	}
	dl := filepath.Join(s.dir, "download")
	if err = os.Mkdir(dl, 0o700); err == nil {
		_, err = s.run(ctx, "filesystem", "download", "--file-conflict-strategy", "remove", remote, dl)
	}
	var f *os.File
	if err == nil {
		f, err = os.Open(filepath.Join(dl, path.Base(remote)))
	}
	if err != nil {
		_ = s.close(ctx)
		return nil, err
	}
	return &sessionFile{File: f, close: func() error { return s.close(ctx) }}, nil
}

// sessionFile is a downloaded object; closing it ends the session.
type sessionFile struct {
	*os.File
	close func() error
	once  sync.Once
	err   error
}

func (f *sessionFile) Close() error {
	f.once.Do(func() {
		f.File.Close()
		f.err = f.close()
	})
	return f.err
}

// Delete trashes the object, then deletes it from the trash, where it's
// found by name: object keys are unique.
func (p *ProtonDrive) Delete(ctx context.Context, key string) error {
	remote, err := p.remotePath(key)
	if err != nil {
		return err
	}
	s, err := p.open()
	if err != nil {
		return err
	}
	_, err = s.run(ctx, "filesystem", "trash", remote)
	if err == nil {
		_, err = s.run(ctx, "filesystem", "delete", "/trash/"+escapeName(path.Base(remote)))
	}
	if errors.Is(err, errNotFound) {
		err = nil
	}
	if cerr := s.close(ctx); err == nil {
		err = cerr
	}
	return err
}

func (p *ProtonDrive) Check(ctx context.Context) error {
	if err := p.Put(ctx, ".kipitiny-check", strings.NewReader("ok")); err != nil {
		return fmt.Errorf("write test file: %w", err)
	}
	return p.Delete(ctx, ".kipitiny-check")
}

// Account is the email of the signed-in account (the owner of My files).
func (p *ProtonDrive) Account(ctx context.Context) (string, error) {
	out, err := p.do(ctx, "filesystem", "info", "--json", "/my-files")
	if err != nil {
		return "", err
	}
	var node struct {
		OwnedBy struct {
			Email string `json:"email"`
		} `json:"ownedBy"`
	}
	if err := json.Unmarshal(out, &node); err != nil {
		return "", fmt.Errorf("proton-drive info: %w", err)
	}
	return node.OwnedBy.Email, nil
}

// escapeName escapes / in a node name, as the CLI's paths expect.
func escapeName(name string) string { return strings.ReplaceAll(name, "/", `\/`) }

// protonError keeps the CLI's last line. "Node not found" (the CLI's own
// message, unlike API errors which come in the account's language) wraps
// errNotFound.
func protonError(args []string, err error, output string) error {
	msg := ""
	for _, l := range strings.Split(strings.TrimSpace(output), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			msg = l
		}
	}
	if msg == "" {
		msg = err.Error()
	}
	op := strings.Join(args[:min(2, len(args))], " ")
	if strings.HasPrefix(msg, "Node not found") || strings.HasPrefix(msg, "Trashed node not found") {
		return fmt.Errorf("proton-drive %s: %s: %w", op, msg, errNotFound)
	}
	return fmt.Errorf("proton-drive %s: %s", op, msg)
}

// ProtonLogin is a browser sign-in started with `proton-drive auth login`:
// it prints a URL to open on any device and exits once signed in, leaving
// the session files in Dir.
type ProtonLogin struct {
	Dir string
	URL string

	done chan struct{}
	err  error
}

// StartProtonLogin starts a sign-in in a new directory under work. It
// returns once the CLI printed the URL, waiting at most wait; ctx bounds the
// whole sign-in.
func StartProtonLogin(ctx context.Context, bin, work string, wait time.Duration) (*ProtonLogin, error) {
	if err := os.MkdirAll(work, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(work, "login-*")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "auth", "login")
	cmd.Env = ProtonEnv(dir)
	cmd.Dir = dir
	out, err := cmd.StdoutPipe()
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	l := &ProtonLogin{Dir: dir, done: make(chan struct{})}
	urls := make(chan string, 1)
	go func() {
		var all bytes.Buffer
		buf := make([]byte, 4096)
		sent := false
		for {
			n, rerr := out.Read(buf)
			all.Write(buf[:n])
			if !sent {
				for _, line := range strings.Split(all.String(), "\n") {
					if line = strings.TrimSpace(line); strings.HasPrefix(line, "https://") {
						urls <- line
						sent = true
						break
					}
				}
			}
			if rerr != nil {
				break
			}
		}
		if err := cmd.Wait(); err != nil {
			l.err = protonError([]string{"auth", "login"}, err, stderr.String()+"\n"+all.String())
		} else if _, serr := os.Stat(filepath.Join(dir, ProtonSessionFile)); serr != nil {
			l.err = errors.New("proton-drive auth login: no session was saved")
		}
		close(urls)
		close(l.done)
	}()
	select {
	case u, ok := <-urls:
		if !ok {
			<-l.done
			os.RemoveAll(dir)
			return nil, l.err
		}
		l.URL = u
		return l, nil
	case <-time.After(wait):
		cmd.Process.Kill()
		<-l.done
		os.RemoveAll(dir)
		return nil, errors.New("proton-drive auth login: no sign-in URL")
	}
}

// Done is closed once the sign-in finished; Err then says how it went.
func (l *ProtonLogin) Done() <-chan struct{} { return l.done }
func (l *ProtonLogin) Err() error            { return l.err }

// Session reads the session files of a finished sign-in, as target config.
func (l *ProtonLogin) Session() (map[string]string, error) {
	cfg := map[string]string{}
	for _, f := range []string{ProtonSessionFile, ProtonClientFile} {
		data, err := os.ReadFile(filepath.Join(l.Dir, f))
		if err != nil {
			return nil, err
		}
		cfg[f] = string(data)
	}
	return cfg, nil
}

// ProtonAvailable reports whether bin can be run.
func ProtonAvailable(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}
