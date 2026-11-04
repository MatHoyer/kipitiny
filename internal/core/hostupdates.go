package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/ids"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// updatesTimeout is how long an update install may run before kipitiny stops
// waiting for it.
const updatesTimeout = time.Hour

// UpdateRun is an install of a host's updates started from kipitiny.
type UpdateRun struct {
	// All is every pending update; otherwise security ones only.
	All bool `json:"all"`
	// Unit is the systemd unit running it on the host.
	Unit       string    `json:"unit"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`
	Running    bool      `json:"running"`
	Error      string    `json:"error,omitempty"`
	// Log is the end of the package manager's output.
	Log string `json:"log,omitempty"`
}

func updateRunSetting(serverID string) string { return "security-install/" + serverID }

func (c *Core) updateRun(ctx context.Context, serverID string) (UpdateRun, error) {
	var run UpdateRun
	v, err := c.store.GetSetting(ctx, updateRunSetting(serverID))
	if errors.Is(err, store.ErrNotFound) || err == nil && v == "" {
		return run, nil
	}
	if err != nil {
		return run, err
	}
	return run, json.Unmarshal([]byte(v), &run)
}

func (c *Core) saveUpdateRun(ctx context.Context, serverID string, run UpdateRun) error {
	b, err := json.Marshal(run)
	if err != nil {
		return err
	}
	return c.store.SetSetting(context.WithoutCancel(ctx), updateRunSetting(serverID), string(b))
}

// installScript is what the unit runs. It keeps changed config files as they
// are and restarts services using updated libraries (needrestart leaves
// Docker alone).
func installScript(all bool) string {
	a := "0"
	if all {
		a = "1"
	}
	return "ALL=" + a + `
set -e
if command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a
  o="-y -q -o DPkg::Lock::Timeout=600 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold"
  apt-get -q -o DPkg::Lock::Timeout=600 update
  if [ "$ALL" = 1 ]; then apt-get $o --with-new-pkgs upgrade; exit; fi
  pk=$(apt-get -s --with-new-pkgs upgrade | awk '/^Inst / && $3 ~ /^\[/ && tolower($0) ~ /security/ {print $2}')
  if [ -z "$pk" ]; then echo "No security updates to install."; exit 0; fi
  apt-get $o install --only-upgrade $pk
elif command -v dnf >/dev/null 2>&1; then
  if [ "$ALL" = 1 ]; then dnf -y upgrade; else dnf -y upgrade --security; fi
else
  echo "No apt or dnf on this host." >&2; exit 1
fi
`
}

// startUpdatesScript starts the install as a transient systemd unit, so it
// outlives the helper container: upgrading Docker restarts it.
func startUpdatesScript(unit string, all bool) string {
	return `command -v systemd-run >/dev/null 2>&1 || { echo "systemd-run not found: kipitiny installs updates through systemd" >&2; exit 1; }
cat > /run/` + unit + `.sh <<'KIPITINY_EOF'
` + installScript(all) + `KIPITINY_EOF
systemd-run --quiet --unit=` + unit + ` --description="kipitiny: install updates" /bin/sh /run/` + unit + `.sh
`
}

// waitUpdatesScript waits for the unit to stop, then prints its outcome and
// the end of its log.
func waitUpdatesScript(unit string) string {
	return `while systemctl is-active -q ` + unit + `; do sleep 3; done
rm -f /run/` + unit + `.sh
if systemctl is-failed -q ` + unit + `; then echo state=failed; systemctl reset-failed ` + unit + `; else echo state=done; fi
journalctl -u ` + unit + ` -o cat --no-pager -n 60
`
}

// installError picks the reason an install failed from its log: apt's or
// dnf's first error, or else the last line that isn't systemd's.
func installError(log, unit string) string {
	last := ""
	for l := range strings.Lines(log) {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "E: ") || strings.HasPrefix(l, "Error:") {
			return l
		}
		if l != "" && !strings.HasPrefix(l, unit+".service") && !strings.HasPrefix(l, "Started "+unit) {
			last = l
		}
	}
	return last
}

// InstallUpdates starts installing a server's pending security updates, or
// all of them, in the background; the security report follows it and the
// host is checked again when it's done. Admin only.
func (c *Core) InstallUpdates(ctx context.Context, serverID string, all bool) (SecurityReport, error) {
	if err := RequireAdmin(ctx); err != nil {
		return SecurityReport{}, err
	}
	sv, err := c.store.GetServer(ctx, serverID)
	if err != nil {
		return SecurityReport{}, err
	}
	c.hostActionMu.Lock()
	defer c.hostActionMu.Unlock()
	run, err := c.updateRun(ctx, serverID)
	if err != nil {
		return SecurityReport{}, err
	}
	if run.Running {
		return SecurityReport{}, fmt.Errorf("%w: updates are already being installed", ErrBusy)
	}
	run = UpdateRun{All: all, Unit: "kipitiny-updates-" + strings.ToLower(ids.New()), StartedAt: time.Now().UTC(), Running: true}
	// Not the request's: a closed tab mustn't kill the helper halfway. 5 minutes for pulling it.
	startCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	if _, err := c.runOnHost(startCtx, serverID, startUpdatesScript(run.Unit, all)); err != nil {
		return SecurityReport{}, fmt.Errorf("start the install: %w", err)
	}
	if err := c.saveUpdateRun(ctx, serverID, run); err != nil {
		return SecurityReport{}, err
	}
	if err := c.goLoop(func() { c.waitUpdates(sv, run) }); err != nil {
		return SecurityReport{}, err
	}
	return c.ServerSecurity(ctx, serverID)
}

