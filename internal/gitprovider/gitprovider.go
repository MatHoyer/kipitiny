// Package gitprovider talks to the APIs of git forges (GitHub, GitLab,
// Gitea): GitHub App manifests and installation tokens, OAuth
// authorizations, and listing the repositories and branches a credential
// can read. It keeps no state; the core stores credentials.
package gitprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// maxPages bounds listings, enough for any account a picker can show.
const maxPages = 10

// pageSize is the most a forge returns per page (Gitea caps at 50 by
// default).
func pageSize(kind store.GitProviderKind) int {
	if kind == store.Gitea {
		return 50
	}
	return 100
}

var client = &http.Client{Timeout: 30 * time.Second}

// Repo is a repository a provider can read.
type Repo struct {
	// FullName is owner/name (group/subgroup/name on GitLab).
	FullName      string `json:"fullName"`
	CloneURL      string `json:"cloneUrl"`
	DefaultBranch string `json:"defaultBranch"`
	Private       bool   `json:"private"`
}

// DefaultBaseURL is the hosted forge of a kind.
func DefaultBaseURL(kind store.GitProviderKind) string {
	switch kind {
	case store.GitHub:
		return "https://github.com"
	case store.GitLab:
		return "https://gitlab.com"
	}
	return ""
}

// APIURL is the REST API root of a forge.
func APIURL(kind store.GitProviderKind, baseURL string) string {
	switch kind {
	case store.GitHub:
		if baseURL == "https://github.com" {
			return "https://api.github.com"
		}
		return baseURL + "/api/v3" // GitHub Enterprise Server
	case store.GitLab:
		return baseURL + "/api/v4"
	}
	return baseURL + "/api/v1"
}

// CloneUsername goes with the token in HTTPS clone credentials.
func CloneUsername(kind store.GitProviderKind) string {
	if kind == store.GitHub {
		return "x-access-token"
	}
	return "oauth2"
}

// Repos lists the repositories token can read.
func Repos(ctx context.Context, kind store.GitProviderKind, baseURL, token string) ([]Repo, error) {
	api, size := APIURL(kind, baseURL), pageSize(kind)
	repos := []Repo{}
	for page := 1; page <= maxPages; page++ {
		var batch []Repo
		var n int
		var err error
		switch kind {
		case store.GitHub:
			var body struct {
				Repositories []githubRepo `json:"repositories"`
			}
			err = getJSON(ctx, kind, fmt.Sprintf("%s/installation/repositories?per_page=%d&page=%d", api, size, page), token, &body)
			for _, r := range body.Repositories {
				batch = append(batch, Repo{r.FullName, r.CloneURL, r.DefaultBranch, r.Private})
			}
			n = len(body.Repositories)
		case store.GitLab:
			var body []struct {
				PathWithNamespace string `json:"path_with_namespace"`
				HTTPURLToRepo     string `json:"http_url_to_repo"`
				DefaultBranch     string `json:"default_branch"`
				Visibility        string `json:"visibility"`
			}
			err = getJSON(ctx, kind, fmt.Sprintf("%s/projects?membership=true&simple=true&order_by=last_activity_at&per_page=%d&page=%d", api, size, page), token, &body)
			for _, r := range body {
				batch = append(batch, Repo{r.PathWithNamespace, r.HTTPURLToRepo, r.DefaultBranch, r.Visibility != "public"})
			}
			n = len(body)
		default:
			var body []githubRepo // Gitea's shape matches GitHub's
			err = getJSON(ctx, kind, fmt.Sprintf("%s/user/repos?limit=%d&page=%d", api, size, page), token, &body)
			for _, r := range body {
				batch = append(batch, Repo{r.FullName, r.CloneURL, r.DefaultBranch, r.Private})
			}
			n = len(body)
		}
		if err != nil {
			return nil, err
		}
		repos = append(repos, batch...)
		if n < size {
			break
		}
	}
	return repos, nil
}

type githubRepo struct {
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

// Branches lists a repository's branches (fullName as Repos returns it).
func Branches(ctx context.Context, kind store.GitProviderKind, baseURL, token, fullName string) ([]string, error) {
	api, size := APIURL(kind, baseURL), pageSize(kind)
	names := []string{}
	for page := 1; page <= maxPages; page++ {
		var u string
		switch kind {
		case store.GitHub:
			u = fmt.Sprintf("%s/repos/%s/branches?per_page=%d&page=%d", api, fullName, size, page)
		case store.GitLab:
			u = fmt.Sprintf("%s/projects/%s/repository/branches?per_page=%d&page=%d", api, url.PathEscape(fullName), size, page)
		default:
			u = fmt.Sprintf("%s/repos/%s/branches?limit=%d&page=%d", api, fullName, size, page)
		}
		var body []struct {
			Name string `json:"name"`
		}
		if err := getJSON(ctx, kind, u, token, &body); err != nil {
			return nil, err
		}
		for _, b := range body {
			names = append(names, b.Name)
		}
		if len(body) < size {
			break
		}
	}
	return names, nil
}

// Account is the login token belongs to (OAuth providers).
func Account(ctx context.Context, kind store.GitProviderKind, baseURL, token string) (string, error) {
	var body struct {
		Login    string `json:"login"`
		Username string `json:"username"`
	}
	if err := getJSON(ctx, kind, APIURL(kind, baseURL)+"/user", token, &body); err != nil {
		return "", err
	}
	if body.Username != "" {
		return body.Username, nil
	}
	return body.Login, nil
}

func getJSON(ctx context.Context, kind store.GitProviderKind, u, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	switch kind {
	case store.GitHub:
		req.Header.Set("Authorization", "token "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
	default:
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
	}
	return do(req, out)
}

func do(req *http.Request, out any) error {
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 10<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return &APIError{Status: res.StatusCode, Message: apiMessage(body)}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// APIError is a forge's refusal.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
}

// Unauthorized reports a refused credential (revoked, uninstalled).
func Unauthorized(err error) bool {
	var e *APIError
	return errors.As(err, &e) && (e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden)
}

// apiMessage picks the explanation out of an error body.
func apiMessage(body []byte) string {
	var m struct {
		Message          string `json:"message"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if json.Unmarshal(body, &m) == nil {
		for _, s := range []string{m.Message, m.ErrorDescription, m.Error} {
			if s != "" {
				return s
			}
		}
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
