package experiment_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/util/mm"
)

// appInputMM is a minimega of one schedulable host and one VM, whose host
// query fails with hostErr when it is set.
type appInputMM struct {
	mm.MM

	hostErr error
}

func (m appInputMM) GetClusterHosts(bool, bool) (mm.Hosts, error) {
	return mm.Hosts{{Name: "compute1", Schedulable: true}}, m.hostErr
}

func (appInputMM) GetVMInfo(...mm.Option) mm.VMs {
	return mm.VMs{{Name: "web", Host: "compute1"}}
}

// AppInput gives the stored experiment with the hosts and VMs, the store's
// error for a missing experiment, and ErrAppRuntime when minimega fails.
func TestAppInput(t *testing.T) { //nolint:paralleltest // replaces the store and minimega
	storetest.Use(t)

	c := &store.Config{
		Version: "phenix.sandia.gov/v1", Kind: "Experiment", Metadata: store.ConfigMetadata{Name: "exp"},
		Spec: map[string]any{"experimentName": "exp", "topology": map[string]any{"nodes": []any{}}},
	}
	if err := store.Create(c); err != nil {
		t.Fatalf("storing experiment exp: %v", err)
	}

	errHosts := errors.New("no cluster hosts")

	for name, tc := range map[string]struct {
		exp     string
		mm      appInputMM
		wantErr error
	}{
		"a stored experiment":   {exp: "exp"},
		"a missing experiment":  {exp: "missing", wantErr: store.ErrNotExist},
		"a minimega that fails": {exp: "exp", mm: appInputMM{hostErr: errHosts}, wantErr: experiment.ErrAppRuntime},
	} {
		t.Run(name, func(t *testing.T) {
			original := mm.DefaultMM
			mm.DefaultMM = tc.mm //nolint:reassign // install test double

			t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore

			got, err := experiment.AppInput(t.Context(), tc.exp)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil) != (err == nil) {
				t.Fatalf("AppInput(%s) error = %v, want %v", tc.exp, err, tc.wantErr)
			}

			if err != nil {
				return
			}

			if got.Metadata.Name != "exp" {
				t.Errorf("AppInput(exp) name = %q, want exp", got.Metadata.Name)
			}

			if diff := cmp.Diff(mm.Hosts{{Name: "compute1", Schedulable: true}}, got.Hosts); diff != "" {
				t.Errorf("AppInput(exp) hosts mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(mm.VMs{{Name: "web", Host: "compute1"}}, got.VMs); diff != "" {
				t.Errorf("AppInput(exp) VMs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
