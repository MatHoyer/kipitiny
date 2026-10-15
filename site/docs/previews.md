---
title: Preview environments
description: A copy of a project per branch or stack of pull requests, with its own domain, data and secrets, asleep when unused
order: 18
---

A **preview** is a copy of a project that CI opens for a branch: the same
services, with the images CI just built, fresh databases, apps on a domain of
their own, and none of production's secrets. Previews use [sleep
mode](/docs/sleep), so a dozen of them cost almost nothing while nobody looks
at them, and close themselves after a week unused.

kipitiny knows nothing about pull requests: CI gives each preview a name. The
recipe below names one **per stack** of pull requests, not per pull request,
so a stack of five stacked PRs is one preview, not five.

## Set up a project

In the project's **Previews** tab, under *Preview settings*:

- **Base domain**, e.g. `preview.example.com`. Preview `pr-12`'s app `web`
  answers at `pr-12-web.preview.example.com` (`pr-12.preview.example.com` when
  the project has one public app). Point a wildcard record
  `*.preview.example.com` at the server, or let the manager do it if
  [Cloudflare is connected](/docs/domains#cloudflare-dns). Use a domain of its
  own rather than one under production's: cookies production sets for
  `.example.com` never reach previews.
- **Variables & secrets** previews get instead of production's: a test Stripe
  key, a sandbox SMTP server…
- **Inherit**: production secrets previews may still use as they are, e.g. an
  error tracker's DSN. Every other secret is left out.
- How long an unused preview lives (default 7 days), when it sleeps
  (default 30 minutes), and whether CI may **seed** it with production data.

### What a preview gets

| | Production | Preview |
|---|---|---|
| Services | as set up | the same; a git-linked project's compose file is read at the preview's branch, so a PR can change it |
| Images | as deployed | the tags CI passes (`--tag web=sha-abc`); the others keep production's |
| Databases | as they are | new and empty (new passwords); `--seed` fills them from the latest backups |
| Project variables | as set | production's plain variables, then the inherited secrets, then the preview settings, then CI's `--env` |
| Service and project secrets | as set | **left out**: a service's secret `API_KEY` takes the preview variable `API_KEY` |
| Password manager references | resolved | left out, unless inherited or set again in the preview settings (e.g. `{{ pass://Staging/stripe/key }}`) |
| Domains | as set | `<preview>-<service>.<base domain>`, with `X-Robots-Tag: noindex` |
| Replicas | as set | one each |
| Published ports, host access | as set | left out (the server's ports are production's) |
| Uptime checks, backups | as set | none |
| Maintenance and starting pages | as set | the project's, kept up to date on each deploy; maintenance mode is the preview's own |

A preview never quietly falls back on production:

- A value it needs and doesn't get (a secret the preview settings don't set)
  stops the deploy with its name: `previews need a value for STRIPE_KEY`.
- A value that names a production domain (`APP_URL=https://shop.example.com`,
  in a service or a project variable) stops it too. Write such values as
  `{{ app.web.URL }}` (or `{{ app.web.DOMAIN }}`): production resolves it to its
  own domain, each preview to its own. A value set on purpose in the preview
  settings is allowed.

## Open previews from CI

Create a token restricted to *deploy* on the project and keep it
as a CI secret. `kipitiny preview` (in the manager image) creates or updates a
preview, waits for the services that changed and prints their URLs:

```sh
kipitiny preview --url https://kipitiny.example.com --project shop --name pr-12 \
  --tag web=sha-abc1234 --tag worker=sha-abc1234 \
  --branch feat/checkout --commit abc1234
```

- `--tag service=tag` (or `service=sha256:…`) per service CI built; never
  another repository.
- `--env KEY=value` adds a plain variable to this preview only (no
  references).
- `--seed` fills the databases of a **new** preview from the project's latest
  backups (the preview settings must allow it). Later deploys keep the
  preview's data.
- `--ttl 72h` overrides how long it lives unused.
- `--close` deletes it: containers, volumes, databases.

The API is `PUT /api/projects/{id}/previews/{name}` (same fields as JSON:
`tags`, `env`, `branch`, `commit`, `seed`, `ttlSeconds`) and
`DELETE /api/projects/{id}/previews/{name}`; agents use `deploy_preview`,
`list_previews` and `close_preview`.

## One preview per stack of pull requests

With stacked pull requests (each PR based on the one below), a preview per PR
is wasteful: the top PR contains every change below it, a rebase of the bottom
re-pushes every branch (one build and deploy each), and every preview would
get its own databases. Instead:

