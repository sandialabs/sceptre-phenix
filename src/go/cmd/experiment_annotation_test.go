package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/util/common"
)

// runExperimentCreate runs `phenix experiment create exp -t vm-topo` with args added, against a
// new store holding the vm-topo topology of one VM named "vm".
func runExperimentCreate(t *testing.T, args ...string) error {
	t.Helper()

	storetest.Use(t)

	originalBase := common.PhenixBase
	common.PhenixBase = t.TempDir() //nolint:reassign // experiment files go to a temporary tree

	t.Cleanup(func() { common.PhenixBase = originalBase }) //nolint:reassign // restore

	topology := &store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: "vm-topo"},
		Spec: map[string]any{
			"nodes": []any{map[string]any{
				"type":    "VirtualMachine",
				"general": map[string]any{"hostname": "vm"},
				"hardware": map[string]any{
					"os_type": "linux",
					"drives":  []any{map[string]any{"image": "ubuntu.qc2"}},
				},
				"network": map[string]any{
					"interfaces": []any{
						map[string]any{"name": "IF0", "type": "ethernet", "vlan": "EXP-1", "proto": "dhcp"},
					},
				},
			}},
		},
	}

	if err := store.Create(topology); err != nil {
		t.Fatalf("store.Create(topology vm-topo) error = %v", err)
	}

	create := newExperimentCreateCmd()
	create.SetArgs(append([]string{"exp", "-t", "vm-topo"}, args...))
	create.SetOut(io.Discard)
	create.SetErr(io.Discard)

	return create.ExecuteContext(context.Background())
}

func TestExperimentCreateAnnotationFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string // part of the error; "" when it creates the experiment

		wantAnnotations map[string]string // the experiment's
		wantNode        map[string]any    // the VM's
	}{
		{
			name:            "without any",
			wantAnnotations: map[string]string{"topology": "vm-topo"},
		},
		{
			name: "keeps what follows the first = as the value",
			args: []string{
				"--annotation", "phenix.workflow/tags=a=1,b=2",
				"--annotation", "note=",
			},
			wantAnnotations: map[string]string{
				"topology":             "vm-topo",
				"phenix.workflow/tags": "a=1,b=2",
				"note":                 "",
			},
		},
		{
			name: "keeps the node annotation types the default apps read",
			args: []string{
				"--node-annotation", "phenix/default-apps=false",
				"--node-annotation", "phenix/startup-via-cc=true",
				"--node-annotation", `phenix/startup-autotunnel=["8080:80"]`,
				"--node-annotation", "vrouter/vyos-password=123456",
				"--node-annotation", "vrouter/enable-ssh=IF0",
				"--node-annotation", "broken=[not json",
			},
			wantAnnotations: map[string]string{"topology": "vm-topo"},
			wantNode: map[string]any{
				"phenix/default-apps":       false,
				"phenix/startup-via-cc":     true,
				"phenix/startup-autotunnel": []any{"8080:80"},
				"vrouter/vyos-password":     "123456",
				"vrouter/enable-ssh":        "IF0",
				"broken":                    "[not json",
			},
		},
		{
			name: "keeps a JSON object, a spaced boolean and a number as a string",
			args: []string{
				"--node-annotation", `ports={"http": 80}`,
				"--node-annotation", "enabled= true ",
				"--node-annotation", "count=5",
				"--node-annotation", "count=6",
			},
			wantAnnotations: map[string]string{"topology": "vm-topo"},
			wantNode: map[string]any{
				"ports":   map[string]any{"http": float64(80)},
				"enabled": true,
				"count":   "6",
			},
		},
		{
			name:            "keeps the last of repeated annotations",
			args:            []string{"--annotation", "note=a", "--annotation", "note=b"},
			wantAnnotations: map[string]string{"topology": "vm-topo", "note": "b"},
		},
		{
			name:    "rejects an annotation without =",
			args:    []string{"--annotation", "no-equals"},
			wantErr: `"no-equals" is not of the form key=value`,
		},
		{
			name:    "rejects an annotation without a key",
			args:    []string{"--annotation", " =value"},
			wantErr: `" =value" is not of the form key=value`,
		},
		{
			name:    "rejects a node annotation without a key",
			args:    []string{"--node-annotation", "=false"},
			wantErr: `"=false" is not of the form key=value`,
		},
		{
			name:    "rejects a node annotation value the default apps would ignore",
			args:    []string{"--node-annotation", "phenix/default-apps=no"},
			wantErr: "Unable to create the exp experiment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runExperimentCreate(t, tt.args...)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("experiment create exp -t vm-topo %q error = %v, want one containing %q", tt.args, err, tt.wantErr)
				}

				if _, err := experiment.Get("exp"); !errors.Is(err, store.ErrNotExist) {
					t.Errorf("experiment.Get(exp) after a rejected create error = %v, want %v", err, store.ErrNotExist)
				}

				return
			}

			if err != nil {
				t.Fatalf("experiment create exp -t vm-topo %q error = %v", tt.args, err)
			}

			exp, err := experiment.Get("exp")
			if err != nil {
				t.Fatalf("experiment.Get(exp) error = %v", err)
			}

			// the saved VM settings have tests of their own
			saved := cmpopts.IgnoreMapEntries(func(k, _ string) bool { return k == experiment.VMDefaultsAnnotation })
			if diff := cmp.Diff(tt.wantAnnotations, map[string]string(exp.Metadata.Annotations), saved); diff != "" {
				t.Errorf("experiment create exp -t vm-topo %q annotations mismatch (-want +got):\n%s", tt.args, diff)
			}

			nodes := exp.Spec.Topology().Nodes()
			if len(nodes) != 1 {
				t.Fatalf("experiment create exp -t vm-topo %q made %d nodes, want 1", tt.args, len(nodes))
			}

			if diff := cmp.Diff(tt.wantNode, nodes[0].Annotations(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("experiment create exp -t vm-topo %q VM annotations mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// experimentAnnotationValue draws a flag value: key=value with a key that can
// be blank, or text that can lack the =.
func experimentAnnotationValue(t *rapid.T) string {
	value := rapid.OneOf(
		rapid.Map(rapid.Bool(), strconv.FormatBool),
		rapid.SampledFrom([]string{" true ", "[1,2]", `{"a":1}`, "[bad", "{", "5", "", "a=b", "null"}),
		rapid.String(),
	)

	return rapid.OneOf(
		rapid.Map(rapid.StringMatching(`[ a-z/.-]{0,6}`), func(key string) string {
			return key + "=" + value.Draw(t, "value")
		}),
		rapid.StringMatching(`[ a-z=]{0,6}`),
	).Draw(t, "flag")
}

// Whatever the flag values, the annotations are the text before and after
// each value's first =, the last value of a key winning, and a value with no
// = or a blank key fails. A node annotation keeps true, false and a JSON
// list or object as that value, and any other text as it is.
func TestExperimentAnnotationParseProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		values := rapid.SliceOf(rapid.Custom(experimentAnnotationValue)).Draw(t, "values")

		want, wantNode := map[string]string{}, map[string]any{}
		bad, refused := "", false

		for _, value := range values {
			key, val, ok := strings.Cut(value, "=")
			if !ok || strings.TrimSpace(key) == "" {
				bad, refused = value, true

				break
			}

			want[key] = val

			var node any = val

			yes, no := strconv.FormatBool(true), strconv.FormatBool(false)

			switch trimmed := strings.TrimSpace(val); {
			case trimmed == yes, trimmed == no:
				node = trimmed == yes
			case (strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{")) && json.Valid([]byte(trimmed)):
				_ = json.Unmarshal([]byte(trimmed), &node)
			}

			wantNode[key] = node
		}

		got, err := parseAnnotationFlags(values)
		gotNode, nodeErr := parseNodeAnnotationFlags(values)

		if refused {
			for _, e := range []error{err, nodeErr} {
				if e == nil || !strings.Contains(e.Error(), strconv.Quote(bad)) {
					t.Fatalf("parsing %q error = %v, want one naming %q", values, e, bad)
				}
			}

			return
		}

		if err != nil || nodeErr != nil {
			t.Fatalf("parsing %q errors = %v, %v", values, err, nodeErr)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("parseAnnotationFlags(%q) mismatch (-want +got):\n%s", values, diff)
		}

		if diff := cmp.Diff(wantNode, gotNode); diff != "" {
			t.Fatalf("parseNodeAnnotationFlags(%q) mismatch (-want +got):\n%s", values, diff)
		}
	})
}
