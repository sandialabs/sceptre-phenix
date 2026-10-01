package types_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"phenix/store"
	"phenix/types"
	"phenix/types/version"
)

// explainTopologyYAML has a valid node and a node whose drive has no image;
// the image key sits one level too high, under hardware.
const explainTopologyYAML = `apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: demo
spec:
  nodes:
  - type: VirtualMachine
    general:
      hostname: host-00
    hardware:
      os_type: linux
      drives:
      - image: ubuntu.qc2
  - type: VirtualMachine
    general:
      hostname: ADServer
    hardware:
      os_type: windows
      image: win10.qc2
      drives:
      - interface: ide
`

// explainTopologyJSON is the broken node alone, as the single-line JSON the
// UI config editor sends.
const explainTopologyJSON = `{"apiVersion":"phenix.sandia.gov/v1","kind":"Topology","metadata":{"name":"demo"},` +
	`"spec":{"nodes":[{"type":"VirtualMachine","general":{"hostname":"ADServer"},` +
	`"hardware":{"os_type":"windows","image":"win10.qc2","drives":[{"interface":"ide"}]}}]}}`

// explainTopologyPrettyJSON is explainTopologyYAML as pretty-printed JSON, as
// jq writes it: a multi-line source, so the explained lines carry the JSON
// source lines.
const explainTopologyPrettyJSON = `{
  "apiVersion": "phenix.sandia.gov/v1",
  "kind": "Topology",
  "metadata": {
    "name": "demo"
  },
  "spec": {
    "nodes": [
      {
        "type": "VirtualMachine",
        "general": {
          "hostname": "host-00"
        },
        "hardware": {
          "os_type": "linux",
          "drives": [
            {
              "image": "ubuntu.qc2"
            }
          ]
        }
      },
      {
        "type": "VirtualMachine",
        "general": {
          "hostname": "ADServer"
        },
        "hardware": {
          "os_type": "windows",
          "image": "win10.qc2",
          "drives": [
            {
              "interface": "ide"
            }
          ]
        }
      }
    ]
  }
}
`

// explainScenarioYAML has an app whose second host has no hostname; the
// hostname key sits one level too deep, under the host's metadata.
const explainScenarioYAML = `apiVersion: phenix.sandia.gov/v2
kind: Scenario
metadata:
  name: demo
spec:
  apps:
  - name: ntp
    hosts:
    - hostname: host-00
    - metadata:
        hostname: host-01
`

// explainScalarAppYAML lists an app as a plain string.
const explainScalarAppYAML = `apiVersion: phenix.sandia.gov/v2
kind: Scenario
metadata:
  name: demo
spec:
  apps:
  - ntp
`

// explainAliasYAML reuses the first node's general block, through a YAML
// alias, as the second node's hardware.
const explainAliasYAML = `apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: demo
spec:
  nodes:
  - type: VirtualMachine
    general: &gen
      hostname: host-00
    hardware:
      os_type: linux
      drives:
      - image: ubuntu.qc2
  - type: VirtualMachine
    general:
      hostname: host-01
    hardware: *gen
`

// explainExternalYAML has an external (hardware in the loop) node whose
// interface has no name.
const explainExternalYAML = `apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: demo
spec:
  nodes:
  - type: HIL
    external: true
    general:
      hostname: plc-01
    network:
      interfaces:
      - vlan: EXP
`

// explainExternalMaskYAML has an external node whose interface mask is out of
// range.
const explainExternalMaskYAML = `apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: demo
spec:
  nodes:
  - type: HIL
    external: true
    general:
      hostname: plc-01
    network:
      interfaces:
      - name: eth0
        vlan: EXP
        proto: static
        address: 10.0.0.5
        mask: 40
`

// explainNoSpecYAML nests spec under metadata by mistake.
const explainNoSpecYAML = `apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: demo
  spec:
    nodes: []
`

