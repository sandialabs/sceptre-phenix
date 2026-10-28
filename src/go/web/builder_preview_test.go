package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"gopkg.in/yaml.v2"

	bapi "phenix/api/builder"
	"phenix/api/disk"
	"phenix/store"
	bdoc "phenix/types/builder"
	v1 "phenix/types/version/v1"
	"phenix/web/rbac"
)

// previewTargets are the configs a role of these tests may change: every
// Topology config, which a bare "*" does not name.
func previewTargets() []string {
	return []string{"*", "Topology/*"}
}

// previewBuilderDraft posts a dry run of a publication of the draft as role
// (nil for the full role), and returns the answer and, on 200, its preview.
func previewBuilderDraft(
	t *testing.T,
	harness *builderHarness,
	draft builderDraftResponse,
	role *rbac.Role,
	body string,
) (*httptest.ResponseRecorder, builderPublishPreview) {
	t.Helper()

	recorder := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.ID + "/publish",
		body:   body, user: builderAlice, role: role,
	})

	var preview builderPublishPreview

	if recorder.Code == http.StatusOK {
		decodeBuilderAnswer(t, recorder, &preview)
	}

	return recorder, preview
}

// previewDisks lists the disk images named as the server's, counting the
// listings in calls when it is not nil.
func previewDisks(calls *int, names ...string) builderOption {
	return withBuilderDisks(func() ([]disk.Details, error) {
		if calls != nil {
			*calls++
		}

		disks := make([]disk.Details, 0, len(names))

		for _, name := range names {
			disks = append(disks, disk.Details{Name: name, FullPath: "/phenix/images/" + name})
		}

		return disks, nil
	})
}

// builderDryRunState is what a dry run must leave as it was.
//
//nolint:unused // cmp.Diff reads the fields by reflection
type builderDryRunState struct {
	configs                                                any
	writes, experiments, broadcasts, documents, configured int
	etag                                                   string
	publication                                            *bapi.PublicationState
}

// dryRunState returns the configs, the write counts and the draft's record.
func (h *builderHarness) dryRunState(draftID string) builderDryRunState {
	h.t.Helper()

	meta := mustDraftMeta(h.t, h, draftID)

	return builderDryRunState{
		configs: builderJSONValue(h.t, h.configs), writes: h.configWrites, experiments: h.experimentWrites,
		broadcasts: h.broadcasts, documents: h.store.Count(bapi.NamespacePublished), configured: len(h.reconfigured),
		etag: meta.ETag(), publication: meta.Publication,
	}
}

// dryRunChanges returns how the harness and the draft differ from before, or
// "" when they are as they were.
func (h *builderHarness) dryRunChanges(draftID string, before builderDryRunState) string {
	h.t.Helper()

	return cmp.Diff(before, h.dryRunState(draftID), cmp.AllowUnexported(builderDryRunState{}))
}

