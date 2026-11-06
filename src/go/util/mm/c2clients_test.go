package mm

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"pgregory.net/rapid"

	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
)

func TestActiveC2ClientsChecksAllVMsWithTwoCommands(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		switch cmd.Base {
		case vmInfoCmd:
			return []*minicli.Response{
				mmtest.Tabular("head", []string{"name", "uuid"},
					[]string{"Web", "uuid-web-upper"},
					[]string{"web", "uuid-web"},
					[]string{"db", "uuid-db"},
				),
				mmtest.Tabular("compute1", []string{"name", "uuid"},
					[]string{"Win", "UUID-WIN"},
					[]string{"idle", "uuid-idle"},
				),
			}
		case ccClientCmd:
			header := []string{"uuid", "hostname", "arch", "os", "ip", "mac"}

			return []*minicli.Response{
				mmtest.Tabular("head", header,
					// web registered under its topology name, db only by UUID
					[]string{"uuid-web", "WEB", "amd64", "linux", "[]", "[]"},
					[]string{"uuid-db", "localhost", "amd64", "linux", "[]", "[]"},
				),
				mmtest.Tabular("compute1", header,
					[]string{"uuid-win", "DESKTOP-1234", "amd64", "windows", "[]", "[]"},
				),
			}
		}

		return nil
	})

	refs := []C2ClientRef{
		{VM: "web", ByUUID: false},  // hostname matches ignoring case
		{VM: "db", ByUUID: false},   // reports another hostname
		{VM: "db", ByUUID: true},    // but its UUID is registered
		{VM: "WIN", ByUUID: true},   // name and UUID both fold case
		{VM: "idle", ByUUID: true},  // no client
		{VM: "gone", ByUUID: false}, // no such VM
	}

	got := Minimega{}.ActiveC2Clients("exp", refs)

	want := map[C2ClientRef]bool{
		{VM: "web", ByUUID: false}:  true,
		{VM: "db", ByUUID: false}:   false,
		{VM: "db", ByUUID: true}:    true,
		{VM: "WIN", ByUUID: true}:   true,
		{VM: "idle", ByUUID: true}:  false,
		{VM: "gone", ByUUID: false}: false,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ActiveC2Clients(exp, %+v) mismatch (-want +got):\n%s", refs, diff)
	}

	wantCmds := []mmtest.Command{{Namespace: "exp", Base: vmInfoCmd}, {Namespace: "exp", Base: ccClientCmd}}

	if diff := cmp.Diff(wantCmds, received(), ignoreRaw, sortCommands); diff != "" {
		t.Errorf("ActiveC2Clients(exp, %+v) sent commands mismatch (-want +got):\n%s", refs, diff)
	}
}

// The batched check must agree with c2Client, which filters in minimega, for
// every VM it can answer for.
func TestActiveC2ClientsMatchesC2Client(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	vms := [][]string{{"VM1", "uuid-upper"}, {"vm1", "uuid-lower"}, {"vm2", "uuid-2"}}
	clients := [][]string{{"uuid-lower", "vm1"}, {"uuid-2", "[vm2 alias]"}}

	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		switch cmd.Base {
		case vmInfoCmd:
			return []*minicli.Response{filtered(cmd, []string{"name", "uuid"}, vms)}
		case ccClientCmd:
			return []*minicli.Response{filtered(cmd, []string{"uuid", "hostname"}, clients)}
		}

		return nil
	})

	for _, ref := range []C2ClientRef{
		{VM: "vm1", ByUUID: true}, {VM: "VM1", ByUUID: true}, {VM: "vm1", ByUUID: false},
		{VM: "VM1", ByUUID: false}, {VM: "vm2", ByUUID: false}, {VM: "vm2", ByUUID: true},
	} {
		opts := []C2Option{C2NS("exp"), C2VM(ref.VM), C2Timeout(50 * time.Millisecond)}
		if ref.ByUUID {
			opts = append(opts, C2IDClientsByUUID())
		}

		want := Minimega{}.IsC2ClientActive(opts...) == nil

		if got := (Minimega{}).ActiveC2Clients("exp", []C2ClientRef{ref})[ref]; got != want {
			t.Errorf("ActiveC2Clients(exp, %+v) = %t, want %t as IsC2ClientActive reports", ref, got, want)
		}
	}
}