// explainEmptySpecYAML has an empty spec, which the config schema reports as
// missing.
const explainEmptySpecYAML = `apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: demo
spec: {}
`

// explainAPIGroupYAML uses an API group other than phenix.sandia.gov.
const explainAPIGroupYAML = `apiVersion: example.com/v1
kind: Topology
metadata:
  name: demo
spec:
  nodes: []
`

// explainAliasLoopYAML nests spec under metadata, after a list that holds
// itself through a YAML alias. The config parser skips unknown metadata keys,
// so the loop reaches the explainer.
const explainAliasLoopYAML = `apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: demo
  loop: &loop
  - *loop
  spec:
    nodes: []
`

// explainRepeatedSpecJSON holds spec twice. encoding/json keeps the second,
// whose node "culprit" has a drive without an image; the first is valid.
const explainRepeatedSpecJSON = `{
  "apiVersion": "phenix.sandia.gov/v1",
  "kind": "Topology",
  "metadata": {"name": "demo"},
  "spec": {"nodes": [{"type": "VirtualMachine", "general": {"hostname": "innocent"},
    "hardware": {"os_type": "linux", "drives": [{"image": "ubuntu.qc2"}]}}]},
  "spec": {"nodes": [{"type": "VirtualMachine", "general": {"hostname": "culprit"},
    "hardware": {"os_type": "linux", "drives": [
      {"interface": "ide"}]}}]}
}
`

// explainAliasDeadline bounds each call in the alias tests. The hint search
// visits each node once, so a call returns in well under a millisecond; one
// that walks every path through the aliases does not return for hours.
const explainAliasDeadline = 5 * time.Second

// explainWorkflowHeader is the envelope of a valid workflow config.
const explainWorkflowHeader = "apiVersion: phenix.sandia.gov/v0\nkind: Workflow\nmetadata: {}\n"

// matchLine reports whether line matches pattern, in which "*" stands for any
// text. The validator's reason is matched that way; what phenix adds around
// it is matched exactly.
func matchLine(line, pattern string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return line == pattern
	}

	prefix, suffix := parts[0], parts[len(parts)-1]
	if len(line) < len(prefix)+len(suffix) || !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
		return false
	}

	rest := line[len(prefix) : len(line)-len(suffix)]

	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(rest, part)
		if i < 0 {
			return false
		}

		rest = rest[i+len(part):]
	}

	return true
}

// assertLines checks that got, the explained lines, match the patterns in
// want, line for line.
func assertLines(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) || !slices.EqualFunc(got, want, matchLine) {
		t.Errorf("explained lines:\n got %q\nwant %q", got, want)
	}
}

// explainTestError parses src as a config, validates it and returns the
// validation error.
func explainTestError(t *testing.T, src string) error {
	t.Helper()

	c, err := store.NewConfigFromYAML([]byte(src))
	if err != nil {
		t.Fatalf("parsing config: %v", err)
	}

	err = types.ValidateConfigSpec(*c)
	if !errors.Is(err, types.ErrValidationFailed) {
		t.Fatalf("expected a validation error, got %v", err)
	}

	return err
}

// explainWorkflowTestError parses src as a workflow config, validates it
// against the Workflow schema and returns the validation error.
func explainWorkflowTestError(t *testing.T, src string) error {
	t.Helper()

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("parsing document: %v", err)
	}

	err := types.ValidateSchema("Workflow", "v0", doc)
	if !errors.Is(err, types.ErrValidationFailed) {
		t.Fatalf("expected a validation error, got %v", err)
	}

	return err
}

