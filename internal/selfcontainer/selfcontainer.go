// Package selfcontainer finds the Docker container this process runs in.
package selfcontainer

import (
	"os"
	"regexp"
)

// Docker bind-mounts /etc/hostname etc. from /var/lib/docker/containers/<id>/.
var mountinfoID = regexp.MustCompile(`/containers/([0-9a-f]{64})/`)

// ID returns the ID of the container this process runs in, or "" when it
// runs directly on a host.
func ID() string {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}
	return parseMountinfoID(b)
}

func parseMountinfoID(mountinfo []byte) string {
	if m := mountinfoID.FindSubmatch(mountinfo); m != nil {
		return string(m[1])
	}
	return ""
}
