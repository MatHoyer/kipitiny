package compose

import (
	"regexp"
	"strings"
)

var (
	varNameRe = `[A-Za-z_][A-Za-z0-9_]*`
	// $$, $NAME, ${NAME}, ${NAME:-default}, ${NAME-default},
	// ${NAME:?error}, ${NAME?error}.
	varRe     = regexp.MustCompile(`\$(?:\$|(` + varNameRe + `)|\{(` + varNameRe + `)(?:(:?[-?])([^}]*))?\})`)
	soleVarRe = regexp.MustCompile(`^\s*\$(?:(` + varNameRe + `)|\{(` + varNameRe + `)\})\s*$`)
)

// SoleVar reports the name of a value made of exactly one variable, like
// ${NAME}.
func SoleVar(s string) (string, bool) {
	m := soleVarRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return m[1] + m[2], true
}

// interpolate expands variables like docker compose does; a missing one
// without default is recorded in p.missing.
func (p *parser) interpolate(s string) string {
	if !strings.Contains(s, "$") {
		return s
	}
	return varRe.ReplaceAllStringFunc(s, func(match string) string {
		if match == "$$" {
			return "$"
		}
		m := varRe.FindStringSubmatch(match)
		name, op, arg := m[1]+m[2], m[3], m[4]
		v, ok := p.lookup(name)
		switch op {
		case ":-":
			if !ok || v == "" {
				return arg
			}
		case "-":
			if !ok {
				return arg
			}
		case ":?", "?":
			if !ok || (op == ":?" && v == "") {
				p.fail("variable %s: %s", name, arg)
				return ""
			}
		default:
			if !ok {
				p.missing = append(p.missing, name)
			}
		}
		return v
	})
}
