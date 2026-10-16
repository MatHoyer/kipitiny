package compose

import (
	"maps"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// Compose's depends_on conditions, by store.Dependency condition.
var conditions = map[string]string{"": "service_started", "healthy": "service_healthy", "completed": "service_completed_successfully"}

// dependsOn reads depends_on: a list of names or a mapping of name to
// {condition, required, restart}, sorted by name so that reordering the file
// (or an export, which sorts) changes nothing.
func (p *parser) dependsOn(n *yaml.Node) []store.Dependency {
	out := p.readDependsOn(n)
	slices.SortFunc(out, func(a, b store.Dependency) int { return strings.Compare(a.Service, b.Service) })
	return out
}

func (p *parser) readDependsOn(n *yaml.Node) []store.Dependency {
	var out []store.Dependency
	if n.Kind == yaml.SequenceNode {
		for _, item := range n.Content {
			out = append(out, store.Dependency{Service: p.str(item)})
		}
		return out
	}
	if n.Kind != yaml.MappingNode {
		p.fail("depends_on must be a list or a mapping")
		return nil
	}
	for name, v := range pairs(n) {
		d := store.Dependency{Service: name}
		for k, val := range pairs(v) {
			switch k {
			case "condition":
				c := p.str(val)
				found := false
				for cond, compose := range conditions {
					if compose == c {
						d.Condition, found = cond, true
					}
				}
				if !found {
					p.fail("depends_on %s: condition %s is not supported", name, c)
				}
			case "required", "restart":
				p.warn("depends_on %s: %s is ignored", name, k)
			default:
				p.fail("depends_on %s: %s is not supported", name, k)
			}
		}
		out = append(out, d)
	}
	return out
}

// checkDependencies refuses depends_on naming a service the file doesn't
// have, and cycles.
func (p *parser) checkDependencies(services map[string]Service) {
	for _, name := range slices.Sorted(maps.Keys(services)) {
		for _, d := range services[name].DependsOn {
			if _, ok := services[d.Service]; !ok {
				p.fail("service %s: depends_on %s: no such service", name, d.Service)
			}
		}
	}
	state := map[string]int{} // 1 visiting, 2 done
	var visit func(name string, path []string) bool
	visit = func(name string, path []string) bool {
		switch state[name] {
		case 1:
			p.fail("depends_on cycle: %s", strings.Join(append(path, name), " → "))
			return false
		case 2:
			return true
		}
		state[name] = 1
		for _, d := range services[name].DependsOn {
			if _, ok := services[d.Service]; ok && !visit(d.Service, append(path, name)) {
				return false
			}
		}
		state[name] = 2
		return true
	}
	for _, name := range slices.Sorted(maps.Keys(services)) {
		if !visit(name, nil) {
			return
		}
	}
}

type outDependency struct {
	Condition string `yaml:"condition"`
}

// outDependsOn writes depends_on as a list of names, or a mapping when a
// condition isn't service_started.
func outDependsOn(deps []store.Dependency) any {
	if len(deps) == 0 {
		return nil
	}
	if !slices.ContainsFunc(deps, func(d store.Dependency) bool { return d.Condition != "" }) {
		names := make([]string, len(deps))
		for i, d := range deps {
			names[i] = d.Service
		}
		return names
	}
	out := map[string]outDependency{}
	for _, d := range deps {
		out[d.Service] = outDependency{Condition: conditions[d.Condition]}
	}
	return out
}
