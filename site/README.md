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
```

A new file in `docs/` is a new page; nothing else to register. The footer shows
the repo's `VERSION`.

```sh
make dev-site    # from the repo root: http://localhost:5173
make site        # typecheck + static build in site/build/client
docker build -t kipitiny-homepage site && docker run --rm -p 8080:80 kipitiny-homepage
```

The *site* workflow publishes the image on every release tag, or when run by hand.
