package data

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

// Volume files: a service's volumes browsed through a volume helper (see
// volume_backup.go) mounting them under /v/<name>, read-only for databases.
// The helper stays up while used and goes after filesIdle; paths always reach
// it as exec arguments, and its scripts resolve symlinks and refuse anything
// outside the volume.
const (
	// FilesListMax caps the entries of one listing.
	FilesListMax = 5000
	// FilesTextMax caps a text file read whole (to show or edit it).
	FilesTextMax = 1 << 20

	filesIdle            = 5 * time.Minute
	filesBrowseTimeout   = 30 * time.Second
	filesTransferTimeout = time.Hour
)

// Exit codes of the helper scripts.
const (
	filesExitNotFound = 2
	filesExitOutside  = 3
	filesExitKind     = 4
	filesExitExists   = 5
	filesExitChanged  = 6
)

// filesGuard resolves $p (an absolute path under the volume root $root) and
// exits unless it exists inside the volume.
const filesGuard = `p=$(realpath "$p" 2>/dev/null) && [ -e "$p" ] || exit 2
case "$p" in "$root"|"$root"/*) ;; *) exit 3 ;; esac
`

// filesStat prints an entry's raw mode, size, mtime, uid and gid, then its
// name and symlink target, each NUL-terminated (names may hold newlines).
const filesStat = `st() { stat -c "%f %s %Y %u %g" "$1"; printf '%s\0' "$2"; [ -L "$1" ] && readlink "$1" | tr -d '\n'; printf '\0'; }
`

// VolumeEntry is a file, folder or symlink in a volume; at the root, a volume.
type VolumeEntry struct {
	Name     string    `json:"name"`
	Type     string    `json:"type"` // "volume", "dir", "file", "link" or "other"
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified,omitzero"`
	Mode     string    `json:"mode,omitempty"` // permission bits, octal
	UID      int       `json:"uid"`
	GID      int       `json:"gid"`
	// Target is a symlink's, not followed.
	Target string `json:"target,omitempty"`
	// MountPath is where a volume is mounted in the service's containers.
	MountPath string `json:"mountPath,omitempty"`
}

type VolumeListing struct {
	Path     string        `json:"path"`
	ReadOnly bool          `json:"readOnly"`
	Entries  []VolumeEntry `json:"entries"`
	// Truncated means more than FilesListMax entries; the rest are left out.
	Truncated bool `json:"truncated"`
}

type VolumeFile struct {
	VolumeEntry
	Path     string `json:"path"`
	ReadOnly bool   `json:"readOnly"`
	Content  string `json:"content"`
}

// fileVolume is a volume of a service as the browser shows it, or the
// filesystem of one of its containers (name "@container" for the first
// running replica, "@<container ID>" for a given one).
type fileVolume struct {
	name, mountPath string
	container       bool
}

// containerPrefix starts the path of a container's filesystem; volume names
// can't hold "@".
const containerPrefix = "@"

// FilesContainer names the first running replica's filesystem in paths.
const FilesContainer = containerPrefix + "container"

var containerRefRe = regexp.MustCompile(`^@(container|[0-9a-f]{12,64})$`)

// root is where the volume's paths start: its folder in the helper, or ""
// for a container's filesystem (paths are then absolute in the container).
func (v fileVolume) root() string {
	if v.container {
		return ""
	}
	return helperVolumeRoot + "/" + v.name
}

// top is the volume's own folder: root, or "/" for a container.
func (v fileVolume) top() string {
	if v.container {
		return "/"
	}
	return v.root()
}

// display turns an absolute path in the volume back into "<volume>/<path>".
func (v fileVolume) display(abs string) string {
	return strings.TrimSuffix(v.name+strings.TrimPrefix(abs, v.root()), "/")
}

// fileVolumes lists what can be browsed: a database's data volume, or an
// app's volumes.
func fileVolumes(svc domain.Service) []fileVolume {
	switch svc.Kind {
	case domain.ServiceKindPostgres:
		return []fileVolume{{name: dataVolumeName, mountPath: pgVolumeMount}}
	case domain.ServiceKindRedis:
		return []fileVolume{{name: dataVolumeName, mountPath: redisVolumeMount}}
	case domain.ServiceKindMySQL, domain.ServiceKindMariaDB:
		return []fileVolume{{name: dataVolumeName, mountPath: mysqlVolumeMount}}
	case domain.ServiceKindMongoDB:
		return []fileVolume{{name: dataVolumeName, mountPath: mongoVolumeMount}}
	}
	vols := make([]fileVolume, len(svc.Volumes))
	for i, v := range svc.Volumes {
		vols[i] = fileVolume{name: v.Name, mountPath: v.Path}
	}
	return vols
}

