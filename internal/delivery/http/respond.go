package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/app"
)

func (a *API) fail(w http.ResponseWriter, err error) {
	code, msg := a.failure(err)
	writeError(w, code, msg)
}

// failure maps err to the status and message the API answers with.
func (a *API) failure(err error) (int, string) {
	switch {
	case errors.Is(err, identity.ErrUnauthorized):
		return http.StatusUnauthorized, reason(err, identity.ErrUnauthorized)
	case errors.Is(err, identity.ErrForbidden):
		return http.StatusForbidden, reason(err, identity.ErrForbidden)
	case errors.Is(err, app.ErrInvalid):
		return http.StatusBadRequest, reason(err, app.ErrInvalid)
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "not found"
	case errors.Is(err, domain.ErrConflict):
		msg := "already exists (name or domain taken)"
		if strings.Contains(err.Error(), domain.ErrConflict.Error()+": ") {
			msg = reason(err, domain.ErrConflict) // core's own explanation
		}
		return http.StatusConflict, msg
	case errors.Is(err, app.ErrBusy):
		msg := "another operation is in progress for this service"
		if strings.Contains(err.Error(), app.ErrBusy.Error()+": ") {
			msg = reason(err, app.ErrBusy)
		}
		return http.StatusConflict, msg
	default:
		a.log.Error("request failed", "err", err)
		return http.StatusInternalServerError, "internal error"
	}
}

// reason drops the sentinel's own text ("invalid input: ") so the UI shows
// only the explanation; a bare sentinel stays as is.
func reason(err, sentinel error) string {
	msg, prefix := err.Error(), sentinel.Error()+": "
	if i := strings.Index(msg, prefix); i >= 0 {
		return msg[i+len(prefix):]
	}
	return msg
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
