package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/gorilla/mux"

	"phenix/api/config"
	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// saveBuilderRequest builds the request the editor sends to POST /builder/save.
// mxXmlRequest.simulate posts encodeURIComponent() results through a hidden
// form, so the document and the file name both reach the server percent-encoded
// twice. The format field is appended raw and carries a single layer.
func saveBuilderRequest(form url.Values) *http.Request {
	encoded := url.Values{}
	for key, values := range form {
		for _, value := range values {
			if key == "xml" || key == builderFilenameForm {
				value = url.QueryEscape(value)
			}

			encoded.Add(key, value)
		}
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/builder/save",
		strings.NewReader(encoded.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return req
}

func TestSaveBuilderTopology(t *testing.T) {
	documents := map[string]string{
		"plain":           `<mxGraphModel><root><mxCell/></root></mxGraphModel>`,
		"percent escape":  `<mxGraphModel value="%2B"/>`,
		"plus and spaces": `<mxGraphModel value="a + b" other="c d"/>`,
		"non-ascii":       `<mxGraphModel value="phēnix"/>`,
		"percent literal": `<mxGraphModel value="100%"/>`,
	}

	for name, xml := range documents {
		t.Run(name, func(t *testing.T) {
			res := httptest.NewRecorder()

			SaveBuilderTopology(res, saveBuilderRequest(url.Values{
				"filename": {`diagram "one".xml`},
				"xml":      {xml},
			}))

			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
			}
			if got := res.Body.String(); got != xml {
				t.Fatalf("body = %q, want %q", got, xml)
			}
			if got := res.Header().Get("Content-Type"); got != "application/xml; charset=utf-8" {
				t.Fatalf("Content-Type = %q", got)
			}

			_, params, err := mime.ParseMediaType(res.Header().Get("Content-Disposition"))
			if err != nil {
				t.Fatalf("parsing Content-Disposition: %v", err)
			}
			if got := params["filename"]; got != `diagram "one".xml` {
				t.Fatalf("filename = %q", got)
			}
		})
	}
}

func TestSaveBuilderTopologyDecodesFilename(t *testing.T) {
	names := map[string]string{
		"plain":           "diagram.xml",
		"quoted":          `diagram "one".xml`,
		"spaces and plus": "branch office + lab.xml",
		"non-ascii":       "phēnix.xml",
		"percent literal": "100% done.xml",
		"percent escape":  "diagram%2Bone.xml",
	}

	for label, name := range names {
		t.Run(label, func(t *testing.T) {
			res := httptest.NewRecorder()

			SaveBuilderTopology(res, saveBuilderRequest(url.Values{
				builderFilenameForm: {name},
				"xml":               {"<mxGraphModel/>"},
			}))

			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
			}

			_, params, err := mime.ParseMediaType(res.Header().Get("Content-Disposition"))
			if err != nil {
				t.Fatalf("parsing Content-Disposition: %v", err)
			}
			if got := params[builderFilenameForm]; got != name {
				t.Fatalf("filename = %q, want %q", got, name)
			}
		})
	}
}

