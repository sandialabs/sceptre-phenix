package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gorilla/mux"

	"phenix/api/vlan"
	"phenix/util/freshness"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// The RBAC resources of the VLAN routes, and the resource name of the
// blacklist and unowned namespaces.
const (
	resourceVLANs           = "vlans"
	resourceVLANBlacklist   = "vlans/blacklist"
	resourceVLANAllocations = "vlans/allocations"
	allResourceNames        = "*"
	verbList                = "list"
	maxVLANRequestBytes     = 1 << 20
)

// vlanView is what role may see of the cluster's VLANs: the experiments it
// may list VLANs of, also, if given, the one it may get them of.
func vlanView(role rbac.Role, own string) vlan.View {
	return vlan.View{
		Visible: func(exp string) bool {
			return (own != "" && exp == own) || role.Allowed(resourceVLANs, verbList, exp)
		},
		All: role.Allowed(resourceVLANs, verbList, allResourceNames),
	}
}

// vlanWebError answers a VLAN API error with its status.
func vlanWebError(err error, format string, args ...any) *weberror.WebError {
	var conflict *vlan.ConflictError

	status := http.StatusInternalServerError

	switch {
	case errors.As(err, &conflict):
		werr := weberror.NewWebError(err, "%s", conflict.Error()).SetStatus(http.StatusConflict)
		werr.Issues = conflict.Issues

		return werr
	case errors.Is(err, vlan.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, vlan.ErrNotFound), errors.Is(err, vlan.ErrAliasNotFound):
		status = http.StatusNotFound
	case errors.Is(err, vlan.ErrRunning), errors.Is(err, vlan.ErrNotRunning), errors.Is(err, vlan.ErrInUse),
		errors.Is(err, vlan.ErrMinimegaDown):
		status = http.StatusConflict
	case errors.Is(err, vlan.ErrUnsupported):
		status = http.StatusNotImplemented
	}

	if status != http.StatusInternalServerError {
		return weberror.NewWebError(err, "%s", err.Error()).SetStatus(status)
	}

	return weberror.NewWebError(err, format, args...).SetStatus(status)
}

// readVLANs reads and merges the cluster's VLANs for view, reporting when
// minimega was read.
func readVLANs(w http.ResponseWriter, r *http.Request, view vlan.View) (vlan.Report, error) {
	in, observed, err := vlan.Read(r.Context())
	if err != nil {
		return vlan.Report{}, vlanWebError(err, "unable to read the experiments")
	}

	freshness.SetObserved(w, observed)

	return vlan.Merge(in, view), nil
}

// publishVLANs tells the clients that may list the VLANs of exp, or, for ""
// (the blacklist or an unowned namespace), any VLANs, of a change.
func publishVLANs(exp string) {
	body, _ := json.Marshal(map[string]string{"experiment": exp})

	broker.Broadcast(
		bt.NewRequestPolicy(resourceVLANs, verbList, exp),
		bt.NewResource(resourceVLANs, exp, actionUpdate),
		body,
	)
}

// vlanDenied answers 403 for an action on VLANs that role may not take.
func vlanDenied(r *http.Request, what string) error {
	plog.Warn(plog.TypeSecurity, what+" not allowed", "user", middleware.UserFromContext(r.Context()))

	return weberror.New(http.StatusForbidden, what+" not allowed")
}

// parseVLANID parses a VLAN ID of a path or query.
func parseVLANID(raw string) (int, error) {
	id, err := strconv.Atoi(raw)
	if err != nil || !vlan.ValidID(id) {
		return 0, weberror.NewWebError(nil, "%q is not a VLAN ID in %d–%d", raw, vlan.MinID, vlan.MaxID)
	}

	return id, nil
}

