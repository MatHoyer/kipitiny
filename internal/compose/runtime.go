package compose

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// runtimeKey reads k into rt when it is one of the container settings
// store.Runtime holds, reporting whether it was.
func (p *parser) runtimeKey(k string, v *yaml.Node, rt *store.Runtime) bool {
	switch k {
	case "command":
		rt.Command = p.command(k, v)
	case "entrypoint":
		rt.Entrypoint = p.command(k, v)
		if rt.Entrypoint != nil && len(rt.Entrypoint) == 0 {
			rt.Entrypoint = []string{""} // entrypoint: "" or [] clears the image's
		}
	case "user":
		rt.User = p.str(v)
	case "working_dir":
		rt.WorkingDir = p.str(v)
	case "labels":
		rt.Labels = p.mapping(k, v)
		for name := range rt.Labels {
			if lower := strings.ToLower(name); strings.HasPrefix(lower, "kipitiny.") || strings.HasPrefix(lower, "traefik.") {
				p.warn("label %s is ignored (kipitiny sets its own and routes through x-kipitiny.domain)", name)
				delete(rt.Labels, name)
			}
		}
	case "tmpfs":
		items := []*yaml.Node{v}
		if v.Kind == yaml.SequenceNode {
			items = v.Content
		}
		for _, item := range items {
			path, opts, _ := strings.Cut(p.str(item), ":")
			if rt.Tmpfs == nil {
				rt.Tmpfs = map[string]string{}
			}
			rt.Tmpfs[path] = opts
		}
	case "shm_size":
		rt.ShmSizeMB = p.memory(v)
	case "ulimits":
		rt.Ulimits = map[string]store.Ulimit{}
		for name, lv := range pairs(v) {
			if lv.Kind == yaml.ScalarNode {
				n := int64(p.int(lv))
				rt.Ulimits[name] = store.Ulimit{Soft: n, Hard: n}
				continue
			}
			var u store.Ulimit
			for lk, lvv := range pairs(lv) {
				switch lk {
				case "soft":
					u.Soft = int64(p.int(lvv))
				case "hard":
					u.Hard = int64(p.int(lvv))
				default:
					p.fail("ulimits %s: %s is not supported", name, lk)
				}
			}
			rt.Ulimits[name] = u
		}
	case "sysctls":
		rt.Sysctls = p.mapping(k, v)
	case "read_only":
		rt.ReadOnly = p.bool(v)
	case "init":
		rt.Init = p.bool(v)
	case "stop_signal":
		rt.StopSignal = p.str(v)
	case "tty":
		rt.TTY = p.bool(v)
	case "stdin_open":
		rt.StdinOpen = p.bool(v)
	case "extra_hosts":
		for host, ip := range p.mapping(k, v) {
			rt.ExtraHosts = append(rt.ExtraHosts, host+":"+ip)
		}
		slices.Sort(rt.ExtraHosts)
	case "cap_add":
		rt.CapAdd = p.list(k, v)
	case "cap_drop":
		rt.CapDrop = p.list(k, v)
	case "devices":
		rt.Devices = p.list(k, v)
	case "privileged":
		rt.Privileged = p.bool(v)
	case "security_opt":
		rt.SecurityOpt = p.list(k, v)
	default:
		return false
	}
	return true
}

func (p *parser) list(k string, n *yaml.Node) []string {
	if n.Kind != yaml.SequenceNode {
		p.fail("%s must be a list", k)
		return nil
	}
	var out []string
	for _, item := range n.Content {
		out = append(out, p.str(item))
	}
	return out
}

// command reads a command as a list, or a string split like a shell does
// (no variables or globs: "sh -c '...'" for those).
func (p *parser) command(k string, n *yaml.Node) []string {
	switch n.Kind {
	case yaml.SequenceNode:
		out := []string{}
		for _, item := range n.Content {
			out = append(out, p.str(item))
		}
		return out
	case yaml.ScalarNode:
		if n.Tag == "!!null" {
			return nil
		}
		args, err := SplitCommand(p.str(n))
		if err != nil {
			p.fail("%s: %v", k, err)
		}
		return args
	}
	p.fail("%s must be a string or a list", k)
	return nil
}

// mapping reads a mapping or a list of KEY=value (or host:ip for
// extra_hosts) entries.
func (p *parser) mapping(k string, n *yaml.Node) map[string]string {
	out := map[string]string{}
	switch n.Kind {
	case yaml.MappingNode:
		for key, v := range pairs(n) {
			out[key] = p.str(v)
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			entry := p.str(item)
			key, val, ok := strings.Cut(entry, "=")
			if !ok && k == "extra_hosts" {
				key, val, ok = strings.Cut(entry, ":")
			}
			if !ok && k != "labels" {
				p.fail("%s %q: expected KEY=value", k, entry)
				continue
			}
			out[key] = val
		}
	default:
		p.fail("%s must be a mapping or a list", k)
	}
	return out
}

func (p *parser) bool(n *yaml.Node) bool {
	s := p.str(n)
	v, err := strconv.ParseBool(s)
	if err != nil {
		p.fail("line %d: %q is not true or false", n.Line, s)
	}
	return v
}

// SplitCommand splits s into arguments the way a shell does with quotes
// and backslashes, without expanding anything.
func SplitCommand(s string) ([]string, error) {
	args := []string{}
	var cur strings.Builder
	inArg := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == ' ' || c == '\t' || c == '\n':
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, fmt.Errorf("unterminated quote in %q", s)
			}
			cur.WriteString(s[i+1 : i+1+end])
			i += end + 1
			inArg = true
		case c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte(`"\$`+"`", s[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, fmt.Errorf("unterminated quote in %q", s)
			}
			inArg = true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			inArg = true
		default:
			cur.WriteByte(c)
			inArg = true
		}
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}
