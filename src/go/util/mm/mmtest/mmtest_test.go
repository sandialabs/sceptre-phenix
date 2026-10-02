package mmtest_test

import (
	"reflect"
	"testing"

	"github.com/activeshadow/libminimega/minicli"

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

	if rows := mmcli.RunTabular(info); len(rows) != 1 || rows[0]["name"] != "vm1" {
		t.Fatalf("vm info rows = %v, want vm1's", rows)
	}

	version := mmcli.NewCommand()
	version.Command = "version"

	if err := mmcli.ErrorResponse(mmcli.Run(version)); err != nil {
		t.Fatalf("unanswered command failed: %v", err)
	}

	quit := mmcli.NewCommand()
	quit.Command = "quit"

	if err := mmcli.ErrorResponse(mmcli.Run(quit)); err == nil {
		t.Fatal("command answered by a disconnect succeeded")
	}

	want := []mmtest.Command{
		{Raw: info.String(), Namespace: "exp", Base: "vm info"},
		{Raw: version.String(), Namespace: "", Base: "version"},
		{Raw: quit.String(), Namespace: "", Base: "quit"},
	}

	if got := received(); !reflect.DeepEqual(got, want) {
		t.Fatalf("received\n%+v\nwant\n%+v", got, want)
	}

	if got := mmtest.Bases(want); !reflect.DeepEqual(got, []string{"vm info", "version", "quit"}) {
		t.Fatalf("Bases = %q", got)
	}

	if got := mmtest.Count(want, "v"); got != 2 {
		t.Fatalf("Count(v) = %d, want 2", got)
	}

	mmtest.Await(t, "quit")
}