// TestBuilderPublishDryRunWritesNothing asserts a dry run of a topology and
// experiment says what it would change, and writes nothing.
func TestBuilderPublishDryRunWritesNothing(t *testing.T) {
	configs := slices.Concat(includedTopologyFixture(t, "shared"), []store.Config{namedScenario(t, "water-ops", "other")})
	harness := newBuilderHarnessWith(t, []builderOption{previewDisks(nil, "miniccc.qc2")}, configs...)

	document := generateBuilderDocument(t, harness, "Topology/root")
	document.Scenarios = []string{"water-ops"}

	alias := 101
	aliases := make([]bapi.VLANAliasChange, 0, len(document.Networks))

	for i := range document.Networks {
		document.Networks[i].Alias = &alias
		aliases = append(aliases, bapi.VLANAliasChange{
			Name: document.Networks[i].Name, From: nil, To: &alias, Change: bapi.ItemAdded,
		})
	}

	slices.SortFunc(aliases, func(a, b bapi.VLANAliasChange) int { return strings.Compare(a.Name, b.Name) })

	draft := createBuilderPublishDraft(t, harness, document, "Topology/root")
	before := harness.dryRunState(draft.ID)

	recorder, preview := previewBuilderDraft(t, harness, draft, nil,
		`{"mode":"topology-experiment","topology":{"name":"root","action":"update"},`+
			`"scenario":{"name":"water-ops"},"experiment":{"name":"lab","action":"create"},"dryRun":true}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("dry run of root and lab: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	onServer := true
	want := builderPublishPreview{Status: builderPublishPreviewStatus, Errors: []bdoc.Issue{}, Changes: &bapi.PublishChanges{
		Topology:  bapi.ConfigChange{Name: "root", Action: bapi.ChangeUpdate},
		Includes:  []bapi.IncludeChange{{Name: "shared", Change: bapi.ItemKept}},
		Scenarios: []bapi.ScenarioAnnotation{{Name: "water-ops", Change: bapi.ScenarioAnnotate}},
		Images: []bapi.ImageChange{
			{Name: "miniccc.qc2", Change: bapi.ItemKept, Devices: []string{"web"}, OnServer: &onServer},
		},
		Experiment:  &bapi.ConfigChange{Name: "lab", Action: bapi.ChangeCreate},
		VLANAliases: aliases,
	}}

	if diff := cmp.Diff(want, preview, cmpopts.IgnoreFields(builderPublishPreview{}, "Warnings")); diff != "" {
		t.Errorf("dry run of root and lab mismatch (-want +got):\n%s", diff)
	}

	if diff := harness.dryRunChanges(draft.ID, before); diff != "" {
		t.Errorf("dry run of root and lab changed the state (-before +after):\n%s", diff)
	}
}

// TestBuilderPublishDryRunListsRefusals asserts a dry run lists what a
// publication would refuse as errors, while permission and request refusals keep their status.
func TestBuilderPublishDryRunListsRefusals(t *testing.T) {
	noVLAN := bdoc.NewDocument("no-vlan")
	noVLAN.Nodes = append(noVLAN.Nodes, bdoc.Node{
		ID: bdoc.DeviceNodeID("host"), Kind: bdoc.NodeKindDevice, Label: "host",
		Device: &bdoc.Device{
			Hostname: "host",
			Spec: map[string]any{
				"type":    "VirtualMachine",
				"general": map[string]any{"hostname": "host"},
				"network": map[string]any{"interfaces": []any{map[string]any{"name": "eth0"}}},
			},
			Interfaces: []bdoc.InterfaceHandle{},
		},
	})

	missing := bdoc.NewDocument("missing")
	missing.Scenarios = []string{"gone"}

	for name, test := range map[string]struct {
		document *bdoc.Document
		body     string
		code     bdoc.Code
		message  string
		nodeID   string
	}{
		"an existing topology to create": {
			document: bdoc.NewDocument("fresh"),
			body:     `{"mode":"topology","topology":{"name":"taken","action":"create"},"dryRun":true}`,
			code:     bdoc.CodePublishTopologyExists, message: "config taken already exists",
		},
		"a scenario that does not exist": {
			document: missing,
			body:     `{"mode":"topology","topology":{"name":"missing","action":"create"},"dryRun":true}`,
			code:     bdoc.CodePublishScenarioMissing, message: "scenario gone does not exist",
		},
		"a name that is not a config name": {
			document: bdoc.NewDocument("named"),
			body:     `{"mode":"topology","topology":{"name":"bad name","action":"create"},"dryRun":true}`,
			code:     bdoc.CodePublishTargetInvalid, message: `target "bad name" is not a valid config name`,
		},
		"an interface without a VLAN": {
			document: noVLAN,
			body:     `{"mode":"topology","topology":{"name":"no-vlan","action":"create"},"dryRun":true}`,
			code:     bdoc.CodeInterfaceVLANMissing, message: `interface "eth0" of device "host" has no VLAN`,
			nodeID: bdoc.DeviceNodeID("host"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			harness := newBuilderHarnessWith(t, []builderOption{previewDisks(nil)},
				builderConfig(t, builderKindTopology, "taken"))
			draft := createBuilderPublishDraft(t, harness, test.document)

			recorder, preview := previewBuilderDraft(t, harness, draft, nil, test.body)
			if recorder.Code != http.StatusOK {
				t.Fatalf("dry run %s: status = %d, want %d: %s", test.body, recorder.Code, http.StatusOK, recorder.Body)
			}

			if preview.Changes != nil {
				t.Errorf("dry run %s: changes = %+v, want none", test.body, preview.Changes)
			}

			if len(preview.Errors) == 0 {
				t.Fatalf("dry run %s: errors = none, want the refusal", test.body)
			}

			want := bdoc.Issue{Code: test.code, Severity: bdoc.SeverityError, NodeID: test.nodeID}
			if diff := cmp.Diff(want, preview.Errors[0],
				cmpopts.IgnoreFields(bdoc.Issue{}, "Message", "Path", "EdgeID", "NetworkID", "Field")); diff != "" {
				t.Errorf("dry run %s: first error mismatch (-want +got):\n%s", test.body, diff)
			}

			if got := preview.Errors[0].Message; !strings.Contains(got, test.message) {
				t.Errorf("dry run %s: first error message = %q, want it to say %q", test.body, got, test.message)
			}

			if writes, documents := harness.configWrites, harness.store.Count(bapi.NamespacePublished); writes != 0 || documents != 0 {
				t.Errorf("dry run %s wrote %d configs and %d published documents, want none", test.body, writes, documents)
			}
		})
	}

	harness := newBuilderHarnessWith(t, []builderOption{previewDisks(nil)})
	draft := harness.createDraft(builderAlice, "kept")
	noCreate := builderRole(builderPolicy([]string{"configs"}, previewTargets(), []string{"list", "get", "update"}))
	body := `{"mode":"topology","topology":{"name":"kept","action":"create"},"dryRun":true}`

	for name, test := range map[string]struct {
		draft  builderDraftResponse
		role   *rbac.Role
		body   string
		status int
	}{
		"a target the role may not create": {draft: draft, role: noCreate, body: body, status: http.StatusForbidden},
		"a draft that does not exist": {
			draft: builderDraftResponse{ID: "id-missing", ETag: draft.ETag}, role: nil, body: body, status: http.StatusNotFound,
		},
		"a key a publish request has not": {
			draft: draft, role: nil, status: http.StatusBadRequest,
			body: `{"mode":"topology","topology":{"name":"kept","action":"create"},"dryRun":true,"force":true}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			recorder, _ := previewBuilderDraft(t, harness, test.draft, test.role, test.body)
			if recorder.Code != test.status {
				t.Errorf("dry run %s: status = %d, want %d: %s", test.body, recorder.Code, test.status, recorder.Body)
			}
		})
	}
}

