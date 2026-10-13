// Package registry lists the tags of a public image and reads its labels,
// following the OCI distribution API's anonymous token flow (ghcr.io,
// Docker Hub, ...).
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strings"
)

const maxPages = 20

// Tags returns every tag of ref (e.g. "ghcr.io/owner/name").
func Tags(ctx context.Context, hc *http.Client, ref string) ([]string, error) {
	host, repo := split(ref)
	next := "https://" + host + "/v2/" + repo + "/tags/list?n=1000"
	var token string
	var tags []string
	for page := 0; next != "" && page < maxPages; page++ {
		res, err := get(ctx, hc, next, token)
		if err != nil {
			return nil, err
		}
		if res.StatusCode == http.StatusUnauthorized && token == "" {
			challenge := res.Header.Get("WWW-Authenticate")
			res.Body.Close()
			if token, err = fetchToken(ctx, hc, challenge); err != nil {
				return nil, err
			}
			page--
			continue
		}
		var body struct {
			Tags []string `json:"tags"`
		}
		err = decode(res, &body)
		if err != nil {
			return nil, err
		}
		tags = append(tags, body.Tags...)
		next = nextPage(res.Header.Get("Link"), next)
	}
	return tags, nil
}

// Manifest media types: an index lists one manifest per platform.
var manifestTypes = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

// Labels returns the labels of ref:tag's image, for this machine's
// architecture when the tag is multi-platform.
func Labels(ctx context.Context, hc *http.Client, ref, tag string) (map[string]string, error) {
	host, repo := split(ref)
	base := "https://" + host + "/v2/" + repo
	var token string
	var m struct {
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform struct {
				OS           string `json:"os"`
				Architecture string `json:"architecture"`
			} `json:"platform"`
		} `json:"manifests"`
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
	}
	if err := fetchJSON(ctx, hc, base+"/manifests/"+url.PathEscape(tag), manifestTypes, &token, &m); err != nil {
		return nil, err
	}
	if len(m.Manifests) > 0 {
		digest := m.Manifests[0].Digest
		for _, p := range m.Manifests {
			if p.Platform.OS == "linux" && p.Platform.Architecture == runtime.GOARCH {
				digest = p.Digest
			}
		}
		m.Manifests = nil
		if err := fetchJSON(ctx, hc, base+"/manifests/"+digest, manifestTypes, &token, &m); err != nil {
			return nil, err
		}
	}
	if m.Config.Digest == "" {
		return nil, errors.New("registry: the manifest names no image config")
	}
	var cfg struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
	}
	if err := fetchJSON(ctx, hc, base+"/blobs/"+m.Config.Digest, "", &token, &cfg); err != nil {
		return nil, err
	}
	return cfg.Config.Labels, nil
}

// fetchJSON GETs u into v, answering an auth challenge once with an
// anonymous token kept in token for the next calls.
func fetchJSON(ctx context.Context, hc *http.Client, u, accept string, token *string, v any) error {
	for range 2 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		if *token != "" {
			req.Header.Set("Authorization", "Bearer "+*token)
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		res, err := hc.Do(req)
		if err != nil {
			return err
		}
		if res.StatusCode == http.StatusUnauthorized && *token == "" {
			challenge := res.Header.Get("WWW-Authenticate")
			res.Body.Close()
			if *token, err = fetchToken(ctx, hc, challenge); err != nil {
				return err
			}
			continue
		}
		return decode(res, v)
	}
	return errors.New("registry: still unauthorized with a token")
}

// split turns an image reference into registry host and repository path,
// defaulting to Docker Hub like the Docker CLI does.
func split(ref string) (host, repo string) {
	first, rest, ok := strings.Cut(ref, "/")
	if ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		return first, rest
	}
	if !ok {
		return "registry-1.docker.io", "library/" + ref
	}
	return "registry-1.docker.io", ref
}

func get(ctx context.Context, hc *http.Client, u, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return hc.Do(req)
}

func decode(res *http.Response, v any) error {
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("registry: %s %s", res.Request.URL.Redacted(), res.Status)
	}
	return json.NewDecoder(res.Body).Decode(v)
}

var challengeParam = regexp.MustCompile(`(\w+)="([^"]*)"`)

// fetchToken answers a `Bearer realm=...,service=...,scope=...` challenge
// with an anonymous token.
func fetchToken(ctx context.Context, hc *http.Client, challenge string) (string, error) {
	scheme, params, _ := strings.Cut(challenge, " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return "", errors.New("registry: unsupported auth challenge (is the image public?)")
	}
	p := map[string]string{}
	for _, m := range challengeParam.FindAllStringSubmatch(params, -1) {
		p[m[1]] = m[2]
	}
	realm, err := url.Parse(p["realm"])
	if err != nil || realm.Scheme != "https" {
		return "", fmt.Errorf("registry: bad token realm %q", p["realm"])
	}
	q := realm.Query()
	for _, k := range []string{"service", "scope"} {
		if p[k] != "" {
			q.Set(k, p[k])
		}
	}
	realm.RawQuery = q.Encode()
	res, err := get(ctx, hc, realm.String(), "")
	if err != nil {
		return "", err
	}
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		res.Body.Close()
		return "", errors.New("registry: anonymous access refused (is the image published and public?)")
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := decode(res, &body); err != nil {
		return "", err
	}
	if body.Token != "" {
		return body.Token, nil
	}
	return body.AccessToken, nil
}

var linkNext = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// nextPage resolves a `Link: </v2/...>; rel="next"` header against the
// current page, or returns "" on the last page.
func nextPage(link, current string) string {
	m := linkNext.FindStringSubmatch(link)
	if m == nil {
		return ""
	}
	base, err := url.Parse(current)
	if err != nil {
		return ""
	}
	u, err := base.Parse(m[1])
	if err != nil {
		return ""
	}
	return u.String()
}
