package api

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/compose"
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

// applyCompose makes the project match a compose file (see
// core.ApplyCompose); env is the optional .env text.
func (a *API) applyCompose(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Compose string `json:"compose"`
		Env     string `json:"env"`
		Prune   bool   `json:"prune"`
		DryRun  bool   `json:"dryRun"`
		Deploy  bool   `json:"deploy"`
	}
	if !decode(w, r, &body) {
		return
	}
	env, err := compose.ParseEnv([]byte(body.Env))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := a.core.ApplyCompose(r.Context(), r.PathValue("id"), []byte(body.Compose), core.ApplyOptions{
		Env: env, Prune: body.Prune, DryRun: body.DryRun, Deploy: body.Deploy,
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}
