package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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

// previewBuilderDraft posts a dry run of a publication of the draft, with
// no If-Match, as the caller holding role (the full role when nil), and
// returns the response and, when it is a 200, the preview it holds.
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
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   body, user: builderTestOwner, role: role,
	})

	var preview builderPublishPreview

	if recorder.Code == http.StatusOK {
		harness.decode(recorder, &preview)
	}

	return recorder, preview
}

// previewDisks lists the disk images named as the server's, and counts the
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

// TestBuilderPublishDryRunWritesNothing previews a publication of a
// topology and an experiment: the answer says what it would change, and no
// published document, config, scenario, experiment or draft record is
// written. A dry run needs no If-Match.
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
	before := asBuilderJSON(t, harness.configs)

	recorder, preview := previewBuilderDraft(t, harness, draft, nil,
		`{"mode":"topology-experiment","topology":{"name":"root","action":"update"},`+
			`"scenario":{"name":"water-ops"},"experiment":{"name":"lab","action":"create"},"dryRun":true}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("dry run: status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}

	onServer := true
	want := &bapi.PublishChanges{
		Topology:  bapi.ConfigChange{Name: "root", Action: bapi.ChangeUpdate},
		Includes:  []bapi.IncludeChange{{Name: "shared", Change: bapi.ItemKept}},
		Scenarios: []bapi.ScenarioAnnotation{{Name: "water-ops", Change: bapi.ScenarioAnnotate}},
		Images: []bapi.ImageChange{
			{Name: "miniccc.qc2", Change: bapi.ItemKept, Devices: []string{"web"}, OnServer: &onServer},
		},
		Experiment:  &bapi.ConfigChange{Name: "lab", Action: bapi.ChangeCreate},
		VLANAliases: aliases,
	}

	if preview.Status != builderPublishPreviewStatus || len(preview.Errors) != 0 ||
		!reflect.DeepEqual(preview.Changes, want) {
		t.Fatalf("preview = %s, want status preview, no errors and changes %s",
			recorder.Body.String(), asBuilderJSON(t, want))
	}

	if harness.configWrites != 0 || harness.experimentWrites != 0 ||
		harness.store.Count(bapi.NamespacePublished) != 0 {
		t.Fatalf("a dry run wrote %d configs, %d experiments and %d published documents",
			harness.configWrites, harness.experimentWrites, harness.store.Count(bapi.NamespacePublished))
	}

	if after := asBuilderJSON(t, harness.configs); after != before {
		t.Fatalf("a dry run changed the configs:\n%s\nwas\n%s", after, before)
	}

	meta, err := harness.service.GetDraft(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if meta.ETag() != draft.ETag || meta.Publication != nil {
		t.Fatalf("draft after a dry run = %s with publication %+v, want it as it was (%s)",
			meta.ETag(), meta.Publication, draft.ETag)
	}
}

// TestBuilderPublishDryRunListsRefusals answers a dry run that a
// publication would refuse with 200 and the refusal in its errors, each an
// error with its code, and no changes. Refusals of who may do what keep
// their status, as does a body that is not a publish request.
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
				t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
			}

			if preview.Changes != nil || len(preview.Errors) == 0 {
				t.Fatalf("preview = %s, want no changes and the refusal", recorder.Body.String())
			}

			refusal := preview.Errors[0]
			if refusal.Code != test.code || refusal.Severity != bdoc.SeverityError ||
				!strings.Contains(refusal.Message, test.message) || refusal.NodeID != test.nodeID {
				t.Fatalf("errors = %+v, want an error of code %s saying %q at node %q",
					preview.Errors, test.code, test.message, test.nodeID)
			}

			if harness.configWrites != 0 || harness.store.Count(bapi.NamespacePublished) != 0 {
				t.Fatal("a refused dry run had side effects")
			}
		})
	}

	harness := newBuilderHarnessWith(t, []builderOption{previewDisks(nil)})
	draft := harness.createDraft(builderTestOwner, "kept")
	theirs := harness.createDraft(builderTestPeer, "theirs")

	everything := []string{"list", "get", "create", "update", "delete"}
	noCreate := builderRole(builderPolicy([]string{"configs"}, previewTargets(), []string{"list", "get", "update"}))
	configsOnly := builderRole(builderPolicy([]string{"configs"}, previewTargets(), everything))
	body := `{"mode":"topology","topology":{"name":"kept","action":"create"},"dryRun":true}`

	for name, test := range map[string]struct {
		draft  builderDraftResponse
		role   *rbac.Role
		body   string
		status int
	}{
		"a target the role may not create": {draft: draft, role: &noCreate, body: body, status: http.StatusForbidden},
		"a draft the caller may not see":   {draft: theirs, role: &configsOnly, body: body, status: http.StatusNotFound},
		"a key a publish request has not": {
			draft: draft, role: nil, status: http.StatusBadRequest,
			body: `{"mode":"topology","topology":{"name":"kept","action":"create"},"dryRun":true,"force":true}`,
		},
	} {
		recorder, _ := previewBuilderDraft(t, harness, test.draft, test.role, test.body)
		if recorder.Code != test.status {
			t.Errorf("%s: status = %d, want %d: %s", name, recorder.Code, test.status, recorder.Body.String())
		}
	}
}

// TestBuilderPublishDryRunAfterPublishing previews a publication the
// topology already holds as leaving it unchanged, and never lists the
// server's disks for a role that may not list them. Its drafts use no disk
// image; TestBuilderPublishDryRunComparesDiskImages compares images.
func TestBuilderPublishDryRunAfterPublishing(t *testing.T) {
	failing := withBuilderDisks(func() ([]disk.Details, error) {
		return nil, errors.New("minimega is not running")
	})

	harness := newBuilderHarnessWith(t, []builderOption{failing})
	draft := harness.createDraft(builderTestOwner, "again")

	_, before := previewBuilderDraft(t, harness, draft, nil,
		`{"mode":"topology","topology":{"name":"again","action":"create"},"dryRun":true}`)
	if before.Changes == nil || before.Changes.Topology.Action != bapi.ChangeCreate {
		t.Fatalf("preview before publishing = %+v, want the topology created", before)
	}

	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"again","action":"create"}}`, http.StatusOK)

	recorder, after := previewBuilderDraft(t, harness, draft, nil,
		`{"mode":"topology","topology":{"name":"again","action":"update"},"dryRun":true}`)
	if after.Changes == nil || after.Changes.Topology.Action != bapi.ChangeUnchanged {
		t.Fatalf("preview after publishing = %s, want the topology unchanged", recorder.Body.String())
	}

	for _, image := range after.Changes.Images {
		if image.OnServer != nil || image.Change != bapi.ItemKept {
			t.Fatalf("images = %+v, want each kept, and not known to be on the server", after.Changes.Images)
		}
	}

	// A role that may not list disks never has them listed.
	calls := 0
	listed := newBuilderHarnessWith(t, []builderOption{previewDisks(&calls, "miniccc.qc2")})
	other := listed.createDraft(builderTestOwner, "unlisted")
	configsOnly := builderRole(builderPolicy([]string{"configs"}, previewTargets(),
		[]string{"list", "get", "create", "update", "delete"}))

	recorder, unlisted := previewBuilderDraft(t, listed, other, &configsOnly,
		`{"mode":"topology","topology":{"name":"unlisted","action":"create"},"dryRun":true}`)
	if recorder.Code != http.StatusOK || unlisted.Changes == nil {
		t.Fatalf("dry run without disks: status %d: %s", recorder.Code, recorder.Body.String())
	}

	for _, image := range unlisted.Changes.Images {
		if image.OnServer != nil {
			t.Fatalf("images = %+v, want none known to be on the server", unlisted.Changes.Images)
		}
	}

	if calls != 0 {
		t.Fatalf("the disks were listed %d times for a role that may not list them", calls)
	}
}

