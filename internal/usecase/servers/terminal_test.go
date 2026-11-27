package servers

import (
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestPickContainer(t *testing.T) {
	ct := func(id, name, replica string, state string) domain.Container {
		return domain.Container{ID: id, Names: []string{"/" + name}, State: state,
			Labels: map[string]string{domain.LabelReplica: replica}}
	}
	cts := []domain.Container{
		ct("aaaaaaaaaaaa1111", "p-web-10", "10", domain.StateRunning),
		ct("bbbbbbbbbbbb2222", "p-web-2", "2", domain.StateRunning),
		ct("cccccccccccc3333", "p-web-1", "1", domain.StateExited),
	}
	for _, tc := range []struct {
		ref, want string
		ok        bool
	}{
		{"", "p-web-2", true}, // lowest running replica, compared as numbers
		{"p-web-10", "p-web-10", true},
		{"bbbbbbbbbbbb", "p-web-2", true},
		{"bbbb", "", false}, // too short to be an ID
		{"p-web-1", "", false},
	} {
		got, ok := pickContainer(cts, tc.ref)
		if ok != tc.ok || ok && containerName(got) != tc.want {
			t.Errorf("pickContainer(%q) = %q, %v; want %q, %v", tc.ref, containerName(got), ok, tc.want, tc.ok)
		}
	}
}
