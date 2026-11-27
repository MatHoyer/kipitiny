package domain

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestNormalizeTags(t *testing.T) {
	got, err := NormalizeTags([]string{" Prod ", "prod", "", "client  A", "v1.2/eu:west_x-y"})
	if err != nil || !slices.Equal(got, []string{"client a", "prod", "v1.2/eu:west_x-y"}) {
		t.Errorf("NormalizeTags = %q, %v", got, err)
	}
	if got, err := NormalizeTags(nil); err != nil || got == nil || len(got) != 0 {
		t.Errorf("no tags = %#v, %v", got, err)
	}
	for _, bad := range [][]string{{"-x"}, {"a,b"}, {strings.Repeat("a", MaxTagLen+1)}, {"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}} {
		if _, err := NormalizeTags(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("NormalizeTags(%q) = %v, want ErrInvalid", bad, err)
		}
	}
}
