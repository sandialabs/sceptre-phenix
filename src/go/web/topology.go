package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/gorilla/mux"
	"google.golang.org/protobuf/types/known/structpb"

	"phenix/api/config"
	"phenix/api/scenario"
	"phenix/app"
	"phenix/store"
	"phenix/types"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/proto"
	"phenix/web/weberror"
)

// GetApplications - GET /applications.
func GetApplications(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
	)

	if !role.Allowed("applications", "list") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(plog.TypeSecurity, "listing applications not allowed", "user", user)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	allowed := []string{}

	for _, app := range app.List() {
		if role.Allowed("applications", "list", app) {
			allowed = append(allowed, app)
		}
	}

	body, err := marshaler.Marshal(&proto.AppList{Applications: allowed})
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// GetTopologyLaunchableVMNames - GET /topologies/{topo}/launchable-vm-names.
func GetTopologyLaunchableVMNames(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		topo = vars["topo"]
	)

	if !role.Allowed("topologies", "get", topo) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"getting topology launchable vm names not allowed",
			"user",
			user,
			"topology",
			topo,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	topoConfig, err := store.NewConfig("topology/" + topo)
	if err != nil {
		return weberror.NewWebError(err, "unable to build topology config key").
			SetStatus(http.StatusInternalServerError)
	}

	if err := store.Get(topoConfig); err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return weberror.New(http.StatusNotFound, "topology not found")
		}

		return weberror.NewWebError(err, "unable to get topology").
			SetStatus(http.StatusInternalServerError)
	}

	topology, err := types.DecodeTopologyFromConfig(*topoConfig)
	if err != nil {
		return weberror.NewWebError(err, "unable to decode topology").
			SetStatus(http.StatusInternalServerError)
	}

	names := topology.LaunchableVMNames()
	if names == nil {
		// A topology with only external or do_not_boot nodes has no
		// launchable VMs; encode that as [] rather than null so the response
		// matches the OpenAPI array schema.
		names = []string{}
	}

	body, err := json.Marshal(map[string][]string{"vm_names": names})
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// GetTopologies - GET /topologies.
func GetTopologies(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
	)

	if !role.Allowed("topologies", "list") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(plog.TypeSecurity, "listing topologies not allowed", "user", user)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	topologies, err := config.List("topology")
	if err != nil {
		return weberror.NewWebError(err, "unable to list topologies").SetStatus(http.StatusInternalServerError)
	}

	allowed := []string{}

	for _, topo := range topologies {
		if role.Allowed("topologies", "list", topo.Metadata.Name) {
			allowed = append(allowed, topo.Metadata.Name)
		}
	}

	body, err := marshaler.Marshal(&proto.TopologyList{Topologies: allowed})
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// GetScenarios - GET /topologies/{topo}/scenarios.
func GetScenarios(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		topo = vars["topo"]
	)

	if !role.Allowed("scenarios", "list") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(plog.TypeSecurity, "listing scenarios not allowed", "user", user)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	scenarios, err := config.List("scenario")
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	allowed := make(map[string]*structpb.ListValue)

	for _, s := range scenarios {
		var (
			// A scenario can be associated with more than one topology.
			topos = strings.Split(s.Metadata.Annotations["topology"], ",")
			found bool
		)

		if slices.Contains(topos, topo) {
			found = true
		}

		if !found {
			continue
		}

		if role.Allowed("scenarios", "list", s.Metadata.Name) {
			apps, err := scenario.AppList(s.Metadata.Name)
			if err != nil {
				plog.Error(
					plog.TypeSystem,
					"getting apps for scenario",
					"scenario",
					s.Metadata.Name,
					"err",
					err,
				)

				continue
			}

			list := make([]any, len(apps))
			for i, a := range apps {
				list[i] = a
			}

			val, _ := structpb.NewList(list)
			allowed[s.Metadata.Name] = val
		}
	}

	body, err := marshaler.Marshal(&proto.ScenarioList{Scenarios: allowed})
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}
