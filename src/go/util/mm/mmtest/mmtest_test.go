package mmtest_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"

	"phenix/util/common"
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
	errors   []string
	cleanups []func()
}

func (o *otherTest) Name() string { return o.name }

func (o *otherTest) Helper() {}

func (o *otherTest) Cleanup(f func()) { o.cleanups = append(o.cleanups, f) }

func (o *otherTest) Errorf(format string, args ...any) {
	o.errors = append(o.errors, fmt.Sprintf(format, args...))
}

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
		other := &otherTest{TB: t, name: tc.name, failed: false, errors: nil, cleanups: nil}

		useAs(other)
		other.cleanup()

		if other.failed != tc.wantFailed {
			t.Errorf("Use as %q while %q uses the fake failed = %v, want %v", tc.name, t.Name(), other.failed, tc.wantFailed)
		}
	}

	// a subtest's sibling fails while the subtest runs, but not once it ended
	first := &otherTest{TB: t, name: t.Name() + "/a", failed: false, errors: nil, cleanups: nil}
	useAs(first)

	during := &otherTest{TB: t, name: t.Name() + "/b", failed: false, errors: nil, cleanups: nil}
	useAs(during)
	during.cleanup()
	first.cleanup()

	after := &otherTest{TB: t, name: t.Name() + "/b", failed: false, errors: nil, cleanups: nil}
	useAs(after)
	after.cleanup()

	if first.failed || !during.failed || after.failed {
		t.Errorf("Use as %q failed = %v, then as %q while it runs = %v and after it ended = %v, want false, true, false",
			first.name, first.failed, during.name, during.failed, after.failed)
	}
}

func TestResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  *minicli.Response
		want *minicli.Response
	}{
		{name: "Text", got: mmtest.Text("h1", "body"), want: &minicli.Response{Host: "h1", Response: "body"}},
		{name: "empty Text", got: mmtest.Text("", ""), want: &minicli.Response{}},
		{name: "Error", got: mmtest.Error("h2", "failed"), want: &minicli.Response{Host: "h2", Error: "failed"}},
		{
			name: "Tabular",
			got:  mmtest.Tabular("h3", []string{"name"}, []string{"vm1"}, []string{"vm2"}),
			want: &minicli.Response{Host: "h3", Header: []string{"name"}, Tabular: [][]string{{"vm1"}, {"vm2"}}},
		},
		{name: "Tabular without rows", got: mmtest.Tabular("h4", nil), want: &minicli.Response{Host: "h4"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, tc.got); diff != "" {
				t.Errorf("mmtest.%s mismatch (-want +got):\n%s", tc.name, diff)
			}
		})
	}
}

// Truncated adds its marker to a copy, leaving the caller's responses alone,
// and Disconnect is a reply of its own.
func TestTruncatedAndDisconnect(t *testing.T) {
	t.Parallel()

	resps := []*minicli.Response{mmtest.Text("h", "a"), mmtest.Text("h", "b")}
	reply := mmtest.Truncated(slices.Clip(resps)...)

	if len(reply) != 3 || reply[0] != resps[0] || reply[1] != resps[1] {
		t.Errorf("Truncated(%d responses) = %d responses, want them followed by its marker", len(resps), len(reply))
	}

	if len(resps) != 2 || cap(resps) != 2 {
		t.Errorf("Truncated changed its argument to len %d, cap %d; want 2, 2", len(resps), cap(resps))
	}

	if got := mmtest.Truncated(); len(got) != 1 {
		t.Errorf("Truncated() = %d responses, want only its marker", len(got))
	}

	if got := mmtest.Disconnect(); len(got) != 1 || got[0] == reply[2] {
		t.Errorf("Disconnect() = %v, want one marker that is not Truncated's", got)
	}
}

