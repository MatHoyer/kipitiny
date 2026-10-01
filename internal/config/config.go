// Package config loads manager settings from the environment.
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	// Addr is the HTTP listen address for the UI and API.
	Addr string
	// Domain, if set, serves the UI and API over HTTPS through Traefik.
	Domain string
	// DataDir holds the SQLite database, deploy logs and local backups.
	// Must be on a local disk (never NFS/SMB).
	DataDir string
	// LogLevel is one of debug, info, warn, error.
	LogLevel string

	// SetupToken, if set, replaces the random token printed on first start
	// that is required to create the admin account.
	SetupToken string

	// BuilderImage runs `docker build` for git services (BuildKit via the CLI).
	BuilderImage string

	// ProtonPassCLI is the pass-cli binary (path or name on PATH).
	ProtonPassCLI string

	// Rclone (Google Drive) and ProtonDriveCLI (proton-drive) reach drive
	// backup targets.
	Rclone         string
	ProtonDriveCLI string

	// Version is the running build (set by main, not the environment).
	Version string
	Update  Update

	Traefik       Traefik
	Tunnel        Tunnel
	ManagerBackup ManagerBackup
}

// Update is where the manager looks for new versions of itself.
type Update struct {
	// Image is the published manager image, without tag.
	Image string
	// Check is false when KIPITINY_UPDATE_CHECK=off.
	Check bool
}

// Tunnel receives public traffic through a Cloudflare Tunnel instead of
// ports 80/443, on the manager's own server.
type Tunnel struct {
	// Token of a remotely-managed tunnel; empty disables it.
	Token string
	Image string
}

// ManagerBackup schedules backups of the manager's own SQLite state.
type ManagerBackup struct {
	// Cron is empty when disabled (KIPITINY_MANAGER_BACKUP_CRON=off).
	Cron     string
	TargetID string
	// Keep is how many manager backups to keep per target (0 = all).
	Keep int
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
		Addr:           env("KIPITINY_ADDR", ":3000"),
		Domain:         strings.ToLower(strings.TrimSpace(env("KIPITINY_DOMAIN", ""))),
		DataDir:        env("KIPITINY_DATA_DIR", "/data"),
		LogLevel:       env("KIPITINY_LOG_LEVEL", "info"),
		SetupToken:     env("KIPITINY_SETUP_TOKEN", ""),
		BuilderImage:   env("KIPITINY_BUILDER_IMAGE", "docker:cli"),
		ProtonPassCLI:  env("KIPITINY_PROTONPASS_CLI", "pass-cli"),
		Rclone:         env("KIPITINY_RCLONE", "rclone"),
		ProtonDriveCLI: env("KIPITINY_PROTONDRIVE_CLI", "proton-drive"),
		Traefik: Traefik{
			Enabled:      env("KIPITINY_TRAEFIK", "true") != "false",
			Image:        env("KIPITINY_TRAEFIK_IMAGE", "traefik:v3.7"),
			HTTPPort:     env("KIPITINY_HTTP_PORT", "80"),
			HTTPSPort:    env("KIPITINY_HTTPS_PORT", "443"),
			ACMEEmail:    env("KIPITINY_ACME_EMAIL", ""),
			DockerSocket: env("KIPITINY_DOCKER_SOCKET", "/var/run/docker.sock"),
		},
		Update: Update{
			Image: env("KIPITINY_IMAGE", "ghcr.io/mathoyer/kipitiny"),
			Check: !strings.EqualFold(env("KIPITINY_UPDATE_CHECK", "on"), "off"),
		},
		Tunnel: Tunnel{
			Token: env("KIPITINY_CLOUDFLARE_TUNNEL_TOKEN", ""),
			Image: env("KIPITINY_CLOUDFLARED_IMAGE", "cloudflare/cloudflared:2026.9.3"),
		},
		ManagerBackup: ManagerBackup{
			Cron:     managerCron(env("KIPITINY_MANAGER_BACKUP_CRON", "@daily")),
			TargetID: env("KIPITINY_MANAGER_BACKUP_TARGET", "local"),
			Keep:     envInt("KIPITINY_MANAGER_BACKUP_KEEP", 14),
		},
	}
}

func managerCron(v string) string {
	if strings.EqualFold(v, "off") {
		return ""
	}
	return v
}

func envInt(key string, fallback int) int {
	n, err := strconv.Atoi(env(key, ""))
	if err != nil {
		return fallback
	}
	return n
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
