// Package compose maps a project to a docker-compose file and back: the
// subset of the compose spec kipitiny can run, plus a per-service
// x-kipitiny block for what compose has no field for (domain, middlewares,
// database kind…). Docker Compose ignores x-* keys, so an exported file
// still runs locally. It knows nothing about Docker or the store's rules:
// core validates what Parse returns.
package compose

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// File is a project as a compose file.
type File struct {
	// Name is the compose project name.
	Name     string
	Services map[string]Service
	// X is the top-level x-kipitiny block.
	X FileExt
}

// FileExt holds the project's shared variables, referenced by services as
// {{ project.NAME }}.
type FileExt struct {
	Variables map[string]string `yaml:"variables,omitempty"`
	// Secrets names the write-only Variables.
	Secrets []string `yaml:"secrets,omitempty"`
}

// Service is one compose service, in kipitiny's terms.
type Service struct {
	Image       string
	Environment map[string]string
	Ports       []store.PublishedPort
	Volumes     []store.Volume
	// Replicas is 0 when the file doesn't set it.
	Replicas         int
	MemoryMB         int
	CPUs             float64
	StopGraceSeconds int
	// HostNetwork is network_mode: host.
	HostNetwork bool
	// DockerSocket is "ro" or "rw" when the service mounts the host's
	// Docker socket.
	DockerSocket string
	// Healthcheck is compose's healthcheck key; nil when the file has none.
	// Tagged so the hash of a service that doesn't use it stays the same.
	Healthcheck *store.Healthcheck `json:",omitempty"`
	// VarEnv lists the environment keys whose whole value is a ${NAME}
	// variable: secrets, unless x-kipitiny.secrets says otherwise.
	VarEnv []string `json:"-"`
	X      Ext
}

// DockerSocket is the only path a service may bind-mount: the host's
// Docker socket, at the same path in the container.
const DockerSocket = "/var/run/docker.sock"

// Ext is a service's x-kipitiny block.
type Ext struct {
	// Kind is app, postgres or redis; empty means app, unless the image is
	// the official postgres or redis one.
	Kind       string   `yaml:"kind,omitempty"`
	Domain     string   `yaml:"domain,omitempty"`
	Port       int      `yaml:"port,omitempty"`
	HealthPath string   `yaml:"health_path,omitempty"`
	PreDeploy  string   `yaml:"pre_deploy,omitempty"`
	PreBackup  string   `yaml:"pre_backup,omitempty"`
	Icon       string   `yaml:"icon,omitempty"`
	Secrets    []string `yaml:"secrets,omitempty"`
	// Password is a database's password, usually ${NAME}; empty generates
	// one.
	Password    string       `yaml:"password,omitempty"`
	Middlewares *Middlewares `yaml:"middlewares,omitempty"`
}

type Middlewares struct {
	BasicAuth   []BasicAuthUser   `yaml:"basic_auth,omitempty"`
	IPAllowList []string          `yaml:"ip_allowlist,omitempty"`
	RateLimit   *RateLimit        `yaml:"rate_limit,omitempty"`
	Headers     map[string]string `yaml:"headers,omitempty"`
}

// BasicAuthUser has a bcrypt Hash or a password manager Ref, never a
// plain password.
type BasicAuthUser struct {
	Name string `yaml:"name"`
	Hash string `yaml:"hash,omitempty"`
	Ref  string `yaml:"ref,omitempty"`
}

type RateLimit struct {
	Average int `yaml:"average"`
	Burst   int `yaml:"burst,omitempty"`
}

// ExtKey is the service key kipitiny reads its own settings from.
const ExtKey = "x-kipitiny"

// supportedServiceKeys are the compose service keys Parse reads.
var supportedServiceKeys = []string{"image", "build", "environment", "ports", "expose", "volumes", "deploy", "mem_limit", "cpus", "stop_grace_period", "network_mode", "healthcheck", ExtKey}

// Keys that change nothing kipitiny can't do itself (it orders, networks
// and restarts services on its own): dropped with a warning. Any other
// unknown key is an error, as ignoring it would run something else than
// the file says.
var (
	ignoredServiceKeys = []string{
		"container_name", "depends_on", "hostname",
		"labels", "links", "logging", "networks", "pull_policy", "restart",
		"init", "extra_hosts", "stop_signal",
	}
	ignoredTopKeys = []string{"version", "networks"}
)

