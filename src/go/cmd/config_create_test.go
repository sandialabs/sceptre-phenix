package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/spf13/cobra"

	"phenix/store"
)

// configCreateTestTopology is a topology whose second node's drive has no
// image; the image key sits one level too high, under hardware.
const configCreateTestTopology = `apiVersion: phenix.sandia.gov/v1
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

// configCreateTestTopologyJSON is configCreateTestTopology as pretty-printed
// JSON, as jq writes it. config create reads a .json file as JSON, and the
// explained lines carry its line numbers.
const configCreateTestTopologyJSON = `{
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

// runConfigCreate runs "phenix config create <path>" with a store mock that
// fails the test on any call, and returns the command's error.
func runConfigCreate(t *testing.T, path string) error {
	t.Helper()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	original := store.DefaultStore
	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore test double

	store.DefaultStore = store.NewMockStore(ctrl) //nolint:reassign // monkey patching for test

	root := &cobra.Command{Use: "phenix", SilenceUsage: true}
	configCmd := newConfigCmd()
	configCmd.AddCommand(newConfigCreateCmd())
	root.AddCommand(configCmd)
	root.SetArgs([]string{"config", "create", path})

	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)

	_, err := root.ExecuteC()

	return err
}

func TestConfigCreateExplainsValidationErrors(t *testing.T) {
	tests := []struct {
		file    string
		content string
		want    []string
	}{
		{
			file:    "topology.yml",
			content: configCreateTestTopology,
			want: []string{
				`  nodes[1] "ADServer" (line 21): property "image" is missing (at hardware.drives[0].image)`,
				`    hint: "image:" is on line 19 under nodes[1].hardware, ` +
					`but the schema expects it at nodes[1].hardware.drives[0].image`,
				`  nodes[1] "ADServer" (line 14): property "external" is missing (at external)`,
			},
		},
		{
			// A pretty-printed JSON file spans many lines, so its lines are numbered too.
			file:    "broken.json",
			content: configCreateTestTopologyJSON,
			want: []string{
				`  nodes[1] "ADServer" (line 32): property "image" is missing (at hardware.drives[0].image)`,
				`    hint: "image:" is on line 30 under nodes[1].hardware, ` +
					`but the schema expects it at nodes[1].hardware.drives[0].image`,
				`  nodes[1] "ADServer" (line 23): property "external" is missing (at external)`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tt.file)
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("writing config: %v", err)
			}

			err := runConfigCreate(t, path)
			if err == nil {
				t.Fatal("expected a validation error")
			}

			lines := strings.Split(err.Error(), "\n")

			prefix := "Unable to create configuration from " + path + " (search error logs for "
			if !strings.HasPrefix(lines[0], prefix) {
				t.Errorf("first line:\n got %s\nwant prefix %s", lines[0], prefix)
			}

			if !slices.Equal(lines[1:], tt.want) {
				t.Errorf("explained lines:\n got %q\nwant %q", lines[1:], tt.want)
			}
		})
	}
}

func TestConfigCreateKeepsOtherErrorsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.yml")
	if err := os.WriteFile(path, []byte("kind: [\n"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	err := runConfigCreate(t, path)
	if err == nil {
		t.Fatal("expected a parse error")
	}

	prefix := "Unable to create configuration from " + path + " (search error logs for "
	if msg := err.Error(); !strings.HasPrefix(msg, prefix) || strings.Contains(msg, "\n") {
		t.Errorf("expected the single humanized line, got:\n%s", msg)
	}
}
