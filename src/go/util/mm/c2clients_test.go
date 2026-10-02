package mm

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"

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

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ActiveC2Clients = %v, want %v", got, want)
	}

	cmds := received()
	if len(cmds) != 2 || mmtest.Count(cmds, vmInfoCmd) != 1 || mmtest.Count(cmds, ccClientCmd) != 1 {
		t.Fatalf("sent %v, want one vm info and one cc client", cmds)
	}

	for _, cmd := range cmds {
		if cmd.Namespace != "exp" {
			t.Fatalf("%q sent in namespace %q, want exp", cmd.Base, cmd.Namespace)
		}
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
			t.Errorf("ActiveC2Clients(%+v) = %t, IsC2ClientActive says %t", ref, got, want)
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
		t.Fatalf("c2Client: %v", err)
	}

	if name != "vm1" || uuid != "uuid-lower" {
		t.Fatalf("c2Client = %q, %q; want the exact name match", name, uuid)
	}

	cmds := received()

	if len(cmds) != 2 {
		t.Fatalf("sent %v, want only a vm info and a cc client", cmds)
	}

	if want := `.record false namespace "exp" .columns "name","uuid" .filter name=vm1 vm info`; cmds[0].Raw != want {
		t.Fatalf("vm info command = %q, want %q", cmds[0].Raw, want)
	}
}

func TestIsC2ClientActiveGivesUpAtTimeoutOrCancel(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == vmInfoCmd {
			return []*minicli.Response{mmtest.Tabular("head", []string{"name", "uuid"}, []string{"vm1", "uuid-1"})}
		}

		return nil // no client registered
	})

	start := time.Now()

	err := Minimega{}.IsC2ClientActive(C2NS("exp"), C2VM("vm1"), C2Timeout(20*time.Millisecond))
	if !errors.Is(err, ErrC2ClientNotActive) {
		t.Fatalf("IsC2ClientActive = %v, want ErrC2ClientNotActive", err)
	}

	if elapsed := time.Since(start); elapsed >= c2ActiveCheckInterval {
		t.Fatalf("gave up after %v, want at the timeout", elapsed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)

	go func() { errc <- Minimega{}.IsC2ClientActive(C2NS("exp"), C2VM("vm1"), C2Context(ctx)) }()

	// Cancel while it waits to check again.
	polltest.Until(t, "the client was checked twice", func() bool { return mmtest.Count(received(), ccClientCmd) == 2 })
	cancel()

	canceled := time.Now()

	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("IsC2ClientActive = %v, want context.Canceled", err)
	}

	if elapsed := time.Since(canceled); elapsed >= c2ActiveCheckInterval/2 {
		t.Fatalf("returned %v after the context was canceled", elapsed)
	}
}

var filterPrefix = regexp.MustCompile(`\.filter (\w+)=(\S+) `)

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
		"vm1": true, "web-01.example": true, "a!b": true,
		"": false, "a=b": false, "a~b": false, "a b": false, `a"b`: false, "a'b": false,
	} {
		if got := filterSafe(value); got != want {
			t.Errorf("filterSafe(%q) = %t, want %t", value, got, want)
		}
	}
}