func TestSaveBuilderTopologyRejectsUndecodableFilename(t *testing.T) {
	form := url.Values{builderFilenameForm: {"%zz"}, "xml": {"<output/>"}}
	req := httptest.NewRequest(
		http.MethodPost,
		"/builder/save",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()

	SaveBuilderTopology(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}

func TestSaveBuilderTopologySVG(t *testing.T) {
	const svg = `<svg viewBox="0 0 10 10"/>`

	res := httptest.NewRecorder()

	SaveBuilderTopology(res, saveBuilderRequest(url.Values{
		"filename": {"diagram.svg"},
		"format":   {"svg"},
		"xml":      {svg},
	}))

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	if got := res.Body.String(); got != svg {
		t.Fatalf("body = %q, want %q", got, svg)
	}
	if got := res.Header().Get("Content-Type"); got != "image/svg+xml; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestSaveBuilderTopologyRejectsUnsupportedFormat(t *testing.T) {
	res := httptest.NewRecorder()

	SaveBuilderTopology(res, saveBuilderRequest(url.Values{
		"format": {"png"},
		"xml":    {"<output/>"},
	}))

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}

func TestSaveBuilderTopologyRejectsUndecodableXML(t *testing.T) {
	form := url.Values{"xml": {"%zz"}}
	req := httptest.NewRequest(
		http.MethodPost,
		"/builder/save",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()

	SaveBuilderTopology(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}

func TestSaveBuilderTopologyRequiresXML(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/builder/save", nil)
	res := httptest.NewRecorder()

	SaveBuilderTopology(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}

func TestValidateBuilderRequest(t *testing.T) {
	valid := builder{
		Name:     "test",
		Topology: map[string]any{"nodes": []any{}},
		XML:      "<mxGraphModel/>",
	}

	tests := []struct {
		name string
		req  builder
	}{
		{name: "missing name", req: builder{Topology: valid.Topology, XML: valid.XML}},
		{name: "missing topology", req: builder{Name: valid.Name, XML: valid.XML}},
		{name: "missing XML", req: builder{Name: valid.Name, Topology: valid.Topology}},
	}

	if err := validateBuilderRequest(valid); err != nil {
		t.Fatalf("valid request returned error: %v", err)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateBuilderRequest(test.req); err == nil {
				t.Fatal("validateBuilderRequest() returned no error")
			}
		})
	}
}

func TestBuilderValidationErrorMapping(t *testing.T) {
	schema := errors.New("spec.nodes: Invalid type. Expected: array, given: string")

	// types.ValidateConfigSpec joins its sentinel and the schema error with a
	// multi-error, which callers may hand over bare or wrapped once more.
	joined := fmt.Errorf("%w: %w", types.ErrValidationFailed, schema)

	failures := map[string]error{
		"joined sentinel and schema error": joined,
		"joined error wrapped once":        fmt.Errorf("validating config: %w", joined),
		"sentinel without a schema error": fmt.Errorf(
			"%w: missing API group",
			types.ErrValidationFailed,
		),
	}

	mappers := map[string]func(error) *weberror.WebError{
		"builderCreateError": func(err error) *weberror.WebError {
			return builderCreateError(err, "topology")
		},
		"builderTopologyUpdateError": func(err error) *weberror.WebError {
			return builderTopologyUpdateError(err, "test")
		},
	}

	for mapper, mapErr := range mappers {
		for failure, err := range failures {
			t.Run(mapper+"/"+failure, func(t *testing.T) {
				webErr := mapErr(err)

				if webErr == nil {
					t.Fatal("mapper returned nil")
				}
				if webErr.Status != http.StatusBadRequest {
					t.Fatalf("status = %d, want %d", webErr.Status, http.StatusBadRequest)
				}
				if webErr.Message == "" {
					t.Fatal("mapper returned an empty message")
				}
				if webErr.UserMetadata["validation"] == "" {
					t.Fatal("mapper returned no validation metadata")
				}
			})
		}
	}
}

func TestUpdateBuilderTopologyRejectsInvalidSpec(t *testing.T) {
	topo, err := store.NewConfig("topology/test")
	if err != nil {
		t.Fatalf("creating topology config: %v", err)
	}
	topo.Metadata.Annotations = store.Annotations{
		config.BuilderXMLAnnotation: "<mxGraphModel><root/></mxGraphModel>",
	}
	topo.Spec = map[string]any{"nodes": []any{}}

	ctrl := gomock.NewController(t)
	mock := store.NewMockStore(ctrl)
	mock.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		if c.Kind == "Experiment" {
			return store.ErrNotExist
		}

		return copyStoredConfig(topo)(c)
	}).AnyTimes()
	useBuilderTestStore(t, mock)

	body := `{"topology":{"nodes":"not-a-list"},"builderXML":"<mxGraphModel/>"}`
	req := builderRequestWithRole(
		t,
		http.MethodPut,
		"/api/v1/builder/topologies/test",
		body,
		"update",
	)
	req = mux.SetURLVars(req, map[string]string{"name": "test"})

	err = UpdateBuilderTopology(httptest.NewRecorder(), req)

	var webErr *weberror.WebError
	if !errors.As(err, &webErr) {
		t.Fatalf("UpdateBuilderTopology() error = %v, want WebError", err)
	}
	if webErr.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", webErr.Status, http.StatusBadRequest)
	}
	if webErr.UserMetadata["validation"] == "" {
		t.Fatal("UpdateBuilderTopology() returned no validation metadata")
	}
}