// parseVLANQuery reads the filters of GET /vlans.
func parseVLANQuery(query url.Values) (vlan.Query, error) {
	q := vlan.Query{Experiment: query.Get("experiment"), VLAN: 0, State: "", IssuesOnly: false}

	if raw := query.Get("vlan"); raw != "" {
		id, err := parseVLANID(raw)
		if err != nil {
			return q, err
		}

		q.VLAN = id
	}

	state, err := enumQuery(query, "state", "", vlan.StatePinned, vlan.StateAllocated, vlan.StateBlacklisted)
	if err != nil {
		return q, err
	}

	q.State = state

	issues, err := parseBoolQuery(query, "issues")
	if err != nil {
		return q, err
	}

	q.IssuesOnly = issues

	return q, nil
}

// queryInt reads an optional integer query parameter in lo–hi.
func queryInt(query url.Values, name string, def, lo, hi int) (int, error) {
	raw := query.Get(name)
	if raw == "" {
		return def, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		return 0, weberror.NewWebError(nil, "%s must be a whole number in %d–%d, not %q", name, lo, hi, raw)
	}

	return n, nil
}

// GetVLANs - GET /vlans.
func GetVLANs(w http.ResponseWriter, r *http.Request) error {
	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed(resourceVLANs, verbList) {
		return vlanDenied(r, "listing VLANs")
	}

	q, err := parseVLANQuery(r.URL.Query())
	if err != nil {
		return err
	}

	format, err := enumQuery(r.URL.Query(), "format", "json", "json", "csv")
	if err != nil {
		return err
	}

	rep, err := readVLANs(w, r, vlanView(role, ""))
	if err != nil {
		return err
	}

	if requestGone(r) {
		return nil
	}

	rep = vlan.Filter(rep, q)

	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=vlans.csv")

		return vlan.WriteCSV(w, rep, q) //nolint:wrapcheck // the error names the CSV
	}

	return writeJSONBody(w, rep)
}

// vlanDetails is the body of GET /vlans/{id}.
type vlanDetails struct {
	ID     int          `json:"id"`
	VLANs  []vlan.Row   `json:"vlans"`
	VMs    []vlan.VM    `json:"vms"`
	Issues []vlan.Issue `json:"issues"`
}

// GetVLAN - GET /vlans/{id}.
func GetVLAN(w http.ResponseWriter, r *http.Request) error {
	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed(resourceVLANs, verbGet) {
		return vlanDenied(r, "getting a VLAN")
	}

	id, err := parseVLANID(mux.Vars(r)["id"])
	if err != nil {
		return err
	}

	rep, err := readVLANs(w, r, vlanGetView(role))
	if err != nil {
		return err
	}

	if requestGone(r) {
		return nil
	}

	rep = vlan.Filter(rep, vlan.Query{Experiment: "", VLAN: id, State: "", IssuesOnly: false})

	return writeJSONBody(w, vlanDetails{ID: id, VLANs: rep.VLANs, VMs: vlan.VMsOn(rep, id), Issues: rep.Issues})
}

// vlanGetView is what role may see of one VLAN ID: the experiments it may get
// VLANs of, and the blacklist and unowned namespaces with "*".
func vlanGetView(role rbac.Role) vlan.View {
	return vlan.View{
		Visible: func(exp string) bool { return role.Allowed(resourceVLANs, verbGet, exp) },
		All:     role.Allowed(resourceVLANs, verbGet, allResourceNames),
	}
}

