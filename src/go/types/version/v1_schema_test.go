package version

import (
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/go-cmp/cmp"

	v1 "phenix/types/version/v1"
)

// v1NodeComponents are the v1 component schemas that describe topology nodes
// and their interfaces.
var v1NodeComponents = []string{ //nolint:gochecknoglobals // test table
	"minimega_node", "external_node", "iface", "iface_address", "iface_rulesets",
	"static_iface", "dhcp_iface", "serial_iface",
}

// TestV1NodeFieldsDescribed proves every node and interface field in v1.yaml has
// a description, except an object field whose own fields are all described.
func TestV1NodeFieldsDescribed(t *testing.T) {
	t.Parallel()

	components := schemaComponents(t, "v1")

	for _, name := range v1NodeComponents {
		for _, path := range undescribedFields(components[name], name) {
			t.Errorf("v1.yaml %s has no description", path)
		}
	}
}

// undescribedFields lists the paths of the leaf properties under schema that
// have no description. It follows properties, items, allOf and oneOf.
func undescribedFields(schema any, path string) []string {
	node, ok := schema.(map[string]any)
	if !ok {
		return nil
	}

	var found []string

	properties, _ := node["properties"].(map[string]any)
	for name, value := range properties {
		field, _ := value.(map[string]any)
		if _, ok := field["description"]; !ok && !hasFields(field) {
			found = append(found, path+"."+name)
		}

		found = append(found, undescribedFields(field, path+"."+name)...)
	}

	found = append(found, undescribedFields(node["items"], path+"[]")...)

	for _, key := range []string{"allOf", "oneOf"} {
		list, _ := node[key].([]any)
		for _, item := range list {
			found = append(found, undescribedFields(item, path)...)
		}
	}

	return found
}

// hasFields reports whether schema is an object, or an array of objects, with
// properties of its own.
func hasFields(schema map[string]any) bool {
	if _, ok := schema["properties"]; ok {
		return true
	}

	items, _ := schema["items"].(map[string]any)
	_, ok := items["properties"]

	return ok
}

// TestV1NodeSchemaDefaultsMatchInit proves the defaults v1.yaml shows for a
// VM node are the values phenix fills in when it initializes a topology.
func TestV1NodeSchemaDefaultsMatchInit(t *testing.T) {
	t.Parallel()

	node := &v1.Node{
		GeneralF:  &v1.General{HostnameF: "host"},
		HardwareF: &v1.Hardware{DrivesF: []*v1.Drive{{ImageF: "ubuntu.qc2"}}},
	}

	if err := (&v1.TopologySpec{NodesF: []*v1.Node{node}}).Init("phenix"); err != nil {
		t.Fatalf("TopologySpec.Init(phenix) error = %v", err)
	}

	got := map[string]any{
		"general.vm_type":     node.GeneralF.VMType(),
		"general.snapshot":    *node.GeneralF.Snapshot(),
		"general.do_not_boot": *node.GeneralF.DoNotBoot(),
		"hardware.cpu":        node.HardwareF.CPU(),
		"hardware.vcpus":      node.HardwareF.VCPU(),
		"hardware.memory":     node.HardwareF.Memory(),
		"hardware.os_type":    node.HardwareF.OSType(),
	}

	general, hardware := nodeSection(t, "general"), nodeSection(t, "hardware")
	want := map[string]any{
		"general.vm_type":     general["vm_type"]["default"],
		"general.snapshot":    general["snapshot"]["default"],
		"general.do_not_boot": general["do_not_boot"]["default"],
		"hardware.cpu":        hardware["cpu"]["default"],
		"hardware.vcpus":      hardware["vcpus"]["default"],
		"hardware.memory":     hardware["memory"]["default"],
		"hardware.os_type":    hardware["os_type"]["default"],
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("minimega_node schema defaults differ from TopologySpec.Init (-schema +init):\n%s", diff)
	}
}

// nodeSection returns the properties of section in the v1 minimega_node
// schema, each as its schema map.
func nodeSection(t *testing.T, section string) map[string]map[string]any {
	t.Helper()

	node, _ := schemaComponents(t, "v1")["minimega_node"].(map[string]any)
	nodeProps, _ := node["properties"].(map[string]any)
	sectionSchema, _ := nodeProps[section].(map[string]any)
	props, _ := sectionSchema["properties"].(map[string]any)

	fields := make(map[string]map[string]any, len(props))
	for name, value := range props {
		fields[name], _ = value.(map[string]any)
	}

	if len(fields) == 0 {
		t.Fatalf("v1.yaml minimega_node has no %s properties", section)
	}

	return fields
}

// TestOpenAPIParsedOnce proves each schema file is loaded once, so concurrent
// and repeated lookups share one validator.
func TestOpenAPIParsedOnce(t *testing.T) {
	t.Parallel()

	const callers = 8

	type call struct {
		schema *openapi3.Schema
		err    error
	}

	got := make([]call, callers)

	var wg sync.WaitGroup

	for i := range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			schema, err := GetVersionedValidatorForKind("Topology", "v1")
			got[i] = call{schema: schema, err: err}
		}()
	}

	wg.Wait()

	for i, call := range got {
		if call.err != nil {
			t.Fatalf("GetVersionedValidatorForKind(Topology, v1) call %d error = %v", i, call.err)
		}

		if call.schema != got[0].schema {
			t.Errorf("GetVersionedValidatorForKind(Topology, v1) call %d = %p, want %p", i, call.schema, got[0].schema)
		}
	}
}

// TestLoadOpenAPICacheHit proves a cache hit does not copy the schema file and an
// unknown version is not cached. It is not parallel because AllocsPerRun is process-wide.
func TestLoadOpenAPICacheHit(t *testing.T) {
	if _, err := loadOpenAPI("v1"); err != nil {
		t.Fatalf("loadOpenAPI(v1) error = %v", err)
	}

	allocs := testing.AllocsPerRun(100, func() {
		_, _ = loadOpenAPI("v1")
	})
	if allocs != 0 {
		t.Errorf("loadOpenAPI(v1) on a cache hit allocs = %v, want 0", allocs)
	}

	if _, err := loadOpenAPI("v99"); err == nil {
		t.Error("loadOpenAPI(v99) error = nil, want an error")
	}

	openAPIMu.Lock()
	_, cached := openAPILoaded["v99"]
	openAPIMu.Unlock()

	if cached {
		t.Error("loadOpenAPI(v99) cached an unknown version")
	}
}