// TestBuilderPublishDryRunAfterPublishing asserts a dry run of what the
// topology already holds previews it as unchanged.
func TestBuilderPublishDryRunAfterPublishing(t *testing.T) {
	harness := newBuilderHarnessWith(t, []builderOption{previewDisks(nil)})
	draft := harness.createDraft(builderAlice, "again")

	_, before := previewBuilderDraft(t, harness, draft, nil,
		`{"mode":"topology","topology":{"name":"again","action":"create"},"dryRun":true}`)

	switch {
	case before.Changes == nil:
		t.Errorf("dry run before publishing = %+v, want changes", before)
	case before.Changes.Topology.Action != bapi.ChangeCreate:
		t.Errorf("dry run before publishing: topology action = %q, want %q", before.Changes.Topology.Action, bapi.ChangeCreate)
	}

	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"again","action":"create"}}`, http.StatusOK)

	recorder, after := previewBuilderDraft(t, harness, draft, nil,
		`{"mode":"topology","topology":{"name":"again","action":"update"},"dryRun":true}`)

	switch {
	case after.Changes == nil:
		t.Errorf("dry run after publishing = %d %s, want changes", recorder.Code, recorder.Body)
	case after.Changes.Topology.Action != bapi.ChangeUnchanged:
		t.Errorf("dry run after publishing: topology action = %q, want %q", after.Changes.Topology.Action, bapi.ChangeUnchanged)
	}
}

// The disk images of the image tests: one kept, one added and one no longer used.
const (
	previewKeptImage    = "keep.qc2"
	previewAddedImage   = "new.qc2"
	previewRemovedImage = "gone.qc2"
)

// imagedNode is [includeNode] whose drives use the images given.
func imagedNode(hostname string, images ...string) map[string]any {
	drives := make([]any, 0, len(images))

	for _, image := range images {
		drives = append(drives, map[string]any{"image": image})
	}

	node := includeNode(hostname)
	node["hardware"] = map[string]any{"os_type": "linux", "drives": drives}

	return node
}

// The devices of the image tests.
const (
	previewHostA = "host-a"
	previewHostB = "host-b"
)

// imagedPreview returns a harness with topology imaged (host-a on keep and gone,
// host-b on keep) and a draft of it where host-a uses keep and new.
func imagedPreview(t *testing.T, disks builderOption) (*builderHarness, builderDraftResponse) {
	t.Helper()

	topology := builderConfig(t, builderKindTopology, "imaged")
	topology.Spec = map[string]any{"nodes": []any{
		imagedNode(previewHostA, previewKeptImage, previewRemovedImage), imagedNode(previewHostB, previewKeptImage),
	}}

	harness := newBuilderHarnessWith(t, []builderOption{disks}, topology)
	document := generateBuilderDocument(t, harness, "Topology/imaged")

	device := document.FindDevice(previewHostA)
	if device == nil {
		t.Fatalf("device %s is not in the document: %s", previewHostA, builderJSON(t, document))
	}

	device.Device.Spec["hardware"] = imagedNode(previewHostA, previewKeptImage, previewAddedImage)["hardware"]

	return harness, createBuilderPublishDraft(t, harness, document, "Topology/imaged")
}

// previewRole may use the Builder, change every config, and list the disk
// images named (none without a name).
func previewRole(disks ...string) *rbac.Role {
	policies := []*v1.PolicySpec{builderPolicy(
		[]string{"configs", "topologies", "experiments", "scenarios"},
		[]string{"*", "*/*"},
		[]string{"list", "get", "create", "update", "delete"},
	)}

	if len(disks) > 0 {
		policies = append(policies, builderPolicy([]string{"disks"}, disks, []string{"list"}))
	}

	role := builderRole(policies...)

	return role
}

// TestBuilderPublishDryRunComparesDiskImages asserts each image is added,
// removed or kept, and on the server only when the caller may list it.
func TestBuilderPublishDryRunComparesDiskImages(t *testing.T) {
	const body = `{"mode":"topology","topology":{"name":"imaged","action":"update"},"dryRun":true}`

	has, lacks := true, false
	failing := withBuilderDisks(func() ([]disk.Details, error) {
		return nil, errors.New("minimega is not running")
	})

	for name, test := range map[string]struct {
		disks builderOption
		role  *rbac.Role
		// onServer is what each image reads, by name.
		onServer map[string]*bool
		// listed is whether the server's images are listed at all.
		listed bool
	}{
		"images the caller may list": {
			disks: previewDisks(nil, previewKeptImage), role: nil, listed: true,
			onServer: map[string]*bool{previewKeptImage: &has, previewAddedImage: &lacks, previewRemovedImage: &lacks},
		},
		"a listing that fails": {
			disks: failing, role: nil, listed: true,
			onServer: map[string]*bool{previewKeptImage: nil, previewAddedImage: nil, previewRemovedImage: nil},
		},
		"a role that may not list disks": {
			disks: previewDisks(nil, previewKeptImage), role: previewRole(), listed: false,
			onServer: map[string]*bool{previewKeptImage: nil, previewAddedImage: nil, previewRemovedImage: nil},
		},
		// new.qc2 is on the server but hidden. gone.qc2 is listable but absent.
		"a role that may list some images by name": {
			disks: previewDisks(nil, previewKeptImage, previewAddedImage), listed: true,
			role:     previewRole(previewKeptImage, previewRemovedImage),
			onServer: map[string]*bool{previewKeptImage: &has, previewAddedImage: nil, previewRemovedImage: &lacks},
		},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			counted := func(api *builderAPI) {
				list := api.disks.source
				api.disks.source = func() ([]disk.Details, error) {
					calls++

					return list()
				}
			}

			harness, draft := imagedPreview(t, func(api *builderAPI) {
				test.disks(api)
				counted(api)
			})

			recorder, preview := previewBuilderDraft(t, harness, draft, test.role, body)
			if recorder.Code != http.StatusOK || preview.Changes == nil {
				t.Fatalf("dry run of imaged = %d %s, want %d with changes", recorder.Code, recorder.Body, http.StatusOK)
			}

			if len(preview.Errors) != 0 {
				t.Errorf("dry run of imaged: errors = %+v, want none", preview.Errors)
			}

			want := []bapi.ImageChange{
				{
					Name: previewRemovedImage, Change: bapi.ItemRemoved, Devices: []string{previewHostA},
					OnServer: test.onServer[previewRemovedImage],
				},
				{
					Name: previewKeptImage, Change: bapi.ItemKept, Devices: []string{previewHostA, previewHostB},
					OnServer: test.onServer[previewKeptImage],
				},
				{
					Name: previewAddedImage, Change: bapi.ItemAdded, Devices: []string{previewHostA},
					OnServer: test.onServer[previewAddedImage],
				},
			}

			if diff := cmp.Diff(want, preview.Changes.Images); diff != "" {
				t.Errorf("dry run of imaged: images mismatch (-want +got):\n%s", diff)
			}

			if listed := calls > 0; listed != test.listed {
				t.Errorf("dry run of imaged: disk listings = %d, want listed %t", calls, test.listed)
			}
		})
	}
}

// TestBuilderPublishDryRunListsDisksOnlyForImages asserts the server's disk
// images are listed only for a topology that names a drive image.
func TestBuilderPublishDryRunListsDisksOnlyForImages(t *testing.T) {
	calls := 0
	harness, imaged := imagedPreview(t, previewDisks(&calls, previewKeptImage))
	plain := harness.createDraft(builderAlice, "plain")

	recorder, preview := previewBuilderDraft(t, harness, plain, nil,
		`{"mode":"topology","topology":{"name":"plain","action":"create"},"dryRun":true}`)
	if recorder.Code != http.StatusOK || preview.Changes == nil || len(preview.Changes.Images) != 0 {
		t.Errorf("dry run of plain = %d %s, want %d with changes and no images", recorder.Code, recorder.Body, http.StatusOK)
	}

	if calls != 0 {
		t.Errorf("dry run of plain: disk listings = %d, want 0 for a topology that names no image", calls)
	}

	recorder, preview = previewBuilderDraft(t, harness, imaged, nil,
		`{"mode":"topology","topology":{"name":"imaged","action":"update"},"dryRun":true}`)
	if recorder.Code != http.StatusOK || preview.Changes == nil || len(preview.Changes.Images) == 0 {
		t.Errorf("dry run of imaged = %d %s, want %d with images", recorder.Code, recorder.Body, http.StatusOK)
	}

	if calls != 1 {
		t.Errorf("dry runs of plain and imaged: disk listings = %d, want 1", calls)
	}
}

// TestBuilderPublishDryRunReusesTheDiskListing asserts dry runs within
// [builderDiskListTTL] share one disk listing, each filtered by the caller's role.
func TestBuilderPublishDryRunReusesTheDiskListing(t *testing.T) {
	const body = `{"mode":"topology","topology":{"name":"imaged","action":"update"},"dryRun":true}`

	var (
		calls     = 0
		now       = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		has, lack = true, false
	)

	harness, draft := imagedPreview(t, func(api *builderAPI) {
		previewDisks(&calls, previewKeptImage, previewAddedImage)(api)
		api.disks.now = func() time.Time { return now }
	})

	onServer := func(role *rbac.Role) map[string]*bool {
		t.Helper()

		recorder, preview := previewBuilderDraft(t, harness, draft, role, body)
		if recorder.Code != http.StatusOK || preview.Changes == nil {
			t.Fatalf("dry run of imaged = %d %s, want %d with changes", recorder.Code, recorder.Body, http.StatusOK)
		}

		images := map[string]*bool{}
		for _, image := range preview.Changes.Images {
			images[image.Name] = image.OnServer
		}

		return images
	}

	everything := map[string]*bool{previewKeptImage: &has, previewAddedImage: &has, previewRemovedImage: &lack}
	if diff := cmp.Diff(everything, onServer(nil)); diff != "" {
		t.Errorf("dry run as the full role: images on the server mismatch (-want +got):\n%s", diff)
	}

	// new.qc2 is on the server but hidden from this role: the shared listing does not show it.
	byName := map[string]*bool{previewKeptImage: &has, previewAddedImage: nil, previewRemovedImage: &lack}

	now = now.Add(builderDiskListTTL - time.Second)

	if diff := cmp.Diff(byName, onServer(previewRole(previewKeptImage, previewRemovedImage))); diff != "" {
		t.Errorf("dry run as a role listing some images by name: images on the server mismatch (-want +got):\n%s", diff)
	}

	if calls != 1 {
		t.Errorf("two dry runs within %s: disk listings = %d, want 1", builderDiskListTTL, calls)
	}

	now = now.Add(time.Second)

	if diff := cmp.Diff(everything, onServer(nil)); diff != "" {
		t.Errorf("dry run after the listing expired: images on the server mismatch (-want +got):\n%s", diff)
	}

	if calls != 2 {
		t.Errorf("a dry run once the listing is %s old: disk listings = %d, want 2", builderDiskListTTL, calls)
	}
}

// vlanNode is [includeNode] with its interface on the VLAN given.
func vlanNode(hostname, vlan string) map[string]any {
	node := includeNode(hostname)

	network, _ := node["network"].(map[string]any)
	interfaces, _ := network["interfaces"].([]any)
	first, _ := interfaces[0].(map[string]any)
	first["vlan"] = vlan

	return node
}

// setPreviewAliases sets the VLAN alias of each network of the document
// that aliases names, and removes the others'.
func setPreviewAliases(t *testing.T, document *bdoc.Document, aliases map[string]int) {
	t.Helper()

	set := 0

	for i := range document.Networks {
		alias, ok := aliases[document.Networks[i].Name]
		if !ok {
			document.Networks[i].Alias = nil

			continue
		}

		document.Networks[i].Alias = &alias
		set++
	}

	if set != len(aliases) {
		t.Fatalf("networks = %s, want each of %v", builderJSON(t, document.Networks), aliases)
	}
}

// previewWhileLocked posts a dry run while a publication holds the publish
// lock, and fails the test unless it is answered with 200 all the same.
func previewWhileLocked(
	t *testing.T,
	harness *builderHarness,
	draft builderDraftResponse,
	body string,
) builderPublishPreview {
	t.Helper()

	builderPublishLock.Lock()

	answered := make(chan *httptest.ResponseRecorder, 1)

	go func() {
		answered <- harness.do(builderRequest{
			method: http.MethodPost,
			path:   "/builder/drafts/" + draft.ID + "/publish",
			body:   body, user: builderAlice,
		})
	}()

	var recorder *httptest.ResponseRecorder

	select {
	case recorder = <-answered:
		builderPublishLock.Unlock()
	case <-time.After(10 * time.Second):
		builderPublishLock.Unlock()
		<-answered
		t.Fatal("a dry run waited for the publish lock")
	}

	if recorder.Code != http.StatusOK {
		t.Fatalf("dry run while locked: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var preview builderPublishPreview

	decodeBuilderAnswer(t, recorder, &preview)

	return preview
}

// TestBuilderPublishDryRunOfAnExperimentUpdateWritesNothing asserts a dry run
// of an experiment update lists the alias changes, skips the lock, and writes nothing.
func TestBuilderPublishDryRunOfAnExperimentUpdateWritesNothing(t *testing.T) {
	topology := builderConfig(t, builderKindTopology, "aliased")
	topology.Spec = map[string]any{"nodes": []any{vlanNode(previewHostA, "EXP"), vlanNode(previewHostB, "OT")}}

	harness := newBuilderHarnessWith(t, []builderOption{previewDisks(nil)},
		topology, namedScenario(t, "already", "other, aliased"))

	document := generateBuilderDocument(t, harness, "Topology/aliased")
	document.Scenarios = []string{"already"}
	setPreviewAliases(t, document, map[string]int{"EXP": 101, "OT": 102})

	draft := createBuilderPublishDraft(t, harness, document, "Topology/aliased")
	published, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology-experiment","topology":{"name":"aliased","action":"update"},`+
			`"experiment":{"name":"lab","action":"create"}}`, http.StatusOK)

	setPreviewAliases(t, document, map[string]int{"EXP": 120})

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	edited := saveBuilderDraft(t, harness, builderAlice, published.Draft, data)
	before := harness.dryRunState(edited.ID)

	preview := previewWhileLocked(t, harness, edited,
		`{"mode":"topology-experiment","topology":{"name":"aliased","action":"update"},`+
			`"experiment":{"name":"lab","action":"update"},"dryRun":true}`)

	wasEXP, wasOT, nowEXP := 101, 102, 120
	wantAliases := []bapi.VLANAliasChange{
		{Name: "EXP", From: &wasEXP, To: &nowEXP, Change: bapi.ItemChanged},
		{Name: "OT", From: &wasOT, To: nil, Change: bapi.ItemRemoved},
	}

	if len(preview.Errors) != 0 {
		t.Errorf("dry run of the lab update: errors = %+v, want none", preview.Errors)
	}

	want := &bapi.PublishChanges{
		Experiment:  &bapi.ConfigChange{Name: "lab", Action: bapi.ChangeUpdate},
		VLANAliases: wantAliases,
		Scenarios:   []bapi.ScenarioAnnotation{{Name: "already", Change: bapi.ScenarioUnchanged}},
	}
	if diff := cmp.Diff(want, preview.Changes,
		cmpopts.IgnoreFields(bapi.PublishChanges{}, "Topology", "Includes", "Images")); diff != "" {
		t.Errorf("dry run of the lab update: changes mismatch (-want +got):\n%s", diff)
	}

	if diff := harness.dryRunChanges(edited.ID, before); diff != "" {
		t.Errorf("dry run of the lab update changed the state (-before +after):\n%s", diff)
	}
}