// mmtestDial connects to the fake minimega's socket as miniclient does.
func mmtestDial(t *testing.T) net.Conn {
	t.Helper()

	var dialer net.Dialer

	conn, err := dialer.DialContext(t.Context(), "unix", path.Join(common.MinimegaBase, "minimega"))
	if err != nil {
		t.Fatalf("dialing the fake minimega: %v", err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	return conn
}

// The fake strips the prefixes mmcli adds, in any order and number, to find
// a command's namespace and base, and leaves anything else in the base.
func TestFakeParsesCommands(t *testing.T) {
	received := mmtest.Use(t, nil)

	tests := []struct {
		raw, namespace, base string
	}{
		{raw: "vm info", base: "vm info"},
		{raw: "", base: ""},
		{
			raw:       `.record false namespace "exp" .columns "name","src port" .filter 'src port'=22 .filter name=vm1 vm info`,
			namespace: "exp", base: "vm info",
		},
		{raw: `namespace "" vm start all`, base: "vm start all"},
		{raw: `.columns "name" namespace "a b" vm info`, namespace: "a b", base: "vm info"},
		{raw: `namespace exp vm info`, base: `namespace exp vm info`},
		{raw: `.filter name=vm1`, base: `.filter name=vm1`},
		{raw: `.record true vm info`, base: `.record true vm info`},
		{raw: `.record false .preprocess false shell echo $HOME`, base: `shell echo $HOME`},
		{raw: `vm info .filter name=vm1 `, base: `vm info .filter name=vm1 `},
	}

	conn := mmtestDial(t)
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)

	for _, tc := range tests {
		if err := enc.Encode(miniclient.Request{Command: tc.raw}); err != nil {
			t.Fatalf("sending %q: %v", tc.raw, err)
		}

		var resp miniclient.Response
		if err := dec.Decode(&resp); err != nil {
			t.Fatalf("reading the answer to %q: %v", tc.raw, err)
		}

		// nil answers with one empty success from head
		want := miniclient.Response{Resp: minicli.Responses{{Host: "head"}}}
		if diff := cmp.Diff(want, resp); diff != "" {
			t.Errorf("answer to %q mismatch (-want +got):\n%s", tc.raw, diff)
		}
	}

	want := make([]mmtest.Command, 0, len(tests))
	for _, tc := range tests {
		want = append(want, mmtest.Command{Raw: tc.raw, Namespace: tc.namespace, Base: tc.base})
	}

	if diff := cmp.Diff(want, received()); diff != "" {
		t.Errorf("received() mismatch (-want +got):\n%s", diff)
	}
}

// As minimega expands environment variables in every argument, the fake fails the test for a command whose "$" would
// expand, unless the command turns preprocessing off, and answers it with an error.
func TestFakeFailsCommandsMinimegaWouldExpand(t *testing.T) {
	other := &otherTest{TB: t, name: t.Name(), failed: false, errors: nil, cleanups: nil}
	received := mmtest.Use(other, func(mmtest.Command) []*minicli.Response {
		return []*minicli.Response{mmtest.Text("head", "handled")}
	})

	conn := mmtestDial(t)
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)

	for _, tc := range []struct {
		raw     string
		wantErr bool
	}{
		{raw: `shell sh -c "dd if=\"$1\" count=1"`, wantErr: true},
		{raw: `namespace "exp" vm config disk ${DISK}.qcow2`, wantErr: true},
		{raw: `.record false .preprocess false shell sh -c "echo $1"`, wantErr: false},
		{raw: `shell sh -c "echo $(df /) $"`, wantErr: false}, // os.ExpandEnv keeps "$(" and a trailing "$"
		{raw: "vm info", wantErr: false},
	} {
		other.errors = nil

		if err := enc.Encode(miniclient.Request{Command: tc.raw}); err != nil {
			t.Fatalf("sending %q: %v", tc.raw, err)
		}

		var resp miniclient.Response
		if err := dec.Decode(&resp); err != nil {
			t.Fatalf("reading the answer to %q: %v", tc.raw, err)
		}

		gotErr := len(resp.Resp) == 1 && strings.Contains(resp.Resp[0].Error, "would expand")
		if gotErr != tc.wantErr || (len(other.errors) > 0) != tc.wantErr {
			t.Errorf("answer to %q = %+v with test errors %q, want an expansion error %t", tc.raw, resp.Resp, other.errors, tc.wantErr)
		}
	}

	other.cleanup()

	if got := len(received()); got != 5 {
		t.Errorf("received() = %d commands, want 5", got)
	}
}

// The fake closes a connection that sends something other than a command,
// without recording anything.
func TestFakeClosesAConnectionSendingGarbage(t *testing.T) {
	received := mmtest.Use(t, nil)
	conn := mmtestDial(t)

	if _, err := conn.Write([]byte("not json\n")); err != nil {
		t.Fatalf("writing to the fake minimega: %v", err)
	}

	if n, err := conn.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Errorf("reading after garbage = %d bytes, %v; want 0, %v", n, err, io.EOF)
	}

	if got := received(); len(got) != 0 {
		t.Errorf("received() after garbage = %+v, want nothing", got)
	}
}
