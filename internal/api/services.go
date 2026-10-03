package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) listServices(w http.ResponseWriter, r *http.Request) {
	svcs, err := a.core.ListServices(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, svcs)
}

func (a *API) createService(w http.ResponseWriter, r *http.Request) {
	var in core.ServiceInput
	if !decode(w, r, &in) {
		return
	}
	svc, err := a.core.CreateService(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, svc)
}

func (a *API) getService(w http.ResponseWriter, r *http.Request) {
	svc, err := a.core.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, svc)
}

func (a *API) updateService(w http.ResponseWriter, r *http.Request) {
	var p core.ServicePatch
	if !decode(w, r, &p) {
		return
	}
	svc, err := a.core.UpdateService(r.Context(), r.PathValue("id"), p)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, svc)
}

func (a *API) deleteService(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteService(r.Context(), r.PathValue("id"), r.URL.Query().Get("confirm")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) connection(w http.ResponseWriter, r *http.Request) {
	conn, err := a.core.DatabaseConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, conn)
}

func (a *API) deployService(w http.ResponseWriter, r *http.Request) {
	var opts core.DeployOptions // optional: an empty body deploys the current settings
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&opts); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	dep, err := a.core.Deploy(r.Context(), r.PathValue("id"), opts)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, dep)
}

func (a *API) rollbackService(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeploymentID string `json:"deploymentId"`
	}
	if !decode(w, r, &body) {
		return
	}
	dep, err := a.core.Rollback(r.Context(), r.PathValue("id"), body.DeploymentID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, dep)
}

func (a *API) serviceAction(w http.ResponseWriter, r *http.Request) {
	action := core.Action(r.PathValue("action"))
	switch action {
	case core.ActionStart, core.ActionStop, core.ActionRestart:
	default:
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	svc, err := a.core.ServiceAction(r.Context(), r.PathValue("id"), action)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, svc)
}

func (a *API) listDeployments(w http.ResponseWriter, r *http.Request) {
	ds, err := a.core.ListDeployments(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ds)
}

func (a *API) getDeployment(w http.ResponseWriter, r *http.Request) {
	d, err := a.core.GetDeployment(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (a *API) deploymentLog(w http.ResponseWriter, r *http.Request) {
	rc, err := a.core.DeploymentLog(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.Copy(w, rc)
}

// streamLogs sends container log lines as Server-Sent Events. A final "end"
// event tells the client the streams closed (e.g. containers were replaced),
// so it can reconnect deliberately instead of EventSource's blind retry.
func (a *API) streamLogs(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	started := false
	start := func() {
		if !started {
			w.WriteHeader(http.StatusOK)
			started = true
		}
	}
	err := a.core.StreamLogs(r.Context(), r.PathValue("id"), tail, func(l core.LogLine) {
		start()
		b, _ := json.Marshal(l)
		fmt.Fprintf(w, "data: %s\n\n", b)
		_ = rc.Flush()
	})
	if err != nil && !started {
		w.Header().Set("Content-Type", "application/json")
		a.fail(w, err)
		return
	}
	start()
	fmt.Fprint(w, "event: end\ndata: {}\n\n")
	_ = rc.Flush()
}