// vlanBlock is a block of free VLAN IDs.
type vlanBlock struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// FindFreeVLANs - GET /vlans/free.
func FindFreeVLANs(w http.ResponseWriter, r *http.Request) error {
	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed(resourceVLANs, verbList) {
		return vlanDenied(r, "finding free VLANs")
	}

	query := r.URL.Query()

	count, err := queryInt(query, "count", 1, 1, vlan.MaxID)
	if err != nil {
		return err
	}

	lo, err := queryInt(query, "min", vlan.MinID, vlan.MinID, vlan.MaxID)
	if err != nil {
		return err
	}

	hi, err := queryInt(query, "max", vlan.MaxID, vlan.MinID, vlan.MaxID)
	if err != nil {
		return err
	}

	if lo > hi {
		return weberror.NewWebError(nil, "min %d is more than max %d", lo, hi)
	}

	outside, err := parseBoolQuery(query, "outside_ranges")
	if err != nil {
		return err
	}

	in, observed, err := vlan.Read(r.Context())
	if err != nil {
		return vlanWebError(err, "unable to read the experiments")
	}

	freshness.SetObserved(w, observed)

	if requestGone(r) {
		return nil
	}

	start, end, err := vlan.FindFree(in, count, lo, hi, outside)
	if err != nil {
		return weberror.NewWebError(err, "no %d free VLAN IDs in a row in %d–%d", count, lo, hi).
			SetStatus(http.StatusNotFound)
	}

	return writeJSONBody(w, vlanBlock{Start: start, End: end})
}

// vlanIssues is the body of POST /vlans/check.
type vlanIssues struct {
	Issues []vlan.Issue `json:"issues"`
}

// CheckVLANs - POST /vlans/check.
func CheckVLANs(w http.ResponseWriter, r *http.Request) error {
	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed(resourceVLANs, verbList) {
		return vlanDenied(r, "checking VLANs")
	}

	var p vlan.Proposal

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxVLANRequestBytes)).Decode(&p); err != nil {
		return weberror.NewWebError(err, "unable to parse the VLAN check request")
	}

	if err := vlan.CheckProposal(p); err != nil {
		return weberror.NewWebError(nil, "%s", err.Error())
	}

	in, observed, err := vlan.Read(r.Context())
	if err != nil {
		return vlanWebError(err, "unable to read the experiments")
	}

	freshness.SetObserved(w, observed)

	if requestGone(r) {
		return nil
	}

	return writeJSONBody(w, vlanIssues{Issues: vlan.Check(in, vlanView(role, ""), p)})
}

// vlanBlacklist is the body of GET /vlans/blacklist.
type vlanBlacklist struct {
	VLANs     []int `json:"vlans"`
	Removable bool  `json:"removable"`
}

// GetVLANBlacklist - GET /vlans/blacklist.
func GetVLANBlacklist(w http.ResponseWriter, r *http.Request) error {
	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed(resourceVLANs, verbList, allResourceNames) {
		return vlanDenied(r, "listing the VLAN blacklist")
	}

	ids, removable, err := vlan.Blacklist(r.Context())
	if requestCanceled(r, err) {
		return nil
	}

	if err != nil {
		return vlanWebError(err, "unable to read the minimega VLAN blacklist")
	}

	return writeJSONBody(w, vlanBlacklist{VLANs: ids, Removable: removable})
}

// vlanAudit is the audit fields of an action on the VLAN ID of a path, which
// name the ID when it is valid.
func vlanAudit(r *http.Request) (int, audit.Fields, error) {
	id, err := parseVLANID(mux.Vars(r)["id"])

	at := audit.Fields{}
	if err == nil {
		at.Extra = []any{"phenix.vlan.id", id}
	}

	return id, at, err
}

// blacklistChange is one change to the minimega blacklist: its RBAC verb, its
// audit action, the words that name it and the change itself.
type blacklistChange struct {
	verb   string
	action audit.Action
	what   string // "blacklisting a VLAN"
	done   string // "VLAN blacklisted"
	fail   string // "unable to blacklist VLAN %d"
	change func(ctx context.Context, id int) error
}

