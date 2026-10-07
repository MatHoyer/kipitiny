// Package docs fetches kipitiny's documentation for agents: from the website
// (site/ in this repo), or from the repo on GitHub when the site can't be
// reached. The manager itself doesn't ship the docs.
package docs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// SiteURL is where the documentation is published.
const SiteURL = "https://kipitiny.mathieuhoyer.fr"

const (
	rawURL = "https://raw.githubusercontent.com/MatHoyer/kipitiny"
	apiURL = "https://api.github.com/repos/MatHoyer/kipitiny/contents"
	// dir holds one markdown file per page, in the repo.
	dir     = "site/docs"
	maxBody = 2 << 20
)

// ErrNotFound means no page has that name.
var ErrNotFound = errors.New("no such documentation page")

var (
	slugRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	releaseRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	frontRe   = regexp.MustCompile(`(?s)^---\n(.*?)\n---\n+`)
	titleRe   = regexp.MustCompile(`(?m)^title:\s*(.+)$`)
)

// Page is one document as markdown, with the URL it was read from.
type Page struct {
	Source  string `json:"source"`
	Content string `json:"content"`
}

// Fetcher reads pages from the site, falling back to GitHub.
type Fetcher struct {
	Client *http.Client
	Site   string // published site, without trailing slash
	Raw    string // raw.githubusercontent.com/<owner>/<repo>
	API    string // GitHub contents API of the repo
	// Ref is the git ref the GitHub fallback reads: the manager's release
	// tag, so its docs match, or main for development builds.
	Ref string
}

// New returns a Fetcher for a manager of that version.
func New(version string) *Fetcher {
	ref := "main"
	if releaseRe.MatchString(version) {
		ref = version
	}
	return &Fetcher{
		Client: &http.Client{Timeout: 10 * time.Second},
		Site:   SiteURL,
		Raw:    rawURL,
		API:    apiURL,
		Ref:    ref,
	}
}

// Get returns the page named slug (as in /docs/<slug>), or the index of
// every page when slug is empty.
func (f *Fetcher) Get(ctx context.Context, slug string) (Page, error) {
	if slug == "" {
		return f.index(ctx)
	}
	if !slugRe.MatchString(slug) {
		return Page{}, ErrNotFound
	}
	url := f.Site + "/docs/" + slug + "/llms.txt"
	if body, err := f.get(ctx, url, ""); err == nil {
		return Page{Source: url, Content: body}, nil
	}
	for _, ref := range f.refs() {
		url := fmt.Sprintf("%s/%s/%s/%s.md", f.Raw, ref, dir, slug)
		body, err := f.get(ctx, url, "")
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return Page{}, fmt.Errorf("documentation unreachable (%s and GitHub): %w", f.Site, err)
		}
		return Page{Source: url, Content: stripFrontMatter(body)}, nil
	}
	return Page{}, ErrNotFound
}

// index is /llms.txt, or the list of files on GitHub.
func (f *Fetcher) index(ctx context.Context) (Page, error) {
	url := f.Site + "/llms.txt"
	if body, err := f.get(ctx, url, ""); err == nil {
		return Page{Source: url, Content: body}, nil
	}
	var lastErr error
	for _, ref := range f.refs() {
		url := fmt.Sprintf("%s/%s?ref=%s", f.API, dir, ref)
		body, err := f.get(ctx, url, "application/vnd.github+json")
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			lastErr = err
			break
		}
		var files []struct{ Name, Type string }
		if err := json.Unmarshal([]byte(body), &files); err != nil {
			return Page{}, fmt.Errorf("documentation index: %w", err)
		}
		var b strings.Builder
		b.WriteString("# kipitiny\n\n## Docs\n\n")
		for _, file := range files {
			if name, ok := strings.CutSuffix(file.Name, ".md"); ok && file.Type == "file" {
				fmt.Fprintf(&b, "- %s\n", name)
			}
		}
		return Page{Source: url, Content: b.String()}, nil
	}
	if lastErr == nil {
		lastErr = ErrNotFound
	}
	return Page{}, fmt.Errorf("documentation unreachable (%s and GitHub): %w", f.Site, lastErr)
}

// refs are the git refs to read on GitHub: the manager's own, then main
// for pages written after that release.
func (f *Fetcher) refs() []string {
	if f.Ref == "main" {
		return []string{"main"}
	}
	return []string{f.Ref, "main"}
}

func (f *Fetcher) get(ctx context.Context, url, accept string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	res, err := f.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return "", ErrNotFound
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	return string(body), err
}

// stripFrontMatter turns a page's source into the markdown the site serves:
// its title as a heading instead of the front matter.
func stripFrontMatter(src string) string {
	m := frontRe.FindStringSubmatchIndex(src)
	if m == nil {
		return src
	}
	body := src[m[1]:]
	if t := titleRe.FindStringSubmatch(src[m[2]:m[3]]); t != nil {
		return "# " + strings.TrimSpace(t[1]) + "\n\n" + body
	}
	return body
}