// The disk images of the image tests: one the publication keeps, one it
// adds and one it no longer uses.
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

// imagedPreview returns a harness whose server lists its disk images with
// disks, holding topology "imaged", whose device host-a uses keep.qc2 and
// gone.qc2 and device host-b keep.qc2, and a draft imported from it in
// which host-a uses keep.qc2 and new.qc2.
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
		t.Fatalf("device %s is not in the document: %s", previewHostA, asBuilderJSON(t, document))
	}

	device.Device.Spec["hardware"] = imagedNode(previewHostA, previewKeptImage, previewAddedImage)["hardware"]

	return harness, createBuilderPublishDraft(t, harness, document, "Topology/imaged")
}

// previewRole may do what the full role does with configs and drafts, and
// list the disk images named, or none without a name.
func previewRole(disks ...string) *rbac.Role {
	policies := []*v1.PolicySpec{builderPolicy(
		[]string{"configs", "topologies", "experiments", "scenarios", "builder-drafts"},
		[]string{"*", "*/*"},
		[]string{"list", "get", "create", "update", "delete"},
	)}

	if len(disks) > 0 {
		policies = append(policies, builderPolicy([]string{"disks"}, disks, []string{"list"}))
	}

	role := builderRole(policies...)

	return &role
}

// TestBuilderPublishDryRunComparesDiskImages previews an update of a
// topology whose devices use disk images: each image the stored topology or
// the publication uses is added, removed or kept, with the devices that use
// it, and says whether the server has it only when the caller may list
// that image and the server's images can be read. An image the server has
// that the caller may not list reads as unknown, as does one it lacks.
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
		// new.qc2 is on the server but hidden from the caller, and the caller
		// may list gone.qc2, which the server lacks.
		"a role that may list some images by name": {
			disks: previewDisks(nil, previewKeptImage, previewAddedImage), listed: true,
			role:     previewRole(previewKeptImage, previewRemovedImage),
			onServer: map[string]*bool{previewKeptImage: &has, previewAddedImage: nil, previewRemovedImage: &lacks},
		},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			counted := func(api *builderAPI) {
				list := api.listDisks
				api.listDisks = func() ([]disk.Details, error) {
					calls++

					return list()
				}
			}

			harness, draft := imagedPreview(t, func(api *builderAPI) {
				test.disks(api)
				counted(api)
			})

			recorder, preview := previewBuilderDraft(t, harness, draft, test.role, body)
			if recorder.Code != http.StatusOK || preview.Changes == nil || len(preview.Errors) != 0 {
				t.Fatalf("dry run: status %d: %s", recorder.Code, recorder.Body.String())
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

			if !reflect.DeepEqual(preview.Changes.Images, want) {
				t.Errorf("images = %s, want %s", asBuilderJSON(t, preview.Changes.Images), asBuilderJSON(t, want))
			}

			if listed := calls > 0; listed != test.listed {
				t.Errorf("the server's images were listed %d times, want listed %t", calls, test.listed)
			}
		})
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
		t.Fatalf("networks = %s, want each of %v", asBuilderJSON(t, document.Networks), aliases)
	}
}

