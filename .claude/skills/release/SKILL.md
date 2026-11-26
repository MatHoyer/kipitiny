---
name: release
description: Cut a kipitiny release — bump `VERSION` in a `chore(release): X.Y.Z` commit on main, tag it X.Y.Z, push. Use when the user says "release", "cut a release", "tag a version", or invokes /release [version|major|minor|patch].
---

# Release

A release is a chore commit on `main` that bumps the root `VERSION` file, plus a
lightweight tag on it. The
workflow fails if the tag and `VERSION` disagree. Pushing the tag triggers
`.github/workflows/release.yml` (multi-arch image to
`ghcr.io/kipit-labs/kipitiny:<tag>` + `:latest`, GitHub release with generated
notes) and `.github/workflows/site.yml` (the website, deployed to GitHub Pages).

Tags are bare semver, no `v` prefix: `0.6.0`, not `v0.6.0` (the workflow only
matches `[0-9]+.[0-9]+.[0-9]+`).

## Steps

1. **Preflight** — stop and report if any fails:
   - on `main`, working tree clean (`git status --porcelain` empty)
   - `git fetch --tags origin` then local `main` == `origin/main`
   - there are commits since the last tag (`git describe --tags --abbrev=0`)
   - `VERSION` content equals the last tag

2. **Pick the version.** Arg may be an explicit `X.Y.Z` or `major|minor|patch`.
   Without one, infer from conventional commits since the last tag
   (`git log <last>..HEAD --format=%s`):
   - any `!:` or `BREAKING CHANGE` → major (while < 1.0, bump minor instead)
   - any `feat` → minor
   - otherwise → patch
   New version must be greater than the last tag and not already exist.

3. **Confirm with the user** before touching anything: show last tag → new
   version and the commit subjects included, and the upgrade notices under
   `## Unreleased` in `UPGRADE.md` (none is fine). Flag every `warning:` one:
   managers show it next to the update and the release notes open with it.
   If a commit clearly disrupts running servers (Traefik recreated, a
   manual step) and has no notice, propose one first. Wait for a yes.

4. **Commit and tag:**
   ```sh
   printf 'X.Y.Z\n' > VERSION
   # The next release's notices become this one's (no-op without any).
   sed -i 's/^## Unreleased$/## X.Y.Z/' UPGRADE.md
   go run ./internal/upgrade/notes release X.Y.Z   # must succeed: the workflow runs it
   git add VERSION UPGRADE.md
   git commit -m "chore(release): X.Y.Z"
   git tag X.Y.Z
   ```
   No AI attribution trailer on the commit.

5. **Push the tag and `main`:**
   ```sh
   git fetch origin && test "$(git rev-parse origin/main)" = "$(git rev-parse HEAD~1)"
   git push origin X.Y.Z main
   ```

6. **Report** the workflow runs: `gh run list --workflow release.yml --limit 1`
   and `gh run list --workflow site.yml --limit 1`,
   and the release URL once it exists (`gh release view X.Y.Z --json url`).

## If something goes wrong

- `origin/main` moved before the tag push (the `test` fails): tag not pushed
  yet, so `git tag -d X.Y.Z`, `git reset --hard HEAD~1`, `git pull --rebase
  origin main`, re-check the version (new commits may change the bump), redo.
- Push of `main` rejected after the tag is out: stop and report; the tag is
  published and must not move.
- Never move or delete a tag that has already been pushed without the user's
  explicit go-ahead: managers in the wild pull images by tag.
