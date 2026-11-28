package compose

import (
	"reflect"
	"testing"
)

func TestEnvRoundTrip(t *testing.T) {
	in := map[string]string{"A": "plain", "B": "it's $x", "C": "line\nbreak \\ \"q\"", "D": ""}
	got, err := ParseEnv(MarshalEnv(in))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("got %q, want %q", got, in)
	}
	got, err = ParseEnv([]byte("# c\nexport X=1 # note\nY=\"a\\tb\"\n"))
	if err != nil || got["X"] != "1" || got["Y"] != "a\tb" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := ParseEnv([]byte("nope\n")); err == nil {
		t.Error("want error for a line without =")
	}
}
