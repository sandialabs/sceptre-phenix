package experiment

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// useVLANCluster points minimega at a fake whose namespaces give the VLAN aliases, until the test ends. With down
// set, it refuses to list the namespaces.
func useVLANCluster(t *testing.T, namespaces map[string]map[string]string, down bool) {
	t.Helper()

	original := mm.DefaultMM
	mm.DefaultMM = new(mm.Minimega) //nolint:reassign // the real one, against the fake minimega

	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore

	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		switch cmd.Base {
		case "namespace":
			if down {
				return []*minicli.Response{mmtest.Error("head", "minimega is down")}
			}

			rows := make([][]string, 0, len(namespaces))
			for name := range namespaces {
				rows = append(rows, []string{name, "", "false"})
			}

			return []*minicli.Response{mmtest.Tabular("head", []string{"namespace", "vlans", "active"}, rows...)}
		case "vlans":
			rows := make([][]string, 0, len(namespaces[cmd.Namespace]))
			for alias, id := range namespaces[cmd.Namespace] {
				rows = append(rows, []string{alias, id})
			}

			return []*minicli.Response{mmtest.Tabular("head", []string{"alias", "vlan"}, rows...)}
		}

		return nil
	})
}

// explainVLANInUse names the pinned ID that minimega gives to another alias, of another namespace or of the
// experiment's own, and keeps any other error as it is.
//
//nolint:paralleltest // replaces minimega
func TestExplainVLANInUse(t *testing.T) {
	inUse := fmt.Errorf("reading mmcli script: %w", errors.New("vlan already in use"))
	other := errors.New("vm launch failed")
	cluster := map[string]map[string]string{
		"exp":   {"A": "101", "C": "103"},
		"other": {"X": "102"},
	}

	tests := map[string]struct {
		pins    map[string]int
		err     error
		down    bool
		want    *VLANInUseError // nil when explainVLANInUse returns err; it wraps err
		wantErr error
	}{
		"no error":      {pins: map[string]int{"A": 101}, err: nil, wantErr: nil},
		"another error": {pins: map[string]int{"A": 101}, err: other, wantErr: other},
		"another namespace": {
			pins: map[string]int{"A": 101, "B": 102}, err: inUse, want: &VLANInUseError{ID: 102, Alias: "B", Err: inUse},
		},
		"another alias": {
			pins: map[string]int{"A": 101, "B": 103}, err: inUse, want: &VLANInUseError{ID: 103, Alias: "B", Err: inUse},
		},
		"no pin in use":       {pins: map[string]int{"A": 101, "D": 0}, err: inUse, wantErr: inUse},
		"minimega cannot say": {pins: map[string]int{"B": 102}, err: inUse, down: true, wantErr: inUse},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			useVLANCluster(t, cluster, test.down)

			err := explainVLANInUse(context.Background(), "exp", test.pins, test.err)

			if test.want == nil {
				if err != test.wantErr { //nolint:errorlint // the same error, not one that wraps it
					t.Errorf("explainVLANInUse(%v) = %v, want %v", test.err, err, test.wantErr)
				}

				return
			}

			var got *VLANInUseError
			if !errors.As(err, &got) {
				t.Fatalf("explainVLANInUse(%v) = %v, want a *VLANInUseError", test.err, err)
			}

			if got.ID != test.want.ID || got.Alias != test.want.Alias {
				t.Errorf("explainVLANInUse(%v) = ID %d of %s, want ID %d of %s", test.err, got.ID, got.Alias,
					test.want.ID, test.want.Alias)
			}

			if !errors.Is(err, ErrVLANInUse) || !errors.Is(err, test.err) {
				t.Errorf("explainVLANInUse(%v) = %v, want it to match ErrVLANInUse and wrap %v", test.err, err, test.err)
			}
		})
	}
}
