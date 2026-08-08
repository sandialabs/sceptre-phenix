package web

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gorilla/mux"

	"phenix/api/vlan"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/util"
)

// GetVLANs - GET /vlans.
// Returns VLAN alias information for all experiments.
func GetVLANs(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetVLANs")

	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
	)

	if !role.Allowed("vlans", "list") {
		plog.Warn(
			plog.TypeSecurity,
			"listing VLANs not allowed",
			"user",
			middleware.UserFromContext(ctx),
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	aliases, err := vlan.Aliases()
	if err != nil {
		plog.Error(plog.TypeSystem, "getting VLAN aliases", "err", err)
		http.Error(w, "unable to get VLAN aliases", http.StatusInternalServerError)

		return
	}

	body, err := json.Marshal(util.WithRoot("vlans", aliases))
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling VLAN aliases", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// GetExperimentVLANAliases - GET /experiments/{name}/vlans/aliases.
func GetExperimentVLANAliases(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetExperimentVLANAliases")

	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/vlans", "get", name) {
		plog.Warn(
			plog.TypeSecurity,
			"getting experiment VLAN aliases not allowed",
			"user",
			middleware.UserFromContext(ctx),
			"exp",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	aliases, err := vlan.Aliases(vlan.Experiment(name))
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiment VLAN aliases", "exp", name, "err", err)
		http.Error(w, "unable to get VLAN aliases for experiment "+name, http.StatusInternalServerError)

		return
	}

	body, err := json.Marshal(util.WithRoot("aliases", aliases[name]))
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling VLAN aliases", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

type setVLANAliasRequest struct {
	Alias string `json:"alias"`
	ID    int    `json:"id"`
	Force bool   `json:"force"`
}

// SetExperimentVLANAlias - POST /experiments/{name}/vlans/aliases.
func SetExperimentVLANAlias(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "SetExperimentVLANAlias")

	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/vlans", "patch", name) {
		plog.Warn(
			plog.TypeSecurity,
			"setting experiment VLAN alias not allowed",
			"user",
			middleware.UserFromContext(ctx),
			"exp",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)
		http.Error(w, "unable to read request body", http.StatusInternalServerError)

		return
	}

	var req setVLANAliasRequest

	if err := json.Unmarshal(body, &req); err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)

		return
	}

	if req.Alias == "" {
		http.Error(w, "alias is required", http.StatusBadRequest)

		return
	}

	if req.ID == 0 {
		http.Error(w, "id is required", http.StatusBadRequest)

		return
	}

	opts := []vlan.Option{
		vlan.Experiment(name),
		vlan.Alias(req.Alias),
		vlan.ID(req.ID),
		vlan.Force(req.Force),
	}

	if err := vlan.SetAlias(opts...); err != nil {
		plog.Error(plog.TypeSystem, "setting VLAN alias", "exp", name, "alias", req.Alias, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	plog.Info(
		plog.TypeAction,
		"VLAN alias set",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		name,
		"alias",
		req.Alias,
		"id",
		req.ID,
	)
	w.WriteHeader(http.StatusNoContent)
}

// GetExperimentVLANRanges - GET /experiments/{name}/vlans/ranges.
func GetExperimentVLANRanges(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetExperimentVLANRanges")

	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/vlans", "get", name) {
		plog.Warn(
			plog.TypeSecurity,
			"getting experiment VLAN ranges not allowed",
			"user",
			middleware.UserFromContext(ctx),
			"exp",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	ranges, err := vlan.Ranges(vlan.Experiment(name))
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiment VLAN ranges", "exp", name, "err", err)
		http.Error(w, "unable to get VLAN ranges for experiment "+name, http.StatusInternalServerError)

		return
	}

	r2 := ranges[name]

	result := map[string]int{
		"min": r2[0],
		"max": r2[1],
	}

	body, err := json.Marshal(util.WithRoot("range", result))
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling VLAN ranges", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

type setVLANRangeRequest struct {
	Min   int  `json:"min"`
	Max   int  `json:"max"`
	Force bool `json:"force"`
}

// SetExperimentVLANRange - POST /experiments/{name}/vlans/ranges.
func SetExperimentVLANRange(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "SetExperimentVLANRange")

	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/vlans", "patch", name) {
		plog.Warn(
			plog.TypeSecurity,
			"setting experiment VLAN range not allowed",
			"user",
			middleware.UserFromContext(ctx),
			"exp",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)
		http.Error(w, "unable to read request body", http.StatusInternalServerError)

		return
	}

	var req setVLANRangeRequest

	if err := json.Unmarshal(body, &req); err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)

		return
	}

	if req.Min == 0 {
		http.Error(w, "min is required", http.StatusBadRequest)

		return
	}

	if req.Max == 0 {
		http.Error(w, "max is required", http.StatusBadRequest)

		return
	}

	opts := []vlan.Option{
		vlan.Experiment(name),
		vlan.Min(req.Min),
		vlan.Max(req.Max),
		vlan.Force(req.Force),
	}

	if err := vlan.SetRange(opts...); err != nil {
		plog.Error(plog.TypeSystem, "setting VLAN range", "exp", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	plog.Info(
		plog.TypeAction,
		"VLAN range set",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		name,
		"min",
		req.Min,
		"max",
		req.Max,
	)
	w.WriteHeader(http.StatusNoContent)
}