func TestMergeBuilderVLANAliases(t *testing.T) {
	tests := map[string]struct {
		existing map[string]int
		topology []string
		request  map[string]int
		want     map[string]int
	}{
		"keeps aliases the request omits": {
			existing: map[string]int{"users": 101, "mgmt": 102},
			topology: []string{"users", "mgmt"},
			request:  map[string]int{"users": 101},
			want:     map[string]int{"users": 101, "mgmt": 102},
		},
		"request wins for shared aliases": {
			existing: map[string]int{"users": 101},
			topology: []string{"users"},
			request:  map[string]int{"users": 201},
			want:     map[string]int{"users": 201},
		},
		"adds aliases the experiment lacks": {
			existing: map[string]int{"users": 101},
			topology: []string{"users", "ot"},
			request:  map[string]int{"ot": 300},
			want:     map[string]int{"users": 101, "ot": 300},
		},
		"empty request keeps the stored IDs": {
			existing: map[string]int{"users": 101},
			topology: []string{"users"},
			request:  map[string]int{},
			want:     map[string]int{"users": 101},
		},
		"no stored aliases": {
			existing: nil,
			topology: []string{"users"},
			request:  map[string]int{"users": 101},
			want:     map[string]int{"users": 101},
		},
		"drops aliases the topology no longer names": {
			existing: map[string]int{"users": 101, "retired": 102},
			topology: []string{"users"},
			request:  map[string]int{"users": 101},
			want:     map[string]int{"users": 101},
		},
		"renaming an alias replaces the old entry": {
			existing: map[string]int{"users": 101},
			topology: []string{"staff"},
			request:  map[string]int{},
			want:     map[string]int{"staff": 0},
		},
		"topology without interfaces keeps no aliases": {
			existing: map[string]int{"users": 101},
			topology: nil,
			request:  map[string]int{},
			want:     map[string]int{},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			vlans := &v1.VLANSpec{AliasesF: maps.Clone(test.existing)}

			mergeBuilderVLANAliases(vlans, topologyWithVLANs(test.topology...), test.request)

			if got := vlans.Aliases(); !maps.Equal(got, test.want) {
				t.Fatalf("aliases = %v, want %v", got, test.want)
			}
		})
	}
}

// topologyWithVLANs builds a topology whose single node carries one interface
// per VLAN alias.
func topologyWithVLANs(aliases ...string) *v1.TopologySpec {
	interfaces := make([]*v1.Interface, 0, len(aliases))
	for _, alias := range aliases {
		interfaces = append(interfaces, &v1.Interface{VLANF: alias})
	}

	return &v1.TopologySpec{
		NodesF: []*v1.Node{{NetworkF: &v1.Network{InterfacesF: interfaces}}},
	}
}

func TestAddScenarioTopology(t *testing.T) {
	scenario := &store.Config{}

	addScenarioTopology(scenario, "alpha")
	addScenarioTopology(scenario, "alpha")
	addScenarioTopology(scenario, "beta")

	if got := scenario.Metadata.Annotations["topology"]; got != "alpha,beta" {
		t.Fatalf("topology annotation = %q, want %q", got, "alpha,beta")
	}
}

func TestGetBuilderScenarioRequiresGetAndUpdate(t *testing.T) {
	tests := []struct {
		name  string
		verbs []string
	}{
		{name: "no config permissions"},
		{name: "get only", verbs: []string{"get"}},
		{name: "update only", verbs: []string{"update"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			role := rbac.Role{Spec: &v1.RoleSpec{
				Name: "experiment-admin",
				Policies: []*v1.PolicySpec{{
					Resources:     []string{"configs"},
					ResourceNames: []string{"Scenario/test"},
					Verbs:         test.verbs,
				}},
			}}

			_, err := getBuilderScenario(role, "user", "test")
			var webErr *weberror.WebError
			if !errors.As(err, &webErr) {
				t.Fatalf("getBuilderScenario() error = %v, want WebError", err)
			}
			if webErr.Status != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", webErr.Status, http.StatusForbidden)
			}
		})
	}
}

