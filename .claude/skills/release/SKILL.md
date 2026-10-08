---
name: release
description: Cut a kipitiny release — bump `VERSION` in a `chore(release): X.Y.Z` commit on main, tag it X.Y.Z, push. Use when the user says "release", "cut a release", "tag a version", or invokes /release [version|major|minor|patch].
---

# Release

A release is a chore commit on `main` that bumps the root `VERSION` file and the
website's image tag in `site/compose.yaml`, plus a lightweight tag on it. The
workflow fails if the tag and `VERSION` disagree. Pushing the tag triggers
`.github/workflows/release.yml` (multi-arch image to
`ghcr.io/mathoyer/kipitiny:<tag>` + `:latest`, GitHub release with generated
notes) and `.github/workflows/site.yml` (the website image,
`ghcr.io/mathoyer/kipitiny-homepage:<tag>`).

The website's kipitiny project follows `site/compose.yaml` on `main` (git sync),
so `main` must only name a website image that exists: the tag goes first, `main`
once the site image is pushed.

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
   version and the commit subjects included. Wait for a yes.

4. **Commit and tag:**
   ```sh
   printf 'X.Y.Z\n' > VERSION
   sed -i -E 's#(kipitiny-homepage:)[0-9]+\.[0-9]+\.[0-9]+#\1X.Y.Z#' site/compose.yaml
   git add VERSION site/compose.yaml
   git commit -m "chore(release): X.Y.Z"
   git tag X.Y.Z
   ```
   No AI attribution trailer on the commit.

5. **Push the tag**, then **`main` once the website image exists**:
   ```sh
   git fetch origin && test "$(git rev-parse origin/main)" = "$(git rev-parse HEAD~1)"
   git push origin X.Y.Z
   # wait for the site workflow of the tag (about 2 minutes)
   gh run watch "$(gh run list --workflow site.yml --branch X.Y.Z --limit 1 --json databaseId -q '.[0].databaseId')" --exit-status
   git push origin main
   ```
   If the site workflow fails, stop and report: don't push `main` (its
   `site/compose.yaml` would name an image that doesn't exist).

6. **Report** the workflow run: `gh run list --workflow release.yml --limit 1`,
   and the release URL once it exists (`gh release view X.Y.Z --json url`).

## If something goes wrong

- `origin/main` moved before the tag push (the `test` fails): tag not pushed
  yet, so `git tag -d X.Y.Z`, `git reset --hard HEAD~1`, `git pull --rebase
  origin main`, re-check the version (new commits may change the bump), redo.
- Push of `main` rejected after the tag is out: stop and report; the tag is
  published and must not move.
- Never move or delete a tag that has already been pushed without the user's
  explicit go-ahead: managers in the wild pull images by tag.
