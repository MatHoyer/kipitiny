---
title: Multiple servers
description: Manage remote Docker hosts over SSH from one manager
order: 10
---

*Settings → Servers* adds remote Docker hosts over SSH. The manager reaches the
remote Docker **socket** through an SSH channel (pure Go, no `ssh` binary, the
daemon never listens on TCP) with its own ed25519 key, shown in the form to add
to `authorized_keys`. The server's host key is pinned on first connection.
The remote sshd must allow `AllowTcpForwarding yes` (or `local`) and
`AllowStreamLocalForwarding yes` (Debian/Ubuntu defaults; Alpine disables it).

A project lives on one server (its services share a private network); choose it
when creating the project. Each server gets its own Traefik, so point a domain's
DNS at the server running the app. That Traefik reaches the manager at
`KIPITINY_DOMAIN` for [maintenance pages](/docs/domains#maintenance-page). Deploys, builds, backups (streamed back
through SSH to any target), restore tests, logs and the reconciler all work the
same on every server. The injected health probe is copied to each server once
(it needs the same CPU architecture as the manager; otherwise replicas are
gated on staying up instead).

## Host usage

Each server's page shows its **Host** section: CPU (with cores, load average
and uptime), memory (with swap and the share of kipitiny's containers) and the
disk holding Docker's data, with the last five minutes. They're read every ten
seconds from inside the server's Traefik container, which sees the host's
`/proc` and sits on Docker's data disk, so nothing else runs on the server.
They're kept in memory only: a manager restart starts the charts over. The API
serves them at `GET /api/servers/{id}/stats`.

## Security checklist

Admins see a **Security** section on each server's page: the host is checked a
minute after the manager starts, then every six hours, or with **Check now**.

| Check | Looks at |
|-------|----------|
| Security updates | pending updates from apt or dnf (security ones counted apart) |
| Reboot | whether installed updates wait for a reboot |
| Automatic security updates | unattended-upgrades, or the dnf-automatic timer |
| SSH password login, SSH root login | the SSH server's effective settings (`sshd -T`) |
| Firewall | ufw, firewalld or nftables active |
| Brute-force protection | fail2ban running |
| Open ports | what listens on a public address besides SSH and Docker's published ports |

The check runs the host's own tools as root in a short-lived container (the
same way as the host terminal) and only reads. New security updates and a
needed reboot are [notifications](/docs/monitoring#notifications)
(`server.updates_pending`, `server.reboot_required`).

### Installing updates

The **Security updates** row lists the pending updates (security ones first)
with what installing them restarts: *restarts Docker* (`docker-ce`,
`containerd.io`…: every app on the server stops for about a minute, and the
manager too when it runs there), *needs a reboot* (kernel, libc, systemd),
*restarts SSH* (open sessions stay). **Install updates** asks first, with the
same summary; *Also install non-security updates* is checked by default, untick
it to install only the security ones.

The install runs on the host as a systemd unit (`kipitiny-updates-…`), so it
keeps going when Docker restarts: `apt-get update`, then the security updates
(or `apt-get --with-new-pkgs upgrade` for all), keeping changed config files;
`dnf upgrade --security` (or `dnf upgrade`) on dnf hosts. Services using an
updated library restart (needrestart, which leaves Docker alone). The page
follows it, shows the end of its log, and checks the host again once it's
done; a manager restarted meanwhile picks it up again. The API is
`POST /api/servers/{id}/security/updates` with `{"all": false}`, the MCP tool
`install_server_updates`.

### Rebooting

When installed updates wait for a reboot, the **Reboot** row has a **Reboot**
button. It asks first, then reboots the host a few seconds later through
systemd: every app on it is down for a few minutes, until Docker starts them
again, and so is the manager when it runs there (the page reconnects). It's
refused while updates are being installed. The section shows the reboot until
the host answers on a new boot (at most 20 minutes), then checks it again. The
API is `POST /api/servers/{id}/reboot`, the MCP tool `reboot_server` (the
server's name as confirmation).