func TestC2ClientUsesNarrowVMInfo(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		switch cmd.Base {
		case vmInfoCmd:
			// minicli's name filter folds case, so both come back
			return []*minicli.Response{mmtest.Tabular("head", []string{"name", "uuid"},
				[]string{"VM1", "uuid-upper"},
				[]string{"vm1", "uuid-lower"},
			)}
		case ccClientCmd:
			return []*minicli.Response{mmtest.Tabular("head", []string{"uuid"}, []string{"uuid-lower"})}
		}

		return nil
	})

	name, uuid, err := Minimega{}.c2Client(NewC2Options(
		C2NS("exp"), C2VM("vm1"), C2Context(context.Background()), C2IDClientsByUUID(),
	))
	if err != nil {
		t.Fatalf("c2Client(vm1 by UUID) error = %v", err)
	}

	if name != "vm1" || uuid != "uuid-lower" {
		t.Errorf("c2Client(vm1 by UUID) = %q, %q; want %q, %q", name, uuid, "vm1", "uuid-lower")
	}

	cmds := received()

	if diff := cmp.Diff([]string{vmInfoCmd, ccClientCmd}, mmtest.Bases(cmds)); diff != "" {
		t.Fatalf("c2Client(vm1 by UUID) sent commands mismatch (-want +got):\n%s", diff)
	}

	if want := `.record false namespace "exp" .columns "name","uuid" .filter name=vm1 vm info`; cmds[0].Raw != want {
		t.Errorf("c2Client(vm1 by UUID) sent %q, want %q", cmds[0].Raw, want)
	}
}

// vm1WithoutClient answers `vm info` with vm1, which has no client registered.
func vm1WithoutClient(cmd mmtest.Command) []*minicli.Response {
	if cmd.Base == vmInfoCmd {
		return []*minicli.Response{mmtest.Tabular("head", []string{"name", "uuid"}, []string{"vm1", "uuid-1"})}
	}

	return nil
}

func TestIsC2ClientActiveGivesUpAtTimeout(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useFakeMinimega(t, vm1WithoutClient)

	start := time.Now()

	err := Minimega{}.IsC2ClientActive(C2NS("exp"), C2VM("vm1"), C2Timeout(20*time.Millisecond))
	if !errors.Is(err, ErrC2ClientNotActive) {
		t.Errorf("IsC2ClientActive(vm1, 20ms timeout) = %v, want %v", err, ErrC2ClientNotActive)
	}

	if elapsed := time.Since(start); elapsed >= c2ActiveCheckInterval {
		t.Errorf("IsC2ClientActive(vm1, 20ms timeout) gave up after %v, want before the next check at %v", elapsed, c2ActiveCheckInterval)
	}
}

func TestIsC2ClientActiveGivesUpOnCancel(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, vm1WithoutClient)

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)

	go func() { errc <- Minimega{}.IsC2ClientActive(C2NS("exp"), C2VM("vm1"), C2Context(ctx)) }()

	// Cancel while it waits to check again.
	polltest.Until(t, "the client was checked twice", func() bool { return mmtest.Count(received(), ccClientCmd) == 2 })
	cancel()

	canceled := time.Now()

	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Errorf("IsC2ClientActive(vm1, canceled ctx) = %v, want %v", err, context.Canceled)
	}

	if elapsed := time.Since(canceled); elapsed >= c2ActiveCheckInterval/2 {
		t.Errorf("IsC2ClientActive(vm1, canceled ctx) returned %v after the cancel, want under %v", elapsed, c2ActiveCheckInterval/2)
	}
}

var filterPrefix = regexp.MustCompile(`\.filter (\w+)=(\S*) `)

