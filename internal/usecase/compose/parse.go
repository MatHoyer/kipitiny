package compose

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// Vars resolves ${NAME} while parsing.
type Vars struct {
	// Lookup gives a variable its value; nil knows none.
	Lookup func(name string) (string, bool)
	// Ref, if set, stands in for a missing variable that makes up a whole
	// environment value (service's key), e.g. a reference to a project
	// variable.
	Ref func(service, key, name string) string
	// Files reads a path of the git repository, relative to the compose
	// file: the file (Path "") or a directory's files (relative paths). nil
	// without a repository.
	Files func(path string) ([]domain.ServiceFile, error)
}

// Parse reads a compose file. Warnings name what was dropped; an error
// means the file asks for something kipitiny can't run.
func Parse(data []byte, vars Vars) (File, []string, error) { return parse(data, vars, 0) }

// parse reads a compose file included depth levels deep.
func parse(data []byte, vars Vars, depth int) (File, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return File{}, nil, fmt.Errorf("compose: %w", err)
	}
	if len(doc.Content) == 0 {
		return File{}, nil, fmt.Errorf("compose: empty file")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return File{}, nil, fmt.Errorf("compose: top level must be a mapping")
	}
	p := &parser{vars: vars}
	f := File{Services: map[string]Service{}}
	var services, include *yaml.Node
	for k, v := range pairs(root) {
		switch {
		case k == "name":
			f.Name = p.str(v)
		case k == "services":
			services = v
		case k == "include":
			include = v
		case k == "volumes":
			p.top = p.topVolumes(v)
		case k == "networks":
			p.nets = p.topNetworks(v)
		case k == "configs":
			p.configs = p.topFiles("config", v)
		case k == "secrets":
			p.secrets = p.topFiles("secret", v)
		case k == ExtKey:
			p.strict(v, &f.X)
		case slices.Contains(ignoredTopKeys, k):
			p.warn("%s is ignored", k)
		case strings.HasPrefix(k, "x-"):
		default:
			p.fail("top-level %s is not supported", k)
		}
	}
	if services != nil && services.Kind == yaml.MappingNode {
		p.resolveExtends(services)
		for name, v := range pairs(services) {
			p.svc = name
			if s := p.service(name, v); !s.disabled {
				f.Services[name] = s
			}
		}
	}
	p.svc = ""
	if include != nil {
		p.includes(include, &f, depth)
	}
	if len(f.Services) == 0 {
		p.fail("no services")
	}
	p.checkDependencies(f.Services)
	if len(p.missing) > 0 {
		slices.Sort(p.missing)
		p.fail("variables not set: %s", strings.Join(slices.Compact(p.missing), ", "))
	}
	if len(p.errs) > 0 {
		return File{}, p.warns, fmt.Errorf("compose: %s", strings.Join(p.errs, "; "))
	}
	return f, p.warns, nil
}

type parser struct {
	vars Vars
	// top holds the top-level volumes' options, by name; configs and
	// secrets the top-level ones.
	top              map[string]domain.Volume
	configs, secrets map[string]topFile
	// nets holds the top-level networks: external ones map to their name,
	// the others to "".
	nets    map[string]string
	svc     string
	ctx     string
	warns   []string
	errs    []string
	missing []string
	// varEnv collects the current service's environment keys written as a
	// whole ${NAME}.
	varEnv []string
}

func (p *parser) at(format string, args ...any) string {
	msg := fmt.Sprintf(format, args...)
	if p.ctx != "" {
		msg = p.ctx + ": " + msg
	}
	return msg
}

func (p *parser) warn(format string, args ...any) { p.warns = append(p.warns, p.at(format, args...)) }
func (p *parser) fail(format string, args ...any) { p.errs = append(p.errs, p.at(format, args...)) }

// pairs iterates a mapping node's keys and values.
func pairs(n *yaml.Node) func(yield func(string, *yaml.Node) bool) {
	return func(yield func(string, *yaml.Node) bool) {
		if n.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if !yield(n.Content[i].Value, n.Content[i+1]) {
				return
			}
		}
	}
}

