# Server setup with Ansible

Prepares fresh **Debian 12/13 or Ubuntu 22.04/24.04** servers:

| Role | Hosts | Does |
|---|---|---|
| `base` | all | Security updates (unattended-upgrades), SSH keys only, ufw firewall (SSH, plus 80/443 unless tunnelled) |
| `docker` | all | Docker CE from Docker's apt repository |
| `kipitiny` | `manager` | `/opt/kipitiny` with this repo's `docker-compose.yml` and a `.env`, starts the manager, prints the setup token |
| `worker` | `workers` | A `kipitiny` user in the `docker` group with the manager's key, limited to the Docker socket (no shell) |

```sh
cd deploy/ansible
ansible-galaxy collection install -r requirements.yml
cp inventory.example.yml inventory.yml   # hosts and settings; git-ignored
ansible-playbook site.yml
```

Log in as root or a sudo user **with an SSH key**: password login is turned
off (skipped with a warning if that user has no key). Re-running is safe and
doesn't upgrade kipitiny: use the UI's update button or
`docker compose pull && docker compose up -d` in `/opt/kipitiny`.

**Manager settings** (per host, see `roles/kipitiny/defaults/main.yml`):
`kipitiny_domain` (DNS must point at the server first), `kipitiny_acme_email`,
`kipitiny_cloudflare_tunnel_token` (keep it in Ansible Vault; the firewall
then keeps 80/443 closed), `kipitiny_env`
for any other `KIPITINY_*` variable. With a domain, port 3000 is bound to
localhost only: `ssh -L 3000:localhost:3000 server` if you need it.

**Adding a worker:** in the manager, *Settings → Servers → Add server* shows
its SSH key. Set it as `kipitiny_manager_ssh_key` (in `group_vars/all.yml`),
add the host under `workers`, run `ansible-playbook site.yml --limit workers`,
then finish adding the server with user `kipitiny`. A worker can have its own
Cloudflare tunnel: set `kipitiny_cloudflare_tunnel_token` on that host (its
firewall keeps 80/443 closed) and paste the same token in the manager under
*Settings → Servers → Network*, which starts cloudflared there.
