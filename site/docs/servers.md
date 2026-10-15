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

### Fixing

Points kipitiny can set up get a **Fix…** button that says what it will do
before doing it, and **Fix all** does every one in a row. The section shows
each step as it runs, then checks the host again. Every fix is recorded in the
audit log.

| Point | What the fix does |
|-------|-------------------|
| Security updates | installs the pending security updates only, with Docker's packages held back so no app restarts |
| Automatic security updates | installs unattended-upgrades (dnf-automatic on dnf hosts) for daily security updates; they never reboot |
| Brute-force protection | installs fail2ban with its `sshd` jail; a banned address is blocked on every port, so it keeps working if the SSH port changes |
| Firewall | Debian and Ubuntu: installs ufw, allows every port sshd listens on, 80 and 443, denies other incoming traffic |
| SSH password login | `PasswordAuthentication no`, in `/etc/ssh/sshd_config.d/00-kipitiny-password.conf` |
| SSH root login | `PermitRootLogin prohibit-password`, in `/etc/ssh/sshd_config.d/00-kipitiny-root.conf` |

To keep you from being locked out, the SSH fixes are only offered when an
`authorized_keys` file holds a key, and the new setting is checked with
`sshd -t` and removed if sshd rejects it; make sure your own key logs in first.
The firewall keeps open the ports sshd really listens on (read from `sshd -T`). To move SSH to another port later, `ufw allow` the new one first. Ports published
by Docker go around ufw, so apps stay reachable; other programs listening on a
public address stop being reachable. Updating Docker itself and rebooting are left
to you: both restart every app.
