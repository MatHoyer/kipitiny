package selfcontainer

import "testing"

func TestParseMountinfoID(t *testing.T) {
	id := "3f4e5d6c7b8a99887766554433221100ffeeddccbbaa00112233445566778899"
	in := "1 0 0:1 / / rw - overlay overlay rw\n" +
		"2 1 8:1 /var/lib/docker/containers/" + id + "/hostname /etc/hostname rw - ext4 /dev/sda1 rw\n"
	if got := parseMountinfoID([]byte(in)); got != id {
		t.Errorf("got %q", got)
	}
	if got := parseMountinfoID([]byte("1 0 8:1 / / rw - ext4 /dev/sda1 rw\n")); got != "" {
		t.Errorf("host process: got %q", got)
	}
}
