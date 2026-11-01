package mmtest_test

import (
	"context"
	"runtime"
	"slices"
	"strings"
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

// A truncated reply gives the rows sent before the connection closed and an
// error; a host's error leaves out only that host's rows.
func TestFakeTruncatesAndReportsHostErrors(t *testing.T) {
	header := []string{"name", "state"}

	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		switch cmd.Base {
		case "vm info summary":
			return mmtest.Truncated(mmtest.Tabular("head", header, []string{"vm1", "RUNNING"}))
		case "vm info":
			return []*minicli.Response{
				mmtest.Tabular("head", header, []string{"vm1", "RUNNING"}),
				mmtest.Error("compute1", "vm not found: vm2"),
			}
		}

		return nil
	})

	tests := map[string]struct {
		command string
		wantErr string
	}{
		"truncated":  {command: "vm info summary", wantErr: "server disconnected"},
		"host error": {command: "vm info", wantErr: "vm not found: vm2"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cmd := mmcli.NewCommand()
			cmd.Command = tc.command

			rows, err := mmcli.RunTabularCheckedContext(context.Background(), cmd, nil)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("RunTabularCheckedContext(%s) error = %v, want one containing %q", tc.command, err, tc.wantErr)
			}

			want := []map[string]string{{"host": "head", "name": "vm1", "state": "RUNNING"}}
			if diff := cmp.Diff(want, rows); diff != "" {
				t.Errorf("RunTabularCheckedContext(%s) rows mismatch (-want +got):\n%s", tc.command, diff)
			}
		})
	}

	// the connection a truncated reply closed is dialed again
	version := mmcli.NewCommand()
	version.Command = "version"

	if err := mmcli.ErrorResponse(mmcli.Run(version)); err != nil {
		t.Errorf("Run(version) after a truncated reply error = %v, want nil", err)
	}
}

// A handler may return the same Truncated reply on every call.
func TestFakeTruncatesAReusedReplyEveryTime(t *testing.T) {
	reply := mmtest.Truncated(mmtest.Tabular("head", []string{"name"}, []string{"vm1"}))

	mmtest.Use(t, func(mmtest.Command) []*minicli.Response { return reply })

	for i := range 2 {
		cmd := mmcli.NewCommand()
		cmd.Command = "vm info"

		if _, err := mmcli.RunTabularCheckedContext(context.Background(), cmd, nil); err == nil ||
			!strings.Contains(err.Error(), "server disconnected") {
			t.Errorf("call %d: RunTabularCheckedContext(vm info) error = %v, want one containing %q", i+1, err, "server disconnected")
		}
	}
}

// otherTest is a test named name that records whether it failed and runs its
// cleanups only when told to.
type otherTest struct {
	testing.TB

	name     string
	failed   bool
	cleanups []func()
}

func (o *otherTest) Name() string { return o.name }

func (o *otherTest) Helper() {}

func (o *otherTest) Cleanup(f func()) { o.cleanups = append(o.cleanups, f) }

func (o *otherTest) Fatalf(string, ...any) {
	o.failed = true

	runtime.Goexit()
}

func (o *otherTest) cleanup() {
	for _, f := range slices.Backward(o.cleanups) {
		f()
	}
}

// useAs calls Use as other on a goroutine of its own, as Fatalf ends it.
func useAs(other *otherTest) {
	done := make(chan struct{})

	go func() {
		defer close(done)

		mmtest.Use(other, nil)
	}()

	<-done
}

// Use fails a test while a test that is not it or its parent uses the fake.
func TestUseRefusesAnotherTestAtTheSameTime(t *testing.T) {
	mmtest.Use(t, nil)

	for _, tc := range []struct {
		name       string
		wantFailed bool
	}{
		{name: t.Name(), wantFailed: false},
		{name: t.Name() + "/subtest", wantFailed: false},
		{name: t.Name() + "Other", wantFailed: true},
		{name: "TestOther", wantFailed: true},
	} {
		other := &otherTest{TB: t, name: tc.name, failed: false, cleanups: nil}

		useAs(other)
		other.cleanup()

		if other.failed != tc.wantFailed {
			t.Errorf("Use as %q while %q uses the fake failed = %v, want %v", tc.name, t.Name(), other.failed, tc.wantFailed)
		}
	}

	// a subtest's sibling fails while the subtest runs, but not once it ended
	first := &otherTest{TB: t, name: t.Name() + "/a", failed: false, cleanups: nil}
	useAs(first)

	during := &otherTest{TB: t, name: t.Name() + "/b", failed: false, cleanups: nil}
	useAs(during)
	during.cleanup()
	first.cleanup()

	after := &otherTest{TB: t, name: t.Name() + "/b", failed: false, cleanups: nil}
	useAs(after)
	after.cleanup()

	if first.failed || !during.failed || after.failed {
		t.Errorf("Use as %q failed = %v, then as %q while it runs = %v and after it ended = %v, want false, true, false",
			first.name, first.failed, during.name, during.failed, after.failed)
	}
}
