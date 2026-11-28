package compose

import (
	"slices"

	"go.yaml.in/yaml/v3"
)

// topNetworks reads the top-level networks: external ones are networks
// created by hand in kipitiny, by name; the others the project's own.
func (p *parser) topNetworks(n *yaml.Node) map[string]string {
	out := map[string]string{}
	for key, v := range pairs(n) {
		external, name := false, ""
		for k, val := range pairs(v) {
			switch k {
			case "external":
				external = p.bool(val)
			case "name":
				name = p.str(val)
			default:
				p.warn("network %s: %s is ignored", key, k)
			}
		}
		switch {
		case external && key == "default":
			p.fail("the default network can't be external: list the external network in the services instead")
		case external && name == "":
			out[key] = key
		case external:
			out[key] = name
		default:
			if name != "" {
				p.warn("network %s: name is ignored (kipitiny names it)", key)
			}
			out[key] = ""
		}
	}
	return out
}

// serviceNetworks reads a service's networks, a list or a mapping (whose
// options are ignored): the project's own and the external ones' names.
func (p *parser) serviceNetworks(n *yaml.Node) (own, external []string) {
	var keys []string
	switch n.Kind {
	case yaml.SequenceNode:
		for _, item := range n.Content {
			keys = append(keys, p.str(item))
		}
	case yaml.MappingNode:
		for k, v := range pairs(n) {
			keys = append(keys, k)
			if v.Kind == yaml.MappingNode && len(v.Content) > 0 {
				p.warn("network %s: aliases, addresses and other options are ignored", k)
			}
		}
	default:
		p.fail("networks must be a list or a mapping")
	}
	own = []string{}
	for _, k := range keys {
		name, defined := p.nets[k]
		switch {
		case k == "default" && name == "":
			own = append(own, k)
		case !defined:
			p.fail("network %s is not defined at the top level", k)
		case name != "":
			external = append(external, name)
		default:
			own = append(own, k)
		}
	}
	slices.Sort(own)
	slices.Sort(external)
	return slices.Compact(own), slices.Compact(external)
}

type outNetwork struct {
	External bool `yaml:"external,omitempty"`
}

// marshalNetworks lists a service's networks and declares them at the top
// level. A service on the default network only lists none.
func marshalNetworks(s Service, out *outFile) []string {
	if len(s.ExternalNetworks) == 0 && (len(s.Networks) == 0 || slices.Equal(s.Networks, []string{"default"})) {
		return nil
	}
	if out.Networks == nil {
		out.Networks = map[string]outNetwork{}
	}
	names := slices.Clone(s.Networks)
	for _, n := range s.Networks {
		if n != "default" {
			out.Networks[n] = outNetwork{}
		}
	}
	for _, n := range s.ExternalNetworks {
		out.Networks[n] = outNetwork{External: true}
		names = append(names, n)
	}
	slices.Sort(names)
	return slices.Compact(names)
}
