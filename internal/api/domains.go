package api

import "net/http"

func (a *API) listDomains(w http.ResponseWriter, r *http.Request) {
	ds, err := a.core.DomainViews(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ds)
}

func (a *API) createDomain(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	d, err := a.core.CreateDomain(r.Context(), body.Name)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (a *API) updateDomain(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Proxied bool `json:"proxied"`
	}
	if !decode(w, r, &body) {
		return
	}
	d, err := a.core.SetDomainProxied(r.Context(), r.PathValue("id"), body.Proxied)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (a *API) cloudflareStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.core.CloudflareStatus(r.Context()))
}

func (a *API) connectCloudflare(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &body) {
		return
	}
	v, err := a.core.ConnectCloudflare(r.Context(), body.Token)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) disconnectCloudflare(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DisconnectCloudflare(r.Context()); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) setServerPublicIP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PublicIP string `json:"publicIp"`
	}
	if !decode(w, r, &body) {
		return
	}
	sv, err := a.core.SetServerPublicIP(r.Context(), r.PathValue("id"), body.PublicIP)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sv)
}

func (a *API) deleteDomain(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteDomain(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