// filesReadOnly reports whether svc's volumes are browsed read-only.
func filesReadOnly(svc domain.Service) bool {
	return svc.Kind.IsDatabase()
}

// volumePath splits p ("<volume>/<path>") into the volume and the absolute
// path in the helper, or in the container for "@container/<path>". "" is the
// root, where the volumes are listed.
func volumePath(svc domain.Service, p string) (vol fileVolume, abs string, err error) {
	if strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) {
		return vol, "", fmt.Errorf("%w: path %q must be relative to the volumes", ErrInvalid, p)
	}
	var segs []string
	for s := range strings.SplitSeq(p, "/") {
		switch s {
		case "", ".":
		case "..":
			return vol, "", fmt.Errorf("%w: path %q must not contain ..", ErrInvalid, p)
		default:
			segs = append(segs, s)
		}
	}
	if len(segs) == 0 {
		return vol, "", nil
	}
	if strings.HasPrefix(segs[0], containerPrefix) {
		if !containerRefRe.MatchString(segs[0]) {
			return vol, "", fmt.Errorf("%w: %q: use %s or @<container ID>", ErrInvalid, segs[0], FilesContainer)
		}
		return fileVolume{name: segs[0], mountPath: "/", container: true}, "/" + strings.Join(segs[1:], "/"), nil
	}
	i := slices.IndexFunc(fileVolumes(svc), func(v fileVolume) bool { return v.name == segs[0] })
	if i < 0 {
		return vol, "", fmt.Errorf("volume %q of %s: %w", segs[0], svc.Name, domain.ErrNotFound)
	}
	return fileVolumes(svc)[i], helperVolumeRoot + "/" + strings.Join(segs, "/"), nil
}