// TestBuilderPublishNeedsIfMatch asserts a publication needs a valid If-Match,
// checked before the body, and a dry run needs none.
func TestBuilderPublishNeedsIfMatch(t *testing.T) {
	const unknownKey = `{"mode":"topology","topology":{"name":"ordered","action":"create"},"force":true}`

	harness := newBuilderHarnessWith(t, []builderOption{previewDisks(nil)})
	draft := harness.createDraft(builderAlice, "ordered")

	for name, test := range map[string]struct {
		body, ifMatch, message string
		status                 int
	}{
		"no If-Match": {
			body: unknownKey, status: http.StatusBadRequest, message: "this request needs an If-Match header",
		},
		"a weak If-Match": {
			body: unknownKey, ifMatch: `W/"1"`, status: http.StatusBadRequest,
			message: "the If-Match header must be a single quoted entity tag",
		},
		"no If-Match and a body that is not JSON": {
			body: `{"mode":`, status: http.StatusBadRequest, message: "this request needs an If-Match header",
		},
		"a valid If-Match and a body with an unknown key": {
			body: unknownKey, ifMatch: draft.ETag, status: http.StatusBadRequest, message: "request body is not a valid Builder request",
		},
		"a dry run with an unknown key": {
			body:   `{"mode":"topology","topology":{"name":"ordered","action":"create"},"dryRun":true,"force":true}`,
			status: http.StatusBadRequest, message: "request body is not a valid Builder request",
		},
		"a dry run": {body: `{"mode":"topology","topology":{"name":"ordered","action":"create"},"dryRun":true}`, status: http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := harness.do(builderRequest{
				method: http.MethodPost, path: "/builder/drafts/" + draft.ID + "/publish",
				body: test.body, user: builderAlice, ifMatch: test.ifMatch,
			})
			if recorder.Code != test.status {
				t.Errorf("publish %s with If-Match %q: status = %d, want %d: %s",
					test.body, test.ifMatch, recorder.Code, test.status, recorder.Body)
			}

			if test.message == "" {
				return
			}

			if got := builderRefusal(t, recorder).Message; !strings.Contains(got, test.message) {
				t.Errorf("publish %s with If-Match %q: message = %q, want it to say %q", test.body, test.ifMatch, got, test.message)
			}
		})
	}

	if writes, documents := harness.configWrites, harness.store.Count(bapi.NamespacePublished); writes != 0 || documents != 0 {
		t.Errorf("refused publications and a dry run wrote %d configs and %d published documents, want none", writes, documents)
	}
}

