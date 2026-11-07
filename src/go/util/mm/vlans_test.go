package mm

import (
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm/mmtest"
)

// The minimega help text of `clear vlans` before and after minimega has
// `clear vlans blacklist <vlan>`. The long text names blacklisted VLANs in
// both.
const (
	helpClearVLANsOld = "Usage:\n\tclear vlans [prefix]\n\nClear one or more aliases. Given \"all\", " +
		"all state is cleared, including blacklisted VLANS."
	helpClearVLANsNew = "Usage:\n\tclear vlans [prefix]\n\tclear vlans blacklist <vlan>\n\n" +
		"Clear one or more aliases, or remove a VLAN from the blacklist."
)

// The VLAN functions send their commands in the right namespace, parse what
// minimega answers and report minimega's errors.
func TestVLANCommands(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]commandCase{
		"GetVLANs reports minimega's error": {
			call:    func() (any, error) { return GetVLANs(NS("exp")) },
			answers: map[string][]*minicli.Response{"vlans": failed("namespace busy")},
			sent:    []string{inExp("vlans")},
			want:    map[string]int(nil),
			wantMsg: "namespace busy",
		},
		"GetNamespaces": {
			call: func() (any, error) { return GetNamespaces() },
			answers: map[string][]*minicli.Response{
				"namespace": {mmtest.Tabular("head", []string{"namespace", "vlans", "active"},
					[]string{"zeta", "", "false"}, []string{"alpha", "100-199", "true"})},
			},
			sent: []string{bare("namespace")},
			want: []Namespace{
				{Name: "alpha", MinVLAN: 100, MaxVLAN: 199, Active: true},
				{Name: "zeta", MinVLAN: 0, MaxVLAN: 0, Active: false},
			},
		},
		"GetNamespaces reports a range that is not min-max": {
			call: func() (any, error) { return GetNamespaces() },
			answers: map[string][]*minicli.Response{
				"namespace": {mmtest.Tabular("head", []string{"namespace", "vlans", "active"}, []string{"a", "x", "true"})},
			},
			sent:    []string{bare("namespace")},
			want:    []Namespace(nil),
			wantMsg: `"x"`,
		},
		"GetNamespaces reports minimega's error": {
			call:    func() (any, error) { return GetNamespaces() },
			answers: map[string][]*minicli.Response{"namespace": failed("down")},
			sent:    []string{bare("namespace")},
			want:    []Namespace(nil),
			wantMsg: "down",
		},
		"GetVLANBlacklist": {
			call: func() (any, error) { return GetVLANBlacklist() },
			answers: map[string][]*minicli.Response{
				"vlans blacklist": {mmtest.Tabular("head", []string{"vlan"}, []string{"999"}, []string{"100"}, []string{"100"})},
			},
			sent: []string{bare("vlans blacklist")},
			want: []int{100, 999},
		},
		"GetVLANBlacklist reports minimega's error": {
			call:    func() (any, error) { return GetVLANBlacklist() },
			answers: map[string][]*minicli.Response{"vlans blacklist": failed("down")},
			sent:    []string{bare("vlans blacklist")},
			want:    []int(nil),
			wantMsg: "down",
		},
		"BlacklistVLAN": {
			call: errOnly(func() error { return BlacklistVLAN(100) }),
			sent: []string{bare("vlans blacklist 100")},
		},
		"BlacklistVLAN reports minimega's error": {
			call:    errOnly(func() error { return BlacklistVLAN(100) }),
			answers: map[string][]*minicli.Response{"vlans blacklist 100": failed("down")},
			sent:    []string{bare("vlans blacklist 100")},
			wantMsg: "down",
		},
		"UnblacklistVLAN": {
			call: errOnly(func() error { return UnblacklistVLAN(100) }),
			sent: []string{bare("clear vlans blacklist 100")},
		},
		"UnblacklistVLAN without minimega support": {
			call: errOnly(func() error { return UnblacklistVLAN(100) }),
			answers: map[string][]*minicli.Response{
				"clear vlans blacklist 100": failed("invalid command: `clear vlans blacklist 100`"),
			},
			sent:   []string{bare("clear vlans blacklist 100")},
			wantIs: ErrBlacklistRemovalUnsupported,
		},
		"UnblacklistVLAN reports minimega's refusal": {
			call:    errOnly(func() error { return UnblacklistVLAN(100) }),
			answers: map[string][]*minicli.Response{"clear vlans blacklist 100": failed("VLAN 100 is in use")},
			sent:    []string{bare("clear vlans blacklist 100")},
			wantMsg: "VLAN 100 is in use",
		},
		"VLANBlacklistRemovable with the command": {
			call:    func() (any, error) { return VLANBlacklistRemovable() },
			answers: map[string][]*minicli.Response{"help clear vlans": {mmtest.Text("head", helpClearVLANsNew)}},
			sent:    []string{bare("help clear vlans")},
			want:    true,
		},
		"VLANBlacklistRemovable without the command": {
			call:    func() (any, error) { return VLANBlacklistRemovable() },
			answers: map[string][]*minicli.Response{"help clear vlans": {mmtest.Text("head", helpClearVLANsOld)}},
			sent:    []string{bare("help clear vlans")},
			want:    false,
		},
		"VLANBlacklistRemovable reports minimega's error": {
			call:    func() (any, error) { return VLANBlacklistRemovable() },
			answers: map[string][]*minicli.Response{"help clear vlans": failed("down")},
			sent:    []string{bare("help clear vlans")},
			want:    false,
			wantMsg: "down",
		},
		"ClearVLANs": {
			call: errOnly(func() error { return ClearVLANs(NS("exp")) }),
			sent: []string{inExp("clear vlans")},
		},
		"ClearVLANs without a namespace": {
			call:    errOnly(func() error { return ClearVLANs() }),
			wantMsg: "no namespace",
		},
		"ClearVLANs reports minimega's error": {
			call:    errOnly(func() error { return ClearVLANs(NS("exp")) }),
			answers: map[string][]*minicli.Response{"clear vlans": failed("down")},
			sent:    []string{inExp("clear vlans")},
			wantMsg: "down",
		},
	} {
		t.Run(name, tc.run)
	}
}

// Only a usage line names the blacklist removal command.
func TestUsageHasBlacklistRemoval(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		help string
		want bool
	}{
		"usage line":           {help: helpClearVLANsNew, want: true},
		"long text only":       {help: helpClearVLANsOld, want: false},
		"empty":                {help: "", want: false},
		"no usage heading":     {help: "\tclear vlans blacklist <vlan>", want: false},
		"other command's line": {help: "Usage:\n\tvlans blacklist [vlan]\n", want: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := usageHasBlacklistRemoval(tc.help); got != tc.want {
				t.Errorf("usageHasBlacklistRemoval(%q) = %t, want %t", tc.help, got, tc.want)
			}
		})
	}
}