- **Only the top PR of a stack deploys**: a PR with another open PR based on
  its branch skips the build and the deploy.
- **The preview is named after the bottom PR**, whose number doesn't change
  while the stack grows: `pr-<bottom>`. Adding a PR on top redeploys the same
  preview, at the same URL, with the new top.
- **Closing the bottom PR closes the preview**, e.g. when the whole stack
  merges (`gh stack merge`).
- A PR on its own is a stack of one: one preview per PR, as usual.

```yaml
# .github/workflows/preview.yml
name: preview
on:
  pull_request:
    types: [opened, synchronize, reopened, edited, closed]

jobs:
  plan:
    runs-on: ubuntu-latest
    permissions: { pull-requests: read }
    outputs:
      deploy: ${{ steps.stack.outputs.deploy }}
      name: ${{ steps.stack.outputs.name }}
    steps:
      - id: stack
        env:
          GH_TOKEN: ${{ github.token }}
          GH_REPO: ${{ github.repository }}
          PR: ${{ github.event.pull_request.number }}
          HEAD: ${{ github.event.pull_request.head.ref }}
          BASE: ${{ github.event.pull_request.base.ref }}
          MAIN: ${{ github.event.repository.default_branch }}
        run: |
          # The bottom of the stack: follow the bases down to the default branch.
          root=$PR base=$BASE
          while [ "$base" != "$MAIN" ]; do
            below=$(gh pr list --head "$base" --state open --json number,baseRefName --jq '.[0] // empty')
            [ -z "$below" ] && break
            root=$(jq -r .number <<<"$below") base=$(jq -r .baseRefName <<<"$below")
          done
          # The top of the stack: no open PR is based on this branch.
          above=$(gh pr list --base "$HEAD" --state open --json number --jq length)
          echo "name=pr-$root" >> "$GITHUB_OUTPUT"
          echo "deploy=$([ "$above" = 0 ] && echo true || echo false)" >> "$GITHUB_OUTPUT"

  deploy:
    needs: plan
    # edited: only a new base (a restacked PR), not a new title.
    if: >
      github.event.action != 'closed' && needs.plan.outputs.deploy == 'true' &&
      (github.event.action != 'edited' || github.event.changes.base)
    runs-on: ubuntu-latest
    permissions: { contents: read, packages: write }
    # One deploy per stack at a time: a rebase of the whole stack ends in one.
    concurrency: { group: "preview-${{ needs.plan.outputs.name }}", cancel-in-progress: true }
    steps:
      - uses: actions/checkout@v5
      - uses: docker/login-action@v3
        with: { registry: ghcr.io, username: "${{ github.actor }}", password: "${{ secrets.GITHUB_TOKEN }}" }
      - uses: docker/build-push-action@v6
        with: { push: true, tags: "ghcr.io/${{ github.repository }}:sha-${{ github.event.pull_request.head.sha }}" }
      - run: >
          docker run --rm -e KIPITINY_TOKEN=${{ secrets.KIPITINY_TOKEN }}
          ghcr.io/mathoyer/kipitiny preview
          --url https://kipitiny.example.com --project shop --name ${{ needs.plan.outputs.name }}
          --tag web=sha-${{ github.event.pull_request.head.sha }}
          --branch ${{ github.event.pull_request.head.ref }} --commit ${{ github.event.pull_request.head.sha }}

  close:
    needs: plan
    # The bottom PR closing (merged or not) ends the stack's preview.
    if: github.event.action == 'closed' && github.event.pull_request.base.ref == github.event.repository.default_branch
    runs-on: ubuntu-latest
    steps:
      - run: >
          docker run --rm -e KIPITINY_TOKEN=${{ secrets.KIPITINY_TOKEN }}
          ghcr.io/mathoyer/kipitiny preview
          --url https://kipitiny.example.com --project shop --name pr-${{ github.event.pull_request.number }} --close
```

Two cases get no event on the right PR, so the preview waits for the next
push to the stack: closing the top PR (the one below becomes the top) and
merging only the bottom PR (the next one becomes the bottom, under a new
name). Re-run the new top's workflow to update it at once.

## The preview's page

The project's **Previews** tab lists its previews with their branch, commit,
last deploy and URLs, and closes one on demand. A preview is a project of its
own, opened from there: its services, logs, deployments and data browser work
as usual, and members get on it what they have on its project. It isn't in the
project list. Deleting a project deletes its previews.
