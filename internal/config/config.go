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
}

func Load() Config {
	return Config{
		Addr:     env("KIPITINY_ADDR", ":3000"),
		DataDir:  env("KIPITINY_DATA_DIR", "/data"),
		LogLevel: env("KIPITINY_LOG_LEVEL", "info"),
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
