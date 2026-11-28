package compose

import (
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

type outVolume struct {
	External   bool              `yaml:"external,omitempty"`
	Name       string            `yaml:"name,omitempty"`
	DriverOpts map[string]string `yaml:"driver_opts,omitempty"`
}

// topVolumes reads the top-level volumes: whether each is external (and
// its Docker name), and the local driver's options.
func (p *parser) topVolumes(n *yaml.Node) map[string]domain.Volume {
	out := map[string]domain.Volume{}
	for name, v := range pairs(n) {
		var vol domain.Volume
		external := false
		for k, val := range pairs(v) {
			switch k {
			case "external":
				external = p.bool(val)
			case "name":
				vol.External = p.str(val)
			case "driver":
				if d := p.str(val); d != "local" {
					p.fail("volume %s: driver %s is not supported (only local)", name, d)
				}
			case "driver_opts":
				vol.DriverOpts = p.mapping("driver_opts", val)
			default:
				p.warn("volume %s: %s is ignored", name, k)
			}
		}
		switch {
		case external && vol.External == "":
			vol.External = name
		case !external && vol.External != "":
			p.warn("volume %s: name is ignored (kipitiny names the volumes it creates)", name)
			vol.External = ""
		}
		out[name] = vol
	}
	return out
}

// volumes reads named volumes, and the Docker socket's bind mount ("ro"
// or "rw"; "" without one).
func (p *parser) volumes(n *yaml.Node, rt *domain.Runtime, files *[]domain.ServiceFile) ([]domain.Volume, string) {
	var out []domain.Volume
	socket := ""
	if n.Kind != yaml.SequenceNode {
		p.fail("volumes must be a list")
		return nil, ""
	}
	for _, item := range n.Content {
		var v domain.Volume
		typ := "volume"
		readOnly := false
		var tmpfsOpts []string
		if item.Kind == yaml.MappingNode {
			for k, val := range pairs(item) {
				switch k {
				case "type":
					typ = p.str(val)
				case "source":
					v.Name = p.str(val)
				case "target":
					v.Path = p.str(val)
				case "read_only":
					readOnly = p.str(val) == "true"
				case "tmpfs":
					for tk, tv := range pairs(val) {
						switch tk {
						case "size":
							tmpfsOpts = append(tmpfsOpts, "size="+p.str(tv))
						case "mode":
							tmpfsOpts = append(tmpfsOpts, "mode="+p.str(tv))
						default:
							p.warn("volume tmpfs.%s is ignored", tk)
						}
					}
				case "volume", "bind":
					p.warn("volume %s is ignored", k)
				default:
					p.fail("volume %s is not supported", k)
				}
			}
		} else {
			parts := strings.Split(p.str(item), ":")
			if len(parts) == 3 {
				if slices.Contains(strings.Split(parts[2], ","), "ro") {
					readOnly = true
				}
				parts = parts[:2]
			}
			if len(parts) == 2 {
				v.Name, v.Path = parts[0], parts[1]
			} else {
				v.Path = parts[0]
			}
			if strings.HasPrefix(v.Name, ".") || strings.HasPrefix(v.Name, "/") || strings.HasPrefix(v.Name, "~") {
				typ = "bind"
			}
		}
		switch {
		case typ == "bind" && (v.Name == DockerSocket || v.Name == "/run/docker.sock"):
			if v.Path != DockerSocket {
				p.fail("the Docker socket must be mounted at %s", DockerSocket)
			} else if socket = "rw"; readOnly {
				socket = "ro"
			}
		case typ == "bind" && isRepoPath(v.Name):
			if readOnly {
				// Copies: the container may change them, nothing goes back.
				p.warn("volume %s: read-only is ignored (the files are copied into the container)", v.Name)
			}
			p.bindFiles(v.Name, v.Path, files)
		case typ == "bind" && strings.HasPrefix(v.Name, "/"):
			bind := v.Name + ":" + v.Path
			if readOnly {
				bind += ":ro"
			}
			rt.Binds = append(rt.Binds, bind)
		case typ == "bind":
			p.fail("volume %s: bind mounts take a path relative to the compose file (./) or an absolute host path", v.Name)
		case typ == "tmpfs":
			if rt.Tmpfs == nil {
				rt.Tmpfs = map[string]string{}
			}
			rt.Tmpfs[v.Path] = strings.Join(tmpfsOpts, ",")
		case typ != "volume":
			p.fail("volume type %s is not supported", typ)
		default:
			if v.Name == "" {
				v.Name = anonymousName(v.Path, out)
				p.warn("volume %s is anonymous: kept as the named volume %s", v.Path, v.Name)
			}
			top := p.top[v.Name]
			v.ReadOnly, v.External, v.DriverOpts = readOnly, top.External, top.DriverOpts
			out = append(out, v)
		}
	}
	return out, socket
}
