package protonpass

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/secrets"
)

func TestMain(m *testing.M) {
	// The fake pass-cli starts this binary as the self command.
	if os.Getenv("PRINT_SECRETS") == "1" {
		if err := secrets.PrintEnv(os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeCLI stands in for pass-cli: run prefixes each reference with
// "resolved:", login accepts pst_ok::k, every call is logged to $HOME/calls.
const fakeCLI = `#!/bin/sh
echo "$@" >> "$HOME/calls"
case "$1" in
run)
	shift 3
	for v in $(env | grep '^KIPITINY_SECRET_' | cut -d= -f1); do
		case "$(eval echo \"\$$v\")" in *missing*) echo "Error: Failed to resolve secrets" >&2; echo "Caused by:" >&2; echo "    0: Could not find item" >&2; exit 1;; esac
		eval "export $v=\"resolved:\$$v\""
	done
	PRINT_SECRETS=1 exec "$@";;
login)
	[ "$PROTON_PASS_PERSONAL_ACCESS_TOKEN" = "pst_ok::k" ] || { echo "Error: Invalid token" >&2; exit 1; };;
esac
`

func newFake(t *testing.T) (*Client, string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "pass-cli")
	if err := os.WriteFile(bin, []byte(fakeCLI), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	return New(bin, dir, []string{os.Args[0]}), dir
}

func TestResolve(t *testing.T) {
	c, _ := newFake(t)
	refs := []string{"pass://My Vault/My Item/password", "pass://Work/API/key"}
	got, err := c.Resolve(context.Background(), refs)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{refs[0]: "resolved:" + refs[0], refs[1]: "resolved:" + refs[1]}
	if !maps.Equal(got, want) {
		t.Errorf("Resolve = %v, want %v", got, want)
	}

	_, err = c.Resolve(context.Background(), []string{"pass://Work/missing/key"})
	if err == nil || err.Error() != "Failed to resolve secrets: Could not find item" {
		t.Errorf("err = %v", err)
	}
}

func TestConnect(t *testing.T) {
	c, dir := newFake(t)
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
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if got := strings.Fields(strings.ReplaceAll(string(calls), "\n", " | ")); strings.Join(got, " ") != "logout --force | login | logout --force | login | info |" {
		t.Errorf("calls = %q", calls)
	}
}
