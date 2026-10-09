package protonpass

import (
	"context"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
)

// fakeRunner stands in for pass-cli: run prefixes each reference with
// "resolved:", login accepts pst_ok::k from stdin, every command is logged.
type fakeRunner struct {
	calls []string
	envs  [][]string
	reset bool
}

func (f *fakeRunner) Run(_ context.Context, cmd, env []string, stdin io.Reader) ([]byte, []byte, error) {
	f.envs = append(f.envs, env)
	if cmd[0] == "sh" {
		in, _ := io.ReadAll(stdin)
		f.calls = append(f.calls, "login")
		if string(in) != "pst_ok::k\n" {
			return nil, []byte("2026-10-09T09:51:15.411250Z ERROR pass-cli/src/main.rs:335: nope\nError: Invalid token\n"), errors.New("exit code 1")
		}
		return nil, nil, nil
	}
	f.calls = append(f.calls, strings.Join(cmd[1:], " "))
	if cmd[1] != "run" {
		return nil, nil, nil
	}
	var out []string
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if strings.Contains(v, "missing") {
			return nil, []byte("Error: Failed to resolve secrets\n\nCaused by:\n    0: Could not find item\n"), errors.New("exit code 1")
		}
		out = append(out, k+"=resolved:"+v+"\nsecond line")
	}
	out = append(out, "PATH=/usr/bin")
	return []byte(strings.Join(out, "\x00") + "\x00"), nil, nil
}

func (f *fakeRunner) Reset(context.Context) error { f.reset = true; return nil }

func TestResolve(t *testing.T) {
	f := &fakeRunner{}
	c := New(f)
	refs := []string{"pass://My Vault/My Item/password", "pass://Work/API/key=x"}
	got, err := c.Resolve(context.Background(), refs)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		refs[0]: "resolved:" + refs[0] + "\nsecond line",
		refs[1]: "resolved:" + refs[1] + "\nsecond line",
	}
	if !maps.Equal(got, want) {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
	if f.calls[0] != "run --no-masking -- env -0" {
		t.Errorf("calls = %q", f.calls)
	}

	_, err = c.Resolve(context.Background(), []string{"pass://Work/missing/key"})
	if err == nil || err.Error() != "Failed to resolve secrets: Could not find item" {
		t.Errorf("err = %v", err)
	}
}

func TestParseEnv0(t *testing.T) {
	refs := []string{"pass://a", "pass://b"}
	if _, err := parseEnv0([]byte("KIPITINY_SECRET_0=x\x00"), refs); err == nil {
		t.Error("missing value accepted")
	}
	got, err := parseEnv0([]byte("KIPITINY_SECRET_1=b=c\x00KIPITINY_SECRET_0=\x00KIPITINY_SECRET_7=x\x00"), refs)
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(got, map[string]string{"pass://a": "", "pass://b": "b=c"}) {
		t.Errorf("got %q", got)
	}
}

func TestConnect(t *testing.T) {
	f := &fakeRunner{}
	c := New(f)
	ctx := context.Background()
	if err := c.Connect(ctx, "nope"); err == nil {
		t.Error("malformed token accepted")
	}
	if err := c.Connect(ctx, "pst_bad::k"); err == nil || err.Error() != "Invalid token" {
		t.Errorf("err = %v", err)
	}
	if err := c.Connect(ctx, "pst_ok::k"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"logout --force", "login", "logout --force", "login", "info"}; !slices.Equal(f.calls, want) {
		t.Errorf("calls = %q, want %q", f.calls, want)
	}
	for _, env := range f.envs {
		for _, kv := range env {
			if strings.Contains(kv, "pst_") {
				t.Errorf("token in env: %q", kv)
			}
		}
	}
	if err := c.Disconnect(ctx); err != nil || !f.reset {
		t.Errorf("disconnect: %v, reset %v", err, f.reset)
	}
}
