package mm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"pgregory.net/rapid"

	"phenix/util/mm/mmtest"
)

func TestValidateQoS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		qos   InterfaceQoS
		valid bool
	}{
		{name: "nothing set", qos: InterfaceQoS{}},
		{name: "a loss", qos: InterfaceQoS{Loss: "0.5"}, valid: true},
		{name: "no loss", qos: InterfaceQoS{Loss: "0"}, valid: true},
		{name: "a loss of 100", qos: InterfaceQoS{Loss: "100"}},
		{name: "a negative loss", qos: InterfaceQoS{Loss: "-1"}},
		{name: "a loss that is not a number", qos: InterfaceQoS{Loss: "half"}},
		{name: "a delay", qos: InterfaceQoS{Delay: "100ms"}, valid: true},
		{name: "a loss and a delay", qos: InterfaceQoS{Loss: "1", Delay: "1.5s"}, valid: true},
		{name: "a delay without a unit", qos: InterfaceQoS{Delay: "100"}},
		{name: "a negative delay", qos: InterfaceQoS{Delay: "-5ms"}},
		{name: "a rate in kbit", qos: InterfaceQoS{Rate: "512kbit"}, valid: true},
		{name: "a rate in mbit", qos: InterfaceQoS{Rate: "10mbit"}, valid: true},
		{name: "a rate in gbit", qos: InterfaceQoS{Rate: "1gbit"}, valid: true},
		{name: "a rate without a unit", qos: InterfaceQoS{Rate: "10"}},
		{name: "a rate with a space", qos: InterfaceQoS{Rate: "10 mbit"}},
		{name: "a fractional rate", qos: InterfaceQoS{Rate: "1.5mbit"}},
		{name: "a rate in another unit", qos: InterfaceQoS{Rate: "10mbps"}},
		{name: "a rate and a loss", qos: InterfaceQoS{Rate: "10mbit", Loss: "1"}},
		{name: "a rate and a delay", qos: InterfaceQoS{Rate: "10mbit", Delay: "1ms"}},
		{name: "a loss with an exponent", qos: InterfaceQoS{Loss: "1e1"}},
		{name: "a loss with a sign", qos: InterfaceQoS{Loss: "+1"}},
		{name: "a loss that is not a number", qos: InterfaceQoS{Loss: "NaN"}},
		{name: "a loss with no fraction digits", qos: InterfaceQoS{Loss: "1."}},
		{name: "a loss just under 100", qos: InterfaceQoS{Loss: "99.99"}, valid: true},
		{name: "a delay in microseconds", qos: InterfaceQoS{Delay: "50us"}, valid: true},
		{name: "a delay in seconds", qos: InterfaceQoS{Delay: "2s"}, valid: true},
		{name: "no delay", qos: InterfaceQoS{Delay: "0ms"}, valid: true},
		{name: "a delay in minutes", qos: InterfaceQoS{Delay: "1m"}},
		{name: "a delay in hours", qos: InterfaceQoS{Delay: "1h"}},
		{name: "a delay with a micro sign", qos: InterfaceQoS{Delay: "50µs"}},
		{name: "a delay in nanoseconds", qos: InterfaceQoS{Delay: "50ns"}},
		{name: "a delay in two units", qos: InterfaceQoS{Delay: "1s500ms"}},
		{name: "a rate of zero", qos: InterfaceQoS{Rate: "0mbit"}},
		{name: "a rate with a leading zero", qos: InterfaceQoS{Rate: "010mbit"}},
		{name: "the largest rate", qos: InterfaceQoS{Rate: "999999999kbit"}, valid: true},
		{name: "a rate with ten digits", qos: InterfaceQoS{Rate: "1000000000kbit"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateQoS(tc.qos)
			if tc.valid && err != nil {
				t.Errorf("ValidateQoS(%+v) = %v, want nil", tc.qos, err)
			}

			if !tc.valid && !errors.Is(err, ErrInvalidQoS) {
				t.Errorf("ValidateQoS(%+v) = %v, want an error wrapping %v", tc.qos, err, ErrInvalidQoS)
			}
		})
	}
}

