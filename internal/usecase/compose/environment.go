package compose

import (
	"strings"

	"go.yaml.in/yaml/v3"
)

func (p *parser) environment(n *yaml.Node) map[string]string {
	env := map[string]string{}
	set := func(k string, v *string) {
		val := "${" + k + "}" // KEY alone takes its value from the variables
		if v != nil {
			val = *v
		}
		if _, ok := SoleVar(val); ok {
			p.varEnv = append(p.varEnv, k)
		}
		env[k] = p.envValue(k, val)
	}
	switch n.Kind {
	case yaml.MappingNode:
		for k, v := range pairs(n) {
			if v.Kind != yaml.ScalarNode {
				p.fail("environment %s: expected a value", k)
				continue
			}
			if v.Tag == "!!null" {
				set(k, nil)
				continue
			}
			val := v.Value
			set(k, &val)
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			k, v, ok := strings.Cut(item.Value, "=")
			if !ok {
				set(k, nil)
				continue
			}
			set(k, &v)
		}
	default:
		p.fail("environment must be a mapping or a list")
	}
	return env
}

// envValue interpolates an environment value; a missing variable that is
// the whole value becomes Vars.Ref.
func (p *parser) envValue(key, v string) string {
	if name, ok := SoleVar(v); ok && p.vars.Ref != nil {
		if _, found := p.lookup(name); !found {
			return p.vars.Ref(p.svc, key, name)
		}
	}
	return p.interpolate(v)
}

func (p *parser) lookup(name string) (string, bool) {
	if p.vars.Lookup == nil {
		return "", false
	}
	return p.vars.Lookup(name)
}