// filtered answers cmd from rows as minicli would, applying a `.filter
// <column>=<value>` prefix (the only kind c2Client sends) with filterMatches.
func filtered(cmd mmtest.Command, header []string, rows [][]string) *minicli.Response {
	m := filterPrefix.FindStringSubmatch(cmd.Raw)
	if m == nil {
		return mmtest.Tabular("head", header, rows...)
	}

	column := -1

	for i, h := range header {
		if h == m[1] {
			column = i
		}
	}

	var kept [][]string

	for _, row := range rows {
		if column >= 0 && filterMatches(m[2], row[column]) {
			kept = append(kept, row)
		}
	}

	return mmtest.Tabular("head", header, kept...)
}

func TestFilterSafe(t *testing.T) {
	t.Parallel()

	for value, want := range map[string]bool{
		"vm1": true, "web-01.example": true, "a!b": true, "[a,b]": true, "ünï": true,
		"": false, "a=b": false, "a~b": false, "a b": false, `a"b`: false, "a'b": false, "a`b": false,
		`a\b`: false, "a\tb": false, "a\rb": false, "a\nb": false, "=": false,
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			if got := filterSafe(value); got != want {
				t.Errorf("filterSafe(%q) = %t, want %t", value, got, want)
			}
		})
	}
}

func TestFilterMatches(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		want, cell string
		match      bool
	}{
		{want: "vm1", cell: "vm1", match: true},
		{want: "VM1", cell: "vm1", match: true},
		{want: "vm1", cell: "VM1", match: true},
		{want: "", cell: "", match: true},
		{want: "vm", cell: "vm1", match: false},
		{want: "vm1", cell: "vm", match: false},
		{want: "vm1", cell: "", match: false},
		{want: "vm2", cell: "[vm1 vm2]", match: true},
		{want: "VM2", cell: "[vm1 vm2]", match: true},
		{want: "vm", cell: "[vm1 vm2]", match: true},
		{want: "k", cell: "{k:v}", match: true},
		{want: "vm3", cell: "[vm1 vm2]", match: false},
		{want: "vm1", cell: "[vm1", match: false},
		{want: "vm1", cell: "vm1]", match: false},
		{want: "vm1", cell: "[vm1}", match: false},
		{want: "vm1", cell: "(vm1)", match: false},
		{want: "", cell: "[]", match: true},
	} {
		t.Run(tc.want+" in "+tc.cell, func(t *testing.T) {
			t.Parallel()

			if got := filterMatches(tc.want, tc.cell); got != tc.match {
				t.Errorf("filterMatches(%q, %q) = %t, want %t", tc.want, tc.cell, got, tc.match)
			}
		})
	}
}

// filterMatches ignores case, and a plain cell matches only itself.
func TestFilterMatchesProperties(t *testing.T) {
	t.Parallel()

	gen := rapid.StringMatching(`[\[\]{}a-cA-C ]{0,6}`)

	rapid.Check(t, func(t *rapid.T) {
		want, cell := gen.Draw(t, "want"), gen.Draw(t, "cell")
		got := filterMatches(want, cell)

		if folded := filterMatches(strings.ToUpper(want), strings.ToLower(cell)); folded != got {
			t.Fatalf("filterMatches(%q, %q) = %t, but %t with the case changed", want, cell, got, folded)
		}

		if !filterMatches(want, strings.ToUpper(want)) {
			t.Fatalf("filterMatches(%q, %q) = false, want a match ignoring case", want, strings.ToUpper(want))
		}

		if !strings.ContainsAny(cell, "[]{}") && got != strings.EqualFold(want, cell) {
			t.Fatalf("filterMatches(%q, %q) = %t, want %t for a plain cell", want, cell, got, !got)
		}
	})
}

