---
name: feature
description: Build a kipitiny feature end to end — explore, plan and ask, implement, test for real on the local manager, report coverage in the PR. Use when the user asks for a new feature or invokes /feature <description>.
---

# Feature

Four phases, in order. Never skip the questions or the real-usage tests.

## 1. Explore and plan

- Read `docs/design.md` (roadmap §16) and the code the feature touches: core, store, API, MCP, UI, site docs.
- Write a plan: what changes where, PR split (multi-step work = stacked PRs, one `feat/...` branch per step), and the
  real-usage test list you will run in phase 3.
- **Ask before implementing.** List every open question, ambiguity, and contradiction you found (with the design doc,
  CLAUDE.md rules, existing behavior, or the request itself). Ask them with AskUserQuestion, wait for answers, and
  only then start. A plan with zero questions is suspicious: say explicitly why nothing is unclear.

## 2. Implement

Follow CLAUDE.md rules. Unit/integration tests for new logic, user docs in `site/docs/`, an `UPGRADE.md` notice if
running servers are disrupted. `make test` and `make lint` green before phase 3.

## 3. Test in real usage

Code tests are not enough: exercise every new behavior the way a user would, through the real manager.

**Test environment (default).** Each worktree gets its own manager, isolated from the other worktrees' ones:
`scripts/test-env.sh` runs it in its own Docker daemon (`docker:29-dind`, container `kipitiny-test-<branch>`), from
the image `ghcr.io/mathoyer/kipitiny:dev-<branch>` built from the worktree and the repo's `docker-compose.yml`.

```bash
scripts/test-env.sh up      # build the branch, (re)start its manager, print its URL and Traefik's ports
scripts/test-env.sh url     # print them again (random 127.0.0.1 ports)
scripts/test-env.sh docker logs kipitiny-manager-1   # any command runs inside it; first start logs a setup token
scripts/test-env.sh down    # delete it with its data, when the work is merged
```

- Never test on `kipitiny-manager-1` of the host Docker (`localhost:3000`, image `:dev`): other worktrees use it, and
  rebuilding `:dev` can leave it on a schema it doesn't know. Never start a manager on the host Docker either
  (`go run ./cmd/kipitiny`, fresh `KIPITINY_DATA_DIR`): its reconciler deletes the other managers' containers.
- The test environment is yours: migrations, wipes, `down` and `up` are fine without asking.
- The `kipitiny` MCP server is the **production** manager (manager.mathieuhoyer.fr): never test with it.
- Create test resources in projects named `test-<feature>-*`.
- Drive it at its URL through its API and the UI. UI features are checked in a real browser against this manager
  (playwright-core with the cached Chromium), not only in demo mode. Apps on `*.localhost` go through its Traefik
  ports: curl with `--resolve host:<port>:127.0.0.1 -k`, Chromium with `--host-resolver-rules=MAP *.localhost
  127.0.0.1:<port>`.

**VM (root/host-level changes).** For anything that touches the host itself (install script, systemd, firewall,
Docker install, upgrades from an older release), use a throwaway qemu VM: qemu-system-x86_64 with KVM, an Ubuntu
cloud image, cloud-init seed for an SSH key, port-forward SSH. Keep images and disks in the scratchpad. Delete the VM
when done.

For each case record: what you did (exact compose file, command, click path), expected result, actual result.
Failures you find are fixed and retested, not just reported.

Practical notes:
- API access: on a new environment, `POST /api/auth/setup` with the setup token from the logs (`setupToken`,
  `username`, `password`) gives an admin session; keep passwords and tokens in the scratchpad only. A member: `POST
  /api/invites` (`role`, `grants` per project), then `POST /api/auth/invite/accept`. Lost login: `scripts/test-env.sh
  docker exec -i kipitiny-manager-1 /kipitiny reset-password <user> < pwfile`.
- Git-only features (repo files, include, sync, previews): push fixtures to an orphan `test/<feature>-fixtures` branch of
  this repo, one folder per case, link a test project to it, and delete the branch at the end.
- Before calling something a bug, check that `docker compose` would behave differently with the same file: shell
  splitting of `command`, tmpfs modes, `extends` adding up ports, images that stay apps without `x-kipitiny.kind`.
- A stack fix goes into the commit of the PR that introduced the code (`git commit --amend`, then
  `git rebase --update-refs --onto <branch> <old sha> <top branch>`); renumber later migrations if one is added.
  Bugs that predate the work go in a separate `fix/...` PR on top.

## 4. Report

Put the report in the PR description (the last PR of a stack, or each PR for its own part), under `## Testing`:

```markdown
## Testing

`make test` / `make lint`: green.

Real usage on the test environment (`:dev-<branch>` built from <sha>):

| # | Case | How | Expected | Result |
|---|------|-----|----------|--------|
| 1 | `depends_on` with `service_healthy` | compose apply, project test-compose-a | db healthy before app starts | ✅ |
| 2 | `extends` from another file | ... | ... | ❌ fixed in <sha>, ✅ on retest |
| 3 | `network_mode: host` | ... | rejected with clear error | ⚠️ accepted silently, see note |

Not covered: <case> — <why> (needs real domain, Cloudflare account, ARM host...).
```

✅ works, ❌ broken, ⚠️ works with a caveat. Every new behavior gets a row; every gap goes in "Not covered". In the final
message to the user, repeat only the table summary (counts + the ⚠️/❌ rows) and the PR link.
