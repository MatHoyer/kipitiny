package servers

import (
	"bufio"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// hostFacts is the parsed output of hostCheckScript.
type hostFacts struct {
	kv     map[string]string
	listen []listener
	// sshPorts are the ports sshd listens on, checked numbers.
	sshPorts []string
	updates  []PendingUpdate
	// dnfSecurity names the packages dnf has security advisories for.
	dnfSecurity []string
}

type listener struct{ addr, port, process string }

func parseHostFacts(out string) hostFacts {
	f := hostFacts{kv: map[string]string{}}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		k, v = strings.ToLower(k), strings.TrimSpace(v)
		if k == "ssh_port" {
			// sshd -T prints one line per Port.
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 && !slices.Contains(f.sshPorts, v) {
				f.sshPorts = append(f.sshPorts, v)
			}
			continue
		}
		switch k {
		case "inst":
			// inst=openssl [3.0.13-0ubuntu3.4] (3.0.13-0ubuntu3.5 Ubuntu:24.04/noble-updates, Ubuntu:24.04/noble-security [amd64])
			// A new package has no [old version]: it comes with the upgrade
			// that needs it, so it isn't a security update of its own.
			name, rest, _ := strings.Cut(v, " ")
			upgrade := strings.HasPrefix(rest, "[")
			_, rest, _ = strings.Cut(rest, "(")
			version, _, _ := strings.Cut(rest, " ")
			f.updates = append(f.updates, PendingUpdate{Name: name, Version: version, Security: upgrade && strings.Contains(strings.ToLower(rest), "security")})
			continue
		case "upd":
			// upd=openssl.x86_64 1:3.1.4-2.fc39
			nameArch, version, _ := strings.Cut(v, " ")
			if i := strings.LastIndexByte(nameArch, '.'); i > 0 {
				nameArch = nameArch[:i]
			}
			f.updates = append(f.updates, PendingUpdate{Name: nameArch, Version: version})
			continue
		case "sec":
			// sec=openssl-1:3.1.4-2.fc39.x86_64: the name is before version-release.
			for range 2 {
				if i := strings.LastIndexByte(v, '-'); i > 0 {
					v = v[:i]
				}
			}
			f.dnfSecurity = append(f.dnfSecurity, v)
			continue
		case "listen":
		default:
			f.kv[k] = v
			continue
		}
		// listen=0.0.0.0:22 users:(("sshd",pid=1,fd=3))
		local, users, _ := strings.Cut(v, " ")
		i := strings.LastIndexByte(local, ':')
		if i < 0 {
			continue
		}
		l := listener{addr: strings.Trim(local[:i], "[]"), port: local[i+1:]}
		if _, name, ok := strings.Cut(users, `(("`); ok {
			l.process, _, _ = strings.Cut(name, `"`)
		}
		f.listen = append(f.listen, l)
	}
	for i := range f.updates {
		u := &f.updates[i]
		u.Security = u.Security || slices.Contains(f.dnfSecurity, u.Name)
		u.Impact = updateImpact(u.Name)
	}
	slices.SortStableFunc(f.updates, func(a, b PendingUpdate) int {
		if a.Security != b.Security {
			if a.Security {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return f
}

func (f hostFacts) int(k string) int { n, _ := strconv.Atoi(f.kv[k]); return n }

// securityItems turns the facts into the checklist.
func securityItems(f hostFacts) []SecurityItem {
	var items []SecurityItem
	add := func(id, title, status, detail, hint string) {
		items = append(items, SecurityItem{ID: id, Title: title, Status: status, Detail: detail, Hint: hint})
	}

	pm := f.kv["pm"]
	switch {
	case pm == "":
		add("updates", "Security updates", SecurityUnknown, "No apt or dnf on this host.", "")
	case f.int("security_updates") > 0:
		add("updates", "Security updates", SecurityWarn,
			fmt.Sprintf("%d security updates pending (%d updates in all).", f.int("security_updates"), f.int("updates")),
			"Install them, or turn on automatic updates.")
	default:
		add("updates", "Security updates", SecurityOK, fmt.Sprintf("None pending (%d other updates).", f.int("updates")), "")
	}
	if f.kv["reboot"] == "1" {
		add("reboot", "Reboot", SecurityWarn, "A reboot is needed to finish installing updates.", "Reboot at a quiet time: every app on this server restarts.")
	} else if pm != "" {
		add("reboot", "Reboot", SecurityOK, "No reboot needed.", "")
	}
	switch {
	case pm == "":
	case f.kv["auto_updates"] == "1":
		add("auto_updates", "Automatic security updates", SecurityOK, "On.", "")
	default:
		add("auto_updates", "Automatic security updates", SecurityFail, "Off: security fixes wait for someone to install them.",
			map[string]string{"apt": "Install and enable unattended-upgrades.", "dnf": "Install dnf-automatic and enable its timer."}[pm])
	}

	switch pw := f.kv["ssh_passwordauthentication"]; {
	case pw == "" && f.kv["sshd"] != "1":
		add("ssh_password", "SSH password login", SecurityOK, "No SSH server installed.", "")
	case pw == "":
		add("ssh_password", "SSH password login", SecurityUnknown, "Couldn't read the SSH server's settings.", "")
	case pw == "no":
		add("ssh_password", "SSH password login", SecurityOK, "Off: only keys can log in.", "")
	default:
		add("ssh_password", "SSH password login", SecurityFail, "On: passwords can be guessed from the internet.",
			"Set PasswordAuthentication no, once your key logs in.")
	}
	switch root := f.kv["ssh_permitrootlogin"]; root {
	case "":
	case "yes":
		add("ssh_root", "SSH root login", SecurityFail, "root can log in with a password.", "Set PermitRootLogin prohibit-password (keys only) or no.")
	default:
		add("ssh_root", "SSH root login", SecurityOK, "root: "+root+".", "")
	}

	if fw := f.kv["firewall"]; fw != "" {
		add("firewall", "Firewall", SecurityOK, fw+" is active.", "")
	} else {
		add("firewall", "Firewall", SecurityFail, "No active firewall (ufw, firewalld or nftables).",
			"Allow SSH, then deny other incoming traffic. Ports published by Docker go around it either way.")
	}
	if f.kv["fail2ban"] == "1" {
		add("fail2ban", "Brute-force protection", SecurityOK, "fail2ban is active.", "")
	} else {
		add("fail2ban", "Brute-force protection", SecurityWarn, "fail2ban isn't running: SSH login attempts aren't throttled.", "Install fail2ban with its sshd jail.")
	}

	sshPorts := f.sshPorts
	if len(sshPorts) == 0 {
		sshPorts = []string{"22"}
	}
	var exposed []string
	for _, l := range f.listen {
		if l.process == "docker-proxy" || l.process == "sshd" && slices.Contains(sshPorts, l.port) || !public(l.addr) {
			continue
		}
		s := l.port
		if l.process != "" {
			s += " (" + l.process + ")"
		}
		if !slices.Contains(exposed, s) {
			exposed = append(exposed, s)
		}
	}
	switch {
	case len(exposed) == 0:
		add("ports", "Open ports", SecurityOK, "Only SSH ("+strings.Join(sshPorts, ", ")+") and ports published by Docker.", "")
	case f.kv["firewall"] != "":
		add("ports", "Open ports", SecurityOK, "Also listening: "+strings.Join(exposed, ", ")+"; the firewall filters them.", "")
	default:
		add("ports", "Open ports", SecurityWarn, "Reachable from outside: "+strings.Join(exposed, ", ")+".",
			"Stop what you don't need, or bind it to 127.0.0.1, or turn on a firewall.")
	}
	return items
}

// cgnat is where Tailscale and carrier-grade NAT addresses live.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// public reports whether a listening address can be reached from the
// internet: any address, or a public one, but not loopback, private or VPN
// ones. A zone (%eth0) binds to one interface and doesn't change that.
func public(addr string) bool {
	host, _, _ := strings.Cut(addr, "%")
	if host == "*" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	return ip.IsUnspecified() || ip.IsGlobalUnicast() && !ip.IsPrivate() && !cgnat.Contains(ip)
}