// str reads an interpolated scalar.
func (p *parser) str(n *yaml.Node) string {
	if n.Kind != yaml.ScalarNode {
		p.fail("line %d: expected a value", n.Line)
		return ""
	}
	if n.Tag == "!!null" {
		return ""
	}
	return p.interpolate(n.Value)
}

func (p *parser) service(name string, n *yaml.Node) Service {
	p.ctx = "service " + name
	defer func() { p.ctx = "" }()
	var s Service
	if n.Kind != yaml.MappingNode {
		p.fail("must be a mapping")
		return s
	}
	hasBuild := false
	var expose []int
	var rt domain.Runtime
	var envFile map[string]string
	var profiles []string
	for k, v := range pairs(n) {
		switch {
		case k == "image":
			s.Image = p.str(v)
		case k == "build":
			hasBuild = true
		case k == "environment":
			s.Environment = p.environment(v)
		case k == "ports":
			s.Ports = p.ports(v)
		case k == "expose":
			expose = p.expose(v)
		case k == "healthcheck":
			s.Healthcheck = p.healthcheck(v)
		case k == "volumes":
			s.Volumes, s.DockerSocket = p.volumes(v, &rt, &s.Files)
		case k == "configs":
			p.fileRefs("config", v, p.configs, &s.Files)
		case k == "secrets":
			p.fileRefs("secret", v, p.secrets, &s.Files)
		case k == "env_file":
			envFile = p.envFiles(v)
		case k == "network_mode":
			switch mode := p.str(v); {
			case mode == "host":
				s.HostNetwork = true
			case strings.HasPrefix(mode, "service:") || strings.HasPrefix(mode, "container:"):
				p.fail("network_mode %s is not supported: kipitiny networks each service on its own (project networks, routing, aliases), "+
					"so a service can't share another's network; run both in one image, or use the other service as a proxy (a VPN container's HTTP or SOCKS port)", mode)
			default:
				p.fail("network_mode %s is not supported (only host)", mode)
			}
		case k == "deploy":
			p.deploy(v, &s, &rt)
		case k == "mem_limit":
			s.MemoryMB = p.memory(v)
		case k == "cpus":
			s.CPUs = p.cpus(v)
		case k == "stop_grace_period":
			s.StopGraceSeconds = p.duration(v)
		case k == ExtKey:
			s.X = p.ext(v)
		case k == "extends": // merged before, by resolveExtends
		case k == "depends_on":
			s.DependsOn = p.dependsOn(v)
		case k == "networks":
			s.Networks, s.ExternalNetworks = p.serviceNetworks(v)
		case k == "profiles":
			profiles = p.list(k, v)
		case p.runtimeKey(k, v, &rt):
		case slices.Contains(ignoredServiceKeys, k):
			p.warn("%s is ignored", k)
		case strings.HasPrefix(k, "x-"):
		default:
			p.fail("%s is not supported", k)
		}
	}
	switch {
	case s.Image == "" && hasBuild:
		p.fail("needs an image: kipitiny doesn't build, push the image from CI")
	case s.Image == "" && s.X.Kind == "":
		p.fail("needs an image")
	case hasBuild:
		p.warn("build is ignored, the image is pulled")
	}
	switch {
	case len(expose) == 1 && s.X.Port == 0:
		s.X.Port = expose[0]
	case len(expose) > 1 && s.X.Port == 0:
		p.warn("expose lists several ports: set x-kipitiny.port to the one HTTP is routed to")
	}
	if !rt.IsZero() {
		s.Runtime = &rt
	}
	if len(envFile) > 0 {
		// environment wins over env_file, as in Compose.
		maps.Copy(envFile, s.Environment)
		s.Environment = envFile
	}
	s.VarEnv = slices.Compact(slices.Sorted(slices.Values(p.varEnv)))
	p.varEnv = nil
	if s.X.Kind == "" {
		s.X.Kind = inferKind(s.Image)
	}
	switch {
	case slices.Contains(profiles, "preview") && s.X.Preview == "skip":
		p.fail("the preview profile and %s.preview: skip contradict each other", ExtKey)
	case slices.Contains(profiles, "preview"):
		s.X.Preview = "only"
	case len(profiles) > 0:
		p.warn("skipped: kipitiny only runs the preview profile (and services without profiles)")
		s.disabled = true
	}
	if s.HostNetwork && s.Networks != nil {
		p.fail("an app in the host network can't join networks")
	}
	if s.X.Anonymize != nil {
		if k := domain.ServiceKind(s.X.Kind); !k.IsDatabase() || k == domain.ServiceKindRedis {
			p.fail("%s.anonymize: only PostgreSQL, MySQL, MariaDB and MongoDB databases are anonymized", ExtKey)
		} else if err := s.X.Anonymize.Validate(); err != nil {
			p.fail("%s.anonymize: %v", ExtKey, err)
		}
	}
	if domain.ServiceKind(s.X.Kind).IsDatabase() {
		// The manager generates a database's environment and owns its volume.
		p.databaseEnv(&s)
		if len(s.Volumes) > 0 {
			p.warn("volumes of a %s service are ignored (the manager keeps its data volume)", s.X.Kind)
			s.Volumes = nil
		}
		if s.HostNetwork || s.DockerSocket != "" {
			p.fail("a %s service can't use the host network or the Docker socket", s.X.Kind)
		}
		s.Files = p.initFiles(s.X.Kind, s.Files)
		if s.Runtime != nil {
			p.warn("command, user and the like of a %s service are ignored (the manager runs it)", s.X.Kind)
			s.Runtime = nil
		}
		if s.DependsOn != nil {
			p.warn("depends_on of a %s service is ignored: databases start first", s.X.Kind)
			s.DependsOn = nil
		}
	}
	return s
}

