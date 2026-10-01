package workflow_test

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"phenix/api/experiment"
	"phenix/api/workflow"
	"phenix/util/common"
)

// boolPtr returns a pointer to v, for the *bool fields of [workflow.Auto].
func boolPtr(v bool) *bool {
	return &v
}

func TestExportedValues(t *testing.T) {
	cases := []struct {
		name string
		got  any
		want any
	}{
		{name: "BranchAnnotation", got: workflow.BranchAnnotation, want: "phenix.workflow/branch"},
		{name: "TagsAnnotation", got: workflow.TagsAnnotation, want: "phenix.workflow/tags"},
		{name: "ErrInvalidSpec", got: workflow.ErrInvalidSpec.Error(), want: "invalid workflow config"},
		{name: "ErrInvalidName", got: workflow.ErrInvalidName.Error(), want: "invalid workflow name"},
	}

	for _, tc := range cases {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Errorf("%s = %#v, want %#v", tc.name, tc.got, tc.want)
		}
	}
}

func TestDecode(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want workflow.Spec
	}{
		{name: "nil spec", in: nil, want: workflow.Spec{}},
		{name: "empty spec", in: map[string]any{}, want: workflow.Spec{}},
		{name: "null auto stays nil", in: map[string]any{"auto": nil}, want: workflow.Spec{}},
		{
			name: "null values leave fields unset",
			in: map[string]any{
				"auto":          map[string]any{"create": nil, "update": nil, "restart": nil},
				"topology":      nil,
				"scenario":      nil,
				"vlans":         nil,
				"schedules":     nil,
				"deployMode":    nil,
				"useGREMesh":    nil,
				"vlanRange":     nil,
				"defaultBridge": nil,
			},
			want: workflow.Spec{Auto: &workflow.Auto{}},
		},
		{
			name: "auto without flags leaves them unset",
			in:   map[string]any{"auto": map[string]any{"create": "foo"}},
			want: workflow.Spec{Auto: &workflow.Auto{Create: "foo"}},
		},
		{
			name: "every field, YAML value types",
			in: map[string]any{
				"auto":          map[string]any{"create": "foo", "update": false, "restart": true},
				"topology":      "topo",
				"scenario":      "scen",
				"vlans":         map[string]any{"EXP": 101},
				"schedules":     map[string]any{"host-a": "compute1"},
				"deployMode":    "all",
				"useGREMesh":    true,
				"vlanRange":     map[string]any{"min": 100, "max": 200},
				"defaultBridge": "br0",
			},
			want: workflow.Spec{
				Auto:          &workflow.Auto{Create: "foo", Update: boolPtr(false), Restart: boolPtr(true)},
				Topology:      "topo",
				Scenario:      "scen",
				VLANs:         map[string]int{"EXP": 101},
				Schedules:     map[string]string{"host-a": "compute1"},
				DeployMode:    "all",
				UseGREMesh:    true,
				VLANRange:     &workflow.VLANRange{Min: 100, Max: 200},
				DefaultBridge: "br0",
			},
		},
		{
			name: "JSON numbers decode into int fields",
			in: map[string]any{
				"vlans":     map[string]any{"EXP": float64(101)},
				"vlanRange": map[string]any{"min": float64(100), "max": float64(200)},
			},
			want: workflow.Spec{VLANs: map[string]int{"EXP": 101}, VLANRange: &workflow.VLANRange{Min: 100, Max: 200}},
		},
		{
			name: "unknown keys are ignored",
			in:   map[string]any{"topology": "topo", "bogus": 1, "auto": map[string]any{"bogus": "x"}},
			want: workflow.Spec{Topology: "topo", Auto: &workflow.Auto{}},
		},
		{
			name: "keys match case-insensitively",
			in:   map[string]any{"TOPOLOGY": "topo", "deploymode": "all"},
			want: workflow.Spec{Topology: "topo", DeployMode: "all"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := workflow.Decode(tc.in)
			if err != nil {
				t.Fatalf("Decode() unexpected error: %v", err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Decode() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want string
	}{
		{
			name: "string for a bool",
			in:   map[string]any{"auto": map[string]any{"update": "true"}},
			want: "'auto.update' expected type 'bool', got unconvertible type 'string'",
		},
		{
			name: "string for useGREMesh",
			in:   map[string]any{"useGREMesh": "yes"},
			want: "'useGREMesh' expected type 'bool', got unconvertible type 'string'",
		},
		{
			name: "scalar for auto",
			in:   map[string]any{"auto": "yes"},
			want: "'auto' expected a map, got 'string'",
		},
		{
			name: "string for a VLAN ID",
			in:   map[string]any{"vlans": map[string]any{"EXP": "101"}},
			want: "'vlans[EXP]' expected type 'int', got unconvertible type 'string'",
		},
		{
			name: "number for topology",
			in:   map[string]any{"topology": 5},
			want: "'topology' expected type 'string', got unconvertible type 'int'",
		},
		{
			name: "list for vlanRange",
			in:   map[string]any{"vlanRange": []any{100, 200}},
			want: "'vlanRange' expected a map, got 'slice'",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := workflow.Decode(tc.in)
			if !errors.Is(err, workflow.ErrInvalidSpec) {
				t.Fatalf("Decode() error = %v, want ErrInvalidSpec", err)
			}

			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Decode() error = %q, want it to contain %q", err, tc.want)
			}

			if !reflect.DeepEqual(got, workflow.Spec{}) {
				t.Errorf("Decode() spec = %+v, want the zero spec with an error", got)
			}
		})
	}
}

// TestSpecValidate checks the bridge name limit, which counts bytes, as the
// Linux interface name limit does.
func TestSpecValidate(t *testing.T) {
	const limit = experiment.MaxBridgeNameLength

	cases := []struct {
		name    string
		bridge  string
		wantErr bool
	}{
		{name: "unset bridge defaults to phenix", bridge: "", wantErr: false},
		{name: "ASCII name at the limit", bridge: strings.Repeat("b", limit), wantErr: false},
		{name: "multi-byte name at the limit", bridge: strings.Repeat("é", limit/2) + "b", wantErr: false},
		{name: "ASCII name one byte over", bridge: strings.Repeat("b", limit+1), wantErr: true},
		{name: "multi-byte name one byte over", bridge: strings.Repeat("é", limit/2+1), wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := workflow.Spec{DefaultBridge: tc.bridge}.Validate()

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Validate() unexpected error: %v", err)
				}

				return
			}

			if !errors.Is(err, workflow.ErrInvalidSpec) {
				t.Fatalf("Validate() error = %v, want ErrInvalidSpec", err)
			}

			for _, part := range []string{strconv.Quote(tc.bridge), "longer than " + strconv.Itoa(limit) + " characters"} {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("Validate() error = %q, want it to contain %q", err, part)
				}
			}
		})
	}
}

