package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// Steps of a fix run besides the fixes themselves, which are the item IDs.
const (
	FixStepRead  = "read"
	FixStepCheck = "check"
)

// FixRun is a run fixing some checklist points of a server; FinishedAt is
// nil while it runs.
type FixRun struct {
	IDs        []string    `json:"ids"`
	StartedAt  time.Time   `json:"startedAt"`
	FinishedAt *time.Time  `json:"finishedAt,omitempty"`
	Results    []FixResult `json:"results"`
	Error      string      `json:"error,omitempty"`
}

type FixResult struct {
	ID    string `json:"id"`
	Error string `json:"error,omitempty"`
	// Output is the end of what the fix printed, to tell why it failed.
	Output string `json:"output,omitempty"`
}

// securityFixState tracks the fix runs, in memory: a restart re-checks the
// host anyway.
type securityFixState struct {
	mu   sync.Mutex
	runs map[string]*FixRun // server ID -> the running or last run
}

// get returns a copy of a server's running or last run.
func (s *securityFixState) get(serverID string) *FixRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.runs[serverID]
	if r == nil {
		return nil
	}
	cp := *r
	cp.Results = slices.Clone(r.Results)
	return &cp
}

// update changes a server's run under the lock.
func (s *securityFixState) update(serverID string, f func(*FixRun)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s.runs[serverID])
}

func securityFixKey(serverID string) string { return "security/" + serverID }

// fixable says why kipitiny can't fix the point id on this host, nil when
// it can. The SSH ones refuse what could lock the admin out.
func fixable(id string, f hostFacts) error {
	pm := f.kv["pm"]
	switch id {
	case "updates", "auto_updates", "fail2ban":
		if pm == "" {
			return errors.New("no apt or dnf on this host")
		}
	case "firewall":
		// shortcut: ufw only; enabling firewalld under Docker can drop its
		// rules, so dnf hosts are left to the admin.
		if pm != "apt" {
			return errors.New("only ufw (Debian, Ubuntu) is set up automatically")
		}
		// No sshd at all: nothing to lock out.
		if len(f.sshPorts) == 0 && f.kv["sshd"] == "1" {
			return errors.New("couldn't read the SSH port to keep it open")
		}
	case "ports":
		return errors.New("only you know which of them should be reachable")
	case "reboot":
		return errors.New("kipitiny doesn't reboot servers yet")
	case "ssh_password", "ssh_root":
		if f.kv["ssh_include"] != "1" {
			return errors.New("sshd_config doesn't include sshd_config.d")
		}
		if f.int("ssh_keys") == 0 {
			return errors.New("no key in any authorized_keys: turning passwords off would lock everyone out")
		}
	default:
		return errors.New("can't be fixed automatically")
	}
	return nil
}

// install is the package manager's install command, waiting for a lock held
// by an automatic update instead of failing.
func install(pm string, pkgs ...string) string {
	if pm == "apt" {
		return "export DEBIAN_FRONTEND=noninteractive\napt-get -q -o DPkg::Lock::Timeout=300 update\n" +
			"apt-get -yq -o DPkg::Lock::Timeout=300 install " + strings.Join(pkgs, " ")
	}
	return "dnf -y install " + strings.Join(pkgs, " ")
}

// fixScript is the root shell script that fixes the point id; fixable(id)
// must be nil.
func fixScript(id string, f hostFacts) string {
	pm := f.kv["pm"]
	var s string
	switch id {
	case "updates":
		// Security updates only, Docker's packages held back: upgrading them
		// restarts the daemon, and every app with it.
		if pm == "apt" {
			s = install(pm, "unattended-upgrades") + `
held=
for p in docker.io docker-ce docker-ce-cli containerd containerd.io runc; do
  if dpkg-query -W -f='${Status}' "$p" 2>/dev/null | grep -q 'ok installed' && ! apt-mark showhold | grep -qx "$p"; then held="$held $p"; fi
done
[ -n "$held" ] && apt-mark hold $held
trap '[ -n "$held" ] && apt-mark unhold $held' EXIT
unattended-upgrade -v`
		} else {
			s = `dnf -y upgrade --security -x 'docker*' -x 'containerd*' -x 'moby*' -x runc`
		}
	case "auto_updates":
		if pm == "apt" {
			s = install(pm, "unattended-upgrades") + `
printf 'APT::Periodic::Update-Package-Lists "1";\nAPT::Periodic::Unattended-Upgrade "1";\n' > /etc/apt/apt.conf.d/20auto-upgrades`
		} else {
			s = install(pm, "dnf-automatic") + `
sed -i 's/^upgrade_type *=.*/upgrade_type = security/; s/^apply_updates *=.*/apply_updates = yes/' /etc/dnf/automatic.conf
systemctl enable --now dnf-automatic.timer`
		}
	case "fail2ban":
		// Hosts logging only to the journal (Debian 12+) need its backend.
		s = `backend=auto
[ -f /var/log/auth.log ] || [ -f /var/log/secure ] || backend=systemd
` + install(pm, "fail2ban") + `
[ "$backend" = systemd ] && [ -x "$(command -v apt-get)" ] && apt-get -yq -o DPkg::Lock::Timeout=300 install python3-systemd
mkdir -p /etc/fail2ban/jail.d
# Bans cover every port: they keep working after the SSH port changes.
printf '[sshd]\nenabled = true\nport = 0:65535\nbackend = %s\n' "$backend" > /etc/fail2ban/jail.d/kipitiny.local
systemctl enable fail2ban
systemctl restart fail2ban`
	case "firewall":
		// Ports published by Docker go around ufw; 80 and 443 are opened for
		// a Traefik run without Docker's iptables rules.
		s = install(pm, "ufw") + "\n"
		for _, p := range f.sshPorts { // checked numbers, never raw host text
			s += "ufw allow " + p + "/tcp\n"
		}
		s += `ufw allow 80/tcp
ufw allow 443/tcp
ufw default deny incoming
ufw default allow outgoing
ufw --force enable`
	case "ssh_password", "ssh_root":
		// 00- sorts first, and the first value sshd reads wins over the
		// cloud images' 50-cloud-init.conf.
		file, conf := "/etc/ssh/sshd_config.d/00-kipitiny-password.conf", `PasswordAuthentication no\nKbdInteractiveAuthentication no\n`
		if id == "ssh_root" {
			file, conf = "/etc/ssh/sshd_config.d/00-kipitiny-root.conf", `PermitRootLogin prohibit-password\n`
		}
		s = `f=` + file + `
printf '` + conf + `' > "$f"
mkdir -p /run/sshd
if ! sshd -t; then rm -f "$f"; echo "sshd rejected the setting; nothing changed" >&2; exit 1; fi
# A socket-activated sshd (Ubuntu 24.04+) has nothing to reload: new
# connections read the file.
systemctl reload ssh 2>/dev/null || systemctl reload sshd 2>/dev/null || true`
	}
	// Run under systemd when there is one, so the fix finishes even if the
	// helper container goes away (a Docker restart) mid-install.
	return `set -e
cat > /run/kipitiny-fix.sh <<'KIPITINY_FIX'
set -e
` + s + `
KIPITINY_FIX
trap 'rm -f /run/kipitiny-fix.sh' EXIT
if command -v systemd-run >/dev/null 2>&1; then
  systemd-run --quiet --wait --pipe --collect --unit kipitiny-fix-` + strings.ReplaceAll(id, "_", "-") + ` /bin/sh /run/kipitiny-fix.sh </dev/null
else
  /bin/sh /run/kipitiny-fix.sh </dev/null
fi
`
}