func TestParseQoS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		column string
		want   map[int]InterfaceQoS
	}{
		{column: "", want: map[int]InterfaceQoS{}},
		{column: "[]", want: map[int]InterfaceQoS{}},
		{
			column: "[0: loss 0.5 delay 100ms, 2: rate 10mbit]",
			want: map[int]InterfaceQoS{
				0: {Interface: 0, Loss: "0.5", Delay: "100ms"},
				2: {Interface: 2, Rate: "10mbit"},
			},
		},
		{column: "[1: delay 1s loss 25]", want: map[int]InterfaceQoS{1: {Interface: 1, Loss: "25", Delay: "1s"}}},
		{column: "[x: loss 1, -1: loss 1, no index]", want: map[int]InterfaceQoS{}},
		{column: "[0: jitter 5ms loss]", want: map[int]InterfaceQoS{0: {Interface: 0}}},
	}

	for _, tc := range tests {
		if diff := cmp.Diff(tc.want, parseQoS(tc.column)); diff != "" {
			t.Errorf("parseQoS(%q) mismatch (-want +got):\n%s", tc.column, diff)
		}
	}
}

// The `qos` column minimega writes for an interface parses back to the
// settings it holds.
func TestParseQoSRoundTrip(t *testing.T) {
	t.Parallel()

	value := rapid.StringMatching(`[0-9]{1,3}(\.[0-9])?(ms|s|mbit)?`)

	rapid.Check(t, func(t *rapid.T) {
		var (
			index = rapid.IntRange(0, 16).Draw(t, "index")
			want  = InterfaceQoS{Interface: index}
			ops   []string
		)

		if rapid.Bool().Draw(t, "rate") {
			want.Rate = value.Draw(t, "rate value")
			ops = append(ops, "rate "+want.Rate)
		} else {
			if rapid.Bool().Draw(t, "loss") {
				want.Loss = value.Draw(t, "loss value")
				ops = append(ops, "loss "+want.Loss)
			}

			if rapid.Bool().Draw(t, "delay") {
				want.Delay = value.Draw(t, "delay value")
				ops = append(ops, "delay "+want.Delay)
			}
		}

		column := fmt.Sprintf("[%d: %s]", index, strings.Join(ops, " "))

		if diff := cmp.Diff(map[int]InterfaceQoS{index: want}, parseQoS(column)); diff != "" {
			t.Fatalf("parseQoS(%q) mismatch (-want +got):\n%s", column, diff)
		}
	})
}

// GetVMQoS lists every interface of the VM, with the link quality minimega
// reports, and errors for a VM minimega does not have or cannot read.
//
//nolint:paralleltest // shares the fake minimega
func TestGetVMQoS(t *testing.T) {
	header := []string{"name", "vlan", "qos"}

	tests := []struct {
		name    string
		resps   []*minicli.Response
		want    []InterfaceQoS
		wantErr error
	}{
		{
			name: "interfaces with and without link quality",
			resps: []*minicli.Response{mmtest.Tabular("head", header,
				[]string{"a", "[EXP_1 (101), EXP_2 (102), disconnected]", "[1: loss 1 delay 10ms, 2: rate 1gbit]"})},
			want: []InterfaceQoS{
				{Interface: 0},
				{Interface: 1, Loss: "1", Delay: "10ms"},
				{Interface: 2, Rate: "1gbit"},
			},
		},
		{
			name:  "no interfaces",
			resps: []*minicli.Response{mmtest.Tabular("head", header, []string{"a", "[]", "[]"})},
			want:  []InterfaceQoS{},
		},
		{name: "a VM minimega does not have", wantErr: ErrVMNotFound},
		{name: "minimega fails", resps: []*minicli.Response{mmtest.Error("head", "boom")}, wantErr: errors.New("boom")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			received := useFakeMinimega(t, func(mmtest.Command) []*minicli.Response {
				if tc.resps == nil {
					return []*minicli.Response{mmtest.Tabular("head", header)}
				}

				return tc.resps
			})

			got, err := GetVMQoS(context.Background(), "exp", "a")

			switch {
			case tc.wantErr == nil && err != nil:
				t.Fatalf("GetVMQoS(exp, a) error = %v", err)
			case errors.Is(tc.wantErr, ErrVMNotFound) && !errors.Is(err, ErrVMNotFound):
				t.Fatalf("GetVMQoS(exp, a) error = %v, want one wrapping %v", err, ErrVMNotFound)
			case tc.wantErr != nil && err == nil:
				t.Fatalf("GetVMQoS(exp, a) error = nil, want %v", tc.wantErr)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("GetVMQoS(exp, a) mismatch (-want +got):\n%s", diff)
			}

			sent := received()
			if len(sent) != 1 || sent[0].Namespace != "exp" || sent[0].Base != vmInfoCmd {
				t.Errorf("GetVMQoS(exp, a) sent %+v, want one vm info in namespace exp", sent)
			}
		})
	}
}