// waitUpdates follows an install until it ends. The helper waiting for it
// dies when Docker restarts, so it's started again; a manager restart
// resumes it from resumeUpdates.
func (c *Core) waitUpdates(sv store.Server, run UpdateRun) {
	ctx, cancel := context.WithDeadline(c.bg, run.StartedAt.Add(updatesTimeout))
	defer cancel()
	for {
		out, err := c.runOnHost(ctx, sv.ID, waitUpdatesScript(run.Unit))
		if err == nil {
			state, log, _ := strings.Cut(out, "\n")
			if strings.TrimSpace(state) == "state=failed" {
				run.Error = "the package manager failed: " + installError(log, run.Unit)
			}
			run.Log = strings.TrimSpace(log)
			break
		}
		if c.bg.Err() != nil {
			return // shutting down: resumed at the next start
		}
		if ctx.Err() != nil {
			run.Error = fmt.Sprintf("still not finished after %s; the systemd unit %s shows where it is", updatesTimeout, run.Unit)
			break
		}
		c.log.Info("waiting for the update install again", "server", sv.Name, "err", err)
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
	}
	run.Running, run.FinishedAt = false, time.Now().UTC()
	if err := c.saveUpdateRun(c.bg, sv.ID, run); err != nil {
		c.log.Warn("cannot record the update install", "server", sv.Name, "err", err)
	}
	c.checkSecurity(c.bg, sv)
}

// resumeUpdates follows the installs a previous run of the manager started,
// for instance before an upgrade of Docker restarted it.
func (c *Core) resumeUpdates() {
	servers, err := c.store.ListServers(c.bg)
	if err != nil {
		return
	}
	for _, sv := range servers {
		if run, err := c.updateRun(c.bg, sv.ID); err == nil && run.Running {
			_ = c.goLoop(func() { c.waitUpdates(sv, run) })
		}
	}
}

// rebootTimeout is how long a server may take to come back from a reboot
// before kipitiny stops waiting for it.
const rebootTimeout = 20 * time.Minute

// hostReboot is a reboot started from kipitiny.
type hostReboot struct {
	At time.Time `json:"at"`
	// BootID is the boot it ends.
	BootID string `json:"bootId"`
}

func rebootSetting(serverID string) string { return "security-reboot/" + serverID }

// pendingReboot returns the reboot started from kipitiny that the report
// doesn't show over yet, or nil.
func (c *Core) pendingReboot(ctx context.Context, serverID string, r SecurityReport) (*hostReboot, error) {
	v, err := c.store.GetSetting(ctx, rebootSetting(serverID))
	if errors.Is(err, store.ErrNotFound) || err == nil && v == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rb hostReboot
	if err := json.Unmarshal([]byte(v), &rb); err != nil {
		return nil, err
	}
	// A host that can't be checked (no BootID) is still rebooting.
	if r.BootID != "" && r.BootID != rb.BootID || time.Since(rb.At) > rebootTimeout {
		return nil, nil
	}
	return &rb, nil
}

// RebootServer reboots a server's host a few seconds from now, then checks
// it again once it's back. Every container on it restarts. Admin only.
func (c *Core) RebootServer(ctx context.Context, serverID string) (SecurityReport, error) {
	if err := RequireAdmin(ctx); err != nil {
		return SecurityReport{}, err
	}
	sv, err := c.store.GetServer(ctx, serverID)
	if err != nil {
		return SecurityReport{}, err
	}
	c.hostActionMu.Lock()
	defer c.hostActionMu.Unlock()
	if run, err := c.updateRun(ctx, serverID); err != nil {
		return SecurityReport{}, err
	} else if run.Running {
		return SecurityReport{}, fmt.Errorf("%w: updates are being installed; reboot once they're done", ErrBusy)
	}
	r, err := c.securityReport(ctx, serverID)
	if err != nil {
		return SecurityReport{}, err
	}
	if rb, err := c.pendingReboot(ctx, serverID, r); err != nil {
		return SecurityReport{}, err
	} else if rb != nil {
		return SecurityReport{}, fmt.Errorf("%w: the server is already rebooting", ErrBusy)
	}
	startCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	// The delay lets the helper answer before the host goes down.
	out, err := c.runOnHost(startCtx, serverID, `command -v systemd-run >/dev/null 2>&1 || { echo "systemd-run not found: kipitiny reboots through systemd" >&2; exit 1; }
cat /proc/sys/kernel/random/boot_id
systemd-run --quiet --on-active=5 --unit=kipitiny-reboot-`+strings.ToLower(ids.New())+` systemctl reboot
`)
	if err != nil {
		return SecurityReport{}, fmt.Errorf("start the reboot: %w", err)
	}
	b, err := json.Marshal(hostReboot{At: time.Now().UTC(), BootID: strings.TrimSpace(out)})
	if err != nil {
		return SecurityReport{}, err
	}
	if err := c.store.SetSetting(context.WithoutCancel(ctx), rebootSetting(serverID), string(b)); err != nil {
		return SecurityReport{}, err
	}
	if err := c.goLoop(func() { c.waitReboot(sv) }); err != nil {
		return SecurityReport{}, err
	}
	return c.ServerSecurity(ctx, serverID)
}

// waitReboot checks a rebooting server until it's back on a new boot. When
// the manager runs there, it goes down too and StartSecurityChecks checks
// the server once it's back.
func (c *Core) waitReboot(sv store.Server) {
	for {
		select {
		case <-c.bg.Done():
			return
		case <-time.After(30 * time.Second):
		}
		r := c.checkSecurity(c.bg, sv)
		if rb, err := c.pendingReboot(c.bg, sv.ID, r); err != nil || rb == nil {
			return
		}
	}
}
