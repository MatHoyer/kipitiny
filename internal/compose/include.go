package compose

import (
	"path"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// maxIncludeDepth bounds nested includes and extends, which also stops
// cycles.
const maxIncludeDepth = 5

// includes parses the files of a top-level include and merges their
// services and project variables into f. Each file's paths are relative
// to itself.
func (p *parser) includes(n *yaml.Node, f *File, depth int) {
	var paths []string
	items := []*yaml.Node{n}
	if n.Kind == yaml.SequenceNode {
		items = n.Content
	}
	for _, item := range items {
		if item.Kind != yaml.MappingNode {
			paths = append(paths, p.str(item))
			continue
		}
		for k, v := range pairs(item) {
			switch k {
			case "path":
				if v.Kind == yaml.SequenceNode {
					for _, pv := range v.Content {
						paths = append(paths, p.str(pv))
					}
				} else {
					paths = append(paths, p.str(v))
				}
			case "project_directory", "env_file":
				p.warn("include %s is ignored", k)
			default:
				p.fail("include %s is not supported", k)
			}
		}
	}
	for _, inc := range paths {
		if depth >= maxIncludeDepth {
			p.fail("include %s: nested more than %d levels (a cycle?)", inc, maxIncludeDepth)
			return
		}
		files := p.repoFiles(inc)
		if files == nil {
			continue
		}
		if len(files) != 1 || files[0].Path != "" {
			p.fail("include %s must be a file", inc)
			continue
		}
		sub, warns, err := parse(files[0].Content, p.vars.under(path.Dir(inc)), depth+1)
		for _, w := range warns {
			p.warn("%s: %s", inc, w)
		}
		if err != nil {
			p.fail("%s: %s", inc, strings.TrimPrefix(err.Error(), "compose: "))
			continue
		}
		for name, s := range sub.Services {
			if _, dup := f.Services[name]; dup {
				p.fail("service %s is defined in %s and another file", name, inc)
				continue
			}
			f.Services[name] = s
		}
		for k, v := range sub.X.Variables {
			if f.X.Variables == nil {
				f.X.Variables = map[string]string{}
			}
			if _, set := f.X.Variables[k]; !set {
				f.X.Variables[k] = v
				if slices.Contains(sub.X.Secrets, k) {
					f.X.Secrets = append(f.X.Secrets, k)
				}
			}
		}
	}
}

// under is vars for a file in dir (relative to the compose file).
func (v Vars) under(dir string) Vars {
	if v.Files == nil || dir == "." {
		return v
	}
	read := v.Files
	v.Files = func(rel string) ([]store.ServiceFile, error) { return read(path.Join(dir, rel)) }
	return v
}

// resolveExtends replaces each service that extends another by the merge
// of the two, in place.
func (p *parser) resolveExtends(services *yaml.Node) {
	for name, n := range pairs(services) {
		if n.Kind == yaml.MappingNode && mapValue(n, "extends") != nil {
			p.ctx = "service " + name
			if merged := p.extended(name, n, services, ".", 0); merged != nil {
				*n = *merged
			}
			p.ctx = ""
		}
	}
}

// extended is n merged over the service it extends: one of services, or
// of another file (relative to dir). nil on an error.
func (p *parser) extended(name string, n, services *yaml.Node, dir string, depth int) *yaml.Node {
	ext := mapValue(n, "extends")
	if ext == nil {
		return n
	}
	if depth >= maxIncludeDepth {
		p.fail("extends nested more than %d levels (a cycle?)", maxIncludeDepth)
		return nil
	}
	baseName, file := "", ""
	if ext.Kind == yaml.ScalarNode {
		baseName = p.str(ext)
	} else {
		for k, v := range pairs(ext) {
			switch k {
			case "service":
				baseName = p.str(v)
			case "file":
				file = p.str(v)
			default:
				p.fail("extends %s is not supported", k)
			}
		}
	}
	baseDir, baseServices := dir, services
	if file != "" {
		files := p.repoFiles(path.Join(dir, file))
		if files == nil {
			return nil
		}
		if len(files) != 1 || files[0].Path != "" {
			p.fail("extends %s must be a file", file)
			return nil
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(files[0].Content, &doc); err != nil || len(doc.Content) == 0 {
			p.fail("extends %s: not a compose file", file)
			return nil
		}
		baseDir, baseServices = path.Join(dir, path.Dir(file)), mapValue(doc.Content[0], "services")
	}
	base := mapValue(baseServices, baseName)
	if base == nil || base.Kind != yaml.MappingNode || (file == "" && baseName == name) {
		p.fail("extends %s: no such service", baseName)
		return nil
	}
	base = p.extended(baseName, base, baseServices, baseDir, depth+1)
	if base == nil {
		return nil
	}
	base = deepCopy(base)
	if baseDir != dir {
		rebasePaths(base, baseDir, dir)
	}
	return mergeService(base, n)
}

// rebasePaths makes a service's relative paths (bind mounts, env_file),
// written relative to from, relative to to.
func rebasePaths(svc *yaml.Node, from, to string) {
	rebase := func(p string) string {
		rel, err := filepath.Rel(to, path.Join(from, p))
		if err != nil {
			return p
		}
		if rel = filepath.ToSlash(rel); !strings.HasPrefix(rel, "../") {
			rel = "./" + rel
		}
		return rel
	}
	if vols := mapValue(svc, "volumes"); vols != nil {
		for _, item := range vols.Content {
			if item.Kind == yaml.ScalarNode {
				if src, rest, ok := strings.Cut(item.Value, ":"); ok && isRepoPath(src) {
					item.Value = rebase(src) + ":" + rest
				}
			} else if src := mapValue(item, "source"); src != nil && isRepoPath(src.Value) {
				src.Value = rebase(src.Value)
			}
		}
	}
	if ef := mapValue(svc, "env_file"); ef != nil {
		items := []*yaml.Node{ef}
		if ef.Kind == yaml.SequenceNode {
			items = ef.Content
		}
		for _, item := range items {
			if item.Kind == yaml.MappingNode {
				item = mapValue(item, "path")
			}
			if item != nil && item.Kind == yaml.ScalarNode && !path.IsAbs(item.Value) {
				item.Value = rebase(item.Value)
			}
		}
	}
}

// Keys whose lists add up when a service extends another; the others
// replace the base's. Volumes, configs and secrets replace the base's at
// the same target.
var (
	appendKeys   = []string{"ports", "expose", "volumes", "configs", "secrets", "extra_hosts", "cap_add", "cap_drop", "devices", "dns", "env_file", "tmpfs", "security_opt"}
	keyedMapKeys = []string{"environment", "labels", "sysctls"}
)

// mergeService merges over (without its extends) into base, as Compose
// does: mappings merge, the lists of appendKeys add up, the rest is
// replaced.
func mergeService(base, over *yaml.Node) *yaml.Node {
	for i := 0; i+1 < len(over.Content); i += 2 {
		k, v := over.Content[i].Value, over.Content[i+1]
		if k == "extends" {
			continue
		}
		cur := mapValue(base, k)
		switch {
		case cur == nil:
			setMapValue(base, k, deepCopy(v))
		case slices.Contains(keyedMapKeys, k):
			setMapValue(base, k, mergeMaps(asMapping(cur), asMapping(v)))
		case slices.Contains(appendKeys, k) && cur.Kind == yaml.SequenceNode && v.Kind == yaml.SequenceNode:
			out := deepCopy(cur)
			for _, item := range v.Content {
				if t := listTarget(k, item); t != "" {
					out.Content = slices.DeleteFunc(out.Content, func(o *yaml.Node) bool { return listTarget(k, o) == t })
				}
				out.Content = append(out.Content, deepCopy(item))
			}
			setMapValue(base, k, out)
		case cur.Kind == yaml.MappingNode && v.Kind == yaml.MappingNode:
			setMapValue(base, k, mergeMaps(cur, v))
		default:
			setMapValue(base, k, deepCopy(v))
		}
	}
	return base
}

// listTarget is where a volume, config or secret goes, to replace the
// base's at the same place; "" for other lists.
func listTarget(k string, item *yaml.Node) string {
	switch k {
	case "volumes":
		if item.Kind == yaml.ScalarNode {
			parts := strings.Split(item.Value, ":")
			if len(parts) == 1 {
				return parts[0]
			}
			return parts[1]
		}
		if t := mapValue(item, "target"); t != nil {
			return t.Value
		}
	case "configs", "secrets":
		if item.Kind == yaml.ScalarNode {
			return item.Value
		}
		if t := mapValue(item, "target"); t != nil {
			return t.Value
		}
		if s := mapValue(item, "source"); s != nil {
			return s.Value
		}
	}
	return ""
}

// mergeMaps merges mapping over into a copy of base, recursively.
func mergeMaps(base, over *yaml.Node) *yaml.Node {
	out := deepCopy(base)
	for i := 0; i+1 < len(over.Content); i += 2 {
		k, v := over.Content[i].Value, over.Content[i+1]
		if cur := mapValue(out, k); cur != nil && cur.Kind == yaml.MappingNode && v.Kind == yaml.MappingNode {
			setMapValue(out, k, mergeMaps(cur, v))
		} else {
			setMapValue(out, k, deepCopy(v))
		}
	}
	return out
}

// asMapping turns a KEY=value list into a mapping (KEY alone is null).
func asMapping(n *yaml.Node) *yaml.Node {
	if n.Kind != yaml.SequenceNode {
		return n
	}
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, item := range n.Content {
		k, v, ok := strings.Cut(item.Value, "=")
		val := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Style: yaml.DoubleQuotedStyle}
		if !ok {
			val = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null"}
		}
		out.Content = append(out.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, val)
	}
	return out
}

func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func setMapValue(n *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content[i+1] = v
			return
		}
	}
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
}

func deepCopy(n *yaml.Node) *yaml.Node {
	c := *n
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, child := range n.Content {
		c.Content[i] = deepCopy(child)
	}
	if n.Alias != nil {
		c.Alias = deepCopy(n.Alias)
	}
	return &c
}
