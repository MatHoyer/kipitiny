package api

import (
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func (a *API) listServiceBackups(w http.ResponseWriter, r *http.Request) {
	bs, err := a.core.ListBackups(r.Context(), store.BackupFilter{ServiceID: r.PathValue("id")})
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bs)
}

func (a *API) listBackups(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	bs, err := a.core.ListBackups(r.Context(), store.BackupFilter{
		ProjectID: r.URL.Query().Get("projectId"),
		Limit:     limit,
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bs)
}

func (a *API) createBackup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetID string `json:"targetId"`
	}
	if !decode(w, r, &body) {
		return
	}
	b, err := a.core.BackupDatabase(r.Context(), r.PathValue("id"), body.TargetID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, b)
}

func (a *API) getBackup(w http.ResponseWriter, r *http.Request) {
	b, err := a.core.GetBackup(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (a *API) deleteBackup(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteBackup(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) downloadBackup(w http.ResponseWriter, r *http.Request) {
	b, rc, err := a.core.OpenBackup(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rc.Close()
	name := fmt.Sprintf("%s-%s", b.ProjectName, path.Base(b.ObjectKey))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Header().Set("Content-Length", strconv.FormatInt(b.SizeBytes, 10))
	if _, err := io.Copy(w, rc); err != nil {
		a.log.Warn("backup download interrupted", "backup", b.ID, "err", err)
	}
}

func (a *API) restoreBackup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServiceID string `json:"serviceId"`
		Confirm   string `json:"confirm"`
	}
	if !decode(w, r, &body) {
		return
	}
	res, err := a.core.RestoreBackup(r.Context(), r.PathValue("id"), body.ServiceID, body.Confirm)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, res)
}

func (a *API) listRestores(w http.ResponseWriter, r *http.Request) {
	rs, err := a.core.ListRestores(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (a *API) startProtonLogin(w http.ResponseWriter, r *http.Request) {
	l, err := a.core.StartProtonLogin()
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, l)
}

func (a *API) protonLogin(w http.ResponseWriter, r *http.Request) {
	l, err := a.core.ProtonLoginStatus(r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (a *API) storageKinds(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.core.BackupTargetKinds())
}

func (a *API) listStorage(w http.ResponseWriter, r *http.Request) {
	ts, err := a.core.ListBackupTargets(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ts)
}

func (a *API) createStorage(w http.ResponseWriter, r *http.Request) {
	var in core.TargetInput
	if !decode(w, r, &in) {
		return
	}
	t, err := a.core.CreateBackupTarget(r.Context(), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (a *API) updateStorage(w http.ResponseWriter, r *http.Request) {
	var in core.TargetInput
	if !decode(w, r, &in) {
		return
	}
	t, err := a.core.UpdateBackupTarget(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (a *API) deleteStorage(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteBackupTarget(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) backupProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetID string `json:"targetId"`
	}
	if !decode(w, r, &body) {
		return
	}
	started, err := a.core.BackupProject(r.Context(), r.PathValue("id"), body.TargetID)
	if err != nil && len(started) == 0 {
		a.fail(w, err)
		return
	}
	res := struct {
		Backups []store.Backup `json:"backups"`
		Error   string         `json:"error,omitempty"`
	}{Backups: started}
	if err != nil {
		res.Error = err.Error() // some databases could not start
	}
	writeJSON(w, http.StatusAccepted, res)
}

func (a *API) listSchedules(w http.ResponseWriter, r *http.Request) {
	scs, err := a.core.ListBackupSchedules(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, scs)
}

func (a *API) createSchedule(w http.ResponseWriter, r *http.Request) {
	var in core.ScheduleInput
	if !decode(w, r, &in) {
		return
	}
	sc, err := a.core.CreateBackupSchedule(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sc)
}

func (a *API) updateSchedule(w http.ResponseWriter, r *http.Request) {
	var in core.ScheduleInput
	if !decode(w, r, &in) {
		return
	}
	sc, err := a.core.UpdateBackupSchedule(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

func (a *API) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteBackupSchedule(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) storageKey(w http.ResponseWriter, r *http.Request) {
	key, err := a.core.BackupTargetKey(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, key)
}

func (a *API) backupManager(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetID string `json:"targetId"`
	}
	if !decode(w, r, &body) {
		return
	}
	b, err := a.core.BackupManager(r.Context(), body.TargetID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, b)
}

func (a *API) verifyBackup(w http.ResponseWriter, r *http.Request) {
	b, err := a.core.VerifyBackup(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, b)
}

func (a *API) testStorage(w http.ResponseWriter, r *http.Request) {
	if err := a.core.TestBackupTarget(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
