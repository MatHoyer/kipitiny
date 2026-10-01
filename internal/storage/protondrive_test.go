package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeProtonDrive writes a stand-in for proton-drive that keeps "the drive"
// in root, answering the commands the driver uses the way the real CLI does
// (checked against proton-drive 0.8.0). Each run rewrites the session file,
// as a token refresh would.
func fakeProtonDrive(t *testing.T, root string) string {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
root=%q
cd "$PROTON_DRIVE_CACHE_DIR" 2>/dev/null || true
if [ "$1 $2" = "auth login" ]; then
  echo "Sign in in your browser. Keep the terminal open. Waiting for authentication to complete..."
  echo "Open following URL manually (can be on another device) if browser did not open automatically:"
  echo "https://account.proton.me/desktop/login?app=drive#payload=test"
  sleep 0.2
  echo '{"session":{"uid":"u"}}' > auth-session.json
  echo '{"clientUid":"c"}' > clientUid.json
  echo "Authentication successful"
  exit 0
fi
[ -f auth-session.json ] || { echo "Not signed in" >&2; exit 1; }
n=$(cat "$root/runs" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$root/runs"
echo "{\"session\":{\"uid\":\"u\",\"accessToken\":\"t$n\"}}" > auth-session.json
shift # filesystem
cmd=$1; shift
case $cmd in
upload)
  while [ "${1#-}" != "$1" ]; do case $1 in --skip-thumbnails) shift;; *) shift 2;; esac; done
  [ "$2" = /my-files ] || { echo "unexpected parent $2"; exit 1; }
  mkdir -p "$root/my-files" && cp -R "$1" "$root/my-files/" && echo "Uploaded" ;;
download)
  shift 2 # --file-conflict-strategy remove
  src="$root$1"; [ -f "$src" ] || { echo "Node not found: $(basename "$1")"; exit 1; }
  cp "$src" "$2/" ;;
trash)
  src="$root$1"; [ -e "$src" ] || { echo "Node not found: $(basename "$1")"; exit 1; }
  mkdir -p "$root/trash" && mv "$src" "$root/trash/" ;;
delete)
  case $1 in /trash/*) ;; *) echo "You can permanently delete items only from trash. Trash your files first."; exit 1;; esac
  [ -e "$root$1" ] || { echo "Trashed node not found"; exit 1; }
  rm -rf "$root$1" ;;
info)
  echo '{"type":"folder","ownedBy":{"email":"me@proton.me"}}' ;;
*) echo "unknown $cmd"; exit 1 ;;
esac
`, root)
	bin := filepath.Join(t.TempDir(), "proton-drive")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestProtonDrive(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	bin := fakeProtonDrive(t, root)
	work := t.TempDir()

	l, err := StartProtonLogin(ctx, bin, work, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(l.URL, "https://account.proton.me/") {
		t.Fatalf("url = %q", l.URL)
	}
	<-l.Done()
	if l.Err() != nil {
		t.Fatal(l.Err())
	}
	session, err := l.Session()
	if err != nil {
		t.Fatal(err)
	}

	var saved map[string]string
	p := &ProtonDrive{
		bin: bin, work: work, id: "t1", prefix: "kipitiny", config: session,
		save: func(_ context.Context, id string, cfg map[string]string) error {
			saved = cfg
			return nil
		},
	}
	if err := p.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Put(ctx, "pg/shop/db-1.dump", strings.NewReader("one")); err != nil {
		t.Fatal(err)
	}
	// A second object in the same folders merges into them.
	if err := p.Put(ctx, "pg/shop/db-2.dump", strings.NewReader("two")); err != nil {
		t.Fatal(err)
	}
	rc, err := p.Get(ctx, "pg/shop/db-1.dump")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	if err := rc.Close(); err != nil || string(body) != "one" {
		t.Fatalf("get = %q, %v", body, err)
	}
	if _, err := p.Get(ctx, "pg/shop/missing.dump"); err == nil {
		t.Fatal("missing object: no error")
	}
	if err := p.Delete(ctx, "pg/shop/db-1.dump"); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(ctx, "pg/shop/db-1.dump"); err != nil {
		t.Fatalf("deleting twice: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "my-files/kipitiny/pg/shop/db-2.dump")); err != nil {
		t.Fatalf("other object gone: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "trash")); len(entries) != 0 {
		t.Fatalf("left in trash: %v", entries)
	}
	if email, err := p.Account(ctx); err != nil || email != "me@proton.me" {
		t.Fatalf("account = %q, %v", email, err)
	}
	// The CLI rewrote the session on every run: the latest is saved.
	if !strings.Contains(saved[ProtonSessionFile], "accessToken") || saved[ProtonClientFile] == "" {
		t.Fatalf("saved = %v", saved)
	}
	if entries, _ := os.ReadDir(work); len(entries) != 1 { // the login dir only
		t.Fatalf("leftover session dirs: %v", entries)
	}
}

func TestProtonDriveNotSignedIn(t *testing.T) {
	p := &ProtonDrive{bin: "true", work: t.TempDir()}
	if err := p.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("err = %v", err)
	}
}
