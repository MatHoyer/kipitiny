---
title: Files
description: Browse, upload, edit and download the files of a service's volumes and containers
order: 7
---

A service's **Files** tab manages its files from the browser: no `docker cp`, no
SSH, no file manager container next to it. Agents get the same through
[MCP](/docs/api) tools, with the same scopes.

The tab starts by listing where files live:

- **Volumes**: an app's named volumes (Settings › Volumes) and a database's data
  volume, with the path each is mounted at. They survive deploys and are
  [backed up](/docs/backups). A volume is browsed through a short-lived helper
  container that mounts it, so the service doesn't need to run, and its image
  needs no tools.
- **Container filesystem**: everything else in the service's running container,
  from `/`. See [below](#container-filesystem) for what differs.

A database's files are read-only, volume and container alike. The database writes
them while it runs, so a download isn't a consistent copy: use a
[backup](/docs/backups) for that.

## Browse

- Click a folder to open it, and the path at the top to go back up. The folder is
  in the page's address (`?path=`), so a link or a reload keeps it.
- Each entry shows its size, when it changed, its permissions and its owner
  (`uid:gid`). Symlinks show their target. A folder lists at most 5,000 entries.
- Click a text file to open it in the editor (see [Edit](#edit)). A file that isn't
  text, or is over 1 MiB, offers a download instead.

## Download

A file downloads as it is. A folder, a volume or a selection downloads as a
`.tar.gz`, streamed (no size cap), with owners and permissions kept and symlinks
stored as links.

## Upload

**Upload** takes files or a whole folder, or drop them onto the list (folders
too). Files go two at a time with their progress, into the folder you're in;
missing folders are created. A file that already exists waits in the queue:
**Replace** it, or skip it (**Replace all** handles several).

An upload is written next to its target and moved into place only once it
arrived whole, so a cut connection changes nothing. Files are limited to 4 GiB.

## Edit

Text files open in an editor (line numbers, undo, tab indent). **Save**, or
Ctrl+S (⌘S on a Mac), writes the file.

If the file changed after you opened it (the app rewrote it, or another admin
saved it), saving doesn't overwrite it: choose **Reload theirs** to drop your
edit, or **Overwrite with mine**. Closing with unsaved changes asks first.

## Manage

- **New folder**, and from a row's **…** menu or a selection: **Rename**,
  **Move**, **Copy** (folders with everything in them) and **Delete**.
- Move and copy take an existing folder as destination: any volume of the
  service, or, for container files, the same container.
- A delete checks every path first: if one is missing, nothing is deleted.
- New files and folders belong to the owner of the folder they land in, so an
  app running as a non-root user (uid 1000, say) can use what you upload. A
  replaced file keeps its owner and permissions.

Most apps read their files at startup. After a change on a running app, the
confirmation offers **Restart**.

## Container filesystem

The container's own files are useful to look at configuration baked into the
image, logs written to disk, or to try a quick fix. Writing there has limits:

- **Changes are temporary.** They are lost on the next deploy, and whenever the
  container is recreated. Keep files that matter in a volume.
- **Changes reach one replica.** With several replicas, pick one at the top of
  the tab; each has its own files. The default is the first running replica.
- **The service must be running.** Commands run inside the container, as root
  whatever user the image runs as.
- **The image needs a shell** and the usual tools (`sh`, `stat`, `realpath`,
  `cat`; busybox or coreutils). Most images have them. Distroless and `scratch`
  images don't: their files can't be browsed, so use a volume.
- Moving or copying between the container and a volume isn't possible:
  download, then upload.

## Access

Listing files needs a read token. Reading or downloading a file's content needs
admin, as files may hold secrets (like backup downloads). Every change (upload,
save, new folder, move, copy, delete) needs admin and lands in the
[audit log](/docs/api) with its path.

## API and MCP

Paths are `<volume>/<path>` (`data/config/app.yml`), or
`@container/<absolute path>` (`@container/etc/nginx/nginx.conf`) for the
container. `@<container ID>/...` picks a replica.

| Route | |
| --- | --- |
| `GET /api/services/{id}/files?path=` | list a folder; an empty path lists the volumes and `@container` |
| `GET /api/services/{id}/files/content?path=` | a text file (up to 1 MiB) |
| `GET /api/services/{id}/files/download?path=[&name=…]` | a file, or a folder (or the `name`d entries in it) as `.tar.gz` |
| `PUT /api/services/{id}/files/content?path=[&overwrite=true][&modified=…]` | write the request body to a file; `modified` (as read) refuses a file changed since |
| `POST /api/services/{id}/files/mkdir`, `/move`, `/copy`, `/delete` | `{"path"}`, `{"from","to"}`, `{"paths":[…]}` |

MCP tools: `list_volume_files`, `read_volume_file`, `write_volume_file` (text, or
base64 up to 16 MiB), `make_volume_dir`, `move_volume_path` (or copy) and
`delete_volume_paths`.
