package mm

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
)

const (
	testNS      = "exp"
	testVM      = "vm1"
	testUUID    = "u"
	testOwner   = "a"
	testSibling = "b"
	testTimeout = 5 * time.Second
)

var clientHeader = []string{"uuid", "hostname"} //nolint:gochecknoglobals // test fixture

var commandsHeader = []string{"id", "responses", "filter"} //nolint:gochecknoglobals // test fixture

// ownerOnly answers `cc client` with the VM registered on the owner host.
func ownerOnly(cmd string) (minicli.Responses, bool) {
	switch {
	case strings.Contains(cmd, "cc client"):
		return minicli.Responses{
			tabular(testOwner, clientHeader, []string{testUUID, testVM}),
			tabular(testSibling, clientHeader),
		}, true
	case strings.Contains(cmd, "cc filter"):
		return minicli.Responses{text(testOwner, ""), text(testSibling, "")}, true
	}

	return nil, false
}

// TestExecC2CommandReadsOwnerHost: command ids are per-host counters. Once
// they diverge, the sibling's row for "our" id belongs to another command,
// and here it is even answered already -- the trap the old any-host scan fell
// into. Only the owner's id, row and response may be used.
func TestExecC2CommandReadsOwnerHost(t *testing.T) {
	useFakeHandler(t, func(cmd string) minicli.Responses {
		if resp, ok := ownerOnly(cmd); ok {
			return resp
		}

		switch {
		case strings.Contains(cmd, "cc exec"):
			return minicli.Responses{data(testOwner, 7), data(testSibling, 9)}
		case strings.Contains(cmd, "cc commands"):
			return minicli.Responses{
				tabular(testOwner, commandsHeader, []string{"7", "1", "uuid=u"}),
				tabular(testSibling, commandsHeader,
					[]string{"7", "1", "uuid=other"},
					[]string{"9", "0", "uuid=u"},
				),
			}
		case strings.Contains(cmd, "cc response 7 raw"):
			return minicli.Responses{text(testOwner, "hello"), errRow(testSibling, "no responses for 7")}
		}

		return nil
	})

	id, err := ExecC2Command(
		C2NS(testNS), C2VM(testVM), C2VMUUID(testUUID),
		C2Command("true"), C2Wait(), C2Timeout(testTimeout),
	)
	if err != nil {
		t.Fatalf("ExecC2Command: %v", err)
	}

	if id != "7" {
		t.Fatalf("id = %q, want the owner host's 7", id)
	}

	if !fakeSaw("cc filter uuid=" + testUUID) {
		t.Fatal("command was not filtered to the VM's uuid")
	}

	resp, err := WaitForC2Response(
		C2NS(testNS), C2VMUUID(testUUID), C2VMHost(testOwner),
		C2CommandID(id), C2Timeout(testTimeout),
	)
	if err != nil {
		t.Fatalf("WaitForC2Response: %v", err)
	}

	if resp != "hello" {
		t.Fatalf("response = %q, want %q", resp, "hello")
	}

	// The sibling's rejection must surface when it is the host asked for.
	_, err = GetC2Response(C2NS(testNS), C2VMHost(testSibling), C2CommandID(id))
	if err == nil || !strings.Contains(err.Error(), "no responses for 7") {
		t.Fatalf("sibling response err = %v, want its error", err)
	}
}

// TestWaitForC2ResponseRejectsForeignCommand: the owner host's row for the id
// targets a different VM, so the id was created for someone else there.
func TestWaitForC2ResponseRejectsForeignCommand(t *testing.T) {
	useFakeHandler(t, func(cmd string) minicli.Responses {
		if strings.Contains(cmd, "cc commands") {
			return minicli.Responses{
				tabular(testOwner, commandsHeader, []string{"7", "1", "uuid=other"}),
			}
		}

		return nil
	})

	_, err := WaitForC2Response(
		C2NS(testNS), C2VMUUID(testUUID), C2VMHost(testOwner),
		C2CommandID("7"), C2Timeout(testTimeout),
	)
	if !errors.Is(err, ErrC2CommandMismatch) {
		t.Fatalf("err = %v, want ErrC2CommandMismatch", err)
	}
}

// TestWaitForC2ResponseNeedsCommandOnOwnerHost: a row on a sibling alone
// means the owner never created the command; give up after the appear grace
// rather than the full timeout.
func TestWaitForC2ResponseNeedsCommandOnOwnerHost(t *testing.T) {
	useFakeHandler(t, func(cmd string) minicli.Responses {
		if strings.Contains(cmd, "cc commands") {
			return minicli.Responses{
				tabular(testOwner, commandsHeader),
				tabular(testSibling, commandsHeader, []string{"7", "1", "uuid=u"}),
			}
		}

		return nil
	})

	start := time.Now()

	_, err := WaitForC2Response(
		C2NS(testNS), C2VMUUID(testUUID), C2VMHost(testOwner), C2CommandID("7"),
		C2AppearGrace(50*time.Millisecond), C2Timeout(testTimeout),
	)
	if !errors.Is(err, ErrC2CommandMismatch) {
		t.Fatalf("err = %v, want ErrC2CommandMismatch", err)
	}

	if elapsed := time.Since(start); elapsed >= testTimeout {
		t.Fatalf("gave up after %s, want well before the %s timeout", elapsed, testTimeout)
	}
}

// TestWaitForC2ResponseAbortsWhenClientVanishes: an unanswered command whose
// VM has dropped out of `cc clients` for longer than the client grace ends
// with ErrC2ClientNotActive instead of running the timeout out. A zero grace
// disables the supervision.
func TestWaitForC2ResponseAbortsWhenClientVanishes(t *testing.T) {
	useFakeHandler(t, func(cmd string) minicli.Responses {
		switch {
		case strings.Contains(cmd, "cc commands"):
			return minicli.Responses{
				tabular(testOwner, commandsHeader, []string{"7", "0", "uuid=u"}),
			}
		case strings.Contains(cmd, "cc client"):
			return minicli.Responses{tabular(testOwner, clientHeader)}
		}

		return nil
	})

	opts := []C2Option{
		C2NS(testNS), C2VMUUID(testUUID), C2VMHost(testOwner), C2CommandID("7"),
	}

	start := time.Now()

	_, err := WaitForC2Response(append(opts,
		C2ClientGrace(50*time.Millisecond), C2Timeout(testTimeout),
	)...)
	if !errors.Is(err, ErrC2ClientNotActive) {
		t.Fatalf("err = %v, want ErrC2ClientNotActive", err)
	}

	if elapsed := time.Since(start); elapsed >= testTimeout {
		t.Fatalf("gave up after %s, want well before the %s timeout", elapsed, testTimeout)
	}

	const timeout = 1200 * time.Millisecond

	_, err = WaitForC2Response(append(opts, C2ClientGrace(0), C2Timeout(timeout))...)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err = %v, want the plain timeout with supervision off", err)
	}
}

func TestFilterTargets(t *testing.T) {
	t.Parallel()

	cases := []struct {
		filter string
		want   bool
	}{
		{"uuid=u", true},
		{"os=linux && uuid=U", true},
		{"uuid=uu", false},
		{"name=u", false},
		{"", false},
	}

	for _, tc := range cases {
		if got := filterTargets(tc.filter, testUUID); got != tc.want {
			t.Errorf("filterTargets(%q) = %v, want %v", tc.filter, got, tc.want)
		}
	}
}