// ListVolumeFiles lists a folder of svc's volumes, or the volumes at "".
func (c *Data) ListVolumeFiles(ctx context.Context, serviceID, p string) (VolumeListing, error) {
	if err := c.mgr.RequireOn(ctx, serviceID, identity.PermFiles); err != nil {
		return VolumeListing{}, err
	}
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return VolumeListing{}, err
	}
	vol, abs, err := volumePath(svc, p)
	if err != nil {
		return VolumeListing{}, err
	}
	l := VolumeListing{ReadOnly: filesReadOnly(svc), Entries: []VolumeEntry{}}
	if abs == "" {
		for _, v := range fileVolumes(svc) {
			l.Entries = append(l.Entries, VolumeEntry{Name: v.name, Type: "volume", MountPath: v.mountPath})
		}
		l.Entries = append(l.Entries, VolumeEntry{Name: FilesContainer, Type: "container", MountPath: "/"})
		return l, nil
	}
	l.Path = vol.display(abs)
	ctx, cancel := context.WithTimeout(ctx, filesBrowseTimeout)
	defer cancel()
	script := filesGuard + filesStat + `[ -d "$p" ] || exit 4
cd "$p" || exit 2
for f in .[!.]* ..?* *; do [ -e "./$f" ] || [ -L "./$f" ] && st "./$f" "$f"; done
exit 0`
	err = c.filesExec(ctx, svc, vol, filesCmd(script, vol, abs), func(r io.Reader) error {
		return readEntries(r, func(e VolumeEntry) error {
			if len(l.Entries) == FilesListMax {
				l.Truncated = true
				return errStopLines
			}
			l.Entries = append(l.Entries, e)
			return nil
		})
	})
	if err != nil {
		return VolumeListing{}, filesError(p, err)
	}
	slices.SortFunc(l.Entries, func(a, b VolumeEntry) int {
		if (a.Type == "dir") != (b.Type == "dir") {
			if a.Type == "dir" {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return l, nil
}

// StatVolumePath describes one path of svc's volumes, symlinks followed.
func (c *Data) StatVolumePath(ctx context.Context, serviceID, p string) (VolumeEntry, error) {
	if err := c.mgr.RequireOn(ctx, serviceID, identity.PermFiles); err != nil {
		return VolumeEntry{}, err
	}
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return VolumeEntry{}, err
	}
	vol, abs, err := volumePath(svc, p)
	if err != nil {
		return VolumeEntry{}, err
	}
	if abs == "" {
		return VolumeEntry{Type: "dir"}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, filesBrowseTimeout)
	defer cancel()
	var e VolumeEntry
	err = c.filesExec(ctx, svc, vol, filesCmd(filesGuard+filesStat+`st "$p" "${p##*/}"`, vol, abs), func(r io.Reader) error {
		return readEntries(r, func(got VolumeEntry) error { e = got; return nil })
	})
	if err != nil {
		return VolumeEntry{}, filesError(p, err)
	}
	if abs == vol.top() {
		e.Type, e.Name, e.MountPath = "volume", vol.name, vol.mountPath
		if vol.container {
			e.Type = "container"
		}
	}
	return e, nil
}

// ReadVolumeFile reads a text file of svc's volumes whole, up to
// FilesTextMax; binary files are refused (download them). Contents may hold
// secrets, like backups: admin only.
func (c *Data) ReadVolumeFile(ctx context.Context, serviceID, p string) (VolumeFile, error) {
	if err := c.mgr.RequireOn(ctx, serviceID, identity.PermFiles); err != nil {
		return VolumeFile{}, err
	}
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return VolumeFile{}, err
	}
	vol, abs, err := volumePath(svc, p)
	if err != nil {
		return VolumeFile{}, err
	}
	if abs == "" {
		return VolumeFile{}, fmt.Errorf("%w: %q is not a file", ErrInvalid, p)
	}
	ctx, cancel := context.WithTimeout(ctx, filesBrowseTimeout)
	defer cancel()
	script := filesGuard + filesStat + `[ -f "$p" ] || exit 4
st "$p" "${p##*/}"
head -c ` + strconv.Itoa(FilesTextMax+1) + ` "$p"`
	f := VolumeFile{Path: vol.display(abs), ReadOnly: filesReadOnly(svc)}
	err = c.filesExec(ctx, svc, vol, filesCmd(script, vol, abs), func(r io.Reader) error {
		br := bufio.NewReader(r)
		e, err := readEntry(br)
		if err != nil {
			return err
		}
		f.VolumeEntry = e
		b, err := io.ReadAll(io.LimitReader(br, FilesTextMax+1))
		if err != nil {
			return err
		}
		if len(b) > FilesTextMax {
			return fmt.Errorf("%w: the file %s is over %d KiB: download it instead", ErrInvalid, p, FilesTextMax>>10)
		}
		if bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b) {
			return fmt.Errorf("%w: the file %s is not text: download it instead", ErrInvalid, p)
		}
		f.Content = string(b)
		return nil
	})
	if err != nil {
		return VolumeFile{}, filesError(p, err)
	}
	return f, nil
}

// DownloadVolumeFile streams a file of svc's volumes to w as it is; admin
// only.
func (c *Data) DownloadVolumeFile(ctx context.Context, serviceID, p string, w io.Writer) error {
	if err := c.mgr.RequireOn(ctx, serviceID, identity.PermFiles); err != nil {
		return err
	}
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return err
	}
	vol, abs, err := volumePath(svc, p)
	if err != nil {
		return err
	}
	if abs == "" {
		return fmt.Errorf("%w: %q is not a file", ErrInvalid, p)
	}
	ctx, cancel := context.WithTimeout(ctx, filesTransferTimeout)
	defer cancel()
	err = c.filesExec(ctx, svc, vol, filesCmd(filesGuard+`[ -f "$p" ] || exit 4
cat "$p"`, vol, abs), func(r io.Reader) error {
		_, err := io.Copy(w, r)
		return err
	})
	return filesError(p, err)
}

