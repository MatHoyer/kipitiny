package servers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/notify"
	"github.com/kipit-labs/kipitiny/internal/ids"
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
	// Updates are the pending package updates, security ones first.
	Updates []PendingUpdate `json:"updates"`
	// Install is the last update install started from kipitiny; not stored
	// with the report.
	Install *UpdateRun `json:"install,omitempty"`
	// BootID changes on every boot: a reboot is over once it does.
	BootID string `json:"bootId,omitempty"`
	// RebootingSince is set while a reboot started from kipitiny isn't
	// over; not stored with the report.
	RebootingSince time.Time `json:"rebootingSince,omitzero"`
}

// Impacts of installing an update.
const (
	// ImpactReboot: takes effect after a reboot (kernel, libc, systemd).
	ImpactReboot = "reboot"
	// ImpactDocker: restarts Docker, so every container of the server.
	ImpactDocker = "docker"
	// ImpactSSH: restarts the SSH server; open sessions stay.
	ImpactSSH = "ssh"
)

// PendingUpdate is a package with a newer version available.
type PendingUpdate struct {
	Name     string `json:"name"`
	Version  string `json:"version,omitempty"`
	Security bool   `json:"security"`
	// Impact is one of the Impact constants, or empty: services using the
	// package restart.
	Impact string `json:"impact,omitempty"`
}

func updateImpact(name string) string {
	switch {
	case slices.Contains([]string{"docker-ce", "docker.io", "docker-engine", "containerd", "containerd.io", "moby-engine", "moby-containerd"}, name):
		return ImpactDocker
	case strings.HasPrefix(name, "linux-image") || strings.HasPrefix(name, "linux-modules") || name == "kernel" || strings.HasPrefix(name, "kernel-core") ||
		strings.HasPrefix(name, "kernel-modules") || slices.Contains([]string{"libc6", "glibc", "systemd", "dbus", "dbus-broker"}, name):
		return ImpactReboot
	case name == "openssh-server":
		return ImpactSSH
	}
	return ""
}

func securitySetting(serverID string) string { return "security/" + serverID }

