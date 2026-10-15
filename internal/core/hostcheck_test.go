package core

import "testing"

func TestSecurityItems(t *testing.T) {
	out := `os=Ubuntu 24.04.1 LTS
pm=apt
updates=12
security_updates=3
reboot=1
auto_updates=0
ssh_port=2222
ssh_passwordauthentication=yes
ssh_permitrootlogin=without-password
ssh_keys=2
firewall=
fail2ban=0
listen=0.0.0.0:2222 users:(("sshd",pid=812,fd=3))
listen=[::]:2222 users:(("sshd",pid=812,fd=4))
listen=0.0.0.0:443 users:(("docker-proxy",pid=1500,fd=7))
listen=127.0.0.53%lo:53 users:(("systemd-resolve",pid=500,fd=14))
listen=100.99.32.90%tailscale0:45091 users:(("tailscaled",pid=900,fd=20))
listen=192.168.122.1:53 users:(("dnsmasq",pid=901,fd=6))
listen=0.0.0.0:3306 users:(("mysqld",pid=1000,fd=21))
listen=[::]:3306 users:(("mysqld",pid=1000,fd=22))
`
	f := parseHostFacts(out)
	if f.kv["os"] != "Ubuntu 24.04.1 LTS" || len(f.listen) != 8 || f.listen[0].process != "sshd" || f.listen[0].port != "2222" {
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
		if it.Status == SecurityFail || it.Status == SecurityWarn {
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
