# design-sync notes

Source is kipitiny's `src/components/ui`, published as the neutral `homelab-ui` package. Its `src/` stays untouched; no logo or kipitiny branding ships. Check with `grep -ri kipitiny ds-bundle/_ds_bundle.* ds-bundle/components ds-bundle/README.md` before upload.

`web/node_modules` here is a symlink to the main checkout. The ignore pattern doesn't match a symlink, so never commit it.

## Known render warns

None. 27/27 render, no errors.

Previews lean on two tricks: `Tip` opens through `autoFocus` (it has no `open` prop), and `Dialog`/`Sheet` set `onOpenAutoFocus` to avoid selected text in the card.

## Re-sync risks

- `build-pkg.mjs` strips `@fontsource` imports from `src/index.css` and appends a utility safelist. New font imports or utilities used by designs need that script updated.
- `index.ts` lists `ui` files by hand, and `config.json` lists subparts in `componentSrcMap`. A new `ui` file or subpart must be added to both.
- `playwright` in `.ds-sync` is pinned to 1.64.0 for the cached chromium build.
- Keycloak and Forgejo adapters in the ops repo copy these tokens into `design-system/tokens.css` by hand, so token changes need applying there too.

## Auth

`/design-login` and `/design-consent` don't work inside Orca chat sessions (SDK entrypoint). Run `/design-login` once in a plain terminal `claude` session; Orca sessions then reuse it.
