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
// {{ scheme://… }} for a secret in a password manager (internal/secrets),
// {{ app.SERVICE.URL }} or {{ app.SERVICE.DOMAIN }} for where an app of the
// project is public (so a copy of the project points at its own apps).
var EnvRefRe = regexp.MustCompile(`\{\{\s*(?:project\.([A-Za-z_][A-Za-z0-9_]*)|db\.([a-z0-9-]+)\.([A-Z]+)|([a-z][a-z0-9+.-]*://[^{}]+?)|app\.([a-z0-9-]+)\.(URL|DOMAIN))\s*\}\}`)

// SoleRefRe matches a value that is exactly one reference.
var SoleRefRe = regexp.MustCompile(`^\s*` + EnvRefRe.String() + `\s*$`)

// DefaultBrandName is the name the manager goes by when no brand is set.
const DefaultBrandName = "kipitiny"
