---
name: release
description: Cut a kipitiny release — bump `VERSION` in a `chore(release): X.Y.Z` PR, merge it, tag the merged commit X.Y.Z, push the tag. Use when the user says "release", "cut a release", "tag a version", or invokes /release [version|major|minor|patch].
---

# Release

A release is a chore commit on `main` that bumps the root `VERSION` file,
merged through a PR, plus a lightweight tag on the merged commit. The
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

4. **Commit on a branch and open the PR.** The `main` ruleset requires a PR
   with passing checks, so the release goes through one:
   ```sh
   git switch -c chore/release-X.Y.Z
   printf 'X.Y.Z\n' > VERSION
   # The next release's notices become this one's (no-op without any).
   sed -i 's/^## Unreleased$/## X.Y.Z/' UPGRADE.md
   go run ./internal/upgrade/notes release X.Y.Z   # must succeed: the workflow runs it
   git add VERSION UPGRADE.md
   git commit -m "chore(release): X.Y.Z"
   git push -u origin chore/release-X.Y.Z
   gh pr create --title "chore(release): X.Y.Z" --body "Release X.Y.Z."
   gh pr merge --rebase --auto
   ```
   No AI attribution trailer on the commit or the PR. Don't tag yet: the
   rebase merge rewrites the commit, so a tag on the branch would sit off `main`.

5. **Tag the merged commit and push the tag** once the PR is merged
   (`gh pr view --json state` says `MERGED`):
   ```sh
   git switch main && git pull --ff-only origin main
   sha=$(git log --format=%H --grep='^chore(release): X.Y.Z$' -1)
   test -n "$sha" && test "$(git show "$sha":VERSION)" = X.Y.Z
   git tag X.Y.Z "$sha"
   git push origin X.Y.Z
   git branch -d chore/release-X.Y.Z
   ```
   Tag the release commit, not `origin/main`: another PR may have merged
   after it.

6. **Report** the workflow runs: `gh run list --workflow release.yml --limit 1`
   and `gh run list --workflow site.yml --limit 1`,
   and the release URL once it exists (`gh release view X.Y.Z --json url`).

## If something goes wrong

- PR checks fail or it conflicts (another PR touched `UPGRADE.md`): rebase
  the branch on `origin/main`, re-check the version and notices (new commits
  may change them), force-push the branch. Nothing is tagged yet.
- Never move or delete a tag that has already been pushed without the user's
  explicit go-ahead: managers in the wild pull images by tag.
