package experiment_test

import (
	"context"
	"strings"
	"testing"

	"phenix/api/experiment"
)

func TestCreateRejectsInvalidOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []experiment.CreateOption
		want string // in the error, naming what was rejected
	}{
		{name: "the all name", opts: withName("all"), want: "cannot use 'all'"},
		{name: "minimega's default namespace", opts: withName("minimega"), want: "cannot use 'minimega'"},
		{name: "a reserved name in any case", opts: withName("MiniMega"), want: "cannot use 'MiniMega'"},
		{name: "phenix's cluster namespace", opts: withName("__phenix__"), want: "cannot use '__phenix__'"},
		{name: "a reserved namespace in any case", opts: withName("__PHENIX__"), want: "cannot use '__PHENIX__'"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// rejected before the store is read
			err := experiment.Create(context.Background(), tc.opts...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Create(%s) error = %v, want one containing %s", tc.name, err, tc.want)
			}
		})
	}
}

func withName(name string) []experiment.CreateOption {
	return []experiment.CreateOption{experiment.CreateWithName(name), experiment.CreateWithTopology("topo")}
}
