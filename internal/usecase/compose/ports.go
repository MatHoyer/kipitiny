package compose

import (
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func (p *parser) ports(n *yaml.Node) []domain.PublishedPort {
	var out []domain.PublishedPort
	if n.Kind != yaml.SequenceNode {
		p.fail("ports must be a list")
		return nil
	}
	for _, item := range n.Content {
		var pp domain.PublishedPort
		if item.Kind == yaml.MappingNode {
			for k, v := range pairs(item) {
				switch k {
				case "target":
					pp.ContainerPort = p.int(v)
				case "published":
					pp.HostPort = p.int(v)
				case "protocol":
					pp.Protocol = p.str(v)
				case "host_ip":
					pp.HostIP = p.str(v)
				case "mode", "name", "app_protocol":
					p.warn("port %s is ignored", k)
				default:
					p.fail("port %s is not supported", k)
				}
			}
		} else {
			out = append(out, p.shortPorts(p.str(item))...)
			continue
		}
		if pp.HostPort == 0 {
			p.warn("port %d isn't published to the host and is ignored; set x-kipitiny.port to route HTTP to it", pp.ContainerPort)
			continue
		}
		if pp.Protocol == "" {
			pp.Protocol = "tcp"
		}
		out = append(out, pp)
	}
	return out
}

// duration reads a compose duration (1m30s) or seconds, as whole seconds.
// expose reads the container ports other services (and kipitiny's router)
// reach: "3000", 3000 or "3000/tcp".
func (p *parser) expose(n *yaml.Node) []int {
	if n.Kind != yaml.SequenceNode {
		p.fail("expose must be a list")
		return nil
	}
	var out []int
	for _, item := range n.Content {
		spec := p.str(item)
		port, proto, _ := strings.Cut(spec, "/")
		v, err := strconv.Atoi(port)
		if err != nil || v < 1 || v > 65535 {
			p.fail("expose %s: only single ports are supported", spec)
			continue
		}
		if proto != "" && proto != "tcp" {
			p.warn("expose %s is ignored (HTTP is routed over tcp)", spec)
			continue
		}
		out = append(out, v)
	}
	return out
}
