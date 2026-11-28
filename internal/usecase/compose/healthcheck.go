package compose

import (
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

type outHealthcheck struct {
	Test        []string `yaml:"test,omitempty,flow"`
	Interval    string   `yaml:"interval,omitempty"`
	Timeout     string   `yaml:"timeout,omitempty"`
	StartPeriod string   `yaml:"start_period,omitempty"`
	Retries     int      `yaml:"retries,omitempty"`
	Disable     bool     `yaml:"disable,omitempty"`
}

func outHealthcheckOf(h domain.Healthcheck) *outHealthcheck {
	if h.Test[0] == "NONE" {
		return &outHealthcheck{Disable: true}
	}
	dur := func(secs int) string {
		if secs == 0 {
			return ""
		}
		return (time.Duration(secs) * time.Second).String()
	}
	return &outHealthcheck{Test: h.Test, Interval: dur(h.IntervalSeconds), Timeout: dur(h.TimeoutSeconds),
		StartPeriod: dur(h.StartPeriodSeconds), Retries: h.Retries}
}

// healthcheck reads compose's healthcheck: test as a string (run by the
// shell) or a list, the durations, retries, and disable.
func (p *parser) healthcheck(n *yaml.Node) *domain.Healthcheck {
	h := &domain.Healthcheck{}
	for k, v := range pairs(n) {
		switch k {
		case "test":
			if v.Kind == yaml.SequenceNode {
				for _, item := range v.Content {
					h.Test = append(h.Test, p.str(item))
				}
			} else if cmd := p.str(v); cmd != "" {
				h.Test = []string{"CMD-SHELL", cmd}
			}
		case "interval":
			h.IntervalSeconds = p.duration(v)
		case "timeout":
			h.TimeoutSeconds = p.duration(v)
		case "start_period":
			h.StartPeriodSeconds = p.duration(v)
		case "retries":
			h.Retries = p.int(v)
		case "disable":
			if p.str(v) == "true" {
				return &domain.Healthcheck{Test: []string{"NONE"}}
			}
		case "start_interval":
			p.warn("healthcheck start_interval is ignored (checked every second while starting)")
		default:
			p.fail("healthcheck %s is not supported", k)
		}
	}
	if len(h.Test) == 0 {
		p.fail("healthcheck needs a test")
		return nil
	}
	return h
}
