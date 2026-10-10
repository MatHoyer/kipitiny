package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/ids"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Writes to volume files need the admin scope, are refused on databases and
// are audited with their paths. What they create belongs to the owner of the
// folder it lands in (an image running as uid 1000 can use an upload), and a
// file replaced keeps its owner and mode. Symlinks are resolved in parent
// folders only: deleting or moving a link acts on the link.

// filesWriteLib holds the shell functions of write scripts:
//
//	inside ROOT PATH  exits unless PATH is ROOT or under it
//	entry ROOT PATH   sets $e to PATH with its parent folder resolved; exits
//	                  unless that folder exists inside ROOT
//	mkp ROOT SEG...   creates the folders ROOT/SEG/... one level at a time,
//	                  refusing a symlink out of ROOT; sets $d to the last
const filesWriteLib = `inside() { case "$2" in "$1"|"$1"/*) ;; *) exit 3 ;; esac; }
own() { stat -c %u:%g "$1"; }
exists() { [ -e "$1" ] || [ -L "$1" ]; }
entry() { d=$(realpath "${2%/*}" 2>/dev/null) && [ -d "$d" ] || exit 2; inside "$1" "$d"; e="$d/${2##*/}"; }
mkp() {
  r=$1; d=$1; shift
  for s; do
    n="$d/$s"
    if exists "$n"; then
      n=$(realpath "$n" 2>/dev/null) && [ -e "$n" ] || exit 2
      inside "$r" "$n"; [ -d "$n" ] || exit 4
    else
      mkdir "$n" && chown "$(own "$d")" "$n" || exit 1
    fi
    d=$n
  done
}
`

// WriteOptions control how WriteVolumeFile treats an existing file.
type WriteOptions struct {
	// Overwrite replaces an existing file; without it, one is a conflict.
	Overwrite bool
	// Modified, if set, is the file's modification time when it was read:
	// a file changed since is a conflict (an editor's save).
	Modified time.Time
}

// volumeEntry is volumePath for a path inside a volume, never a volume
// itself. rel holds its segments below the volume.
func volumeEntry(svc store.Service, p string) (vol fileVolume, abs string, rel []string, err error) {
	vol, abs, err = volumePath(svc, p)
	if err != nil {
		return vol, "", nil, err
	}
	root := helperVolumeRoot + "/" + vol.name
	if abs == "" || abs == root {
		return vol, "", nil, fmt.Errorf("%w: give a path inside a volume, not %q", ErrInvalid, p)
	}
	return vol, abs, strings.Split(strings.TrimPrefix(abs, root+"/"), "/"), nil
}

func volumeRoot(vol fileVolume) string { return helperVolumeRoot + "/" + vol.name }

// writableService loads a service whose volumes may be written.
func (c *Core) writableService(ctx context.Context, id string) (store.Service, error) {
	if err := Require(ctx, store.ScopeAdmin); err != nil {
		return store.Service{}, err
	}
	svc, err := c.store.GetService(ctx, id)
	if err != nil {
		return store.Service{}, err
	}
	if filesReadOnly(svc) {
		return store.Service{}, fmt.Errorf("%w: the volumes of databases are read-only", ErrInvalid)
	}
	return svc, nil
}

// auditFiles records a write on a service's volumes.
func (c *Core) auditFiles(ctx context.Context, action, serviceID, what string, err error) {
	status := http.StatusOK
	if err != nil {
		status = http.StatusBadRequest
	}
	c.Audit(ctx, "volume "+action, serviceID+":"+what, status, err)
}

// filesRun runs a write script (filesWriteLib loaded, arguments as "$@").
func (c *Core) filesRun(ctx context.Context, svc store.Service, script string, args ...string) error {
	return c.filesExec(ctx, svc, append([]string{"sh", "-c", filesWriteLib + script, "sh"}, args...), func(r io.Reader) error {
		_, err := io.Copy(io.Discard, r)
		return err
	})
}