// previewWhileLocked posts a dry run while a publication holds the publish
// lock, and fails the test unless it is answered all the same, with 200: a
// dry run never takes the lock.
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
			path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
			body:   body, user: builderTestOwner,
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
		t.Fatalf("dry run: status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}

	var preview builderPublishPreview

	harness.decode(recorder, &preview)

	return preview
}

// TestBuilderPublishDryRunOfAnExperimentUpdateWritesNothing previews an
// update of an experiment the draft published, after an edit that changes
// one network's VLAN alias and removes another's, with a listed scenario
// that already names the topology. The answer says so, and the dry run,
// made while a publication holds the publish lock, is answered without
// waiting for it, and writes, broadcasts and configures nothing.
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

	edited := saveBuilderDraft(t, harness, builderTestOwner, published.Draft, data)

	before, err := harness.service.GetDraft(context.Background(), edited.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	var (
		configs     = asBuilderJSON(t, harness.configs)
		writes      = harness.configWrites
		experiments = harness.experimentWrites
		broadcasts  = harness.broadcasts
		documents   = harness.store.Count(bapi.NamespacePublished)
		configured  = len(harness.reconfigured)
	)

	preview := previewWhileLocked(t, harness, edited,
		`{"mode":"topology-experiment","topology":{"name":"aliased","action":"update"},`+
			`"experiment":{"name":"lab","action":"update"},"dryRun":true}`)

	wasEXP, wasOT, nowEXP := 101, 102, 120
	wantAliases := []bapi.VLANAliasChange{
		{Name: "EXP", From: &wasEXP, To: &nowEXP, Change: bapi.ItemChanged},
		{Name: "OT", From: &wasOT, To: nil, Change: bapi.ItemRemoved},
	}

	if len(preview.Errors) != 0 || preview.Changes == nil ||
		!reflect.DeepEqual(preview.Changes.Experiment, &bapi.ConfigChange{Name: "lab", Action: bapi.ChangeUpdate}) ||
		!reflect.DeepEqual(preview.Changes.VLANAliases, wantAliases) ||
		!reflect.DeepEqual(preview.Changes.Scenarios,
			[]bapi.ScenarioAnnotation{{Name: "already", Change: bapi.ScenarioUnchanged}}) {
		t.Fatalf("preview = %s, want experiment lab updated, aliases %s and scenario already unchanged",
			asBuilderJSON(t, preview), asBuilderJSON(t, wantAliases))
	}

	if harness.configWrites != writes || harness.experimentWrites != experiments ||
		harness.broadcasts != broadcasts || harness.store.Count(bapi.NamespacePublished) != documents ||
		len(harness.reconfigured) != configured {
		t.Fatalf("a dry run wrote %d configs, %d experiments and %d documents, broadcast %d updates and "+
			"configured %d experiments",
			harness.configWrites-writes, harness.experimentWrites-experiments,
			harness.store.Count(bapi.NamespacePublished)-documents, harness.broadcasts-broadcasts,
			len(harness.reconfigured)-configured)
	}

	if after := asBuilderJSON(t, harness.configs); after != configs {
		t.Fatalf("a dry run changed the configs:\n%s\nwas\n%s", after, configs)
	}

	after, err := harness.service.GetDraft(context.Background(), edited.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if after.ETag() != before.ETag() || asBuilderJSON(t, after.Publication) != asBuilderJSON(t, before.Publication) {
		t.Fatalf("draft after a dry run = %s with publication %s, want it as it was (%s, %s)",
			after.ETag(), asBuilderJSON(t, after.Publication), before.ETag(), asBuilderJSON(t, before.Publication))
	}
}

// TestBuilderPublishIfMatchComesBeforeTheBody answers a publication without
// a valid If-Match with the If-Match 400 whatever its body holds, before the
// body is decoded, and one with a valid If-Match and a body strict decoding
// refuses with the body's 400. Only a body that asks for a dry run is
// answered without an If-Match.
func TestBuilderPublishIfMatchComesBeforeTheBody(t *testing.T) {
	const (
		unknownKey = `{"mode":"topology","topology":{"name":"ordered","action":"create"},"force":true}`
		notJSON    = `{"mode":`
		notDryRun  = `{"mode":"topology","topology":{"name":"ordered","action":"create"},"dryRun":false}`
		dryRun     = `{"mode":"topology","topology":{"name":"ordered","action":"create"},"dryRun":true}`
		missing    = "an If-Match header is required for this request"
		malformed  = "the If-Match header must be a single quoted entity tag"
	)

	harness := newBuilderHarnessWith(t, []builderOption{previewDisks(nil)})
	draft := harness.createDraft(builderTestOwner, "ordered")

	for name, test := range map[string]struct {
		body, ifMatch string
		status        int
		// message is the If-Match refusal, or "" for one of the body.
		message string
	}{
		"no If-Match and a body with an unknown key": {body: unknownKey, status: http.StatusBadRequest, message: missing},
		"no If-Match and a body that is not JSON":    {body: notJSON, status: http.StatusBadRequest, message: missing},
		"no If-Match and dryRun false":               {body: notDryRun, status: http.StatusBadRequest, message: missing},
		"a weak If-Match and a body with an unknown key": {
			body: unknownKey, ifMatch: `W/"1"`, status: http.StatusBadRequest, message: malformed,
		},
		"an If-Match and a body with an unknown key": {
			body: unknownKey, ifMatch: draft.ETag, status: http.StatusBadRequest, message: "",
		},
		"no If-Match and a dry run with an unknown key": {
			body:   `{"mode":"topology","topology":{"name":"ordered","action":"create"},"dryRun":true,"force":true}`,
			status: http.StatusBadRequest, message: "",
		},
		"no If-Match and a dry run":         {body: dryRun, status: http.StatusOK},
		"a wildcard If-Match and a dry run": {body: dryRun, ifMatch: "*", status: http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := harness.do(builderRequest{
				method: http.MethodPost,
				path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
				body:   test.body, user: builderTestOwner, ifMatch: test.ifMatch,
			})
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.status, recorder.Body.String())
			}

			if test.status == http.StatusOK {
				return
			}

			var refusal builderPublishRefusal

			harness.decode(recorder, &refusal)

			switch {
			case test.message != "" && !strings.Contains(refusal.Message, test.message):
				t.Errorf("message = %q, want the If-Match refusal %q", refusal.Message, test.message)
			case test.message == "" && strings.Contains(refusal.Message, "If-Match"):
				t.Errorf("message = %q, want the body's refusal", refusal.Message)
			}
		})
	}

	if harness.configWrites != 0 || harness.store.Count(bapi.NamespacePublished) != 0 {
		t.Fatal("a refused publication or a dry run wrote something")
	}
}

