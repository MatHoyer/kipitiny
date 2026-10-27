---
name: fix
description: Fix a kipitiny bug end to end — reproduce it with a failing test, fix the root cause, test for real on the local manager, report in the PR. Takes a GitHub issue (number or URL) or a description. Use when the user asks to fix a bug, a regression, an audit finding or a failing behavior, or invokes /fix <issue or description>.
---

# Fix

Input: a GitHub issue (number or URL: read it with `gh issue view <n> --comments`) or a description of the bug.
Five phases, in order. Never skip the failing test or the real-usage tests.

## 1. Understand

- Restate the bug: what happens, what should happen and the doc or rule that says so (`site/docs/…`, CLAUDE.md,
  `docs/design.md`), and the code involved.
- Ask with AskUserQuestion only when the expected behavior is genuinely unclear.

## 2. Reproduce

- Write the regression test first (unit or API, next to the existing tests of that code) and see it **fail** on the
  current code, for the reason found in phase 1.
- When the bug shows in the app, reproduce it on the worktree's test environment too (phase 4 setup) and note the symptom.
- Can't reproduce: say so with what you tried, and ask before changing code.

## 3. Fix

- Fix the root cause once, in the shared code: grep every caller of what you change. Follow CLAUDE.md rules.
- Branch `fix/<short-name>` from `main` (several related fixes = stacked PRs, one branch each, per the stacked PRs
  workflow).
- User docs in `site/docs/` when behavior changes; an `UPGRADE.md` notice under `## Unreleased` if running servers
  are disrupted (a sync that now fails, a setting refused).
- The regression test now passes; confirm it still fails with the fix reverted. `make test` and `make lint` green.

## 4. Test in real usage

Same as the feature skill's phase 3 (`.claude/skills/feature/SKILL.md`): the worktree's own test
environment (`scripts/test-env.sh up`), test resources in `test-<fix>-*` projects, API and real browser, never the
production MCP server, never the host's `kipitiny-manager-1`. Exercise:
- the bug's reproduction: now fixed;
- the normal path the fix touches, as an admin **and** as a member, so the fix doesn't break legitimate use;
- for a security fix, the lower-trust principal: now refused, with a clear message.

Failures you find are fixed and retested, not just reported. A stack fix goes into the commit of the PR that
introduced it (see the feature skill).

## 5. Report

- PR body: `Fixes #<n>` when the input was an issue, a short **What** (the bug and its root cause), and the feature
  skill's `## Testing` table (✅/❌/⚠️ rows, "Not covered" for every gap).
- Final message to the user: the PR link and the table summary (counts + ⚠️/❌ rows).