// Marshal writes f as compose YAML. Strings are written as given: literal
// "$" in interpolated fields must be escaped first (Escape).
func Marshal(f File) ([]byte, error) {
	out := outFile{Name: f.Name, Services: map[string]outService{}}
	if len(f.X.Variables) > 0 {
		x := f.X
		out.X = &x
	}
	for name, s := range f.Services {
		o := outService{Image: s.Image}
		if len(s.Environment) > 0 {
			o.Environment = s.Environment
		}
		for _, p := range s.Ports {
			v := fmt.Sprintf("%d:%d", p.HostPort, p.ContainerPort)
			if p.Protocol != "" && p.Protocol != "tcp" {
				v += "/" + p.Protocol
			}
			o.Ports = append(o.Ports, quoted(v))
		}
		for _, v := range s.Volumes {
			o.Volumes = append(o.Volumes, v.Name+":"+v.Path)
			if out.Volumes == nil {
				out.Volumes = map[string]struct{}{}
			}
			out.Volumes[v.Name] = struct{}{}
		}
		if s.DockerSocket != "" {
			v := DockerSocket + ":" + DockerSocket
			if s.DockerSocket == "ro" {
				v += ":ro"
			}
			o.Volumes = append(o.Volumes, v)
		}
		if s.HostNetwork {
			o.NetworkMode = "host"
		}
		if s.StopGraceSeconds > 0 {
			o.StopGracePeriod = (time.Duration(s.StopGraceSeconds) * time.Second).String()
		}
		if s.Replicas > 1 || s.MemoryMB > 0 || s.CPUs > 0 {
			d := &outDeploy{}
			if s.Replicas > 1 {
				d.Replicas = s.Replicas
			}
			if s.MemoryMB > 0 || s.CPUs > 0 {
				d.Resources = &outResources{}
				if s.MemoryMB > 0 {
					d.Resources.Limits.Memory = strconv.Itoa(s.MemoryMB) + "M"
				}
				if s.CPUs > 0 {
					d.Resources.Limits.CPUs = quoted(strconv.FormatFloat(s.CPUs, 'f', -1, 64))
				}
			}
			o.Deploy = d
		}
		x := s.X
		if x.Port != 0 {
			// The port HTTP is routed to is the one the container exposes.
			o.Expose = []quoted{quoted(strconv.Itoa(x.Port))}
			x.Port = 0
		}
		if h := s.Healthcheck; h != nil && h.Set() {
			o.Healthcheck = outHealthcheckOf(*h)
		}
		if !isZeroExt(x) {
			o.X = &x
		}
		out.Services[name] = o
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Escape makes s literal in an interpolated compose field.
func Escape(s string) string {
	return strings.ReplaceAll(s, "$", "$$")
}

func isZeroExt(x Ext) bool {
	return x.Kind == "" && x.Domain == "" && x.Port == 0 && x.HealthPath == "" && x.PreDeploy == "" &&
		x.PreBackup == "" && x.Icon == "" && len(x.Secrets) == 0 && x.Password == "" && x.Middlewares == nil
}

type outFile struct {
	Name     string                `yaml:"name,omitempty"`
	Services map[string]outService `yaml:"services"`
	Volumes  map[string]struct{}   `yaml:"volumes,omitempty"`
	X        *FileExt              `yaml:"x-kipitiny,omitempty"`
}

type outService struct {
	Image           string            `yaml:"image"`
	Environment     map[string]string `yaml:"environment,omitempty"`
	Ports           []quoted          `yaml:"ports,omitempty"`
	Expose          []quoted          `yaml:"expose,omitempty"`
	Volumes         []string          `yaml:"volumes,omitempty"`
	NetworkMode     string            `yaml:"network_mode,omitempty"`
	StopGracePeriod string            `yaml:"stop_grace_period,omitempty"`
	Healthcheck     *outHealthcheck   `yaml:"healthcheck,omitempty"`
	Deploy          *outDeploy        `yaml:"deploy,omitempty"`
	X               *Ext              `yaml:"x-kipitiny,omitempty"`
}

type outHealthcheck struct {
	Test        []string `yaml:"test,omitempty,flow"`
	Interval    string   `yaml:"interval,omitempty"`
	Timeout     string   `yaml:"timeout,omitempty"`
	StartPeriod string   `yaml:"start_period,omitempty"`
	Retries     int      `yaml:"retries,omitempty"`
	Disable     bool     `yaml:"disable,omitempty"`
}

func outHealthcheckOf(h store.Healthcheck) *outHealthcheck {
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

type outDeploy struct {
	Replicas  int           `yaml:"replicas,omitempty"`
	Resources *outResources `yaml:"resources,omitempty"`
}

type outResources struct {
	Limits struct {
		CPUs   quoted `yaml:"cpus,omitempty"`
		Memory string `yaml:"memory,omitempty"`
	} `yaml:"limits"`
}

// quoted is written double-quoted: "8080:80" unquoted is a base-60 number
// to YAML 1.1 parsers.
type quoted string

func (q quoted) IsZero() bool { return q == "" }

func (q quoted) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.ScalarNode, Style: yaml.DoubleQuotedStyle, Value: string(q)}, nil
}

// Vars resolves ${NAME} while parsing.
type Vars struct {
	// Lookup gives a variable its value; nil knows none.
	Lookup func(name string) (string, bool)
	// Ref, if set, stands in for a missing variable that makes up a whole
	// environment value (service's key), e.g. a reference to a project
	// variable.
	Ref func(service, key, name string) string
}

// Parse reads a compose file. Warnings name what was dropped; an error
// means the file asks for something kipitiny can't run.
func Parse(data []byte, vars Vars) (File, []string, error) {
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
	var services *yaml.Node
	for k, v := range pairs(root) {
		switch {
		case k == "name":
			f.Name = p.str(v)
		case k == "services":
			services = v
		case k == "volumes":
			p.topVolumes(v)
		case k == ExtKey:
			p.strict(v, &f.X)
		case slices.Contains(ignoredTopKeys, k):
			p.warn("%s is ignored", k)
		case strings.HasPrefix(k, "x-"):
		default:
			p.fail("top-level %s is not supported", k)
		}
	}
	if services == nil || services.Kind != yaml.MappingNode || len(services.Content) == 0 {
		p.fail("no services")
	} else {
		for name, v := range pairs(services) {
			p.svc = name
			f.Services[name] = p.service(name, v)
		}
	}
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
	vars    Vars
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

func (p *parser) topVolumes(n *yaml.Node) {
	for name, v := range pairs(n) {
		for k := range pairs(v) {
			p.warn("volume %s: %s is ignored (kipitiny creates local named volumes)", name, k)
		}
	}
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
			s.Volumes, s.DockerSocket = p.volumes(v)
		case k == "network_mode":
			if mode := p.str(v); mode == "host" {
				s.HostNetwork = true
			} else {
				p.fail("network_mode %s is not supported (only host)", mode)
			}
		case k == "deploy":
			p.deploy(v, &s)
		case k == "mem_limit":
			s.MemoryMB = p.memory(v)
		case k == "cpus":
			s.CPUs = p.cpus(v)
		case k == "stop_grace_period":
			s.StopGraceSeconds = p.duration(v)
		case k == ExtKey:
			s.X = p.ext(v)
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
	s.VarEnv = slices.Compact(slices.Sorted(slices.Values(p.varEnv)))
	p.varEnv = nil
	if s.X.Kind == "" {
		s.X.Kind = inferKind(s.Image)
	}
	if s.X.Kind == string(store.ServiceKindPostgres) || s.X.Kind == string(store.ServiceKindRedis) {
		// The manager generates a database's environment and owns its volume.
		if len(s.Environment) > 0 {
			p.warn("environment of a %s service is ignored (generated)", s.X.Kind)
			s.Environment = nil
		}
		if len(s.Volumes) > 0 {
			p.warn("volumes of a %s service are ignored (the manager keeps its data volume)", s.X.Kind)
			s.Volumes = nil
		}
		if s.HostNetwork || s.DockerSocket != "" {
			p.fail("a %s service can't use the host network or the Docker socket", s.X.Kind)
		}
	}
	return s
}

// inferKind turns the official postgres and redis images into managed
// databases; set x-kipitiny.kind: app to run them as plain apps.
func inferKind(image string) string {
	repo := image
	if i := strings.LastIndexAny(repo, ":@"); i > strings.LastIndex(repo, "/") {
		repo = repo[:i]
	}
	repo = strings.TrimPrefix(strings.TrimPrefix(repo, "docker.io/"), "library/")
	switch repo {
	case "postgres":
		return string(store.ServiceKindPostgres)
	case "redis":
		return string(store.ServiceKindRedis)
	}
	return ""
}

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

func (p *parser) ports(n *yaml.Node) []store.PublishedPort {
	var out []store.PublishedPort
	if n.Kind != yaml.SequenceNode {
		p.fail("ports must be a list")
		return nil
	}
	for _, item := range n.Content {
		var pp store.PublishedPort
		if item.Kind == yaml.MappingNode {
			for k, v := range pairs(item) {
				switch k {
				case "target":
					pp.ContainerPort = p.int(v)
				case "published":
					pp.HostPort = p.int(v)
				case "protocol":
					pp.Protocol = p.str(v)
				case "host_ip", "mode", "name", "app_protocol":
					p.warn("port %s is ignored", k)
				default:
					p.fail("port %s is not supported", k)
				}
			}
		} else {
			spec := p.str(item)
			if s, proto, ok := strings.Cut(spec, "/"); ok {
				spec, pp.Protocol = s, proto
			}
			parts := strings.Split(spec, ":")
			if len(parts) == 3 {
				p.warn("port %s: host IP is ignored (bound on every address)", spec)
				parts = parts[1:]
			}
			if len(parts) != 2 {
				p.warn("port %s isn't published to the host and is ignored; set x-kipitiny.port to route HTTP to it", spec)
				continue
			}
			var err1, err2 error
			pp.HostPort, err1 = strconv.Atoi(parts[0])
			pp.ContainerPort, err2 = strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil {
				p.fail("port %s: only single ports are supported, not ranges", spec)
				continue
			}
		}
		if pp.HostPort == 0 {
			p.warn("port %d isn't published to the host and is ignored; set x-kipitiny.port to route HTTP to it", pp.ContainerPort)
			continue
		}
		if pp.Protocol == "" {
			pp.Protocol = "tcp"
		}
		out = append(out, pp)
	}
	return out
}

// volumes reads named volumes, and the Docker socket's bind mount ("ro"
// or "rw"; "" without one).
func (p *parser) volumes(n *yaml.Node) ([]store.Volume, string) {
	var out []store.Volume
	socket := ""
	if n.Kind != yaml.SequenceNode {
		p.fail("volumes must be a list")
		return nil, ""
	}
	for _, item := range n.Content {
		var v store.Volume
		typ := "volume"
		readOnly := false
		if item.Kind == yaml.MappingNode {
			for k, val := range pairs(item) {
				switch k {
				case "type":
					typ = p.str(val)
				case "source":
					v.Name = p.str(val)
				case "target":
					v.Path = p.str(val)
				case "read_only":
					readOnly = p.str(val) == "true"
				case "volume", "bind":
					p.warn("volume %s is ignored", k)
				default:
					p.fail("volume %s is not supported", k)
				}
			}
		} else {
			parts := strings.Split(p.str(item), ":")
			if len(parts) == 3 {
				if slices.Contains(strings.Split(parts[2], ","), "ro") {
					readOnly = true
				}
				parts = parts[:2]
			}
			if len(parts) == 2 {
				v.Name, v.Path = parts[0], parts[1]
			} else {
				v.Path = parts[0]
			}
			if strings.HasPrefix(v.Name, ".") || strings.HasPrefix(v.Name, "/") || strings.HasPrefix(v.Name, "~") {
				typ = "bind"
			}
		}
		switch {
		case typ == "bind" && (v.Name == DockerSocket || v.Name == "/run/docker.sock"):
			if v.Path != DockerSocket {
				p.fail("the Docker socket must be mounted at %s", DockerSocket)
			} else if socket = "rw"; readOnly {
				socket = "ro"
			}
		case typ == "bind":
			p.fail("volume %s: bind mounts are not supported (only %s), use a named volume", v.Name, DockerSocket)
		case typ != "volume":
			p.fail("volume type %s is not supported", typ)
		case v.Name == "":
			p.fail("volume %s needs a name (anonymous volumes are not supported)", v.Path)
		default:
			if readOnly {
				p.warn("volume %s: read-only is ignored", v.Name)
			}
			out = append(out, v)
		}
	}
	return out, socket
}

func (p *parser) deploy(n *yaml.Node, s *Service) {
	for k, v := range pairs(n) {
		switch k {
		case "replicas":
			s.Replicas = p.int(v)
		case "resources":
			for rk, rv := range pairs(v) {
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
		default:
			p.warn("deploy.%s is ignored", k)
		}
	}
}

func (p *parser) int(n *yaml.Node) int {
	s := p.str(n)
	v, err := strconv.Atoi(s)
	if err != nil {
		p.fail("line %d: %q is not a number", n.Line, s)
	}
	return v
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

// duration reads a compose duration (1m30s) or seconds, as whole seconds.
// expose reads the container ports other services (and kipitiny's router)
// reach: "3000", 3000 or "3000/tcp".
func (p *parser) expose(n *yaml.Node) []int {
	if n.Kind != yaml.SequenceNode {
		p.fail("expose must be a list")
		return nil
	}
	var out []int
	for _, item := range n.Content {
		spec := p.str(item)
		port, proto, _ := strings.Cut(spec, "/")
		v, err := strconv.Atoi(port)
		if err != nil || v < 1 || v > 65535 {
			p.fail("expose %s: only single ports are supported", spec)
			continue
		}
		if proto != "" && proto != "tcp" {
			p.warn("expose %s is ignored (HTTP is routed over tcp)", spec)
			continue
		}
		out = append(out, v)
	}
	return out
}

// healthcheck reads compose's healthcheck: test as a string (run by the
// shell) or a list, the durations, retries, and disable.
func (p *parser) healthcheck(n *yaml.Node) *store.Healthcheck {
	h := &store.Healthcheck{}
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
				return &store.Healthcheck{Test: []string{"NONE"}}
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

func (p *parser) duration(n *yaml.Node) int {
	s := p.str(n)
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		p.fail("line %d: %q is not a duration", n.Line, s)
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
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
	if !p.strict(n, &x) {
		return Ext{}
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

// ServiceNames returns f's service names, sorted.
func (f File) ServiceNames() []string {
	return slices.Sorted(maps.Keys(f.Services))
}