// TestBuilderPublishPreviewDocumented asserts openapi.yml documents dryRun
// and the JSON fields of each preview component.
func TestBuilderPublishPreviewDocumented(t *testing.T) {
	source, err := os.ReadFile("public/docs/openapi.yml")
	if err != nil {
		t.Fatalf("reading openapi.yml: %v", err)
	}

	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]any `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}

	if err := yaml.Unmarshal(source, &spec); err != nil {
		t.Fatalf("parsing openapi.yml: %v", err)
	}

	if _, ok := spec.Components.Schemas["BuilderPublishRequest"].Properties["dryRun"]; !ok {
		t.Error("openapi.yml BuilderPublishRequest properties lack dryRun, want it documented")
	}

	for component, value := range map[string]any{
		"BuilderPublishPreview":     builderPublishPreview{},
		"BuilderPublishChanges":     bapi.PublishChanges{},
		"BuilderConfigChange":       bapi.ConfigChange{},
		"BuilderIncludeChange":      bapi.IncludeChange{},
		"BuilderScenarioAnnotation": bapi.ScenarioAnnotation{},
		"BuilderImageChange":        bapi.ImageChange{},
		"BuilderVLANAliasChange":    bapi.VLANAliasChange{},
		"BuilderHostPin":            bapi.HostPin{},
		"BuilderPublishResult":      builderPublishResponse{},
	} {
		t.Run(component, func(t *testing.T) {
			documented := make([]string, 0)
			for property := range spec.Components.Schemas[component].Properties {
				documented = append(documented, property)
			}

			slices.Sort(documented)

			if want := jsonFieldNames(reflect.TypeOf(value)); !slices.Equal(documented, want) {
				t.Errorf("openapi.yml %s properties = %v, want %v", component, documented, want)
			}
		})
	}
}
