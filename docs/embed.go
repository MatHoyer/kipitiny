// Package docs embeds the documentation the manager serves.
package docs

import _ "embed"

// ComposeReference is the compose file format, served at /llms.txt and in
// the UI.
//
//go:embed compose.md
var ComposeReference string
