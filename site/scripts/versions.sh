#!/bin/sh
# Snapshots site/docs at the last patch of each minor release, from 0.10 on,
# into site/versions/<minor>/ with that release's template catalog: the site
# publishes them under /docs/<minor>/.
# Needs the release tags (CI: actions/checkout with fetch-depth 0). git archive
# runs from the repo root: from site/ it would only keep paths under site/.
set -eu
cd "$(dirname "$0")/.."
rm -rf versions
mkdir versions
git tag --list | grep -E '^[0-9]+\.[0-9]+\.[0-9]+$' | sort -V |
  awk -F. '$1 > 0 || $2 >= 10 { last[$1 "." $2] = $0 } END { for (m in last) print m, last[m] }' |
  while read -r minor tag; do
    mkdir "versions/$minor"
    git -C .. archive "$tag:site/docs" | tar -x -C "versions/$minor"
    # Its template catalog, which its templates page lists.
    if git -C .. cat-file -e "$tag:web/src/demo/catalog.json" 2>/dev/null; then
      git -C .. show "$tag:web/src/demo/catalog.json" >"versions/$minor/catalog.json"
    fi
    echo "docs $minor <- $tag"
  done
