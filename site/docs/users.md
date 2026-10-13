---
title: Users and permissions
description: Invite admins, or members who only see and do what you grant them, project by project
order: 13
---

The account created at setup is an **admin**: admins do everything. Invite
other people from *Settings › Users* as admins too, or as **members**, who
see only the projects you grant them and only do what each grant allows.

## Invites

*Invite user* picks the access, then shows a link once. Whoever opens it first
picks a username and password and is signed in; the link works for 7 days.
Pending invites are listed under the users, where you can revoke them. The
token is in the link's `#fragment`, so it never reaches a server or proxy log.

With an [SMTP server](/docs/monitoring#email) set, an optional **email**
sends the link to the new user as well (it is still shown once to copy). If
sending fails, the invite exists anyway: copy the link and send it yourself.

## Project permissions

A member's access is set per project. Any permission includes **view**.

| Permission | Allows |
| --- | --- |
| View | Services, logs, stats, deployments, uptime, backups list, the compose export (secrets masked) |
| Deploy | Deploy, roll back, start, stop, restart, maintenance mode |
| Settings | Create and edit services, apply compose files, rename, networks, uptime checks |
| Env | Variables and secrets of the services and the project, database connection strings |
| Domains | Public domains and their middlewares |
| Backups | Back up now, verify, schedules, delete backups |
| Restore | Replace a service's data with a backup (both the backup's project and the target's need it) |
| Download | Download backups, export a project with its secrets |
| Data | The [data browser](/docs/data) and the read-only console |
| Data write | Edit rows and schemas, write queries in the console |
| Files | Browse the [files](/docs/files) of volumes and containers |
| Files write | Upload, edit, move and delete those files |
| Terminal | A shell in the service's containers |
| Git | Link, sync and unlink a [git repository](/docs/compose#git) |
| Delete | Delete services and the project; a compose prune deletes apps, so it needs it too |

A project a member wasn't granted doesn't exist for them: its pages, lists,
map, backups and resource use are hidden and its URLs answer *not found*.

Some things stay with admins whatever the grants:

- giving an app the host's network or the Docker socket ([host access](/docs/compose#host-access)), which is root on the server;
- servers, storage, registries, git providers, password managers, notifications, Cloudflare, cleanup, updates and the audit log;
- users and invites.

## Server features

Features let a member *see* server-level settings, read-only and without how
they connect (hosts, accounts, keys, URLs), to use them in their projects:

| Feature | Lets them |
| --- | --- |
| Create projects | Create projects; they get every permission on the ones they create |
| Storage | Pick where backups go |
| Registries | See which registries pulls can use |
| Git providers | Pick a provider when linking a repository |
| Password managers | Reference their items (`{{ pass://… }}`) in env: the app could read them, so it needs this |
| Servers | See the servers |
| Networks | See the networks created by hand |
| Notifications | See the channels |
| Domains | See the base domains |

## API tokens and git syncs

Members create their own [API tokens](/docs/api). A token acts for its owner:
whatever its scope, it never does more than the owner may, and it follows
their access as you change it. Removing a user revokes their tokens.

A git-linked project syncs with the permissions of the user who linked it. If
that user is removed, syncs stop until someone links the repository again.
Members can't link a repository by URL on a loopback, private or link-local
address (the manager would fetch it from inside your network); a repository
from a [git provider](/docs/compose#git-providers) is fine.

## Locked out

The last admin can't be demoted or removed, and nobody can remove themselves.
If no admin can sign in, make a user admin from the server:

```sh
docker compose exec manager /kipitiny make-admin alice
```