func TestRejectRunningBuilderExperiment(t *testing.T) {
	exp := types.NewExperiment(store.ConfigMetadata{Name: "test"})
	exp.Status.SetStartTime("2026-01-01T00:00:00Z")

	err := rejectRunningBuilderExperiment(exp, "test")
	var webErr *weberror.WebError
	if !errors.As(err, &webErr) {
		t.Fatalf("rejectRunningBuilderExperiment() error = %v, want WebError", err)
	}
	if webErr.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", webErr.Status, http.StatusBadRequest)
	}
}

func TestUpdateBuilderTopologyPreservesOtherAnnotations(t *testing.T) {
	const newXML = "<mxGraphModel><root><mxCell/></root></mxGraphModel>"

	topo, err := store.NewConfig("topology/test")
	if err != nil {
		t.Fatalf("creating topology config: %v", err)
	}
	topo.Metadata.Annotations = store.Annotations{
		config.BuilderXMLAnnotation: "<mxGraphModel><root/></mxGraphModel>",
		"topology":                  "branch-office",
	}
	topo.Spec = map[string]any{"nodes": []any{}}

	ctrl := gomock.NewController(t)
	mock := store.NewMockStore(ctrl)
	mock.EXPECT().Get(gomock.Any()).DoAndReturn(copyStoredConfig(topo)).AnyTimes()
	mock.EXPECT().Update(gomock.Any()).DoAndReturn(func(updated *store.Config) error {
		if got := updated.Metadata.Annotations[config.BuilderXMLAnnotation]; got != newXML {
			t.Fatalf("builder XML = %q, want %q", got, newXML)
		}
		if got := updated.Metadata.Annotations["topology"]; got != "branch-office" {
			t.Fatalf("unrelated annotation = %q, want %q", got, "branch-office")
		}

		return nil
	})
	useBuilderTestStore(t, mock)

	if _, err := updateBuilderTopology(builder{
		Name:     topo.Metadata.Name,
		XML:      newXML,
		Topology: map[string]any{"nodes": []any{}},
	}); err != nil {
		t.Fatalf("updateBuilderTopology() returned error: %v", err)
	}
}

func TestUpdateBuilderTopologyPropagatesStoreError(t *testing.T) {
	topo, err := store.NewConfig("topology/test")
	if err != nil {
		t.Fatalf("creating topology config: %v", err)
	}
	topo.Metadata.Annotations = store.Annotations{
		config.BuilderXMLAnnotation: "<mxGraphModel><root/></mxGraphModel>",
	}
	topo.Spec = map[string]any{"nodes": []any{}}

	updateErr := errors.New("store update failed")

	ctrl := gomock.NewController(t)
	mock := store.NewMockStore(ctrl)
	mock.EXPECT().Get(gomock.Any()).DoAndReturn(copyStoredConfig(topo)).AnyTimes()
	mock.EXPECT().Update(gomock.Any()).Return(updateErr)
	useBuilderTestStore(t, mock)

	if _, err := updateBuilderTopology(builder{
		Name:     topo.Metadata.Name,
		XML:      "<mxGraphModel><root><mxCell/></root></mxGraphModel>",
		Topology: map[string]any{"nodes": []any{}},
	}); !errors.Is(err, updateErr) {
		t.Fatalf("updateBuilderTopology() error = %v, want %v", err, updateErr)
	}
}

func copyStoredConfig(src *store.Config) func(*store.Config) error {
	return func(dst *store.Config) error {
		*dst = *src
		dst.Metadata.Annotations = maps.Clone(src.Metadata.Annotations)

		return nil
	}
}

