package api

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/core"
)

// maxUpload caps one file written to a volume.
const maxUpload = 4 << 30

func (a *API) listVolumeFiles(w http.ResponseWriter, r *http.Request) {
	l, err := a.core.ListVolumeFiles(r.Context(), r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (a *API) readVolumeFile(w http.ResponseWriter, r *http.Request) {
	f, err := a.core.ReadVolumeFile(r.Context(), r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// downloadVolumeFiles sends a file as it is, or a folder (or the entries
// named by "name" in it) as a .tar.gz.
func (a *API) downloadVolumeFiles(w http.ResponseWriter, r *http.Request) {
	id, q := r.PathValue("id"), r.URL.Query()
	p, names := q.Get("path"), q["name"]
	base := path.Base(strings.Trim(p, "/"))
	archive := len(names) > 0
	if !archive {
		e, err := a.core.StatVolumePath(r.Context(), id, p)
		if err != nil {
			a.fail(w, err)
			return
		}
		archive = e.Type != "file"
	}
	if len(names) == 1 {
		base = names[0]
	}
	out := &lazyHeaders{w: w, set: func(h http.Header) {
		name := base
		if archive {
			name += ".tar.gz"
			h.Set("Content-Type", "application/gzip")
		} else {
			h.Set("Content-Type", "application/octet-stream")
		}
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		h.Set("X-Content-Type-Options", "nosniff")
	}}
	var err error
	if archive {
		err = a.core.ArchiveVolumeFiles(r.Context(), id, p, names, out)
	} else {
		err = a.core.DownloadVolumeFile(r.Context(), id, p, out)
	}
	switch {
	case err != nil && !out.wrote:
		a.fail(w, err)
	case err != nil:
		a.log.Warn("download interrupted", "service", id, "path", p, "err", err)
	case !out.wrote: // an empty file
		out.set(w.Header())
		w.WriteHeader(http.StatusOK)
	}
}

// writeVolumeFile stores the request body as a file: an upload, or an
// editor's save (overwrite=true and the modified time it read).
func (a *API) writeVolumeFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opts := core.WriteOptions{Overwrite: q.Get("overwrite") == "true"}
	if m := q.Get("modified"); m != "" {
		t, err := time.Parse(time.RFC3339, m)
		if err != nil {
			writeError(w, http.StatusBadRequest, "modified must be an RFC 3339 time")
			return
		}
		opts.Modified = t
	}
	if r.ContentLength > maxUpload {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("files are limited to %d GiB", maxUpload>>30))
		return
	}
	body := http.MaxBytesReader(w, r.Body, maxUpload)
	e, err := a.core.WriteVolumeFile(r.Context(), r.PathValue("id"), q.Get("path"), body, opts)
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("files are limited to %d GiB", maxUpload>>30))
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (a *API) makeVolumeDir(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := a.core.MakeVolumeDir(r.Context(), r.PathValue("id"), body.Path); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) moveVolumePath(w http.ResponseWriter, r *http.Request) {
	a.transferVolumePath(w, r, a.core.MoveVolumePath)
}

func (a *API) copyVolumePath(w http.ResponseWriter, r *http.Request) {
	a.transferVolumePath(w, r, a.core.CopyVolumePath)
}

func (a *API) transferVolumePath(w http.ResponseWriter, r *http.Request, op func(context.Context, string, string, string) error) {
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := op(r.Context(), r.PathValue("id"), body.From, body.To); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deleteVolumePaths(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths []string `json:"paths"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := a.core.DeleteVolumePaths(r.Context(), r.PathValue("id"), body.Paths); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
