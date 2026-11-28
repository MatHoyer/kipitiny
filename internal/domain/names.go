package domain

import "regexp"

// Names end up in container, network and DNS names, so keep them DNS-safe.
var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

// ValidName reports whether name may name a project, service, user or
// network.
func ValidName(name string) bool { return nameRe.MatchString(name) }

var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$|^localhost$|^([a-z0-9-]+\.)+localhost$`)

// ValidDomain reports whether name is a lower-case DNS name (or localhost).
func ValidDomain(name string) bool { return domainRe.MatchString(name) }

// EnvRefRe matches a reference in a service env value, spaces optional:
// {{ project.NAME }} for a project variable or secret, {{ db.SERVICE.FIELD }}
// for a credential of a database service of the project,
// {{ scheme://… }} for a secret in a password manager (internal/domain/secrets),
// {{ app.SERVICE.URL }} or {{ app.SERVICE.DOMAIN }} for where an app of the
// project is public (so a copy of the project points at its own apps).
var EnvRefRe = regexp.MustCompile(`\{\{\s*(?:project\.([A-Za-z_][A-Za-z0-9_]*)|db\.([a-z0-9-]+)\.([A-Z]+)|([a-z][a-z0-9+.-]*://[^{}]+?)|app\.([a-z0-9-]+)\.(URL|DOMAIN))\s*\}\}`)

// SoleRefRe matches a value that is exactly one reference.
var SoleRefRe = regexp.MustCompile(`^\s*` + EnvRefRe.String() + `\s*$`)

// RefName is how an EnvRefRe match is named in messages: project.NAME,
// db.SERVICE.FIELD, app.SERVICE.FIELD.
func RefName(m []string) string {
	if m[5] != "" {
		return "app." + m[5] + "." + m[6]
	}
	if m[1] != "" {
		return "project." + m[1]
	}
	if m[4] != "" {
		return m[4]
	}
	return "db." + m[2] + "." + m[3]
}

// UsesDatabase reports whether env references the database named name.
func UsesDatabase(env map[string]string, name string) bool {
	for _, v := range env {
		for _, m := range EnvRefRe.FindAllStringSubmatch(v, -1) {
			if m[2] == name {
				return true
			}
		}
	}
	return false
}

// DefaultBrandName is the name the manager goes by when no brand is set.
const DefaultBrandName = "kipitiny"
