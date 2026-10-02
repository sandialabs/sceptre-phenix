package cmd

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/util/common"
)

// runExperimentCreate runs `phenix experiment create exp -t vm-topo`
// with args added, against a new store holding the vm-topo topology of one VM
// named "vm".
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
		t.Fatal(err)
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
					t.Fatalf("create = %v, want an error containing %q", err, tt.wantErr)
				}

				if _, err := experiment.Get("exp"); !errors.Is(err, store.ErrNotExist) {
					t.Fatalf("the rejected experiment was stored: %v", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("create: %v", err)
			}

			exp, err := experiment.Get("exp")
			if err != nil {
				t.Fatal(err)
			}

			if got := exp.Metadata.Annotations; !reflect.DeepEqual(map[string]string(got), tt.wantAnnotations) {
				t.Errorf("experiment annotations = %v, want %v", got, tt.wantAnnotations)
			}

			nodes := exp.Spec.Topology().Nodes()
			if len(nodes) != 1 {
				t.Fatalf("experiment has %d nodes, want 1", len(nodes))
			}

			if got := nodes[0].Annotations(); len(got) != 0 || len(tt.wantNode) != 0 {
				if !reflect.DeepEqual(got, tt.wantNode) {
					t.Errorf("VM annotations = %#v, want %#v", got, tt.wantNode)
				}
			}
		})
	}
}