// ArchiveVolumeFiles streams a gzipped tar of entries (names in folder dir,
// e.g. a selection; empty for the folder itself) of svc's volumes to w.
// Symlinks are archived as links. Admin only.
func (c *Data) ArchiveVolumeFiles(ctx context.Context, serviceID, dir string, names []string, w io.Writer) error {
	if err := c.mgr.RequireOn(ctx, serviceID, identity.PermFiles); err != nil {
		return err
	}
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return err
	}
	vol, abs, err := volumePath(svc, dir)
	if err != nil {
		return err
	}
	if abs == "" {
		return fmt.Errorf("%w: pick a volume to download", ErrInvalid)
	}
	if vol.container && abs == "/" && len(names) == 0 {
		return fmt.Errorf("%w: pick the folders of the container to download, not all of it", ErrInvalid)
	}
	for _, n := range names {
		if n == "" || n == "." || n == ".." || strings.ContainsAny(n, "/\x00") {
			return fmt.Errorf("%w: %q is not a name in %s", ErrInvalid, n, dir)
		}
	}
	// The folder itself: archived from its parent, under its own name.
	script := filesGuard + `[ -d "$p" ] || exit 4
if [ $# -eq 0 ]; then set -- "${p##*/}"; p=${p%/*}; fi
cd "${p:-/}" || exit 2
for n; do [ -e "$n" ] || [ -L "$n" ] || exit 2; done
tar -czf - -- "$@"`
	ctx, cancel := context.WithTimeout(ctx, filesTransferTimeout)
	defer cancel()
	err = c.filesExec(ctx, svc, vol, append(filesCmd(script, vol, abs), names...), func(r io.Reader) error {
		_, err := io.Copy(w, r)
		return err
	})
	return filesError(dir, err)
}

// filesCmd runs script with $root and $p set from the arguments, then the
// remaining arguments as "$@".
func filesCmd(script string, vol fileVolume, abs string) []string {
	return []string{"sh", "-c", `root=$1 p=$2; shift 2
` + script, "sh", vol.root(), abs}
}

// filesError turns a helper script's exit code into an error for the caller.
func filesError(p string, err error) error {
	var ee *docker.ExecError
	if !errors.As(err, &ee) {
		return err
	}
	switch ee.ExitCode {
	case filesExitNotFound:
		return fmt.Errorf("%s: %w", p, domain.ErrNotFound)
	case filesExitOutside:
		return fmt.Errorf("%w: %s leads outside its volume", ErrInvalid, p)
	case filesExitKind:
		return fmt.Errorf("%w: %s is not the expected kind of entry (file or folder)", ErrInvalid, p)
	case filesExitExists:
		return fmt.Errorf("%w: %s already exists", domain.ErrConflict, p)
	case filesExitChanged:
		return fmt.Errorf("%w: %s changed since it was read", domain.ErrConflict, p)
	case 126, 127: // only in a container: its image lacks sh or a tool
		return fmt.Errorf("%w: the container's image lacks a shell or the tools to browse files (sh, stat, realpath, cat): use a volume", ErrInvalid)
	}
	return err
}

// readEntries reads the records filesStat prints and hands each to fn.
// Returning errStopLines from fn ends the read early.
func readEntries(r io.Reader, fn func(VolumeEntry) error) error {
	br := bufio.NewReader(r)
	for {
		e, err := readEntry(br)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
}

// readEntry reads one filesStat record: "mode size mtime uid gid\nname\0target\0".
func readEntry(br *bufio.Reader) (VolumeEntry, error) {
	meta, err := br.ReadString('\n')
	if err != nil {
		if err == io.EOF && meta == "" {
			return VolumeEntry{}, io.EOF
		}
		return VolumeEntry{}, fmt.Errorf("read listing: %w", err)
	}
	name, err := br.ReadString(0)
	if err != nil {
		return VolumeEntry{}, fmt.Errorf("read listing: %w", err)
	}
	target, err := br.ReadString(0)
	if err != nil {
		return VolumeEntry{}, fmt.Errorf("read listing: %w", err)
	}
	f := strings.Fields(meta)
	if len(f) != 5 {
		return VolumeEntry{}, fmt.Errorf("read listing: bad line %q", meta)
	}
	var n [5]int64
	for i, s := range f {
		base := 10
		if i == 0 {
			base = 16
		}
		if n[i], err = strconv.ParseInt(s, base, 64); err != nil {
			return VolumeEntry{}, fmt.Errorf("read listing: bad line %q", meta)
		}
	}
	e := VolumeEntry{
		Name:     strings.TrimSuffix(name, "\x00"),
		Size:     n[1],
		Modified: time.Unix(n[2], 0).UTC(),
		Mode:     fmt.Sprintf("%04o", n[0]&0o7777),
		UID:      int(n[3]),
		GID:      int(n[4]),
		Target:   strings.TrimSuffix(target, "\x00"),
	}
	switch n[0] & 0o170000 {
	case 0o040000:
		e.Type = "dir"
	case 0o100000:
		e.Type = "file"
	case 0o120000:
		e.Type = "link"
	default:
		e.Type = "other"
	}
	if e.Type == "dir" {
		e.Size = 0 // the directory's own block, meaningless here
	}
	return e, nil
}
