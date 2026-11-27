package network

// The manager's maintenance pages, which every Traefik routes to.
const (
	// PagesService is the Traefik service (file provider) reaching the
	// manager's pages; PagesDown the errors middleware every router of
	// websecure goes through.
	PagesService = "kipitiny-pages"
	PagesDown    = "kipitiny-down"
	// PagesPath is where the manager serves maintenance pages and the
	// Traefik configuration that routes to them.
	PagesPath = "/api/pages/"
)

const (
	// MetricsAddr is Traefik's Prometheus entrypoint, on loopback only: the
	// manager reads it with an exec in the container, so it works the same on
	// remote servers and apps on the proxy network can't read it.
	MetricsAddr = "127.0.0.1:8082"
)

// PagesFileConfig is the part of the pages that never changes for a server,
// in Traefik's file provider: if it came from the HTTP provider, a manager
// that is down would take every router using kipitiny-down with it.
func PagesFileConfig(upstream string) map[string]any {
	return map[string]any{
		"middlewares": map[string]any{PagesDown: map[string]any{"errors": map[string]any{
			// Traefik's own failures to reach a replica. Apps' own 503s
			// are theirs; an app without a ready replica has no router,
			// which the fallback router of TraefikConfig catches.
			"status":         []string{"502", "504"},
			"statusRewrites": map[string]int{"502-504": 503},
			"service":        PagesService,
			"query":          PagesPath + "down?url={url}",
			// Not the app's cookies or credentials. Accept tells a page
			// from an API call (an app waking from sleep).
			"errorRequestHeaders": []string{"Accept", "Accept-Language"},
		}}},
		"services": map[string]any{PagesService: map[string]any{"loadBalancer": map[string]any{
			"servers":        []map[string]string{{"url": upstream}},
			"passHostHeader": false,
		}}},
	}
}