// MakeVolumeDir creates a folder, and its missing parents, in svc's volumes.
func (c *Core) MakeVolumeDir(ctx context.Context, serviceID, p string) (err error) {
	defer func() { c.auditFiles(ctx, "mkdir", serviceID, p, err) }()
	svc, err := c.writableService(ctx, serviceID)
	if err != nil {
		return err
	}
	vol, _, rel, err := volumeEntry(svc, p)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, filesBrowseTimeout)
	defer cancel()
	script := `root=$1 last=$2; shift 2
mkp "$root" "$@"
exists "$d/$last" && exit 5
mkdir "$d/$last" && chown "$(own "$d")" "$d/$last"`
	args := append([]string{volumeRoot(vol), rel[len(rel)-1]}, rel[:len(rel)-1]...)
	return filesError(p, c.filesRun(ctx, svc, script, args...))
}

// WriteVolumeFile streams r into a file of svc's volumes, creating missing
// parent folders. The content goes to a temporary file next to it, moved into
// place only once all of r arrived: a cut upload changes nothing.
func (c *Core) WriteVolumeFile(ctx context.Context, serviceID, p string, r io.Reader, opts WriteOptions) (e VolumeEntry, err error) {
	defer func() { c.auditFiles(ctx, "write", serviceID, p, err) }()
	svc, err := c.writableService(ctx, serviceID)
	if err != nil {
		return VolumeEntry{}, err
	}
	vol, _, rel, err := volumeEntry(svc, p)
	if err != nil {
		return VolumeEntry{}, err
	}
	mod := ""
	if !opts.Modified.IsZero() {
		mod = strconv.FormatInt(opts.Modified.Unix(), 10)
	}
	tmp := ".kipitiny-upload-" + strings.ToLower(ids.New())
	over := "0"
	if opts.Overwrite {
		over = "1"
	}
	// prelude creates the folder and sets $n, the target, and $t, the
	// temporary file next to it.
	prelude := `root=$1 name=$2 tmp=$3 over=$4 mod=$5; shift 5
mkp "$root" "$@"
n="$d/$name" t="$d/$tmp"
`
	// check refuses what may not be written; an existing target must be a
	// file (a symlink to a file inside the volume is written through).
	check := `if exists "$n"; then
  [ "$over" = 1 ] || exit 5
  n=$(realpath "$n" 2>/dev/null) && [ -e "$n" ] || exit 2
  inside "$root" "$n"; [ -f "$n" ] || exit 4
  [ -z "$mod" ] || [ "$(stat -c %Y "$n")" = "$mod" ] || exit 6
elif [ -n "$mod" ]; then
  exit 6
fi
`
	args := append([]string{volumeRoot(vol), rel[len(rel)-1], tmp, over, mod}, rel[:len(rel)-1]...)
	cmd := func(script string) []string {
		return append([]string{"sh", "-c", filesWriteLib + prelude + script, "sh"}, args...)
	}
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), filesBrowseTimeout)
		defer cancel()
		if err := c.filesRun(ctx, svc, prelude+`rm -f "$t"`, args...); err != nil {
			c.log.Warn("cannot remove a cut upload", "service", svc.ID, "path", p, "err", err)
		}
	}

	ctx, cancel := context.WithTimeout(ctx, filesTransferTimeout)
	defer cancel()
	in := &countingReader{r: r}
	var size strings.Builder
	err = c.filesExecIn(ctx, svc, docker.ExecOptions{Cmd: cmd(check + `cat > "$t" && stat -c %s "$t"`), Stdin: in}, func(out io.Reader) error {
		_, err := io.Copy(&size, out)
		return err
	})
	if err == nil && strings.TrimSpace(size.String()) != strconv.FormatInt(in.n, 10) {
		err = fmt.Errorf("upload cut short: %s of %d bytes written", strings.TrimSpace(size.String()), in.n)
	}
	if err != nil {
		cleanup()
		return VolumeEntry{}, filesError(p, err)
	}
	// Checked again: the folder or the file may have changed during the
	// upload. Whatever fails, the temporary file goes.
	finish := `trap 'rm -f "$t"' EXIT
` + check + `if [ -f "$n" ]; then chown "$(own "$n")" "$t" && chmod "$(stat -c %a "$n")" "$t"
else chown "$(own "$d")" "$t"; fi || exit 1
mv -f "$t" "$n" || exit 1
trap - EXIT
` + filesStat + `st "$n" "$name"`
	err = c.filesExec(ctx, svc, cmd(finish), func(out io.Reader) error {
		return readEntries(out, func(got VolumeEntry) error { e = got; return nil })
	})
	if err != nil {
		return VolumeEntry{}, filesError(p, err)
	}
	return e, nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// MoveVolumePath moves or renames an entry of svc's volumes, possibly to
