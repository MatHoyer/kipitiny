package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/ids"
	"github.com/MatHoyer/kipitiny/internal/notify"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	// securityEvery is how often every server's host is checked.
	securityEvery = 6 * time.Hour
	// hostCheckComponent labels the helpers that run commands on a host.
	hostCheckComponent = "hostcheck"
)

// Security check statuses.
const (
	SecurityOK      = "ok"
	SecurityWarn    = "warn"
	SecurityFail    = "fail"
	SecurityUnknown = "unknown"
)

// SecurityItem is one point of a host's security checklist.
type SecurityItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	// Detail says what was found, Hint what to do about it.
	Detail string `json:"detail,omitempty"`
	Hint   string `json:"hint,omitempty"`
	// Fix is whether kipitiny can fix it; NoFix says why not when it isn't OK.
	Fix   bool   `json:"fix,omitempty"`
	NoFix string `json:"noFix,omitempty"`
}

// SecurityReport is the last check of a server's host.
type SecurityReport struct {
	CheckedAt time.Time      `json:"checkedAt"`
	OS        string         `json:"os,omitempty"`
	Items     []SecurityItem `json:"items"`
	// Error is set when the host couldn't be checked at all.
	Error string `json:"error,omitempty"`
	// SecurityUpdates and Reboot are kept to notify only on a change.
	SecurityUpdates int  `json:"securityUpdates"`
	Reboot          bool `json:"reboot"`

	// Fix is the running or last fix run since the manager started, at
	// FixStep (a FixStep* or an item ID) while it runs.
	Fix     *FixRun `json:"fix,omitempty"`
	FixStep string  `json:"fixStep,omitempty"`
}

func securitySetting(serverID string) string { return "security/" + serverID }

// hostCheckScript prints what the checklist needs as key=value lines. It
// runs in the host's namespaces and only reads.
const hostCheckScript = `. /etc/os-release 2>/dev/null; echo "os=$PRETTY_NAME"
if command -v apt-get >/dev/null 2>&1; then
  echo pm=apt
  up=$(apt-get -s -o Debug::NoLocking=1 upgrade 2>/dev/null | grep '^Inst ')
  echo "updates=$(printf '%s' "$up" | grep -c '^Inst ')"
  echo "security_updates=$(printf '%s' "$up" | grep -ci security)"
  [ -f /var/run/reboot-required ] && echo reboot=1 || echo reboot=0
  if dpkg-query -W -f='${Status}' unattended-upgrades 2>/dev/null | grep -q 'ok installed' &&
    apt-config dump APT::Periodic::Unattended-Upgrade 2>/dev/null | grep -q '"1"'; then echo auto_updates=1; else echo auto_updates=0; fi
elif command -v dnf >/dev/null 2>&1; then
  echo pm=dnf
  echo "updates=$(dnf -q check-update 2>/dev/null | grep -c '^[^ ]')"
  echo "security_updates=$(dnf -q updateinfo list --security 2>/dev/null | grep -c .)"
  needs-restarting -r >/dev/null 2>&1; [ $? = 1 ] && echo reboot=1 || echo reboot=0
  if systemctl is-enabled -q dnf-automatic.timer 2>/dev/null || systemctl is-enabled -q dnf-automatic-install.timer 2>/dev/null; then echo auto_updates=1; else echo auto_updates=0; fi
fi
command -v sshd >/dev/null 2>&1 && echo sshd=1
[ -d /run/sshd ] || mkdir -p /run/sshd # sshd -T needs it; socket-activated hosts only create it on a connection
sshd -T 2>/dev/null | grep -Ei '^(port|passwordauthentication|permitrootlogin) ' | sed 's/^/ssh_/; s/ /=/'
grep -qiE '^[[:space:]]*Include[[:space:]]+/etc/ssh/sshd_config\.d/\*\.conf' /etc/ssh/sshd_config 2>/dev/null && echo ssh_include=1
echo "ssh_keys=$(cat /root/.ssh/authorized_keys /home/*/.ssh/authorized_keys 2>/dev/null | grep -cE '^(ssh-|ecdsa-|sk-)')"
fw=
if command -v ufw >/dev/null 2>&1 && grep -q '^ENABLED=yes' /etc/ufw/ufw.conf 2>/dev/null; then fw=ufw
elif systemctl is-active -q firewalld 2>/dev/null; then fw=firewalld
elif systemctl is-active -q nftables 2>/dev/null; then fw=nftables; fi
echo "firewall=$fw"
systemctl is-active -q fail2ban 2>/dev/null && echo fail2ban=1 || echo fail2ban=0
ss -tlnpH 2>/dev/null | awk '{print "listen=" $4 " " $6}'
`