func TestValidateName(t *testing.T) {
	valid := []string{"main", "a", "9lives", "feature-1", "v1.2.3", "A_b.c-D", "a..b", "hw-test"}
	invalid := []string{"", ".", "..", ".hidden", "-dash", "_under", "a/b", "../etc", "a b", "a\nb", "main\n", "a\tb", "a\\b", "naïve"}

	for _, name := range valid {
		if err := workflow.ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) unexpected error: %v", name, err)
		}
	}

	for _, name := range invalid {
		err := workflow.ValidateName(name)
		if !errors.Is(err, workflow.ErrInvalidName) {
			t.Errorf("ValidateName(%q) error = %v, want ErrInvalidName", name, err)

			continue
		}

		if !strings.Contains(err.Error(), strconv.Quote(name)) {
			t.Errorf("ValidateName(%q) error = %q, want it to quote the name", name, err)
		}
	}

	// The pattern accepts ASCII letters only, and the message says so.
	want := `invalid workflow name "café": must start with an ASCII letter or digit` +
		` and contain only ASCII letters, digits, '.', '_' and '-'`
	if err := workflow.ValidateName("café"); err == nil || err.Error() != want {
		t.Errorf("ValidateName(%q) error = %v, want %s", "café", err, want)
	}
}

func TestSpecAuto(t *testing.T) {
	cases := []struct {
		name        string
		auto        *workflow.Auto
		wantCreate  string
		wantUpdate  bool
		wantRestart bool
	}{
		{name: "auto missing", auto: nil, wantCreate: "", wantUpdate: true, wantRestart: true},
		{name: "flags unset", auto: &workflow.Auto{Create: "foo"}, wantCreate: "foo", wantUpdate: true, wantRestart: true},
		{name: "flags true", auto: &workflow.Auto{Update: boolPtr(true), Restart: boolPtr(true)}, wantUpdate: true, wantRestart: true},
		{name: "flags false", auto: &workflow.Auto{Update: boolPtr(false), Restart: boolPtr(false)}, wantUpdate: false, wantRestart: false},
		{name: "only update false", auto: &workflow.Auto{Update: boolPtr(false)}, wantUpdate: false, wantRestart: true},
		{name: "only restart false", auto: &workflow.Auto{Restart: boolPtr(false)}, wantUpdate: true, wantRestart: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := workflow.Spec{Auto: tc.auto}

			if got := spec.ExperimentName(); got != tc.wantCreate {
				t.Errorf("ExperimentName() = %q, want %q", got, tc.wantCreate)
			}

			if got := spec.AutoUpdate(); got != tc.wantUpdate {
				t.Errorf("AutoUpdate() = %t, want %t", got, tc.wantUpdate)
			}

			if got := spec.AutoRestart(); got != tc.wantRestart {
				t.Errorf("AutoRestart() = %t, want %t", got, tc.wantRestart)
			}
		})
	}
}

