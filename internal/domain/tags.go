package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Tags group projects and services in the UI. They are free-form names.

const (
	maxTags   = 10
	MaxTagLen = 32
)

var tagRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ._/:-]*$`)

// NormalizeTag lowercases a tag and collapses its spaces.
func NormalizeTag(t string) string {
	return strings.ToLower(strings.Join(strings.Fields(t), " "))
}

// NormalizeTags returns the valid, distinct tags, sorted.
func NormalizeTags(tags []string) ([]string, error) {
	out := []string{}
	for _, t := range tags {
		t = NormalizeTag(t)
		if t == "" || slices.Contains(out, t) {
			continue
		}
		if len([]rune(t)) > MaxTagLen || !tagRe.MatchString(t) {
			return nil, fmt.Errorf("%w: tag %q must be up to %d letters, digits, spaces and . _ / : -", ErrInvalid, t, MaxTagLen)
		}
		out = append(out, t)
	}
	if len(out) > maxTags {
		return nil, fmt.Errorf("%w: up to %d tags", ErrInvalid, maxTags)
	}
	slices.Sort(out)
	return out, nil
}