func useBuilderTestStore(t *testing.T, mock store.Store) {
	t.Helper()

	previous := store.DefaultStore
	store.DefaultStore = mock //nolint:reassign // install test double
	t.Cleanup(func() {
		store.DefaultStore = previous //nolint:reassign // restore test double
	})
}

func builderRequestWithRole(t *testing.T, method, target, body string, verbs ...string) *http.Request {
	t.Helper()

	role := rbac.Role{Spec: &v1.RoleSpec{
		Name: "builder-test",
		Policies: []*v1.PolicySpec{{
			Resources:     []string{"configs"},
			ResourceNames: []string{"*", "*/*"},
			Verbs:         verbs,
		}},
	}}

	req := httptest.NewRequest(method, target, strings.NewReader(body))
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, "user")
	ctx = context.WithValue(ctx, middleware.ContextKeyRole, role)

	return req.WithContext(ctx)
}

func TestCreateBuilderTopologyRequiresCreatePermission(t *testing.T) {
	req := builderRequestWithRole(t, http.MethodPost, "/api/v1/builder/topologies", "{}", "get")

	err := CreateBuilderTopology(httptest.NewRecorder(), req)

	var webErr *weberror.WebError
	if !errors.As(err, &webErr) {
		t.Fatalf("CreateBuilderTopology() error = %v, want WebError", err)
	}
	if webErr.Status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", webErr.Status, http.StatusForbidden)
	}
}

func TestCreateBuilderTopologyRejectsIncompleteRequest(t *testing.T) {
	// A permitted caller that omits the diagram must be refused before the
	// store is touched, so no store double is installed here on purpose.
	body := `{"name":"test","topology":{"nodes":[]}}`
	req := builderRequestWithRole(t, http.MethodPost, "/api/v1/builder/topologies", body, "create")

	if err := CreateBuilderTopology(httptest.NewRecorder(), req); err == nil {
		t.Fatal("CreateBuilderTopology() returned no error for a request missing builderXML")
	}
}

func TestCreateBuilderTopologyStoresDiagram(t *testing.T) {
	const xml = "<mxGraphModel><root/></mxGraphModel>"

	body := `{"name":"test","topology":{"nodes":[]},"builderXML":"` + xml + `"}`
	req := builderRequestWithRole(t, http.MethodPost, "/api/v1/builder/topologies", body, "create")

	ctrl := gomock.NewController(t)
	mock := store.NewMockStore(ctrl)
	mock.EXPECT().Get(gomock.Any()).Return(store.ErrNotExist).AnyTimes()
	mock.EXPECT().Create(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		if c.Kind != "Topology" {
			t.Fatalf("kind = %q, want %q", c.Kind, "Topology")
		}
		if got := c.Metadata.Annotations[config.BuilderXMLAnnotation]; got != xml {
			t.Fatalf("builder XML = %q, want %q", got, xml)
		}

		return nil
	})
	mock.EXPECT().List(gomock.Any()).Return(nil, nil).AnyTimes()
	useBuilderTestStore(t, mock)

	rec := httptest.NewRecorder()
	if err := CreateBuilderTopology(rec, req); err != nil {
		t.Fatalf("CreateBuilderTopology() returned error: %v", err)
	}

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got, want := rec.Header().Get("Location"), "/api/v1/builder/topologies/test"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

func TestCreateBuilderTopologyRejectsRunningExperiment(t *testing.T) {
	const xml = "<mxGraphModel><root/></mxGraphModel>"

	body := `{"name":"test","topology":{"nodes":[]},"builderXML":"` + xml + `"}`
	req := builderRequestWithRole(t, http.MethodPost, "/api/v1/builder/topologies", body, "create")

	ctrl := gomock.NewController(t)
	mock := store.NewMockStore(ctrl)
	// No Create expectation: the guard has to refuse before the store is written.
	mock.EXPECT().Get(gomock.Any()).DoAndReturn(runningExperimentFor("test")).AnyTimes()
	useBuilderTestStore(t, mock)

	err := CreateBuilderTopology(httptest.NewRecorder(), req)

	var webErr *weberror.WebError
	if !errors.As(err, &webErr) {
		t.Fatalf("CreateBuilderTopology() error = %v, want WebError", err)
	}
	if webErr.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", webErr.Status, http.StatusBadRequest)
	}
}

