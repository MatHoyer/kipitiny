// Package config loads manager settings from the environment.
package config

import (
	"os"
	"path/filepath"
)

type Config struct {
	// Addr is the HTTP listen address for the UI and API.
	Addr string
	// DataDir holds the SQLite database, deploy logs and local backups.
	// Must be on a local disk (never NFS/SMB).
	DataDir string
	// LogLevel is one of debug, info, warn, error.
	LogLevel string

	Traefik Traefik
}

type Traefik struct {
	// Enabled makes the manager run and maintain the Traefik container.
	Enabled   bool
	Image     string
	HTTPPort  string
	HTTPSPort string
	// ACMEEmail is passed to Let's Encrypt; optional.
	ACMEEmail string
	// DockerSocket is the socket path on the host, mounted into Traefik.
	DockerSocket string
}

func Load() Config {
	return Config{
		Addr:     env("KIPITINY_ADDR", ":3000"),
		DataDir:  env("KIPITINY_DATA_DIR", "/data"),
		LogLevel: env("KIPITINY_LOG_LEVEL", "info"),
		Traefik: Traefik{
			Enabled:      env("KIPITINY_TRAEFIK", "true") != "false",
			Image:        env("KIPITINY_TRAEFIK_IMAGE", "traefik:v3.7"),
			HTTPPort:     env("KIPITINY_HTTP_PORT", "80"),
			HTTPSPort:    env("KIPITINY_HTTPS_PORT", "443"),
			ACMEEmail:    env("KIPITINY_ACME_EMAIL", ""),
			DockerSocket: env("KIPITINY_DOCKER_SOCKET", "/var/run/docker.sock"),
		},
	}
}

func (c Config) DBPath() string {
	return filepath.Join(c.DataDir, "kipitiny.db")
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
