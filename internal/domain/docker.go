package domain

import "strings"

// The labels and network names of everything the manager creates on Docker.
const (
	// ProxyNetwork is shared by Traefik and every public-facing container.
	ProxyNetwork = "kipitiny-proxy"

	LabelManaged   = "kipitiny.managed"
	LabelComponent = "kipitiny.component"
	LabelProject   = "kipitiny.project"
	LabelService   = "kipitiny.service"
	LabelReplica   = "kipitiny.replica"
	LabelDeploy    = "kipitiny.deploy"
	LabelJob       = "kipitiny.job"
	// LabelConfigHash lets the manager detect when a container it owns needs
	// to be recreated because its desired configuration changed.
	LabelConfigHash = "kipitiny.config-hash"
)

// ProjectNetwork is the private network shared by all containers of a project.
func ProjectNetwork(projectID string) string {
	return "kipitiny-" + projectID
}

// ProjectNetworkNamed is a project's network of a compose file: name
// "default" is the project network.
func ProjectNetworkNamed(projectID, name string) string {
	if name == "default" {
		return ProjectNetwork(projectID)
	}
	return ProjectNetwork(projectID) + "-" + name
}

// customNetworkPrefix starts the names of networks created by hand; project
// IDs never start with "net-".
const customNetworkPrefix = "kipitiny-net-"

// CustomNetwork is the Docker name of a network created by hand.
func CustomNetwork(networkID string) string {
	return customNetworkPrefix + networkID
}

// IsCustomNetwork tells whether a Docker network name is one created by hand.
func IsCustomNetwork(name string) bool {
	return strings.HasPrefix(name, customNetworkPrefix)
}

// Platform is an image platform; the zero value is the server's own.
type Platform struct {
	OS, Architecture, Variant string
}

// ParsePlatform parses os/arch[/variant]; "" is nil, the server's own.
func ParsePlatform(s string) *Platform {
	if s == "" {
		return nil
	}
	parts := append(strings.SplitN(s, "/", 3), "", "")
	return &Platform{OS: parts[0], Architecture: parts[1], Variant: parts[2]}
}

// Filter selects Docker objects as the Engine API's filters do: each key
// (label, label!, until, dangling, id, type, event…) matches any of its
// values, and every key must match.
type Filter map[string][]string

// Add adds values to key and returns f.
func (f Filter) Add(key string, values ...string) Filter {
	f[key] = append(f[key], values...)
	return f
}

// LabelFilter matches every label of labels (key=value).
func LabelFilter(labels map[string]string) Filter {
	f := Filter{}
	for k, v := range labels {
		f.Add("label", k+"="+v)
	}
	return f
}