// runningExperimentFor answers experiment reads with a running experiment built
// from the named topology, and reports every other config as missing.
func runningExperimentFor(topology string) func(*store.Config) error {
	return func(c *store.Config) error {
		if c.Kind != "Experiment" {
			return store.ErrNotExist
		}

		c.Metadata.Annotations = store.Annotations{"topology": topology}
		c.Spec = map[string]any{}
		c.Status = map[string]any{"startTime": "2026-01-01T00:00:00Z"}

		return nil
	}
}

func TestRejectRunningExperimentForTopology(t *testing.T) {
	running := func(topology string) *types.Experiment {
		exp := types.NewExperiment(store.ConfigMetadata{
			Name:        "test",
			Annotations: store.Annotations{"topology": topology},
		})
		exp.Status.SetStartTime("2026-01-01T00:00:00Z")

		return exp
	}

	stopped := types.NewExperiment(store.ConfigMetadata{
		Name:        "test",
		Annotations: store.Annotations{"topology": "test"},
	})

	tests := map[string]struct {
		exp      *types.Experiment
		rejected bool
	}{
		"running, same topology":  {exp: running("test"), rejected: true},
		"running, other topology": {exp: running("elsewhere"), rejected: false},
		"stopped, same topology":  {exp: stopped, rejected: false},
		"no topology annotation": {
			exp:      types.NewExperiment(store.ConfigMetadata{Name: "test"}),
			rejected: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			webErr := rejectRunningExperimentForTopology(test.exp, "test")

			if !test.rejected {
				if webErr != nil {
					t.Fatalf("rejectRunningExperimentForTopology() = %v, want nil", webErr)
				}

				return
			}

			if webErr == nil {
				t.Fatal("rejectRunningExperimentForTopology() returned nil, want error")
			}
			if webErr.Status != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", webErr.Status, http.StatusBadRequest)
			}
		})
	}
}

func TestLockBuilderTopologyBlocksExperimentUpdate(t *testing.T) {
	if err := cache.LockBuilderTopology("test"); err != nil {
		t.Fatalf("LockBuilderTopology() returned error: %v", err)
	}
	t.Cleanup(func() { cache.UnlockBuilderTopology("test") })

	// Builder names a topology and its experiment identically, so the two must
	// contend for one lock.
	if err := cache.LockExperimentForUpdate("test"); err == nil {
		t.Fatal("LockExperimentForUpdate() succeeded while the topology was locked")
	}
}

