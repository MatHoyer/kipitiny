package compose

import (
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// topFile is a top-level config or secret: inline content, a file of the
// repository, or a variable.
type topFile struct {
	content *string
	file    string
	env     string
}

// topFiles reads the top-level configs or secrets.
func (p *parser) topFiles(kind string, n *yaml.Node) map[string]topFile {
	out := map[string]topFile{}
	for name, v := range pairs(n) {
		var f topFile
		for k, val := range pairs(v) {
			switch k {
			case "content":
				s := p.str(val)
				f.content = &s
			case "file":
				f.file = p.str(val)
			case "environment":
				f.env = p.str(val)
			case "name":
				p.warn("%s %s: name is ignored", kind, name)
			default:
				p.fail("%s %s: %s is not supported (only content, file or environment)", kind, name, k)
			}
		}
		if btoi(f.content != nil)+btoi(f.file != "")+btoi(f.env != "") != 1 {
			p.fail("%s %s needs one of content, file or environment", kind, name)
		}
		out[name] = f
	}
	return out
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// fileRefs reads a service's configs or secrets and writes the files they
// name; kind is "config" or "secret".
func (p *parser) fileRefs(kind string, n *yaml.Node, defs map[string]topFile, files *[]store.ServiceFile) {
	if n.Kind != yaml.SequenceNode {
		p.fail("%ss must be a list", kind)
		return
	}
	for _, item := range n.Content {
		var source, target string
		var mode int64
		if item.Kind == yaml.MappingNode {
			for k, v := range pairs(item) {
				switch k {
				case "source":
					source = p.str(v)
				case "target":
					target = p.str(v)
				case "mode":
					m, err := strconv.ParseInt(p.str(v), 0, 32)
					if err != nil || m < 0 || m > 0o7777 {
						p.fail("%s %s: mode %q is not a permission", kind, source, v.Value)
					}
					mode = m
				case "uid", "gid":
					p.warn("%s %s: %s is ignored (files belong to root)", kind, source, k)
				default:
					p.fail("%s %s: %s is not supported", kind, source, k)
				}
			}
		} else {
			source = p.str(item)
		}
		def, ok := defs[source]
		if !ok {
			p.fail("%s %s is not defined at the top level", kind, source)
			continue
		}
		switch {
		case kind == "secret" && target == "":
			target = "/run/secrets/" + source
		case kind == "secret" && !path.IsAbs(target):
			target = "/run/secrets/" + target
		case target == "":
			target = "/" + source
		}
		f := store.ServiceFile{Path: target, Mode: mode, Secret: kind == "secret", Source: kind + ":" + source}
		switch {
		case def.content != nil:
			f.Content = []byte(*def.content)
		case def.env != "":
			if v, found := p.lookup(def.env); found {
				f.Content = []byte(v)
			} else if p.vars.Ref != nil && f.Secret { // only secrets resolve references at deploy
				f.Content = []byte(p.vars.Ref(p.svc, f.Source, def.env))
			} else {
				p.missing = append(p.missing, def.env)
			}
		default:
			repo := p.repoFiles(def.file)
			if len(repo) != 1 || repo[0].Path != "" {
				if repo != nil {
					p.fail("%s %s: %s must be a file", kind, source, def.file)
				}
				continue
			}
			f.Content = repo[0].Content
			if f.Mode == 0 {
				f.Mode = repo[0].Mode
			}
		}
		*files = append(*files, f)
	}
}

// repoFiles reads a path of the git repository, relative to the compose
// file; nil (with an error) without one.
func (p *parser) repoFiles(rel string) []store.ServiceFile {
	if p.vars.Files == nil {
		p.fail("%s: files of the repository need a git project (use content: or a named volume)", rel)
		return nil
	}
	files, err := p.vars.Files(rel)
	if err != nil {
		p.fail("%s: %v", rel, err)
		return nil
	}
	return files
}

// bindFiles turns a bind mount of a repository path into the files it
// holds, at target.
func (p *parser) bindFiles(source, target string, files *[]store.ServiceFile) {
	for _, f := range p.repoFiles(source) {
		rel := f.Path
		f.Path, f.Source = path.Join(target, rel), source
		*files = append(*files, f)
	}
}

// envFiles reads env_file: a path or a list of paths or {path, required}.
func (p *parser) envFiles(n *yaml.Node) map[string]string {
	out := map[string]string{}
	items := []*yaml.Node{n}
	if n.Kind == yaml.SequenceNode {
		items = n.Content
	}
	for _, item := range items {
		rel, required := "", true
		if item.Kind == yaml.MappingNode {
			for k, v := range pairs(item) {
				switch k {
				case "path":
					rel = p.str(v)
				case "required":
					required = p.bool(v)
				case "format":
					p.warn("env_file format is ignored")
				default:
					p.fail("env_file %s is not supported", k)
				}
			}
		} else {
			rel = p.str(item)
		}
		if p.vars.Files == nil {
			p.fail("env_file %s needs a git project: set the values in environment, a .env or the project variables", rel)
			continue
		}
		files, err := p.vars.Files(rel)
		if err != nil {
			if required {
				p.fail("env_file %s: %v", rel, err)
			}
			continue
		}
		if len(files) != 1 || files[0].Path != "" {
			p.fail("env_file %s must be a file", rel)
			continue
		}
		vars, err := ParseEnv(files[0].Content)
		if err != nil {
			p.fail("env_file %s: %v", rel, err)
			continue
		}
		maps.Copy(out, vars)
	}
	return out
}

// isRepoPath reports whether a bind mount's source is relative to the
// compose file.
func isRepoPath(source string) bool {
	return source == "." || strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../")
}

type outTopFile struct {
	Content     string `yaml:"content,omitempty"`
	Environment string `yaml:"environment,omitempty"`
}

type outFileRef struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
	Mode   octal  `yaml:"mode,omitempty"`
}

// octal is a permission, written as an octal number (0640).
type octal int64

func (o octal) IsZero() bool { return o == 0 }

func (o octal) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "0" + strconv.FormatInt(int64(o), 8)}, nil
}

// marshalFiles writes a service's files as configs and secrets: a secret's
// Content is the variable it comes from. Names are the files' sources,
// prefixed with the service when two services' differ.
func marshalFiles(svc string, files []store.ServiceFile, o *outService, out *outFile) {
	for _, f := range files {
		kind, name, ok := strings.Cut(f.Source, ":")
		if !ok || (kind != "config" && kind != "secret") {
			kind, name = "config", strings.Trim(nonNameRe.ReplaceAllString(strings.ToLower(f.Path), "-"), "-")
		}
		top := outTopFile{Content: string(f.Content)}
		defs := &out.Configs
		if f.Secret {
			kind, top = "secret", outTopFile{Environment: string(f.Content)}
			defs = &out.Secrets
		}
		if *defs == nil {
			*defs = map[string]outTopFile{}
		}
		if have, taken := (*defs)[name]; taken && have != top {
			name = svc + "-" + name
		}
		(*defs)[name] = top
		ref := outFileRef{Source: name, Target: f.Path, Mode: octal(f.Mode)}
		if kind == "secret" {
			o.Secrets = append(o.Secrets, ref)
		} else {
			o.Configs = append(o.Configs, ref)
		}
	}
	slices.SortFunc(o.Configs, func(a, b outFileRef) int { return strings.Compare(a.Target, b.Target) })
	slices.SortFunc(o.Secrets, func(a, b outFileRef) int { return strings.Compare(a.Target, b.Target) })
}
