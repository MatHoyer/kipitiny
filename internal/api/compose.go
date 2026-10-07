package api

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/core"
)

// exportCompose returns a project (or ?service=name) as a compose file,
// secrets as ${NAME} variables without values.
func (a *API) exportCompose(w http.ResponseWriter, r *http.Request) {
	out, err := a.core.ExportCompose(r.Context(), r.PathValue("id"), core.ExportOptions{Service: r.URL.Query().Get("service")})
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	if r.URL.Query().Get("download") != "" {
		w.Header().Set("Content-Disposition", `attachment; filename="compose.yaml"`)
	}
	_, _ = w.Write(out.Compose)
}

// exportBundle returns a zip of compose.yaml and the .env holding the
// secret values; the password must be re-entered.
func (a *API) exportBundle(w http.ResponseWriter, r *http.Request) {
	if !a.confirmAllowed(w, r) {
		return
	}
	var body struct {
		Password string `json:"password"`
		Service  string `json:"service"`
	}
	if !decode(w, r, &body) {
		return
	}
	out, err := a.core.ExportWithSecrets(r.Context(), r.PathValue("id"), body.Service, body.Password)
	if err != nil {
		a.failConfirm(w, r, err)
		return
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range map[string][]byte{"compose.yaml": out.Compose, ".env": out.Env} {
		f, err := zw.Create(name)
		if err == nil {
			_, err = f.Write(data)
		}
		if err != nil {
			a.fail(w, err)
			return
		}
	}
	if err := zw.Close(); err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, "kipitiny-export.zip"))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}