// TestBuilderPublishPreviewDocumented holds the OpenAPI document to the
// dry run: the request's dryRun, and the answer's components, whose
// properties are the JSON fields of the Go types.
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
		t.Error("BuilderPublishRequest does not document dryRun")
	}

	for component, value := range map[string]any{
		"BuilderPublishPreview":     builderPublishPreview{},
		"BuilderPublishChanges":     bapi.PublishChanges{},
		"BuilderConfigChange":       bapi.ConfigChange{},
		"BuilderIncludeChange":      bapi.IncludeChange{},
		"BuilderScenarioAnnotation": bapi.ScenarioAnnotation{},
		"BuilderImageChange":        bapi.ImageChange{},
		"BuilderVLANAliasChange":    bapi.VLANAliasChange{},
	} {
		documented := make([]string, 0)
		for property := range spec.Components.Schemas[component].Properties {
			documented = append(documented, property)
		}

		slices.Sort(documented)

		if want := previewJSONFields(value); !slices.Equal(documented, want) {
			t.Errorf("%s documents %v, want %v", component, documented, want)
		}
	}
}

// previewJSONFields returns the sorted JSON names of the fields of a struct.
func previewJSONFields(value any) []string {
	typ := reflect.TypeOf(value)
	names := make([]string, 0, typ.NumField())

	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}
