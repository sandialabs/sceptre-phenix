package types_test

import (
	"errors"
	"testing"

	"gopkg.in/yaml.v3"

	"phenix/store"
	"phenix/types"
	"phenix/types/version"
)

// v1KindSpecs are representative specs of the kinds stored at v1. Each wants
// the outcome the v2.yaml definitions gave when config validation used them
// for every kind, except that includeTopologies and schedules are declared.
// They cover the places where the earlier v1.yaml definitions differed, so
// validating a v1 config against its own schema must keep these outcomes.
var v1KindSpecs = []struct { //nolint:gochecknoglobals // test table
	name  string
	kind  string
	spec  string
	valid bool
}{
	{"topology without nodes", "Topology", `{nodes: []}`, true},
	{"topology with a node", "Topology", `
nodes:
- type: VirtualMachine
  general: {hostname: host-1}
  hardware: {os_type: linux, drives: [{image: ubuntu.qc2}]}`, true},
	{"topology with only includeTopologies", "Topology", `{includeTopologies: [base]}`, true},
	{"topology with a single includeTopologies name", "Topology", `{includeTopologies: base, nodes: []}`, true},
	{"topology with includeTopologies of mappings", "Topology", `{includeTopologies: [{name: x}], nodes: []}`, false},
	{"topology with a node missing hardware", "Topology", `
nodes:
- type: VirtualMachine
  general: {hostname: host-1}`, false},
	{"topology with a 1-character hostname", "Topology", `
nodes:
- type: VirtualMachine
  general: {hostname: a}
  hardware: {os_type: linux, drives: [{image: ubuntu.qc2}]}`, false},
	{"topology with an empty drive image", "Topology", `
nodes:
- type: VirtualMachine
  general: {hostname: host-1}
  hardware: {os_type: linux, drives: [{image: ""}]}`, false},
	{"topology with an unknown os_type", "Topology", `
nodes:
- type: VirtualMachine
  general: {hostname: host-1}
  hardware: {os_type: plan9, drives: [{image: ubuntu.qc2}]}`, false},
	{"topology with a boolean vcpus", "Topology", `
nodes:
- type: VirtualMachine
  general: {hostname: host-1}
  hardware: {os_type: linux, vcpus: true, drives: [{image: ubuntu.qc2}]}`, false},
	{"topology with null optional node sections", "Topology", `
nodes:
- type: VirtualMachine
  general: {hostname: host-1, snapshot: null, do_not_boot: null}
  hardware: {os_type: linux, vcpus: "4", drives: [{image: ubuntu.qc2, inject_partition: null}]}
  network: null
  injections: null
  delay: null
  advanced: null
  commands: null`, true},
	{"topology with string advanced values", "Topology", `
nodes:
- type: VirtualMachine
  general: {hostname: host-1}
  hardware: {os_type: linux, drives: [{image: ubuntu.qc2}]}
  advanced: {qemu-append: -nographic}`, true},
	{"topology with a non-string advanced value", "Topology", `
nodes:
- type: VirtualMachine
  general: {hostname: host-1}
  hardware: {os_type: linux, drives: [{image: ubuntu.qc2}]}
  advanced: {count: 1}`, false},
	{"topology with empty MAC, ruleset names and route", "Topology", `
nodes:
- type: Router
  general: {hostname: rtr}
  hardware: {os_type: vyatta, drives: [{image: vyatta.qc2}]}
  network:
    interfaces:
    - {name: eth0, vlan: EXP, type: ethernet, proto: static, address: 10.0.0.1, mask: 24, mac: "", ruleset_out: "", ruleset_in: ""}
    routes:
    - {destination: "", next: "", cost: null}
    rulesets:
    - name: ""
      default: drop
      rules:
      - {id: 10, action: accept, protocol: tcp, source: {address: ""}, destination: null}`, true},
	{"topology with a malformed MAC", "Topology", `
nodes:
- type: VirtualMachine
  general: {hostname: host-1}
  hardware: {os_type: linux, drives: [{image: ubuntu.qc2}]}
  network:
    interfaces:
    - {name: eth0, vlan: EXP, type: ethernet, proto: dhcp, mac: "00:11"}`, false},
	{"topology with a serial interface missing udp_port", "Topology", `
nodes:
- type: VirtualMachine
  general: {hostname: host-1}
  hardware: {os_type: linux, drives: [{image: ubuntu.qc2}]}
  network:
    interfaces:
    - {name: s0, vlan: EXP, type: serial, proto: static, address: 10.0.0.1, mask: 24, baud_rate: 9600, device: /dev/ttyS0}`, false},
	{"topology with an external node", "Topology", `
nodes:
- external: true
  type: HIL
  general: {hostname: plc}
  network:
    interfaces:
    - {name: eth0, proto: static, address: 10.0.0.9, mask: 24, vlan: EXP}`, true},
	{"topology with an external interface missing its name", "Topology", `
nodes:
- external: true
  type: HIL
  general: {hostname: plc}
  network:
    interfaces:
    - {proto: dhcp, vlan: EXP}`, false},

	{"experiment with a topology", "Experiment", `{topology: {nodes: []}}`, true},
	{"experiment without a topology", "Experiment", `{scenario: null}`, false},
	{"experiment with null optional sections", "Experiment", `
topology: {nodes: []}
scenario: null
vlans: {aliases: null, min: 100, max: 200}
schedules: null`, true},
	{"experiment with null vlans", "Experiment", `{topology: {nodes: []}, vlans: null}`, true},
	{"experiment with a non-integer VLAN alias", "Experiment", `{topology: {nodes: []}, vlans: {aliases: {MGMT: x}}}`, false},
	{"experiment with a non-string schedule", "Experiment", `{topology: {nodes: []}, schedules: {host-1: 1}}`, false},
	{"experiment with a scenario", "Experiment", `
topology: {nodes: []}
scenario:
  apps:
  - name: app
    assetDir: /phenix
    metadata: null
    disabled: null
    hosts: [{hostname: host-1, metadata: {a: 1}}]`, true},
	{"experiment with an empty app name", "Experiment", `{topology: {nodes: []}, scenario: {apps: [{name: ""}]}}`, true},
	{"experiment with null scenario apps", "Experiment", `{topology: {nodes: []}, scenario: {apps: null}}`, true},
	{"experiment with a scenario missing apps", "Experiment", `{topology: {nodes: []}, scenario: {}}`, false},
	{"experiment with an app missing its name", "Experiment", `{topology: {nodes: []}, scenario: {apps: [{}]}}`, false},
	{"experiment with an app host missing its hostname", "Experiment", `
topology: {nodes: []}
scenario: {apps: [{name: app, hosts: [{metadata: {}}]}]}`, false},
	{"experiment with an invalid topology", "Experiment", `{topology: {}}`, false},

	{"image with required fields", "Image", `
{format: qcow2, mirror: http://us.archive.ubuntu.com/ubuntu/, release: focal, size: 10G, variant: minbase}`, true},
	{"image with null optional lists", "Image", `
format: qcow2
mirror: http://us.archive.ubuntu.com/ubuntu/
release: focal
size: 10G
variant: minbase
overlays: null
packages: null
script_order: null
scripts: null`, true},
	{"image without a size", "Image", `
{format: qcow2, mirror: http://us.archive.ubuntu.com/ubuntu/, release: focal, variant: minbase}`, false},
	{"image with a non-string script", "Image", `
{format: qcow2, mirror: m, release: focal, size: 10G, variant: minbase, scripts: {POSTBUILD: 1}}`, false},
	{"image with a non-boolean compress", "Image", `
{format: qcow2, mirror: m, release: focal, size: 10G, variant: minbase, compress: "yes"}`, false},

	{"user with required fields", "User", `{first_name: John, last_name: Doe, username: john}`, true},
	{"user with a role", "User", `
first_name: John
last_name: Doe
username: john
rbac: {roleName: viewer, policies: [{resources: [experiments], verbs: [list]}]}`, true},
	{"user without a username", "User", `{first_name: John, last_name: Doe}`, false},
	{"user with an incomplete role", "User", `{first_name: John, last_name: Doe, username: john, rbac: {}}`, false},

	{"role with required fields", "Role", `
{roleName: viewer, policies: [{resources: ["*"], resourceNames: ["*"], verbs: [get]}]}`, true},
	{"role without a name", "Role", `{policies: []}`, false},
	{"role with non-list policies", "Role", `{roleName: viewer, policies: x}`, false},
}

// TestV1SchemasKeepValidation proves each kind stored at v1 accepts and
// rejects the representative specs exactly as config validation did before
// it validated every config against its own apiVersion's schema.
func TestV1SchemasKeepValidation(t *testing.T) {
	for _, tc := range v1KindSpecs {
		t.Run(tc.name, func(t *testing.T) {
			ver := version.StoredVersion[tc.kind]
			if ver != "v1" {
				t.Fatalf("%s is stored at %s, want v1", tc.kind, ver)
			}

			var spec map[string]any
			if err := yaml.Unmarshal([]byte(tc.spec), &spec); err != nil {
				t.Fatalf("parsing spec: %v", err)
			}

			c := store.Config{
				Version:  store.APIGroup + "/" + ver,
				Kind:     tc.kind,
				Metadata: store.ConfigMetadata{Name: "fixture"},
				Spec:     spec,
			}

			err := types.ValidateConfigSpec(c)

			switch {
			case tc.valid && err != nil:
				t.Errorf("ValidateConfigSpec() error = %v, want nil", err)
			case !tc.valid && !errors.Is(err, types.ErrValidationFailed):
				t.Errorf("ValidateConfigSpec() error = %v, want ErrValidationFailed", err)
			}
		})
	}
}