// hostCheckScript prints what the checklist needs as key=value lines. It
// runs in the host's namespaces and only reads.
const hostCheckScript = `. /etc/os-release 2>/dev/null; echo "os=$PRETTY_NAME"
echo "boot_id=$(cat /proc/sys/kernel/random/boot_id 2>/dev/null)"
if command -v apt-get >/dev/null 2>&1; then
  echo pm=apt
  up=$(apt-get -s -o Debug::NoLocking=1 --with-new-pkgs upgrade 2>/dev/null | grep '^Inst ')
  echo "updates=$(printf '%s' "$up" | grep -c '^Inst ')"
  echo "security_updates=$(printf '%s' "$up" | grep '^Inst [^ ]* \[' | grep -ci security)" # upgrades: a new package comes with another one
  printf '%s\n' "$up" | sed -n 's/^Inst /inst=/p'
  [ -f /var/run/reboot-required ] && echo reboot=1 || echo reboot=0
  if dpkg-query -W -f='${Status}' unattended-upgrades 2>/dev/null | grep -q 'ok installed' &&
    apt-config dump APT::Periodic::Unattended-Upgrade 2>/dev/null | grep -q '"1"'; then echo auto_updates=1; else echo auto_updates=0; fi
elif command -v dnf >/dev/null 2>&1; then
  echo pm=dnf
  up=$(dnf -q check-update 2>/dev/null | grep '^[^ ]')
  echo "updates=$(printf '%s' "$up" | grep -c .)"
  printf '%s\n' "$up" | awk 'NF==3 {print "upd=" $1 " " $2}'
  sec=$(dnf -q updateinfo list --security 2>/dev/null)
  echo "security_updates=$(printf '%s' "$sec" | grep -c .)"
  printf '%s\n' "$sec" | awk 'NF==3 {print "sec=" $3}'
  needs-restarting -r >/dev/null 2>&1; [ $? = 1 ] && echo reboot=1 || echo reboot=0
  if systemctl is-enabled -q dnf-automatic.timer 2>/dev/null || systemctl is-enabled -q dnf-automatic-install.timer 2>/dev/null; then echo auto_updates=1; else echo auto_updates=0; fi
fi
command -v sshd >/dev/null 2>&1 && echo sshd=1
[ -d /run/sshd ] || mkdir -p /run/sshd # sshd -T needs it; socket-activated hosts only create it on a connection
sshd -T 2>/dev/null | grep -Ei '^(port|passwordauthentication|permitrootlogin) ' | sed 's/^/ssh_/; s/ /=/'
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
func (c *Servers) runOnHost(ctx context.Context, serverID, script string) (string, error) {
	dk := c.DockerFor(serverID)
	if err := dk.EnsureImage(ctx, hostShellImage, "", ""); err != nil {
		return "", fmt.Errorf("pull %s: %w", hostShellImage, err)
	}
	var out bytes.Buffer
	code, err := dk.RunAttached(ctx, domain.ContainerSpec{
		Name:  "kipitiny-hostcheck-" + strings.ToLower(ids.New()),
		Image: hostShellImage,
		Cmd:   []string{"nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "--", "/bin/sh", "-s"},
		Labels: map[string]string{
			domain.LabelManaged:   "true",
			domain.LabelComponent: hostCheckComponent,
		},
		Privileged: true,
		PidMode:    "host",
		Log:        true,
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

// checkSecurity checks one server's host, stores the report and notifies
// when security updates appear or a reboot becomes needed.
func (c *Servers) checkSecurity(ctx context.Context, sv domain.Server) SecurityReport {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	r := SecurityReport{CheckedAt: time.Now().UTC(), Items: []SecurityItem{}, Updates: []PendingUpdate{}}
	out, err := c.runOnHost(ctx, sv.ID, hostCheckScript)
	if err != nil {
		r.Error = err.Error()
	} else {
		f := parseHostFacts(out)
		r.OS, r.Items, r.BootID = f.kv["os"], securityItems(f), f.kv["boot_id"]
		r.SecurityUpdates, r.Reboot = f.int("security_updates"), f.kv["reboot"] == "1"
		if f.updates != nil {
			r.Updates = f.updates
		}
	}
	prev, _ := c.securityReport(ctx, sv.ID)
	if b, err := json.Marshal(r); err == nil {
		if err := c.store.SetSetting(context.WithoutCancel(ctx), securitySetting(sv.ID), string(b)); err != nil {
			c.log.Warn("cannot record security check", "server", sv.Name, "err", err)
		}
	}
	link := "/servers/" + sv.ID
	if r.SecurityUpdates > 0 && prev.SecurityUpdates == 0 {
		c.mgr.Notify(notify.Event{Type: domain.EventServerUpdates, Level: notify.Warning, Title: sv.Name + " has security updates",
			Message: fmt.Sprintf("%d security updates are pending.", r.SecurityUpdates), Fields: []notify.Field{{Name: "Server", Value: sv.Name}}}, link, "")
	}
	if r.Reboot && !prev.Reboot {
		c.mgr.Notify(notify.Event{Type: domain.EventServerReboot, Level: notify.Warning, Title: sv.Name + " needs a reboot",
			Message: "Installed updates take effect after a reboot.", Fields: []notify.Field{{Name: "Server", Value: sv.Name}}}, link, "")
	}
	return r
}

func (c *Servers) securityReport(ctx context.Context, serverID string) (SecurityReport, error) {
	var r SecurityReport
	v, err := c.store.GetSetting(ctx, securitySetting(serverID))
	if errors.Is(err, domain.ErrNotFound) {
		return r, nil // not checked yet
	}
	if err != nil || v == "" {
		return r, err
	}
	return r, json.Unmarshal([]byte(v), &r)
}

// StartSecurityChecks checks every server's host a minute after start, then
// every securityEvery, and follows the update installs still running.
func (c *Servers) StartSecurityChecks() error {
	return c.mgr.GoLoop(func() {
		c.resumeUpdates()
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
				r, _ := c.securityReport(c.bg, sv.ID)
				if rb, _ := c.pendingReboot(c.bg, sv.ID, r); rb == nil && time.Since(r.CheckedAt) < securityEvery-time.Minute {
					continue // checked recently, e.g. before a restart
				}
				c.checkSecurity(c.bg, sv)
			}
		}
	})
}

// ServerSecurity returns a server's last security check; CheckedAt is zero
// before the first. Admin only.
func (c *Servers) ServerSecurity(ctx context.Context, serverID string) (SecurityReport, error) {
	if err := identity.RequireAdmin(ctx); err != nil {
		return SecurityReport{}, err
	}
	if _, err := c.store.GetServer(ctx, serverID); err != nil {
		return SecurityReport{}, err
	}
	r, err := c.securityReport(ctx, serverID)
	if err != nil {
		return r, err
	}
	if r.Items == nil {
		r.Items = []SecurityItem{}
	}
	if r.Updates == nil {
		r.Updates = []PendingUpdate{}
	}
	if run, err := c.updateRun(ctx, serverID); err != nil {
		return r, err
	} else if !run.StartedAt.IsZero() {
		r.Install = &run
	}
	if rb, err := c.pendingReboot(ctx, serverID, r); err != nil {
		return r, err
	} else if rb != nil {
		r.RebootingSince = rb.At
	}
	return r, nil
}

// CheckServerSecurity checks a server's host now; a host that can't be
// checked has Error set. Admin only.
func (c *Servers) CheckServerSecurity(ctx context.Context, serverID string) (SecurityReport, error) {
	if err := identity.RequireAdmin(ctx); err != nil {
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