func TestExplainValidationError(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "nested node item with hostname, line, hint and two oneOf branches",
			src:  explainTopologyYAML,
			want: []string{
				`nodes[1] "ADServer" (line 21): *"image"* (at hardware.drives[0].image)`,
				`  hint: "image:" is on line 19 under nodes[1].hardware, ` +
					`but the schema expects it at nodes[1].hardware.drives[0].image`,
				`nodes[1] "ADServer" (line 14): *"external"* (at external)`,
			},
		},
		{
			name: "single-line JSON has no line numbers",
			src:  explainTopologyJSON,
			want: []string{
				`nodes[0] "ADServer": *"image"* (at hardware.drives[0].image)`,
				`  hint: "image:" is under nodes[0].hardware, but the schema expects it at nodes[0].hardware.drives[0].image`,
				`nodes[0] "ADServer": *"external"* (at external)`,
			},
		},
		{
			name: "pretty-printed JSON has line numbers",
			src:  explainTopologyPrettyJSON,
			want: []string{
				`nodes[1] "ADServer" (line 32): *"image"* (at hardware.drives[0].image)`,
				`  hint: "image:" is on line 30 under nodes[1].hardware, ` +
					`but the schema expects it at nodes[1].hardware.drives[0].image`,
				`nodes[1] "ADServer" (line 23): *"external"* (at external)`,
			},
		},
		{
			name: "name label, and the hint skips sibling list items",
			src:  explainScenarioYAML,
			want: []string{
				`apps[0] "ntp" (line 10): *"hostname"* (at hosts[1].hostname)`,
				`  hint: "hostname:" is on line 11 under apps[0].hosts[1].metadata, ` +
					`but the schema expects it at apps[0].hosts[1].hostname`,
			},
		},
		{
			name: "item without a label and an error on the item itself",
			src:  explainScalarAppYAML,
			want: []string{`apps[0] (line 7): *`},
		},
		{
			name: "YAML alias, and identical oneOf branch errors print once",
			src:  explainAliasYAML,
			want: []string{`nodes[1] "host-01" (line 17): *"os_type"* (at hardware.os_type)`},
		},
		{
			name: "external node: the allOf parts and both oneOf branches print once",
			src:  explainExternalYAML,
			want: []string{`nodes[0] "plc-01" (line 13): *"name"* (at network.interfaces[0].name)`},
		},
		{
			name: "external node: the external error leads and the VM branch's extra error follows",
			src:  explainExternalMaskYAML,
			want: []string{
				`nodes[0] "plc-01" (line 17): * (at network.interfaces[0].mask)`,
				`nodes[0] "plc-01" (line 15): * (at network.interfaces[0].proto)`,
			},
		},
		{
			name: "document-level error with a hint",
			src:  explainNoSpecYAML,
			want: []string{
				`config (line 1): *"spec"* (at spec)`,
				`  hint: "spec:" is on line 5 under metadata, but the schema expects it at spec`,
			},
		},
		{
			name: "empty spec: the key is where it belongs, so no hint",
			src:  explainEmptySpecYAML,
			want: []string{`config (line 1): *"spec"* (at spec)`},
		},
		{
			name: "error without a schema pointer keeps its text",
			src:  explainAPIGroupYAML,
			want: []string{`config validation failed: invalid API group example.com: expected phenix.sandia.gov`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := explainTestError(t, tt.src)

			assertLines(t, types.ExplainValidationError([]byte(tt.src), err), tt.want)
		})
	}
}

func TestExplainValidationErrorFallsBack(t *testing.T) {
	var (
		nodeErr     = explainTestError(t, explainTopologyYAML)
		hostErr     = explainTestError(t, explainScenarioYAML)
		externalErr = explainTestError(t, explainExternalYAML)

		// Each error in a multi-error falls back to its own text.
		nodeRaw = []string{
			`*"/nodes/1/hardware/drives/0/image"*"image"*`,
			`*"/nodes/1/external"*"external"*`,
		}

		// A single error falls back to the whole original text.
		hostRaw = []string{`config validation failed: *"/apps/0/hosts/1/hostname"*"hostname"*`}

		// An allOf part falls back with the allOf error's pointer in front.
		externalRaw = []string{
			`*"/nodes/0/network/interfaces/0"*"/name"*"name"*`,
			`*"/nodes/0/network/interfaces/0/name"*"name"*`,
		}
	)

	tests := []struct {
		name string
		src  string
		err  error
		want []string
	}{
		{name: "unparsable source", src: "spec: [", err: hostErr, want: hostRaw},
		{name: "empty source", src: "", err: hostErr, want: hostRaw},
		{name: "list item missing from the source", src: "spec:\n  nodes:\n  - type: VirtualMachine\n", err: nodeErr, want: nodeRaw},
		{name: "key missing from the source", src: "spec:\n  links: []\n", err: nodeErr, want: nodeRaw},
		{name: "scalar where the pointer goes on", src: "spec: none\n", err: nodeErr, want: nodeRaw},
		{name: "allOf part keeps its full pointer", src: "spec:\n  nodes: []\n", err: externalErr, want: externalRaw},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertLines(t, types.ExplainValidationError([]byte(tt.src), tt.err), tt.want)
		})
	}
}