// changeBlacklist checks that the role may make the change, makes it on the
// VLAN ID of the path, and audits it.
func changeBlacklist(w http.ResponseWriter, r *http.Request, c blacklistChange) error {
	role := middleware.RoleFromContext(r.Context())
	id, at, err := vlanAudit(r)

	if !role.Allowed(resourceVLANBlacklist, c.verb, allResourceNames) {
		audit.Denied(r, c.action, c.what+" not allowed", at)

		return vlanDenied(r, c.what)
	}

	if err != nil {
		return err
	}

	err = c.change(r.Context(), id)
	if requestCanceled(r, err) {
		return nil
	}

	if err != nil {
		audit.Failure(r, c.action, c.what+" failed", at, err)

		return vlanWebError(err, c.fail, id)
	}

	audit.Success(r, c.action, c.done, at)
	plog.Info(plog.TypeAction, c.done, "vlan", id, "user", middleware.UserFromContext(r.Context()))
	publishVLANs("")
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// AddVLANBlacklist - PUT /vlans/blacklist/{id}.
func AddVLANBlacklist(w http.ResponseWriter, r *http.Request) error {
	return changeBlacklist(w, r, blacklistChange{
		verb: "create", action: audit.VLANBlacklistAdd, what: "blacklisting a VLAN", done: "VLAN blacklisted",
		fail: "unable to blacklist VLAN %d", change: vlan.AddToBlacklist,
	})
}

// RemoveVLANBlacklist - DELETE /vlans/blacklist/{id}.
func RemoveVLANBlacklist(w http.ResponseWriter, r *http.Request) error {
	return changeBlacklist(w, r, blacklistChange{
		verb: "delete", action: audit.VLANBlacklistRemove, what: "removing a VLAN from the blacklist",
		done: "VLAN removed from the blacklist", fail: "unable to remove VLAN %d from the blacklist",
		change: vlan.RemoveFromBlacklist,
	})
}

// ReleaseVLANAllocations - DELETE /vlans/allocations/{namespace}.
func ReleaseVLANAllocations(w http.ResponseWriter, r *http.Request) error {
	var (
		role = middleware.RoleFromContext(r.Context())
		ns   = mux.Vars(r)["namespace"]
		at   = audit.Fields{Extra: []any{"phenix.vlan.namespace", ns}}
	)

	if !role.Allowed(resourceVLANAllocations, "delete", allResourceNames) {
		audit.Denied(r, audit.VLANRelease, "releasing VLANs not allowed", at)

		return vlanDenied(r, "releasing VLANs")
	}

	err := vlan.Release(r.Context(), ns)
	if requestCanceled(r, err) {
		return nil
	}

	if err != nil {
		audit.Failure(r, audit.VLANRelease, "releasing VLANs failed", at, err)

		return vlanWebError(err, "unable to release the VLANs of namespace %s", ns)
	}

	audit.Success(r, audit.VLANRelease, "VLANs released", at)
	plog.Info(plog.TypeAction, "VLANs released", "namespace", ns, "user", middleware.UserFromContext(r.Context()))
	publishVLANs("")
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// experimentVLANs is the body of GET /experiments/{name}/vlans and of each
// change to an experiment's VLANs.
type experimentVLANs struct {
	Experiment vlan.ExperimentSummary `json:"experiment"`
	VLANs      []vlan.Row             `json:"vlans"`
	Issues     []vlan.Issue           `json:"issues"`
}

// writeExperimentVLANs answers with the VLANs of the experiment name.
func writeExperimentVLANs(w http.ResponseWriter, r *http.Request, role rbac.Role, name string) error {
	rep, err := readVLANs(w, r, vlanView(role, name))
	if err != nil {
		return err
	}

	if requestGone(r) {
		return nil
	}

	rep = vlan.Filter(rep, vlan.Query{Experiment: name, VLAN: 0, State: "", IssuesOnly: false})

	if len(rep.Experiments) == 0 {
		return weberror.NewWebError(nil, "experiment %s not found", name).SetStatus(http.StatusNotFound)
	}

	return writeJSONBody(w, experimentVLANs{Experiment: rep.Experiments[0], VLANs: rep.VLANs, Issues: rep.Issues})
}

// GetExperimentVLANs - GET /experiments/{name}/vlans.
func GetExperimentVLANs(w http.ResponseWriter, r *http.Request) error {
	var (
		role = middleware.RoleFromContext(r.Context())
		name = mux.Vars(r)["name"]
	)

	if !role.Allowed(resourceVLANs, verbGet, name) {
		return vlanDenied(r, "getting the VLANs of experiment "+name)
	}

	return writeExperimentVLANs(w, r, role, name)
}

// changeExperimentVLANs checks that the role may patch the experiment, locks
// it for the change, and answers with its VLANs once change succeeds.
func changeExperimentVLANs(w http.ResponseWriter, r *http.Request, change func(name string) error) error {
	var (
		role = middleware.RoleFromContext(r.Context())
		name = mux.Vars(r)["name"]
		at   = audit.Fields{Experiment: name}
	)

	if !role.Allowed("experiments", verbPatch, name) {
		audit.Denied(r, audit.ExperimentUpdate, "changing experiment VLANs not allowed", at)

		return vlanDenied(r, "changing the VLANs of experiment "+name)
	}

	if err := cache.LockExperimentForUpdate(name); err != nil {
		return weberror.NewWebError(err, "experiment %s is busy", name).SetStatus(http.StatusConflict)
	}

	err := change(name)

	cache.UnlockExperiment(name)

	if requestCanceled(r, err) {
		return nil
	}

	if err != nil {
		audit.Failure(r, audit.ExperimentUpdate, "changing experiment VLANs failed", at, err)

		return vlanWebError(err, "unable to change the VLANs of experiment %s", name)
	}

	audit.Success(r, audit.ExperimentUpdate, "experiment VLANs changed", at)
	publishVLANs(name)

	return writeExperimentVLANs(w, r, role, name)
}

// PinExperimentVLANAlias - PUT /experiments/{name}/vlans/aliases/{alias}.
func PinExperimentVLANAlias(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		ID int `json:"id"`
	}

	force, err := parseBoolQuery(r.URL.Query(), "force")
	if err != nil {
		return err
	}

	role := middleware.RoleFromContext(r.Context())

	return changeExperimentVLANs(w, r, func(name string) error {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxVLANRequestBytes)).Decode(&body); err != nil {
			return fmt.Errorf("%w: the body is not {\"id\": <VLAN ID>}: %w", vlan.ErrInvalid, err)
		}

		return vlan.PinAlias(r.Context(), vlanView(role, name), name, mux.Vars(r)["alias"], body.ID, force)
	})
}