// SetInterfaceQoS clears the interface, then adds each setting, in the
// experiment's namespace; it sends nothing for settings it refuses. When
// minimega refuses a setting, it puts the previous settings back.
//
//nolint:paralleltest // shares the fake minimega
func TestSetInterfaceQoS(t *testing.T) {
	tests := []struct {
		name     string
		qos      InterfaceQoS
		previous InterfaceQoS
		fail     string // the command minimega fails
		want     []string
		wantErr  error
	}{
		{
			name: "a loss and a delay", qos: InterfaceQoS{Loss: "0.5", Delay: "100ms"},
			want: []string{"clear qos a 1", "qos add a 1 loss 0.5", "qos add a 1 delay 100ms"},
		},
		{name: "a rate", qos: InterfaceQoS{Rate: "10mbit"}, want: []string{"clear qos a 1", "qos add a 1 rate 10 mbit"}},
		{name: "refused settings", qos: InterfaceQoS{Rate: "10mbit", Loss: "1"}, want: []string{}, wantErr: ErrInvalidQoS},
		{
			name: "minimega cannot clear", qos: InterfaceQoS{Loss: "1"}, fail: "clear qos",
			want: []string{"clear qos a 1"}, wantErr: errors.New("boom"),
		},
		{
			name: "minimega cannot add", qos: InterfaceQoS{Loss: "1"}, fail: "qos add a 1 loss 1",
			want: []string{"clear qos a 1", "qos add a 1 loss 1", "clear qos a 1"}, wantErr: ErrInvalidQoS,
		},
		{
			name: "minimega cannot add the second setting", qos: InterfaceQoS{Loss: "1", Delay: "5ms"},
			previous: InterfaceQoS{Interface: 1, Rate: "1gbit"}, fail: "qos add a 1 delay",
			want: []string{
				"clear qos a 1", "qos add a 1 loss 1", "qos add a 1 delay 5ms",
				"clear qos a 1", "qos add a 1 rate 1 gbit",
			},
			wantErr: ErrInvalidQoS,
		},
		{
			name: "minimega cannot restore", qos: InterfaceQoS{Rate: "10mbit"},
			previous: InterfaceQoS{Interface: 1, Loss: "2"}, fail: "qos add",
			want:    []string{"clear qos a 1", "qos add a 1 rate 10 mbit", "clear qos a 1", "qos add a 1 loss 2"},
			wantErr: ErrInvalidQoS,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if tc.fail != "" && strings.HasPrefix(cmd.Base, tc.fail) {
					return []*minicli.Response{mmtest.Error("head", "boom")}
				}

				return nil
			})

			err := SetInterfaceQoS(context.Background(), "exp", "a", 1, tc.qos, tc.previous)

			switch {
			case tc.wantErr == nil && err != nil:
				t.Errorf("SetInterfaceQoS(%+v) error = %v", tc.qos, err)
			case errors.Is(tc.wantErr, ErrInvalidQoS) && !errors.Is(err, ErrInvalidQoS):
				t.Errorf("SetInterfaceQoS(%+v) error = %v, want one wrapping %v", tc.qos, err, ErrInvalidQoS)
			case tc.fail == "clear qos" && errors.Is(err, ErrInvalidQoS):
				t.Errorf("SetInterfaceQoS(%+v) error = %v, want one not wrapping %v", tc.qos, err, ErrInvalidQoS)
			case tc.wantErr != nil && err == nil:
				t.Errorf("SetInterfaceQoS(%+v) error = nil, want %v", tc.qos, tc.wantErr)
			}

			sent := received()
			if diff := cmp.Diff(tc.want, mmtest.Bases(sent)); diff != "" {
				t.Errorf("SetInterfaceQoS(%+v) commands mismatch (-want +got):\n%s", tc.qos, diff)
			}

			for _, cmd := range sent {
				if cmd.Namespace != "exp" {
					t.Errorf("SetInterfaceQoS sent %q in namespace %q, want exp", cmd.Base, cmd.Namespace)
				}
			}
		})
	}
}