func TestBuilderTopologyLocationUsesBasePath(t *testing.T) {
	tests := map[string]struct {
		basePath string
		name     string
		want     string
	}{
		"unset base path":   {basePath: "", name: "test", want: "/api/v1/builder/topologies/test"},
		"root base path":    {basePath: "/", name: "test", want: "/api/v1/builder/topologies/test"},
		"nested base path":  {basePath: "/phenix/", name: "test", want: "/phenix/api/v1/builder/topologies/test"},
		"unnormalized base": {basePath: "phenix", name: "test", want: "/phenix/api/v1/builder/topologies/test"},
		"name needing escape": {
			basePath: "/",
			name:     "branch office",
			want:     "/api/v1/builder/topologies/branch%20office",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			previous := o.basePath
			o.basePath = test.basePath
			t.Cleanup(func() { o.basePath = previous })

			if got := builderTopologyLocation(test.name); got != test.want {
				t.Fatalf("builderTopologyLocation() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCreateBuilderTopologyChecksNamedResource(t *testing.T) {
	// A role allowed to create only "other" must not be able to create "test",
	// and must be refused before the payload is validated.
	role := rbac.Role{Spec: &v1.RoleSpec{
		Name: "scoped-creator",
		Policies: []*v1.PolicySpec{{
			Resources:     []string{"configs"},
			ResourceNames: []string{"Topology/other"},
			Verbs:         []string{"create"},
		}},
	}}

	body := `{"name":"test","topology":{"nodes":[]},"builderXML":"<mxGraphModel/>"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/builder/topologies", strings.NewReader(body))
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, "user")
	req = req.WithContext(context.WithValue(ctx, middleware.ContextKeyRole, role))

	err := CreateBuilderTopology(httptest.NewRecorder(), req)

	var webErr *weberror.WebError
	if !errors.As(err, &webErr) {
		t.Fatalf("CreateBuilderTopology() error = %v, want WebError", err)
	}
	if webErr.Status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", webErr.Status, http.StatusForbidden)
	}
}

// builderTopologyConfig returns a stored topology, optionally carrying a
// Builder diagram.
func builderTopologyConfig(t *testing.T, name, xml string) store.Config {
	t.Helper()

	topo, err := store.NewConfig(store.ConfigFullName("topology", name))
	if err != nil {
		t.Fatalf("creating topology config: %v", err)
	}

	if xml != "" {
		topo.Metadata.Annotations = store.Annotations{config.BuilderXMLAnnotation: xml}
	}

	topo.Spec = map[string]any{"nodes": []any{}}

	return *topo
}

func TestGetBuilderTopologiesRequiresList(t *testing.T) {
	req := builderRequestWithRole(t, http.MethodGet, "/api/v1/builder/topologies", "", "get")

	err := GetBuilderTopologies(httptest.NewRecorder(), req)

	var webErr *weberror.WebError
	if !errors.As(err, &webErr) {
		t.Fatalf("GetBuilderTopologies() error = %v, want WebError", err)
	}
	if webErr.Status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", webErr.Status, http.StatusForbidden)
	}
}

func TestGetBuilderTopologiesListsOnlyDiagrams(t *testing.T) {
	// The handler gates on configs/list but filters each entry with
	// configs/get, so only topologies carrying a diagram may be returned.
	stored := store.Configs{
		builderTopologyConfig(t, "with-diagram", "<mxGraphModel/>"),
		builderTopologyConfig(t, "no-diagram", ""),
	}

	ctrl := gomock.NewController(t)
	mock := store.NewMockStore(ctrl)
	mock.EXPECT().List(gomock.Any()).Return(stored, nil).AnyTimes()
	useBuilderTestStore(t, mock)

	req := builderRequestWithRole(
		t, http.MethodGet, "/api/v1/builder/topologies", "", "list", "get",
	)
	res := httptest.NewRecorder()

	if err := GetBuilderTopologies(res, req); err != nil {
		t.Fatalf("GetBuilderTopologies() returned error: %v", err)
	}

	var body struct {
		Topologies []string `json:"topologies"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshaling response: %v", err)
	}

	if len(body.Topologies) != 1 || body.Topologies[0] != "with-diagram" {
		t.Fatalf("topologies = %v, want [with-diagram]", body.Topologies)
	}
}

func TestGetBuilderTopologyReturnsDiagram(t *testing.T) {
	const xml = "<mxGraphModel><root/></mxGraphModel>"

	topo := builderTopologyConfig(t, "test", xml)

	ctrl := gomock.NewController(t)
	mock := store.NewMockStore(ctrl)
	mock.EXPECT().Get(gomock.Any()).DoAndReturn(copyStoredConfig(&topo)).AnyTimes()
	useBuilderTestStore(t, mock)

	req := builderRequestWithRole(t, http.MethodGet, "/api/v1/builder/topologies/test", "", "get")
	req = mux.SetURLVars(req, map[string]string{"name": "test"})
	res := httptest.NewRecorder()

	if err := GetBuilderTopology(res, req); err != nil {
		t.Fatalf("GetBuilderTopology() returned error: %v", err)
	}

	if got := res.Body.String(); got != xml {
		t.Fatalf("body = %q, want %q", got, xml)
	}
	if got := res.Header().Get("Content-Type"); got != "application/xml" {
		t.Fatalf("Content-Type = %q, want %q", got, "application/xml")
	}
}

func TestGetBuilderTopologyErrors(t *testing.T) {
	withoutDiagram := builderTopologyConfig(t, "test", "")

	tests := map[string]struct {
		verbs      []string
		getErr     error
		stored     *store.Config
		wantStatus int
	}{
		"not allowed": {
			verbs:      []string{"list"},
			wantStatus: http.StatusForbidden,
		},
		"topology missing": {
			verbs:      []string{"get"},
			getErr:     store.ErrNotExist,
			wantStatus: http.StatusNotFound,
		},
		"store failure": {
			verbs:      []string{"get"},
			getErr:     errors.New("boom"),
			wantStatus: http.StatusInternalServerError,
		},
		"topology carries no diagram": {
			verbs:  []string{"get"},
			stored: &withoutDiagram,
			// weberror defaults to 400 when no status is set
			wantStatus: http.StatusBadRequest,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mock := store.NewMockStore(ctrl)
			switch {
			case test.getErr != nil:
				mock.EXPECT().Get(gomock.Any()).Return(test.getErr).AnyTimes()
			case test.stored != nil:
				mock.EXPECT().Get(gomock.Any()).
					DoAndReturn(copyStoredConfig(test.stored)).AnyTimes()
			}
			useBuilderTestStore(t, mock)

			req := builderRequestWithRole(
				t, http.MethodGet, "/api/v1/builder/topologies/test", "", test.verbs...,
			)
			req = mux.SetURLVars(req, map[string]string{"name": "test"})

			err := GetBuilderTopology(httptest.NewRecorder(), req)

			var webErr *weberror.WebError
			if !errors.As(err, &webErr) {
				t.Fatalf("GetBuilderTopology() error = %v, want WebError", err)
			}
			if webErr.Status != test.wantStatus {
				t.Fatalf("status = %d, want %d", webErr.Status, test.wantStatus)
			}
		})
	}
}

func TestUpdateBuilderTopologyRequiresUpdate(t *testing.T) {
	body := `{"topology":{"nodes":[]},"builderXML":"<mxGraphModel/>"}`
	req := builderRequestWithRole(
		t, http.MethodPut, "/api/v1/builder/topologies/test", body, "get",
	)
	req = mux.SetURLVars(req, map[string]string{"name": "test"})

	err := UpdateBuilderTopology(httptest.NewRecorder(), req)

	var webErr *weberror.WebError
	if !errors.As(err, &webErr) {
		t.Fatalf("UpdateBuilderTopology() error = %v, want WebError", err)
	}
	if webErr.Status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", webErr.Status, http.StatusForbidden)
	}
}

func TestUpdateBuilderTopologyTakesNameFromURL(t *testing.T) {
	const newXML = "<mxGraphModel><root><mxCell/></root></mxGraphModel>"

	// The payload names a different topology; the URL must win.
	body := `{"name":"ignored","topology":{"nodes":[]},"builderXML":"` + newXML + `"}`

	topo := builderTopologyConfig(t, "test", "<mxGraphModel/>")

	ctrl := gomock.NewController(t)
	mock := store.NewMockStore(ctrl)
	mock.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		if c.Kind == "Experiment" {
			return store.ErrNotExist
		}

		return copyStoredConfig(&topo)(c)
	}).AnyTimes()
	mock.EXPECT().Update(gomock.Any()).DoAndReturn(func(updated *store.Config) error {
		if updated.Metadata.Name != "test" {
			t.Fatalf("updated topology = %q, want %q", updated.Metadata.Name, "test")
		}
		if got := updated.Metadata.Annotations[config.BuilderXMLAnnotation]; got != newXML {
			t.Fatalf("builder XML = %q, want %q", got, newXML)
		}

		return nil
	})
	useBuilderTestStore(t, mock)

	req := builderRequestWithRole(
		t, http.MethodPut, "/api/v1/builder/topologies/test", body, "get", "update", "list",
	)
	req = mux.SetURLVars(req, map[string]string{"name": "test"})
	res := httptest.NewRecorder()

	if err := UpdateBuilderTopology(res, req); err != nil {
		t.Fatalf("UpdateBuilderTopology() returned error: %v", err)
	}

	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
	}
}
