package docs

import (
	"strings"
	"testing"
)

func TestIndex(t *testing.T) {
	idx := Index()
	for _, d := range Documents {
		if d.Content == "" {
			t.Errorf("%s is empty", d.Slug)
		}
		if !strings.Contains(idx, "/docs/"+d.Slug+"/llms.txt") {
			t.Errorf("index doesn't link %s", d.Slug)
		}
		if got, ok := Find(d.Slug); !ok || got.Title != d.Title {
			t.Errorf("Find(%s) = %v %v", d.Slug, got, ok)
		}
	}
	if _, ok := Find("nope"); ok {
		t.Error("Find(nope) found something")
	}
}
