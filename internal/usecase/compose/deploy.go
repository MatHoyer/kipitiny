package compose

import (
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

type outDeploy struct {
	Replicas  int           `yaml:"replicas,omitempty"`
	Resources *outResources `yaml:"resources,omitempty"`
}

type outResources struct {
	Limits struct {
		CPUs   quoted `yaml:"cpus,omitempty"`
		Memory string `yaml:"memory,omitempty"`
	} `yaml:"limits,omitempty"`
	Reservations struct {
		Memory string `yaml:"memory,omitempty"`
	} `yaml:"reservations,omitempty"`
}

func (p *parser) deploy(n *yaml.Node, s *Service, rt *domain.Runtime) {
	for k, v := range pairs(n) {
		switch k {
		case "replicas":
			s.Replicas = p.int(v)
		case "resources":
			for rk, rv := range pairs(v) {
				if rk == "reservations" {
					for lk, lv := range pairs(rv) {
						if lk == "memory" {
							rt.MemoryReservationMB = p.memory(lv)
						} else {
							p.warn("deploy.resources.reservations.%s is ignored", lk)
						}
					}
					continue
				}
				if rk != "limits" {
					p.warn("deploy.resources.%s is ignored", rk)
					continue
				}
				for lk, lv := range pairs(rv) {
					switch lk {
					case "memory":
						s.MemoryMB = p.memory(lv)
					case "cpus":
						s.CPUs = p.cpus(lv)
					default:
						p.warn("deploy.resources.limits.%s is ignored", lk)
					}
				}
			}
		case "restart_policy":
			p.restartPolicy(v, rt)
		case "update_config", "rollback_config":
			p.warn("deploy.%s is ignored: kipitiny starts the new replicas and moves traffic once they're ready, then stops the old ones", k)
		default:
			p.warn("deploy.%s is ignored", k)
		}
	}
}

func (p *parser) cpus(n *yaml.Node) float64 {
	s := p.str(n)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		p.fail("line %d: cpus %q is not a number", n.Line, s)
	}
	return v
}

// memory reads a byte size (512m, 1g, 1073741824) as MB, rounded up.
func (p *parser) memory(n *yaml.Node) int {
	s := strings.ToLower(p.str(n))
	num := strings.TrimRight(s, "bkmg")
	unit := strings.TrimSuffix(s[len(num):], "b")
	v, err := strconv.ParseFloat(num, 64)
	mult := map[string]float64{"": 1, "k": 1 << 10, "m": 1 << 20, "g": 1 << 30}[unit]
	if err != nil || mult == 0 || v < 0 {
		p.fail("line %d: memory %q is not a size", n.Line, s)
		return 0
	}
	bytes := v * mult
	mb := int(bytes) >> 20
	if float64(mb)*(1<<20) < bytes {
		mb++
	}
	return mb
}
