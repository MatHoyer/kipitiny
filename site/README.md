# site

The kipitiny website: landing page and documentation, published as
`ghcr.io/mathoyer/kipitiny-homepage` (nginx, port 80). React Router in framework
mode with `ssr: false`: every page is prerendered to static HTML at build time.
It is not part of the manager image.

```
app/routes/home.tsx         /
app/routes/docs-*.tsx       /docs and /docs/<slug>
app/routes/llms.ts          /llms.txt, index of the docs for LLMs
app/routes/doc-llms.ts      /docs/<slug>/llms.txt, one doc as markdown
docs/<slug>.md              the docs (front matter: title, description, order)
versions/<minor>/<slug>.md  generated: the docs of each release, at /docs/<minor>/...
```

A new file in `docs/` is a new page; nothing else to register. The footer shows
the repo's `VERSION`.

`/docs` is always the latest. `scripts/versions.sh` (run by `make site`,
`make dev-site` and the workflow) copies `docs/` at the last patch tag of every
minor release since 0.10 into `versions/`, so `/docs/0.10/compose` describes
0.10.x and a patch release replaces its minor's copy. Each copy has its own
`/docs/<minor>/llms.txt` and a version picker switches between them; the
manager (UI links, MCP instructions) points to its own version.

```sh
make dev-site    # from the repo root: http://localhost:5173
make site        # typecheck + static build in site/build/client
docker build -t kipitiny-homepage site && docker run --rm -p 8080:80 kipitiny-homepage
```

The *site* workflow publishes the image on every release tag, or when run by hand.

It runs on kipitiny itself: a project linked to `compose.yaml` here (Project ›
Git, path `site/compose.yaml`). The release commit pins the image to its
version and is pushed to `main` after the tag, once the workflow has published
that image; git sync then deploys it.
