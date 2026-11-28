package mcp

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

type deployIn struct {
	Service string `json:"service" jsonschema:"the service as project/service, or its ID"`
	Tag     string `json:"tag,omitempty" jsonschema:"image services only: deploy this tag of the service's image, which then keeps it"`
}

func (t *tools) deploy(ctx context.Context, _ *mcp.CallToolRequest, in deployIn) (*mcp.CallToolResult, domain.Deployment, error) {
	dep, err := mutate(ctx, t.c, "deploy", in.Service, func() (domain.Deployment, error) {
		svc, err := t.service(ctx, in.Service, identity.PermDeploy)
		if err != nil {
			return domain.Deployment{}, err
		}
		return t.c.Deploy(ctx, svc.ID, deploy.DeployOptions{Tag: in.Tag})
	})
	return nil, dep, err
}

type deployImageIn struct {
	Project string            `json:"project" jsonschema:"project name (created if missing)"`
	Name    string            `json:"name" jsonschema:"service name (lowercase, digits, dashes)"`
	Image   string            `json:"image" jsonschema:"Docker image, e.g. ghcr.io/org/app:v1"`
	Domain  string            `json:"domain,omitempty" jsonschema:"public hostname; omit for a private service (an existing app keeps its domain)"`
	Port    int               `json:"port,omitempty" jsonschema:"container port Traefik routes HTTPS to (required with a domain, unless the app publishes ports; an existing app keeps its port)"`
	Env     map[string]string `json:"env,omitempty" jsonschema:"environment variables to set; a value may reference a project variable as {{ project.NAME }}"`
	// Pointers: omitted keeps an existing service's value.
	PublishedPorts   *[]publishedPortIn `json:"publishedPorts,omitempty" jsonschema:"host ports bound straight to the container, for non-HTTP traffic (e.g. a game server); the app then runs one replica and deploys stop-then-start"`
	StopGraceSeconds *int               `json:"stopGraceSeconds,omitempty" jsonschema:"seconds the app gets to exit after SIGTERM before it is killed (default 10, max 600)"`
	Routes           *[]routeIn         `json:"routes,omitempty" jsonschema:"path prefixes of the domain routed to this app, each with its scheme and port; then only these paths reach it (another service of the project can take the rest of the domain)"`
}

type routeIn struct {
	Paths  []string `json:"paths" jsonschema:"path prefixes, e.g. /api or /my.grpc.Service/"`
	Scheme string   `json:"scheme,omitempty" jsonschema:"http (default) or h2c (HTTP/2 cleartext, for gRPC)"`
	Port   int      `json:"port,omitempty" jsonschema:"container port; defaults to port"`
}

func (in deployImageIn) routes() []domain.Route {
	if in.Routes == nil {
		return nil
	}
	out := make([]domain.Route, 0, len(*in.Routes))
	for _, r := range *in.Routes {
		out = append(out, domain.Route{Paths: r.Paths, Scheme: r.Scheme, Port: r.Port})
	}
	return out
}

type publishedPortIn struct {
	HostPort      int    `json:"hostPort" jsonschema:"port on the server"`
	ContainerPort int    `json:"containerPort,omitempty" jsonschema:"port in the container; defaults to hostPort"`
	Protocol      string `json:"protocol,omitempty" jsonschema:"tcp (default) or udp"`
}

func (in deployImageIn) ports() []domain.PublishedPort {
	if in.PublishedPorts == nil {
		return nil
	}
	out := make([]domain.PublishedPort, 0, len(*in.PublishedPorts))
	for _, p := range *in.PublishedPorts {
		out = append(out, domain.PublishedPort{HostPort: p.HostPort, ContainerPort: p.ContainerPort, Protocol: p.Protocol})
	}
	return out
}