// TestExplainValidationErrorUnsupportedKey explains errors from the Workflow
// schema, which validates the whole document and rejects unknown keys. An
// unknown top-level key has an empty pointer; the Workflow schema declares
// apiVersion, so the explainer resolves it from the document root.
func TestExplainValidationErrorUnsupportedKey(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "under spec",
			src:  explainWorkflowHeader + "spec:\n  auto:\n    create: demo\n    updates: true\n",
			want: []string{`spec (line 7): *"updates"* (at auto.updates)`},
		},
		{
			name: "at the top level, from spec keys that lost their indentation",
			src:  explainWorkflowHeader + "spec:\nauto:\n  update: true\n",
			want: []string{`auto (line 5): *"auto"*`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := explainWorkflowTestError(t, tt.src)

			assertLines(t, types.ExplainValidationError([]byte(tt.src), err), tt.want)
		})
	}
}

// TestExplainValidationErrorEscapesKeys explains an error under a key that
// holds characters that are not printable. Each explained line stays a single
// line: such characters are written in their Go escape form, while the
// validator's own text keeps them as they are.
func TestExplainValidationErrorEscapesKeys(t *testing.T) {
	const newlineSrc = explainWorkflowHeader + "spec:\n  vlans:\n    \"EXP\\nFAKE: all configs accepted\": x\n"

	tests := []struct {
		name    string
		src     string
		explain string // the source the error is explained against; "" means src
		want    []string
	}{
		{
			name: "newline in a key",
			src:  newlineSrc,
			want: []string{`spec (line 6): * (at vlans.EXP\nFAKE: all configs accepted)`},
		},
		{
			name: "carriage return and escape in a key",
			src:  explainWorkflowHeader + "spec:\n  vlans:\n    \"EXP\\r\\e[31m\": x\n",
			want: []string{`spec (line 6): * (at vlans.EXP\r\x1b[31m)`},
		},
		{
			name:    "the validator's text, when the pointer does not resolve",
			src:     newlineSrc,
			explain: "spec: none\n",
			want:    []string{`config validation failed: *"/spec/vlans/EXP\nFAKE: all configs accepted"*`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := explainWorkflowTestError(t, tt.src)

			if !strings.Contains(err.Error(), "\n") && !strings.Contains(err.Error(), "\r") {
				t.Fatalf("validator text %q does not keep the key's control character", err)
			}

			explain := tt.explain
			if explain == "" {
				explain = tt.src
			}

			assertLines(t, types.ExplainValidationError([]byte(explain), err), tt.want)
		})
	}
}

// TestExplainValidationErrorRepeatedJSONKey explains a JSON source that holds
// a key twice. encoding/json keeps the last value, which is the one the
// validator checked, so the explained lines name the item in it.
func TestExplainValidationErrorRepeatedJSONKey(t *testing.T) {
	c, err := store.NewConfigFromJSON([]byte(explainRepeatedSpecJSON))
	if err != nil {
		t.Fatalf("parsing config: %v", err)
	}

	err = types.ValidateConfigSpec(*c)
	if !errors.Is(err, types.ErrValidationFailed) {
		t.Fatalf("expected a validation error, got %v", err)
	}

	want := []string{
		`nodes[0] "culprit" (line 9): *"image"* (at hardware.drives[0].image)`,
		`nodes[0] "culprit" (line 7): *"external"* (at external)`,
	}

	assertLines(t, types.ExplainValidationError([]byte(explainRepeatedSpecJSON), err), want)
}

