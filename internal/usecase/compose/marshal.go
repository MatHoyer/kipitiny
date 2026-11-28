package compose

import (
	"bytes"
	"fmt"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/kipit-labs/kipitiny/internal/domain"
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
			if p.HostIP != "" {
				v = net.JoinHostPort(p.HostIP, strconv.Itoa(p.HostPort)) + ":" + strconv.Itoa(p.ContainerPort)
			}
			if p.Protocol != "" && p.Protocol != "tcp" {
				v += "/" + p.Protocol
			}
			o.Ports = append(o.Ports, quoted(v))
		}
		for _, v := range s.Volumes {
			spec := v.Name + ":" + v.Path
			if v.ReadOnly {
				spec += ":ro"
			}
			o.Volumes = append(o.Volumes, spec)
			if out.Volumes == nil {
				out.Volumes = map[string]outVolume{}
			}
			top := outVolume{DriverOpts: v.DriverOpts}
			if v.External != "" {
				top.External = true
				if v.External != v.Name {
					top.Name = v.External
				}
			}
			out.Volumes[v.Name] = top
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
		reserved := 0
		if s.Runtime != nil {
			reserved = s.Runtime.MemoryReservationMB
		}
		if s.Replicas > 1 || s.MemoryMB > 0 || s.CPUs > 0 || reserved > 0 {
			d := &outDeploy{}
			if s.Replicas > 1 {
				d.Replicas = s.Replicas
			}
			if s.MemoryMB > 0 || s.CPUs > 0 || reserved > 0 {
				d.Resources = &outResources{}
				if s.MemoryMB > 0 {
					d.Resources.Limits.Memory = strconv.Itoa(s.MemoryMB) + "M"
				}
				if s.CPUs > 0 {
					d.Resources.Limits.CPUs = quoted(strconv.FormatFloat(s.CPUs, 'f', -1, 64))
				}
				if reserved > 0 {
					d.Resources.Reservations.Memory = strconv.Itoa(reserved) + "M"
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
		if r := s.Runtime; r != nil {
			o.outRuntime = outRuntimeOf(*r)
			o.Volumes = append(o.Volumes, r.Binds...)
		}
		marshalFiles(name, s.Files, &o, &out)
		o.DependsOn = outDependsOn(s.DependsOn)
		o.Networks = marshalNetworks(s, &out)
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
		x.PreBackup == "" && x.Icon == "" && !x.Analytics && !x.Errors && x.Preview == "" && len(x.Tags) == 0 && len(x.Secrets) == 0 && x.Password == "" && x.Middlewares == nil && len(x.Routes) == 0 &&
		x.Uptime == nil && len(x.Jobs) == 0 && x.Anonymize == nil
}

type outFile struct {
	Name     string                `yaml:"name,omitempty"`
	Services map[string]outService `yaml:"services"`
	Volumes  map[string]outVolume  `yaml:"volumes,omitempty"`
	Configs  map[string]outTopFile `yaml:"configs,omitempty"`
	Secrets  map[string]outTopFile `yaml:"secrets,omitempty"`
	Networks map[string]outNetwork `yaml:"networks,omitempty"`
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
	Configs         []outFileRef      `yaml:"configs,omitempty"`
	Secrets         []outFileRef      `yaml:"secrets,omitempty"`
	DependsOn       any               `yaml:"depends_on,omitempty"`
	Networks        []string          `yaml:"networks,omitempty"`
	outRuntime      `yaml:",inline"`
	X               *Ext `yaml:"x-kipitiny,omitempty"`
}

type outRuntime struct {
	Command    []string          `yaml:"command,omitempty,flow"`
	Entrypoint []string          `yaml:"entrypoint,omitempty,flow"`
	User       string            `yaml:"user,omitempty"`
	WorkingDir string            `yaml:"working_dir,omitempty"`
	Labels     map[string]string `yaml:"labels,omitempty"`
	Tmpfs      []string          `yaml:"tmpfs,omitempty"`
	ShmSize    string            `yaml:"shm_size,omitempty"`
	Ulimits    map[string]any    `yaml:"ulimits,omitempty"`
	Sysctls    map[string]string `yaml:"sysctls,omitempty"`
	ReadOnly   bool              `yaml:"read_only,omitempty"`
	Init       bool              `yaml:"init,omitempty"`
	StopSignal string            `yaml:"stop_signal,omitempty"`
	TTY        bool              `yaml:"tty,omitempty"`
	StdinOpen  bool              `yaml:"stdin_open,omitempty"`
	ExtraHosts []string          `yaml:"extra_hosts,omitempty"`
	CapAdd     []string          `yaml:"cap_add,omitempty"`
	CapDrop    []string          `yaml:"cap_drop,omitempty"`
	Devices    []string          `yaml:"devices,omitempty"`
	Privileged bool              `yaml:"privileged,omitempty"`
	SecOpt     []string          `yaml:"security_opt,omitempty"`
	Restart    string            `yaml:"restart,omitempty"`
	Platform   string            `yaml:"platform,omitempty"`
}

func outRuntimeOf(r domain.Runtime) outRuntime {
	o := outRuntime{Command: r.Command, Entrypoint: r.Entrypoint, User: r.User, WorkingDir: r.WorkingDir, Labels: r.Labels, Restart: r.Restart,
		Sysctls: r.Sysctls, ReadOnly: r.ReadOnly, Init: r.Init, StopSignal: r.StopSignal, TTY: r.TTY, StdinOpen: r.StdinOpen,
		ExtraHosts: r.ExtraHosts, CapAdd: r.CapAdd, CapDrop: r.CapDrop, Devices: r.Devices, Privileged: r.Privileged, SecOpt: r.SecurityOpt, Platform: r.Platform}
	for _, path := range slices.Sorted(maps.Keys(r.Tmpfs)) {
		v := path
		if opts := r.Tmpfs[path]; opts != "" {
			v += ":" + opts
		}
		o.Tmpfs = append(o.Tmpfs, v)
	}
	if r.ShmSizeMB > 0 {
		o.ShmSize = strconv.Itoa(r.ShmSizeMB) + "m"
	}
	for name, u := range r.Ulimits {
		if o.Ulimits == nil {
			o.Ulimits = map[string]any{}
		}
		if u.Soft == u.Hard {
			o.Ulimits[name] = u.Soft
		} else {
			o.Ulimits[name] = map[string]int64{"soft": u.Soft, "hard": u.Hard}
		}
	}
	return o
}

// EscapeRuntime makes r's values literal in a compose file (see Escape).
func EscapeRuntime(r domain.Runtime) domain.Runtime {
	list := func(l []string) []string {
		if l == nil {
			return nil
		}
		out := make([]string, len(l))
		for i, v := range l {
			out[i] = Escape(v)
		}
		return out
	}
	values := func(m map[string]string) map[string]string {
		if m == nil {
			return nil
		}
		out := make(map[string]string, len(m))
		for k, v := range m {
			out[k] = Escape(v)
		}
		return out
	}
	r.Command, r.Entrypoint, r.ExtraHosts = list(r.Command), list(r.Entrypoint), list(r.ExtraHosts)
	r.Devices, r.SecurityOpt, r.Binds = list(r.Devices), list(r.SecurityOpt), list(r.Binds)
	r.User, r.WorkingDir, r.StopSignal, r.Platform = Escape(r.User), Escape(r.WorkingDir), Escape(r.StopSignal), Escape(r.Platform)
	r.Labels, r.Tmpfs, r.Sysctls = values(r.Labels), values(r.Tmpfs), values(r.Sysctls)
	return r
}

// quoted is written double-quoted: "8080:80" unquoted is a base-60 number
// to YAML 1.1 parsers.
type quoted string

func (q quoted) IsZero() bool { return q == "" }

func (q quoted) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.ScalarNode, Style: yaml.DoubleQuotedStyle, Value: string(q)}, nil
}