// UnpinExperimentVLANAlias - DELETE /experiments/{name}/vlans/aliases/{alias}.
func UnpinExperimentVLANAlias(w http.ResponseWriter, r *http.Request) error {
	return changeExperimentVLANs(w, r, func(name string) error {
		return vlan.UnpinAlias(name, mux.Vars(r)["alias"])
	})
}

// SetExperimentVLANRange - PUT /experiments/{name}/vlans/range.
func SetExperimentVLANRange(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Min int `json:"min"`
		Max int `json:"max"`
	}

	force, err := parseBoolQuery(r.URL.Query(), "force")
	if err != nil {
		return err
	}

	role := middleware.RoleFromContext(r.Context())

	return changeExperimentVLANs(w, r, func(name string) error {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxVLANRequestBytes)).Decode(&body); err != nil {
			return fmt.Errorf("%w: the body is not {\"min\": <ID>, \"max\": <ID>}: %w", vlan.ErrInvalid, err)
		}

		return vlan.ChangeRange(r.Context(), vlanView(role, name), name, body.Min, body.Max, force)
	})
}

// ClearExperimentVLANRange - DELETE /experiments/{name}/vlans/range.
func ClearExperimentVLANRange(w http.ResponseWriter, r *http.Request) error {
	return changeExperimentVLANs(w, r, vlan.ClearRange)
}

// PinExperimentLiveVLANs - POST /experiments/{name}/vlans/pin-live.
func PinExperimentLiveVLANs(w http.ResponseWriter, r *http.Request) error {
	return changeExperimentVLANs(w, r, func(name string) error {
		_, err := vlan.PinLive(r.Context(), name)

		return err
	})
}
