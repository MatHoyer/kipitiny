package core

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	maxServiceFiles     = 100
	maxServiceFileBytes = 4 << 20 // all of a service's files together
)

// normalizeFiles sets each file's size and sorts them by path. A file
// sent back without its content (as API responses give it) keeps the
// content old has at that path.
func normalizeFiles(files, old []store.ServiceFile) []store.ServiceFile {
	out := make([]store.ServiceFile, 0, len(files))
	for _, f := range files {
		f.Path = strings.TrimSpace(f.Path)
		if f.Content == nil && f.Size > 0 {
			if i := slices.IndexFunc(old, func(o store.ServiceFile) bool { return o.Path == f.Path }); i >= 0 {
				f.Content = old[i].Content
			}
		}
		f.Size = len(f.Content)
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b store.ServiceFile) int { return strings.Compare(a.Path, b.Path) })
	return out
}

func validateFiles(s store.Service) error {
	if len(s.Files) == 0 {
		return nil
	}
	if len(s.Files) > maxServiceFiles {
		return fmt.Errorf("%w: at most %d files", ErrInvalid, maxServiceFiles)
	}
	if s.Runtime.ReadOnly {
		return fmt.Errorf("%w: files can't be written into a read-only root filesystem: drop read_only, or mount a volume", ErrInvalid)
	}
	total := 0
	for i, f := range s.Files {
		total += len(f.Content)
		if !path.IsAbs(f.Path) || path.Clean(f.Path) != f.Path || f.Path == "/" {
			return fmt.Errorf("%w: file path %q must be a clean absolute path", ErrInvalid, f.Path)
		}
		if i > 0 && s.Files[i-1].Path == f.Path {
			return fmt.Errorf("%w: two files at %s", ErrInvalid, f.Path)
		}
		if f.Mode < 0 || f.Mode > 0o7777 {
			return fmt.Errorf("%w: file %s: invalid mode %o", ErrInvalid, f.Path, f.Mode)
		}
		under := func(dir string) bool {
			return f.Path == dir || strings.HasPrefix(f.Path, strings.TrimSuffix(dir, "/")+"/")
		}
		for _, v := range s.Volumes {
			if under(v.Path) {
				return fmt.Errorf("%w: file %s is in volume %s: it would overwrite the volume's data", ErrInvalid, f.Path, v.Name)
			}
		}
		for dir := range s.Runtime.Tmpfs {
			if under(dir) {
				return fmt.Errorf("%w: file %s is in tmpfs %s, which hides it", ErrInvalid, f.Path, dir)
			}
		}
		if under(probeDir) {
			return fmt.Errorf("%w: file %s is in the manager's own mount", ErrInvalid, f.Path)
		}
	}
	if total > maxServiceFileBytes {
		return fmt.Errorf("%w: a service's files take at most %d MB", ErrInvalid, maxServiceFileBytes>>20)
	}
	return nil
}

// serviceFiles are the files written into svc's containers, secrets with
// their references resolved.
func serviceFiles(svc store.Service, src envSources) []docker.File {
	out := make([]docker.File, 0, len(svc.Files))
	for _, f := range svc.Files {
		content := f.Content
		if f.Secret {
			content = []byte(resolveEnv(map[string]string{"v": string(content)}, src)["v"])
		}
		out = append(out, docker.File{Path: f.Path, Content: content, Mode: f.Mode})
	}
	return out
}

// refEnv is svc's env with its secret files as entries, so references in
// them are checked and fetched like the env's.
func refEnv(svc store.Service) map[string]string {
	env := maps.Clone(svc.Env)
	if env == nil {
		env = map[string]string{}
	}
	for _, f := range svc.Files {
		if f.Secret {
			env["file:"+f.Path] = string(f.Content)
		}
	}
	return env
}

func sameFiles(a, b []store.ServiceFile) bool {
	return slices.EqualFunc(a, b, func(x, y store.ServiceFile) bool {
		return x.Path == y.Path && x.Mode == y.Mode && x.Secret == y.Secret && x.Source == y.Source && string(x.Content) == string(y.Content)
	})
}