// FixServerSecurity fixes the given checklist points of a server's host in
// the background, then checks it again. Admin only.
func (c *Core) FixServerSecurity(ctx context.Context, serverID string, ids []string) (SecurityReport, error) {
	if err := RequireAdmin(ctx); err != nil {
		return SecurityReport{}, err
	}
	sv, err := c.store.GetServer(ctx, serverID)
	if err != nil {
		return SecurityReport{}, err
	}
	rep, err := c.securityReport(ctx, serverID)
	if err != nil {
		return SecurityReport{}, err
	}
	if len(ids) == 0 {
		return SecurityReport{}, fmt.Errorf("%w: pick something to fix", ErrInvalid)
	}
	for _, id := range ids {
		i := slices.IndexFunc(rep.Items, func(it SecurityItem) bool { return it.ID == id })
		if i < 0 || !rep.Items[i].Fix {
			return SecurityReport{}, fmt.Errorf("%w: %q can't be fixed on this server; check it again first", ErrInvalid, id)
		}
	}
	fs := &c.securityFix
	fs.mu.Lock()
	if r := fs.runs[serverID]; r != nil && r.FinishedAt == nil {
		fs.mu.Unlock()
		return SecurityReport{}, fmt.Errorf("%w: a fix is already running on %s", ErrBusy, sv.Name)
	}
	if fs.runs == nil {
		fs.runs = map[string]*FixRun{}
	}
	fs.runs[serverID] = &FixRun{IDs: ids, StartedAt: time.Now().UTC(), Results: []FixResult{}}
	fs.mu.Unlock()
	c.steps.set(securityFixKey(serverID), FixStepRead)
	actor := context.WithoutCancel(ctx) // keeps the admin for the audit log
	err = c.goBackground(func() { c.runFixes(actor, sv, ids) })
	if err != nil {
		c.endFixes(serverID, err.Error())
		return SecurityReport{}, err
	}
	return c.ServerSecurity(ctx, serverID)
}

// endFixes marks a server's run finished, with a reason when it couldn't go
// through.
func (c *Core) endFixes(serverID, errMsg string) {
	c.securityFix.update(serverID, func(r *FixRun) {
		finished := time.Now().UTC()
		r.FinishedAt = &finished
		if errMsg != "" {
			r.Error = errMsg
		}
	})
	c.steps.done(securityFixKey(serverID))
}

func (c *Core) runFixes(ctx context.Context, sv store.Server, ids []string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	key := securityFixKey(sv.ID)
	var errMsg string
	defer func() { c.endFixes(sv.ID, errMsg) }()

	// Fresh facts: the stored report may be hours old.
	out, err := c.runOnHost(ctx, sv.ID, hostCheckScript)
	if err != nil {
		errMsg = "couldn't read the host: " + err.Error()
		return
	}
	f := parseHostFacts(out)
	for _, id := range ids {
		c.steps.set(key, id)
		res := FixResult{ID: id}
		if err := fixable(id, f); err != nil {
			res.Error = err.Error()
		} else if out, err := c.runOnHost(ctx, sv.ID, fixScript(id, f)); err != nil {
			res.Error, res.Output = err.Error(), tail(out, 2000)
		}
		status := http.StatusOK
		if res.Error != "" {
			status = http.StatusInternalServerError
		}
		c.Audit(ctx, "security fix "+id, "server "+sv.Name, status, errOrNil(res.Error))
		c.securityFix.update(sv.ID, func(r *FixRun) { r.Results = append(r.Results, res) })
	}
	c.steps.set(key, FixStepCheck)
	c.checkSecurity(ctx, sv)
}

func errOrNil(msg string) error {
	if msg == "" {
		return nil
	}
	return errors.New(msg)
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "…" + s[len(s)-n:]
	}
	return s
}
