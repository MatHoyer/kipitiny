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

With an [email provider](/docs/monitoring#email) set, an optional **email**
sends the link to the new user as well, through the provider chosen for
invites (it is still shown once to copy). If
sending fails, the invite exists anyway: copy the link and send it yourself.

## Single sign-on

Users can sign in through an OpenID Connect provider: Google, Microsoft Entra
ID, Authentik, Keycloak, Pocket ID, Zitadel, Okta… Add it in *Settings ›
Single sign-on*:

1. Register kipitiny as a confidential web client at the provider, with the
   **redirect URI** the form shows (`https://<manager>/api/auth/sso/callback`).
2. Enter the provider's **issuer** URL, the **client ID** and the **client
   secret**. kipitiny reads the issuer's OpenID configuration before saving,
   so a wrong URL shows up right away.

The name you give it labels the sign-in button. Several providers can be
added side by side.

A provider never creates accounts: it signs in users whose identity there is
**linked** to their kipitiny account. Each user links theirs in *Account ›
Single sign-on* (password asked again, then a sign-in at the provider). From
then on, *Sign in with …* on the sign-in page brings them back where they
were. Someone whose identity isn't linked is refused: invite them first.

An invite link offers the providers too: the new user picks a username, then
*Continue with …* creates the account linked to the identity they sign in
with, **without a password**. Such an account confirms sensitive changes
(two-factor authentication, passkeys, exporting secrets) by entering its
credentials at the provider again instead of the password: kipitiny asks the
provider for a fresh sign-in, even if it is still signed in there, and the
confirmation lasts 10 minutes. A passkey sign-in counts too. It can set a
password later in *Account*. Its last way to sign in can't be unlinked.

Signing out of kipitiny doesn't sign you out of the provider: *Sign in with …*
may then come straight back without asking anything. Use a private window to
try another account.

### Requiring single sign-on

*Require single sign-on* turns password and passkey sign-in off: the sign-in
page only shows the providers, and invites are accepted through one. API
tokens and MCP keep working. Two-factor authentication still applies.
Passkeys stay in *Account* but don't sign in until the requirement is turned
off.

You can only turn it on once your own account is linked at a provider, and
the last provider can't be removed while it's on. If the provider goes down,
turn passwords back on from the server:

```sh
docker compose exec manager /kipitiny allow-password-login
```

Disabling someone at the provider stops their next sign-in, not the sessions
they already have (30 days) or their API tokens: remove them from kipitiny
too.

The provider stands in for the password only: a user with two-factor
authentication on still enters a code afterwards. Removing a provider unlinks
every identity linked through it, which locks out accounts without a password
or passkey: give them a password with `reset-password` (below) if needed.

## Project permissions

A member's access is set per project. Any permission includes **view**.

| Permission | Allows |
| --- | --- |
| View | Services, logs, stats, deployments, uptime, jobs and their runs, backups list, the compose export (secrets masked) |
| Deploy | Deploy, roll back, start, stop, restart, maintenance mode, run a job now |
| Settings | Create and edit services, apply compose files, rename, networks, uptime checks, scheduled jobs |
| Env | Variables and secrets of the services and the project (secret files too), database connection strings |
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
| Password managers | Reference their items (`{{ pass://… }}`) in env, secret files, basic auth passwords or preview settings: the app could read them, so it needs this |
| Servers | See the servers |
| Networks | See the networks created by hand |
| Notifications | See the channels |
| Domains | See the base domains |

## API tokens and git syncs

Members create their own [API tokens](/docs/api). A token acts for its owner:
it never does more than the owner may, and it follows
their access as you change it. Removing a user revokes their tokens. A
[restricted token](/docs/api#restricted-tokens) gets only some of its owner's
projects and permissions.

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

A user who can't sign in at all (provider removed, password lost) gets a new
password the same way:

```sh
docker compose exec -it manager /kipitiny reset-password alice
```