// deployImage needs the settings permission to create apps or change anything
// but the tag: it could otherwise run any image with env that references
// password-manager and database secrets, publish host ports and claim domains.
// The deploy permission gets what REST deploy allows, another tag of an
// existing app's image.
func (t *tools) deployImage(ctx context.Context, _ *mcp.CallToolRequest, in deployImageIn) (*mcp.CallToolResult, domain.Deployment, error) {
	dep, err := mutate(ctx, t.c, "deploy_image", in.Project+"/"+in.Name, func() (domain.Deployment, error) {
		project, err := t.findProject(ctx, in.Project)
		if err == nil && identity.Authorize(ctx, project.ID, identity.PermSettings) != nil {
			return t.retag(ctx, in)
		}
		if errors.Is(err, domain.ErrNotFound) {
			project, err = t.c.CreateProject(ctx, in.Project, "")
		}
		if err != nil {
			return domain.Deployment{}, err
		}
		svc, err := t.c.ResolveService(ctx, project.Name+"/"+in.Name)
		switch {
		case errors.Is(err, domain.ErrNotFound):
			input := deploy.ServiceInput{
				Name: in.Name, Image: in.Image, Domain: in.Domain, Port: in.Port, Env: in.Env, PublishedPorts: in.ports(),
				Routes: in.routes(),
			}
			if in.StopGraceSeconds != nil {
				input.StopGraceSeconds = *in.StopGraceSeconds
			}
			view, err := t.c.CreateService(ctx, project.ID, input)
			if err != nil {
				return domain.Deployment{}, err
			}
			svc = view.Service
		case err != nil:
			return domain.Deployment{}, err
		default:
			if svc.Kind != domain.ServiceKindApp {
				return domain.Deployment{}, fmt.Errorf("%s/%s exists and is not an app", project.Name, in.Name)
			}
			patch := deploy.ServicePatch{Image: &in.Image, PublishedPorts: in.ports(), StopGraceSeconds: in.StopGraceSeconds, Routes: in.routes()}
			// Omitted keeps the app's domain and port, as in retag.
			if in.Domain != "" {
				patch.Domain = &in.Domain
			}
			if in.Port != 0 {
				patch.Port = &in.Port
			}
			if in.Env != nil {
				// Keep existing entries; new ones become secrets.
				env := maps.Clone(svc.Env)
				maps.Copy(env, in.Env)
				patch.Env = env
			}
			if _, err := t.c.UpdateService(ctx, svc.ID, patch); err != nil {
				return domain.Deployment{}, err
			}
		}
		return t.c.Deploy(ctx, svc.ID, deploy.DeployOptions{})
	})
	return nil, dep, err
}

// retag is deploy_image without the settings permission: the app must exist
// and only its image tag or digest may change.
func (t *tools) retag(ctx context.Context, in deployImageIn) (domain.Deployment, error) {
	svc, err := t.service(ctx, in.Project+"/"+in.Name, identity.PermDeploy)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Deployment{}, fmt.Errorf("%w: needs the settings permission to create an app", identity.ErrForbidden)
	}
	if err != nil {
		return domain.Deployment{}, err
	}
	if svc.Kind != domain.ServiceKindApp {
		return domain.Deployment{}, fmt.Errorf("%s/%s exists and is not an app", in.Project, in.Name)
	}
	if in.Env != nil || in.PublishedPorts != nil || in.StopGraceSeconds != nil || in.Routes != nil ||
		(in.Domain != "" && in.Domain != svc.Domain) || (in.Port != 0 && in.Port != svc.Port) {
		return domain.Deployment{}, fmt.Errorf("%w: needs the settings permission to change anything but the image tag", identity.ErrForbidden)
	}
	opts, err := deploy.RetagOptions(svc.Image, in.Image)
	if err != nil {
		return domain.Deployment{}, err
	}
	return t.c.Deploy(ctx, svc.ID, opts)
}

type rollbackIn struct {
	Service      string `json:"service" jsonschema:"the service as project/service, or its ID"`
	DeploymentID string `json:"deployment_id,omitempty" jsonschema:"deployment to return to; default: the previous successful one"`
}

func (t *tools) rollback(ctx context.Context, _ *mcp.CallToolRequest, in rollbackIn) (*mcp.CallToolResult, domain.Deployment, error) {
	dep, err := mutate(ctx, t.c, "rollback", in.Service, func() (domain.Deployment, error) {
		svc, err := t.service(ctx, in.Service, identity.PermDeploy)
		if err != nil {
			return domain.Deployment{}, err
		}
		return t.c.Rollback(ctx, svc.ID, in.DeploymentID)
	})
	return nil, dep, err
}
