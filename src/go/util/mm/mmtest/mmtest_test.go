package mmtest_test

import (
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"

	"phenix/util/mm/mmcli"
	"phenix/util/mm/mmtest"
)

func TestFakeAnswersAndRecordsCommands(t *testing.T) {
	received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		switch cmd.Base {
		case "vm info":
			return []*minicli.Response{mmtest.Tabular("head", []string{"name", "src port"}, []string{"vm1", "22"})}
		case "quit":
			return mmtest.Disconnect()
		}

		return nil
	})

	info := mmcli.NewNamespacedCommand("exp")
	info.Command = "vm info"
	info.Columns = []string{"name", "src port"}
	info.Filters = []string{"'src port'=22", "name=vm1"}

	wantRows := []map[string]string{{"name": "vm1", "src port": "22"}}
	if diff := cmp.Diff(wantRows, mmcli.RunTabular(info)); diff != "" {
		t.Errorf("RunTabular(vm info) mismatch (-want +got):\n%s", diff)
	}

	version := mmcli.NewCommand()
	version.Command = "version"

	if err := mmcli.ErrorResponse(mmcli.Run(version)); err != nil {
		t.Errorf("Run(version) with no answer from the handler error = %v, want nil", err)
	}

	quit := mmcli.NewCommand()
	quit.Command = "quit"

	if err := mmcli.ErrorResponse(mmcli.Run(quit)); err == nil {
		t.Error("Run(quit) answered by Disconnect error = nil, want a lost connection")
	}

	want := []mmtest.Command{
		{Raw: info.String(), Namespace: "exp", Base: "vm info"},
		{Raw: version.String(), Namespace: "", Base: "version"},
		{Raw: quit.String(), Namespace: "", Base: "quit"},
	}

	if diff := cmp.Diff(want, received()); diff != "" {
		t.Errorf("received() mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"vm info", "version", "quit"}, mmtest.Bases(want)); diff != "" {
		t.Errorf("Bases(%+v) mismatch (-want +got):\n%s", want, diff)
	}

	if got := mmtest.Count(want, "v"); got != 2 {
		t.Errorf("Count(%+v, %q) = %d, want 2", want, "v", got)
	}

	mmtest.Await(t, "quit")
}
