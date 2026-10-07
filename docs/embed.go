// Package docs embeds the documentation the manager serves.
package docs

import (
	_ "embed"
	"fmt"
	"strings"
)

// ComposeReference is the compose file format.
//
//go:embed compose.md
var ComposeReference string

// Document is one document the manager serves, as markdown at
// /docs/<Slug>/llms.txt.
type Document struct {
	Slug        string
	Title       string
	Description string
	Content     string
}

// Documents are listed by the /llms.txt index and the UI's Docs page.
var Documents = []Document{
	{
		Slug:        "compose",
		Title:       "Compose reference",
		Description: "the docker-compose format of a kipitiny project: keys, x-kipitiny settings, variables and secrets, git sync",
		Content:     ComposeReference,
	},
}

// Find returns the document with that slug.
func Find(slug string) (Document, bool) {
	for _, d := range Documents {
		if d.Slug == slug {
			return d, true
		}
	}
	return Document{}, false
}

// Index is /llms.txt: what kipitiny is and where each document is.
func Index() string {
	var b strings.Builder
	b.WriteString("# kipitiny\n\n> Lightweight self-hosted PaaS: Docker apps and PostgreSQL/Redis databases with clean backups. This manager serves its documentation as markdown.\n\n## Docs\n\n")
	for _, d := range Documents {
		fmt.Fprintf(&b, "- [%s](/docs/%s/llms.txt): %s\n", d.Title, d.Slug, d.Description)
	}
	return b.String()
}