// VMs whose name, or the UUID to look for, cannot go in a filter are each checked
// alone, as IsC2ClientActive would check them.
func TestActiveC2ClientsChecksUnsafeNamesAlone(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	vms := [][]string{{"a=b", "uuid-ab"}, {"blank", ""}, {"web", "uuid-web"}}
	clients := [][]string{{"uuid-ab", "a=b"}, {"uuid-blank", "blank"}}

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		switch cmd.Base {
		case vmInfoCmd:
			return []*minicli.Response{filtered(cmd, []string{"name", "uuid"}, vms)}
		case ccClientCmd:
			return []*minicli.Response{filtered(cmd, []string{"uuid", "hostname"}, clients)}
		}

		return nil
	})

	refs := []C2ClientRef{
		{VM: "a=b", ByUUID: false},  // unsafe name with a client
		{VM: "c~d", ByUUID: true},   // unsafe name of no VM
		{VM: "blank", ByUUID: true}, // safe name, but no UUID to look for
		{VM: "web", ByUUID: false},  // checked in the batch
	}

	got := Minimega{}.ActiveC2Clients("exp", refs)

	want := map[C2ClientRef]bool{
		{VM: "a=b", ByUUID: false}:  true,
		{VM: "c~d", ByUUID: true}:   false,
		{VM: "blank", ByUUID: true}: false,
		{VM: "web", ByUUID: false}:  false,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ActiveC2Clients(exp, %+v) mismatch (-want +got):\n%s", refs, diff)
	}

	var lookups []string

	for _, cmd := range received() {
		if cmd.Base == vmInfoCmd {
			lookups = append(lookups, cmd.Raw)
		}
	}

	wantLookups := []string{
		`.record false namespace "exp" .columns "name","uuid" .filter name=a=b vm info`,
		`.record false namespace "exp" .columns "name","uuid" .filter name=c~d vm info`,
		`.record false namespace "exp" .columns "name","uuid" vm info`,
		`.record false namespace "exp" .columns "name","uuid" .filter name=blank vm info`,
	}

	if diff := cmp.Diff(wantLookups, lookups, cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
		t.Errorf("ActiveC2Clients(exp, %+v) vm info commands mismatch (-want +got):\n%s", refs, diff)
	}
}

func TestActiveC2ClientsWithoutRefsSendsNothing(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, nil)

	for _, refs := range [][]C2ClientRef{nil, {}} {
		got := Minimega{}.ActiveC2Clients("exp", refs)
		if got == nil || len(got) != 0 {
			t.Errorf("ActiveC2Clients(exp, %v) = %v, want an empty map", refs, got)
		}
	}

	if diff := cmp.Diff([]mmtest.Command(nil), received()); diff != "" {
		t.Errorf("ActiveC2Clients(exp, no refs) sent commands mismatch (-want +got):\n%s", diff)
	}
}

func TestC2ClientRows(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	header := []string{"uuid", "hostname", "arch"}

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base != ccClientCmd {
			return nil
		}

		return []*minicli.Response{
			mmtest.Tabular("head", header, []string{"u1", "web", "amd64"}),
			mmtest.Tabular("compute1", header, []string{"u2", "db", "arm64"}),
			mmtest.Error("compute2", "unreachable"),
		}
	})

	got := c2ClientRows("exp")

	want := []map[string]string{
		{"uuid": "u1", "hostname": "web", "arch": "amd64", "host": "head"},
		{"uuid": "u2", "hostname": "db", "arch": "arm64", "host": "compute1"},
	}

	less := func(a, b map[string]string) bool { return a["uuid"] < b["uuid"] }
	if diff := cmp.Diff(want, got, cmpopts.SortSlices(less)); diff != "" {
		t.Errorf("c2ClientRows(exp) mismatch (-want +got):\n%s", diff)
	}

	// No .columns, which would replace the hostname column with the node.
	wantCmds := []mmtest.Command{{Raw: `.record false namespace "exp" cc client`, Namespace: "exp", Base: ccClientCmd}}
	if diff := cmp.Diff(wantCmds, received()); diff != "" {
		t.Errorf("c2ClientRows(exp) sent commands mismatch (-want +got):\n%s", diff)
	}
}
