package domain

import "regexp"

// Names end up in container, network and DNS names, so keep them DNS-safe.
var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

// ValidName reports whether name may name a project, service, user or
// network.
func ValidName(name string) bool { return nameRe.MatchString(name) }