// another of its volumes. The destination's folder must exist and the
// destination must not.
func (c *Core) MoveVolumePath(ctx context.Context, serviceID, from, to string) (err error) {
	defer func() { c.auditFiles(ctx, "move", serviceID, from+" -> "+to, err) }()
	return c.transferVolumePath(ctx, serviceID, from, to, `mv "$src" "$e"`)
}

// CopyVolumePath copies an entry of svc's volumes (a folder recursively,
// owners and modes kept), possibly to another of its volumes.
func (c *Core) CopyVolumePath(ctx context.Context, serviceID, from, to string) (err error) {
	defer func() { c.auditFiles(ctx, "copy", serviceID, from+" -> "+to, err) }()
	return c.transferVolumePath(ctx, serviceID, from, to, `cp -a "$src" "$e"`)
}

func (c *Core) transferVolumePath(ctx context.Context, serviceID, from, to, op string) error {
	svc, err := c.writableService(ctx, serviceID)
	if err != nil {
		return err
	}
	fromVol, fromAbs, _, err := volumeEntry(svc, from)
	if err != nil {
		return err
	}
	toVol, toAbs, _, err := volumeEntry(svc, to)
	if err != nil {
		return err
	}
	if toAbs == fromAbs || strings.HasPrefix(toAbs, fromAbs+"/") {
		return fmt.Errorf("%w: can't move or copy %s into itself", ErrInvalid, from)
	}
	// Deleting from the source first then checking the destination would
	// lose data; both are checked before anything moves.
	script := `entry "$1" "$2"; src=$e; exists "$src" || exit 2
entry "$3" "$4"; exists "$e" && exit 5
case "$e/" in "$src"/*) exit 4 ;; esac
` + op
	ctx, cancel := context.WithTimeout(ctx, filesTransferTimeout)
	defer cancel()
	err = c.filesRun(ctx, svc, script, volumeRoot(fromVol), fromAbs, volumeRoot(toVol), toAbs)
	if err != nil {
		// Which path the code is about isn't known; name both.
		return filesError(from+" -> "+to, err)
	}
	return nil
}

// DeleteVolumePaths deletes entries of svc's volumes, folders with their
// content. Every path must exist before anything is deleted.
func (c *Core) DeleteVolumePaths(ctx context.Context, serviceID string, paths []string) (err error) {
	defer func() { c.auditFiles(ctx, "delete", serviceID, strings.Join(paths, ", "), err) }()
	svc, err := c.writableService(ctx, serviceID)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("%w: nothing to delete", ErrInvalid)
	}
	args := make([]string, 0, 2*len(paths))
	for _, p := range paths {
		vol, abs, _, err := volumeEntry(svc, p)
		if err != nil {
			return err
		}
		args = append(args, volumeRoot(vol), abs)
	}
	script := `check() { while [ $# -gt 0 ]; do entry "$1" "$2"; exists "$e" || exit 2; shift 2; done; }
del() { while [ $# -gt 0 ]; do entry "$1" "$2"; rm -rf -- "$e" || exit 1; shift 2; done; }
check "$@"; del "$@"`
	ctx, cancel := context.WithTimeout(ctx, filesTransferTimeout)
	defer cancel()
	return filesError(strings.Join(paths, ", "), c.filesRun(ctx, svc, script, args...))
}
