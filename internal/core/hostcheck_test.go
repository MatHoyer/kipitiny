package core

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestSecurityItems(t *testing.T) {
	out := `os=Ubuntu 24.04.1 LTS
pm=apt
updates=12
security_updates=3
reboot=1
auto_updates=0
ssh_port=2222
ssh_port=22
ssh_passwordauthentication=yes
ssh_permitrootlogin=without-password
ssh_keys=2
firewall=
fail2ban=0
listen=0.0.0.0:2222 users:(("sshd",pid=812,fd=3))
listen=[::]:2222 users:(("sshd",pid=812,fd=4))
listen=0.0.0.0:22 users:(("sshd",pid=812,fd=5))
listen=0.0.0.0:443 users:(("docker-proxy",pid=1500,fd=7))
listen=127.0.0.53%lo:53 users:(("systemd-resolve",pid=500,fd=14))
listen=100.99.32.90%tailscale0:45091 users:(("tailscaled",pid=900,fd=20))
listen=192.168.122.1:53 users:(("dnsmasq",pid=901,fd=6))
listen=0.0.0.0:3306 users:(("mysqld",pid=1000,fd=21))
listen=[::]:3306 users:(("mysqld",pid=1000,fd=22))
`
	f := parseHostFacts(out)
	if f.kv["os"] != "Ubuntu 24.04.1 LTS" || len(f.listen) != 9 || len(f.sshPorts) != 2 || f.listen[0].process != "sshd" || f.listen[0].port != "2222" {
		t.Fatalf("facts: %+v", f)
	}
	got := map[string]SecurityItem{}
	for _, it := range securityItems(f) {
		got[it.ID] = it
	}
	want := map[string]string{
		"updates":      SecurityWarn,
		"reboot":       SecurityWarn,
		"auto_updates": SecurityFail,
		"ssh_password": SecurityFail,
		"ssh_root":     SecurityOK,
		"firewall":     SecurityFail,
		"fail2ban":     SecurityWarn,
		"ports":        SecurityWarn,
	}
	for id, status := range want {
		if got[id].Status != status {
			t.Errorf("%s: %+v, want %s", id, got[id], status)
		}
	}
	if d := got["ports"].Detail; d != "Reachable from outside: 3306 (mysqld)." {
		t.Errorf("ports: %q", d)
	}

	// No package manager or sshd (a container, a desktop): unknown, not failed.
	items := securityItems(parseHostFacts("firewall=ufw\nfail2ban=1\n"))
	for _, it := range items {
		if it.Status == SecurityFail || it.Status == SecurityWarn || it.ID == "ssh_password" && it.Status != SecurityOK {
			t.Errorf("bare host: %+v", it)
		}
	}
}

func TestPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"0.0.0.0": true, "*": true, "::": true, "203.0.113.7": true, "2001:db8::1": true,
		"127.0.0.1": false, "::1": false, "127.0.0.53%lo": false, "10.0.0.4": false,
		"192.168.122.1": false, "100.99.32.90%tailscale0": false, "fd7a:115c:a1e0::1%tailscale0": false, "fe80::1%eth0": false,
	} {
		if got := public(addr); got != want {
			t.Errorf("public(%q) = %v", addr, got)
		}
	}
}

func TestPendingUpdates(t *testing.T) {
	f := parseHostFacts(`pm=apt
inst=openssl [3.0.13-0ubuntu3.4] (3.0.13-0ubuntu3.5 Ubuntu:24.04/noble-updates, Ubuntu:24.04/noble-security [amd64])
inst=linux-image-6.8.0-50-generic (6.8.0-50.51 Ubuntu:24.04/noble-updates, Ubuntu:24.04/noble-security [amd64])
inst=linux-image-virtual [6.8.0-31.31] (6.8.0-50.51 Ubuntu:24.04/noble-updates, Ubuntu:24.04/noble-security [amd64])
inst=docker-ce [5:27.3.1-1~ubuntu.24.04~noble] (5:27.4.0-1~ubuntu.24.04~noble Docker CE:stable [amd64])
inst=openssh-server [1:9.6p1-3ubuntu13.5] (1:9.6p1-3ubuntu13.8 Ubuntu:24.04/noble-security [amd64])
`)
	want := []PendingUpdate{
		{Name: "linux-image-virtual", Version: "6.8.0-50.51", Security: true, Impact: ImpactReboot},
		{Name: "openssh-server", Version: "1:9.6p1-3ubuntu13.8", Security: true, Impact: ImpactSSH},
		{Name: "openssl", Version: "3.0.13-0ubuntu3.5", Security: true},
		{Name: "docker-ce", Version: "5:27.4.0-1~ubuntu.24.04~noble", Impact: ImpactDocker},
		{Name: "linux-image-6.8.0-50-generic", Version: "6.8.0-50.51", Impact: ImpactReboot},
	}
	if !slices.Equal(f.updates, want) {
		t.Errorf("apt: %+v", f.updates)
	}

	f = parseHostFacts(`pm=dnf
upd=openssl.x86_64 1:3.1.4-2.fc39
upd=kernel-core.x86_64 6.11.4-201.fc40
upd=vim-enhanced.x86_64 2:9.1.785-1.fc40
sec=openssl-1:3.1.4-2.fc39.x86_64
sec=kernel-core-6.11.4-201.fc40.x86_64
`)
	want = []PendingUpdate{
		{Name: "kernel-core", Version: "6.11.4-201.fc40", Security: true, Impact: ImpactReboot},
		{Name: "openssl", Version: "1:3.1.4-2.fc39", Security: true},
		{Name: "vim-enhanced", Version: "2:9.1.785-1.fc40"},
	}
	if !slices.Equal(f.updates, want) {
		t.Errorf("dnf: %+v", f.updates)
	}
}

func TestHostScriptsParse(t *testing.T) {
	for name, s := range map[string]string{
		"check": hostCheckScript, "start": startUpdatesScript("kipitiny-updates-x", false),
		"start all": startUpdatesScript("kipitiny-updates-x", true), "install": installScript(false), "wait": waitUpdatesScript("kipitiny-updates-x"),
	} {
		cmd := exec.Command("sh", "-n")
		cmd.Stdin = strings.NewReader(s)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: %v %s", name, err, out)
		}
	}
}

func TestInstallError(t *testing.T) {
	u := "kipitiny-updates-x"
	for log, want := range map[string]string{
		"Started kipitiny-updates-x.service - kipitiny: install updates.\nE: Malformed line 1 in source list\nE: The list of sources could not be read.\n" +
			"kipitiny-updates-x.service: Main process exited, code=exited, status=100/n/a\nkipitiny-updates-x.service: Failed with result 'exit-code'.": "E: Malformed line 1 in source list",
		"Started kipitiny-updates-x.service - kipitiny: install updates.\ndpkg: error processing package foo\nkipitiny-updates-x.service: Failed with result 'exit-code'.": "dpkg: error processing package foo",
	} {
		if got := installError(log, u); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
