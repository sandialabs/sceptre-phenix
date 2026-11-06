package cmd

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"

	"phenix/api/config"
	"phenix/store/storetest"
	"phenix/util/mm"
	"phenix/web/rbac"
)

// utilCmdMM is a minimega with one schedulable host and one VM. Any other
// method panics, since the embedded interface is nil.
type utilCmdMM struct {
	mm.MM

	hostErr error
}

func (m utilCmdMM) GetClusterHosts(bool) (mm.Hosts, error) {
	if m.hostErr != nil {
		return nil, m.hostErr
	}

	return mm.Hosts{{Name: "compute1", Schedulable: true}}, nil
}

func (utilCmdMM) GetVMInfo(...mm.Option) mm.VMs {
	return mm.VMs{{Name: "web", Host: "compute1"}}
}

// utilCmdUseMM makes m mm.DefaultMM until the test ends.
func utilCmdUseMM(t *testing.T, m mm.MM) {
	t.Helper()

	original := mm.DefaultMM
	mm.DefaultMM = m //nolint:reassign // the test's minimega

	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore minimega
}

// utilCmdRun runs phenix util with args and returns what the command writes
// to its output, and its error.
func utilCmdRun(args ...string) (string, error) {
	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	utilCmd := newUtilCmd()
	utilCmd.AddCommand(newUtilAppJSONCmd(), newUtilRoleTableCmd())
	root.AddCommand(utilCmd)
	root.SetArgs(append([]string{"util"}, args...))

	var out bytes.Buffer

	root.SetOut(&out)
	root.SetErr(io.Discard)

	_, err := root.ExecuteC()

	return out.String(), err
}

// phenix util role-table prints a CSV row for each permission, with a column
// for each role, or a table with --pretty.
func TestUtilCmdRoleTable(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)

	for _, name := range []string{"global-admin", "disabled"} {
		if _, err := config.CreateDefault("role", name); err != nil {
			t.Fatalf("config.CreateDefault(role, %s) error = %v", name, err)
		}
	}

	out, err := utilCmdRun("role-table")
	if err != nil {
		t.Fatalf("phenix util role-table error = %v", err)
	}

	rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("reading the CSV of phenix util role-table: %v", err)
	}

	if len(rows) != len(rbac.Permissions)+1 {
		t.Fatalf("phenix util role-table printed %d rows, want %d", len(rows), len(rbac.Permissions)+1)
	}

	if diff := cmp.Diff([]string{"", "", "Disabled", "Global Admin"}, rows[0]); diff != "" {
		t.Errorf("phenix util role-table header mismatch (-want +got):\n%s", diff)
	}

	for i, p := range rbac.Permissions {
		want := []string{p.Resource, p.Verb, "", "Y"}
		if diff := cmp.Diff(want, rows[i+1]); diff != "" {
			t.Errorf("phenix util role-table row of %s %s mismatch (-want +got):\n%s", p.Resource, p.Verb, diff)
		}
	}

	pretty, err := utilCmdRun("role-table", "--pretty")
	if err != nil {
		t.Fatalf("phenix util role-table --pretty error = %v", err)
	}

	for _, cell := range []string{"DISABLED", "GLOBAL ADMIN", "Y"} {
		if !vmCmdTableHas(pretty, cell) {
			t.Errorf("phenix util role-table --pretty has no cell %q:\n%s", cell, pretty)
		}
	}
}

// phenix util role-table returns an error, and prints nothing, when it cannot
// get the roles.
func TestUtilCmdRoleTableStoreError(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	vlanCmdUseFailingStore(t)

	out, err := utilCmdRun("role-table")
	if !vmCmdHumanized(err, "Unable to get the roles") {
		t.Errorf("phenix util role-table error = %v, want a humanized error that starts with %q", err, "Unable to get the roles")
	}

	if out != "" {
		t.Errorf("phenix util role-table output = %q, want none", out)
	}
}

// phenix util app-json prints the experiment with its runtime details as
// JSON, indented with --pretty.
func TestUtilCmdAppJSON(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	vmCmdUseStore(t, vmCmdExperiment(t, "exp", false))
	utilCmdUseMM(t, utilCmdMM{})

	type named struct {
		Name string `json:"name"`
	}

	type runtime struct {
		Metadata named   `json:"metadata"`
		Hosts    []named `json:"hosts"`
		VMs      []named `json:"vms"`
	}

	want := runtime{Metadata: named{Name: "exp"}, Hosts: []named{{Name: "compute1"}}, VMs: []named{{Name: "web"}}}

	for _, args := range [][]string{{"app-json", "exp"}, {"app-json", "exp", "--pretty"}} {
		out, err := utilCmdRun(args...)
		if err != nil {
			t.Fatalf("phenix util %s error = %v", strings.Join(args, " "), err)
		}

		var got runtime
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("decoding the JSON of phenix util %s: %v", strings.Join(args, " "), err)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("phenix util %s mismatch (-want +got):\n%s", strings.Join(args, " "), diff)
		}

		if indented := strings.Contains(out, "\n  "); indented != (len(args) == 3) {
			t.Errorf("phenix util %s output indented = %t, want %t", strings.Join(args, " "), indented, len(args) == 3)
		}
	}
}

// phenix util app-json returns an error for a missing name or experiment,
// and when it cannot read the runtime details.
func TestUtilCmdAppJSONErrors(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	vmCmdUseStore(t, vmCmdExperiment(t, "exp", false))
	utilCmdUseMM(t, utilCmdMM{hostErr: errors.New("no cluster hosts found")})

	tests := []struct {
		args      []string
		wantErr   string
		humanized string
	}{
		{args: []string{"app-json"}, wantErr: "there was no experiment provided"},
		{args: []string{"app-json", "missing"}, humanized: "Unable to get the missing experiment"},
		{args: []string{"app-json", "exp"}, humanized: "Unable to collect runtime experiment information"},
	}

	for _, test := range tests {
		out, err := utilCmdRun(test.args...)
		if msg := expCmdCheckErr(err, test.wantErr, test.humanized); msg != "" {
			t.Errorf("phenix util %s %s", strings.Join(test.args, " "), msg)
		}

		if out != "" {
			t.Errorf("phenix util %s output = %q, want none", strings.Join(test.args, " "), out)
		}
	}
}