func TestSpecValueAccessors(t *testing.T) {
	var unset workflow.Spec

	set := workflow.Spec{
		Topology:      "topo",
		Scenario:      "scen",
		VLANs:         map[string]int{"EXP": 101},
		Schedules:     map[string]string{"host-a": "compute1"},
		VLANRange:     &workflow.VLANRange{Min: 100, Max: 200},
		DefaultBridge: "br0",
	}

	cases := []struct {
		name string
		got  any
		want any
	}{
		{name: "unset topology", got: unset.ExperimentTopology(), want: ""},
		{name: "unset scenario", got: unset.ExperimentScenario(), want: ""},
		{name: "unset VLAN min", got: unset.VLANMin(), want: 0},
		{name: "unset VLAN max", got: unset.VLANMax(), want: 0},
		{name: "unset default bridge", got: unset.DefaultBridgeName(), want: "phenix"},
		{name: "unset VLAN mappings", got: unset.VLANMappings(), want: map[string]int{}},
		{name: "unset schedule mappings", got: unset.ScheduleMappings(), want: map[string]string{}},
		{name: "topology", got: set.ExperimentTopology(), want: "topo"},
		{name: "scenario", got: set.ExperimentScenario(), want: "scen"},
		{name: "VLAN min", got: set.VLANMin(), want: 100},
		{name: "VLAN max", got: set.VLANMax(), want: 200},
		{name: "default bridge", got: set.DefaultBridgeName(), want: "br0"},
		{name: "VLAN mappings", got: set.VLANMappings(), want: map[string]int{"EXP": 101}},
		{name: "schedule mappings", got: set.ScheduleMappings(), want: map[string]string{"host-a": "compute1"}},
	}

	for _, tc := range cases {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Errorf("%s = %#v, want %#v", tc.name, tc.got, tc.want)
		}
	}
}

func TestSpecMappingDefaultsAreFresh(t *testing.T) {
	var spec workflow.Spec

	spec.VLANMappings()["EXP"] = 101
	spec.ScheduleMappings()["host-a"] = "compute1"

	if got := spec.VLANMappings(); len(got) != 0 {
		t.Errorf("VLANMappings() = %v after writing to an earlier result, want a new empty map", got)
	}

	if got := spec.ScheduleMappings(); len(got) != 0 {
		t.Errorf("ScheduleMappings() = %v after writing to an earlier result, want a new empty map", got)
	}
}

func TestSpecExperimentDeployMode(t *testing.T) {
	orig := common.DeployMode
	common.DeployMode = common.DeployModeOnlyHeadnode //nolint:reassign // exercise the fallback to the server setting

	t.Cleanup(func() { common.DeployMode = orig }) //nolint:reassign // restore global config

	cases := []struct {
		mode string
		want common.DeploymentMode
	}{
		{mode: "", want: common.DeployModeOnlyHeadnode},
		{mode: "bogus", want: common.DeployModeOnlyHeadnode},
		{mode: "no-headnode", want: common.DeployModeNoHeadnode},
		{mode: "ALL", want: common.DeployModeAll},
		{mode: "all", want: common.DeployModeAll},
	}

	for _, tc := range cases {
		spec := workflow.Spec{DeployMode: tc.mode}

		if got := spec.ExperimentDeployMode(); got != tc.want {
			t.Errorf("ExperimentDeployMode() with deployMode %q = %q, want %q", tc.mode, got, tc.want)
		}
	}
}