// explainWithinDeadline returns types.ExplainValidationError(src, err), or
// fails the test when the call has not returned within explainAliasDeadline.
// A search that walks every path through YAML aliases would otherwise run
// until the test binary times out, or exhaust memory.
func explainWithinDeadline(t *testing.T, src []byte, err error) []string {
	t.Helper()

	done := make(chan []string, 1)

	go func() { done <- types.ExplainValidationError(src, err) }()

	select {
	case lines := <-done:
		return lines
	case <-time.After(explainAliasDeadline):
		t.Fatalf("ExplainValidationError has not returned after %s", explainAliasDeadline)

		return nil
	}
}

// TestExplainValidationErrorAliasLoop searches a source whose YAML alias makes
// a list hold itself. The hint search visits each node once, so it ends and
// still finds the misplaced key after the loop.
func TestExplainValidationErrorAliasLoop(t *testing.T) {
	err := explainTestError(t, explainAliasLoopYAML)

	want := []string{
		`config (line 1): *"spec"* (at spec)`,
		`  hint: "spec:" is on line 7 under metadata, but the schema expects it at spec`,
	}

	assertLines(t, explainWithinDeadline(t, []byte(explainAliasLoopYAML), err), want)
}

// TestExplainValidationErrorAliasChain searches a source whose YAML aliases
// reach the same nodes by 9^12 paths: each of twelve lists under an unknown
// metadata key holds nine aliases of the list before it. The config parser
// skips unknown metadata keys, so the chain reaches the explainer unexpanded.
// The hint search visits each node once, so it returns at once; a search
// that guards only against loops would walk every path.
func TestExplainValidationErrorAliasChain(t *testing.T) {
	const (
		levels = 12
		width  = 9
	)

	var src strings.Builder

	src.WriteString("apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: demo\n  l0: &l0 [a]\n")

	for level := 1; level <= levels; level++ {
		aliases := slices.Repeat([]string{fmt.Sprintf("*l%d", level-1)}, width)
		fmt.Fprintf(&src, "  l%d: &l%d [%s]\n", level, level, strings.Join(aliases, ", "))
	}

	err := explainTestError(t, src.String())

	assertLines(t, explainWithinDeadline(t, []byte(src.String()), err), []string{`config (line 1): *"spec"* (at spec)`})
}

func TestExplainValidationErrorNil(t *testing.T) {
	if got := types.ExplainValidationError([]byte("kind: Topology\n"), nil); got != nil {
		t.Fatalf("expected nil, got %q", got)
	}
}

// TestSpecSchemasHaveNoDocumentKeys guards the rule ExplainValidationError
// uses to place pointers: one that starts with a document key, or an empty one
// from a schema that declares apiVersion, is resolved from the document root,
// any other under spec. That holds only while no kind's spec schema has a
// top-level property named like a document key.
func TestSpecSchemasHaveNoDocumentKeys(t *testing.T) {
	for kind := range version.StoredVersion {
		schema, err := version.GetVersionedValidatorForKind(kind, version.LATEST_VERSION)
		if errors.Is(err, version.ErrInvalidKind) {
			continue // no spec schema for this kind in the latest version
		}

		if err != nil {
			t.Fatalf("loading %s schema: %v", kind, err)
		}

		for _, key := range []string{"apiVersion", "kind", "metadata", "spec", "status"} {
			if _, ok := schema.Properties[key]; ok {
				t.Errorf("%s spec schema has top-level property %q", kind, key)
			}
		}
	}
}