// inferKind turns the official postgres and redis images into managed
// databases; set x-kipitiny.kind: app to run them as plain apps. MySQL,
// MariaDB and MongoDB images stay apps unless the kind says otherwise: they
// ran as apps before kipitiny managed them, with their own environment and
// volumes.
func inferKind(image string) string {
	repo := image
	if i := strings.LastIndexAny(repo, ":@"); i > strings.LastIndex(repo, "/") {
		repo = repo[:i]
	}
	repo = strings.TrimPrefix(strings.TrimPrefix(repo, "docker.io/"), "library/")
	switch repo {
	case "postgres":
		return string(domain.ServiceKindPostgres)
	case "redis":
		return string(domain.ServiceKindRedis)
	}
	return ""
}

func (p *parser) int(n *yaml.Node) int {
	s := p.str(n)
	v, err := strconv.Atoi(s)
	if err != nil {
		p.fail("line %d: %q is not a number", n.Line, s)
	}
	return v
}

func (p *parser) duration(n *yaml.Node) int {
	s := p.str(n)
	v, err := parseSeconds(s)
	if err != nil {
		p.fail("line %d: %q is not a duration", n.Line, s)
	}
	return v
}

// strict decodes an x-kipitiny block into v, refusing unknown keys. It
// isn't interpolated (basic auth hashes are full of $): the caller resolves
// values that are a sole ${NAME} (SoleVar).
func (p *parser) strict(n *yaml.Node, v any) bool {
	raw, err := yaml.Marshal(n)
	if err == nil {
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		dec.KnownFields(true)
		err = dec.Decode(v)
	}
	if err != nil {
		p.fail("%s: %v", ExtKey, err)
		return false
	}
	return true
}

func (p *parser) ext(n *yaml.Node) Ext {
	var x Ext
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "sleep_after" {
				// Sleep mode moved to the project, set on its own.
				p.warn("%s.sleep_after is ignored: sleep mode is a project setting now", ExtKey)
				n.Content = slices.Delete(n.Content, i, i+2)
				break
			}
		}
	}
	if !p.strict(n, &x) {
		return Ext{}
	}
	if x.Preview != "" && x.Preview != "only" && x.Preview != "skip" {
		p.fail("%s.preview must be only or skip", ExtKey)
	}
	if x.Middlewares != nil {
		for _, u := range x.Middlewares.BasicAuth {
			if u.Hash == "" && u.Ref == "" {
				p.fail("basic auth user %s needs a bcrypt hash or a password manager ref", u.Name)
			}
		}
	}
	return x
}