// runOnHost runs a shell script as root in a server's host namespaces,
// through a privileged helper like the host terminal's, and returns its
// output. Docker access is root on the host anyway.
func (c *Core) runOnHost(ctx context.Context, serverID, script string) (string, error) {
	dk := c.dockerFor(serverID)
	if err := dk.EnsureImage(ctx, hostShellImage, ""); err != nil {
		return "", fmt.Errorf("pull %s: %w", hostShellImage, err)
	}
	var out bytes.Buffer
	code, err := dk.RunAttached(ctx, client.ContainerCreateOptions{
		Name: "kipitiny-hostcheck-" + strings.ToLower(ids.New()),
		Config: &container.Config{
			Image: hostShellImage,
			Cmd:   []string{"nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "--", "/bin/sh", "-s"},
			Labels: map[string]string{
				docker.LabelManaged:   "true",
				docker.LabelComponent: hostCheckComponent,
			},
		},
		HostConfig: &container.HostConfig{
			Privileged: true,
			PidMode:    "host",
			LogConfig:  docker.DefaultLogConfig(),
		},
	}, strings.NewReader(script), &out)
	if err != nil {
		return out.String(), err
	}
	if code != 0 {
		return out.String(), fmt.Errorf("exit code %d: %s", code, lastLine(out.String()))
	}
	return out.String(), nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	return s[strings.LastIndexByte(s, '\n')+1:]
}

// hostFacts is the parsed output of hostCheckScript.
type hostFacts struct {
	kv     map[string]string
	listen []listener
	// sshPorts are the ports sshd listens on, checked numbers.
	sshPorts []string
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
		if k != "listen" {
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
	for i := range items {
		if items[i].Status == SecurityOK {
			continue
		}
		if err := fixable(items[i].ID, f); err != nil {
			items[i].NoFix = err.Error()
		} else {
			items[i].Fix = true
		}
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

// checkSecurity checks one server's host, stores the report and notifies
// when security updates appear or a reboot becomes needed.
func (c *Core) checkSecurity(ctx context.Context, sv store.Server) SecurityReport {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	r := SecurityReport{CheckedAt: time.Now().UTC(), Items: []SecurityItem{}}
	out, err := c.runOnHost(ctx, sv.ID, hostCheckScript)
	if err != nil {
		r.Error = err.Error()
	} else {
		f := parseHostFacts(out)
		r.OS, r.Items = f.kv["os"], securityItems(f)
		r.SecurityUpdates, r.Reboot = f.int("security_updates"), f.kv["reboot"] == "1"
	}
	prev, _ := c.securityReport(ctx, sv.ID)
	if b, err := json.Marshal(r); err == nil {
		if err := c.store.SetSetting(context.WithoutCancel(ctx), securitySetting(sv.ID), string(b)); err != nil {
			c.log.Warn("cannot record security check", "server", sv.Name, "err", err)
		}
	}
	link := "/servers/" + sv.ID
	if r.SecurityUpdates > 0 && prev.SecurityUpdates == 0 {
		c.notify(notify.Event{Type: EventServerUpdates, Level: notify.Warning, Title: sv.Name + " has security updates",
			Message: fmt.Sprintf("%d security updates are pending.", r.SecurityUpdates), Fields: []notify.Field{{Name: "Server", Value: sv.Name}}}, link, "")
	}
	if r.Reboot && !prev.Reboot {
		c.notify(notify.Event{Type: EventServerReboot, Level: notify.Warning, Title: sv.Name + " needs a reboot",
			Message: "Installed updates take effect after a reboot.", Fields: []notify.Field{{Name: "Server", Value: sv.Name}}}, link, "")
	}
	return r
}

func (c *Core) securityReport(ctx context.Context, serverID string) (SecurityReport, error) {
	var r SecurityReport
	v, err := c.store.GetSetting(ctx, securitySetting(serverID))
	if err != nil || v == "" {
		return r, err
	}
	return r, json.Unmarshal([]byte(v), &r)
}

// StartSecurityChecks checks every server's host a minute after start, then
// every securityEvery.
func (c *Core) StartSecurityChecks() error {
	return c.goLoop(func() {
		wait := time.Minute
		for {
			select {
			case <-c.bg.Done():
				return
			case <-time.After(wait):
			}
			wait = securityEvery
			servers, err := c.store.ListServers(c.bg)
			if err != nil {
				continue
			}
			for _, sv := range servers {
				if r, _ := c.securityReport(c.bg, sv.ID); time.Since(r.CheckedAt) < securityEvery-time.Minute {
					continue // checked recently, e.g. before a restart
				}
				c.checkSecurity(c.bg, sv)
			}
		}
	})
}

// ServerSecurity returns a server's last security check; CheckedAt is zero
// before the first. Admin only.
func (c *Core) ServerSecurity(ctx context.Context, serverID string) (SecurityReport, error) {
	if err := RequireAdmin(ctx); err != nil {
		return SecurityReport{}, err
	}
	if _, err := c.store.GetServer(ctx, serverID); err != nil {
		return SecurityReport{}, err
	}
	r, err := c.securityReport(ctx, serverID)
	if r.Items == nil {
		r.Items = []SecurityItem{}
	}
	r.FixStep, _ = c.steps.get(securityFixKey(serverID))
	r.Fix = c.securityFix.get(serverID)
	return r, err
}

// CheckServerSecurity checks a server's host now; a host that can't be
// checked has Error set. Admin only.
func (c *Core) CheckServerSecurity(ctx context.Context, serverID string) (SecurityReport, error) {
	if err := RequireAdmin(ctx); err != nil {
		return SecurityReport{}, err
	}
	sv, err := c.store.GetServer(ctx, serverID)
	if err != nil {
		return SecurityReport{}, err
	}
	if !c.securityBusy.CompareAndSwap(false, true) {
		return SecurityReport{}, fmt.Errorf("%w: a check is already running", ErrBusy)
	}
	defer c.securityBusy.Store(false)
	c.checkSecurity(ctx, sv)
	return c.ServerSecurity(ctx, serverID)
}
