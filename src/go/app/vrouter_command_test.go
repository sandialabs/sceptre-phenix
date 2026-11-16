package app

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
	"phenix/util/mm/mmtest"
)

// runRouterCommand sends a router command with plain values, and sends nothing
// for one with a value that minimega would change.
func TestRunRouterCommand(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := mmtest.Use(t, nil)

	for _, tc := range []struct {
		command string
		refused bool
	}{
		{command: "router rtr gw 10.0.0.1"},
		{command: "router rtr route static 10.1.0.0/16 $NEXT", refused: true},
		{command: "router rtr dns ${IP} host", refused: true},
		{command: "router rtr interface 0 file:addr/24", refused: true},
	} {
		cmd := mmcli.NewNamespacedCommand("exp")
		cmd.Command = tc.command

		err := runRouterCommand(cmd)
		if refused := errors.Is(err, mm.ErrUnsafeArg); refused != tc.refused || (!refused && err != nil) {
			t.Errorf("runRouterCommand(%q) error = %v, want refused %t", tc.command, err, tc.refused)
		}
	}

	if diff := cmp.Diff([]string{"router rtr gw 10.0.0.1"}, mmtest.Bases(received())); diff != "" {
		t.Errorf("runRouterCommand sent mismatch (-want +got):\n%s", diff)
	}
}

// PostStart sends no router command when one of them has a value that
// minimega would change, so the router is not left half configured.
func TestVrouterPostStartChecksEveryCommandFirst(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := mmtest.Use(t, nil)

	doNotBoot := false
	router := &v1.Node{
		TypeF:     "Router",
		GeneralF:  &v1.General{HostnameF: "rtr", DoNotBootF: &doNotBoot},
		HardwareF: &v1.Hardware{OSTypeF: "minirouter"},
		NetworkF: &v1.Network{
			InterfacesF: []*v1.Interface{
				{NameF: "eth0", ProtoF: "static", AddressF: "10.0.0.1", MaskF: 24, GatewayF: "10.0.0.254"},
			},
			RoutesF: []v1.Route{{DestinationF: "10.1.0.0/16", NextF: "$NEXT"}},
		},
	}

	exp := &types.Experiment{
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec:     &v1.ExperimentSpec{ExperimentNameF: "exp", TopologyF: &v1.TopologySpec{NodesF: []*v1.Node{router}}},
	}

	if err := new(Vrouter).PostStart(t.Context(), exp); !errors.Is(err, mm.ErrUnsafeArg) {
		t.Errorf("PostStart() error = %v, want %v", err, mm.ErrUnsafeArg)
	}

	if got := mmtest.Bases(received()); len(got) != 0 {
		t.Errorf("PostStart() sent %q, want nothing", got)
	}
}

// addChainRules gives the minimega chain commands of a ruleset, and sets the
// default action once, after the rules. A rule with no destination, or with
// no address, matches every address.
func TestAddChainRules(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		rules []*v1.Rule
		want  []string
	}{
		{
			name: "destination with a port",
			rules: []*v1.Rule{{
				IDF: 10, ActionF: "accept", ProtocolF: "tcp",
				DestinationF: &v1.AddrPort{AddressF: "10.0.0.5", PortF: 80},
			}},
			want: []string{
				"router rtr fw chain web action accept 10.0.0.5:80 tcp",
				"router rtr fw chain web default action drop",
			},
		},
		{
			name: "source and destination",
			rules: []*v1.Rule{{
				IDF: 20, ActionF: "drop", ProtocolF: "udp",
				SourceF:      &v1.AddrPort{AddressF: "10.1.0.0/24"},
				DestinationF: &v1.AddrPort{AddressF: "10.0.0.0/24"},
			}},
			want: []string{
				"router rtr fw chain web action drop 10.1.0.0/24 10.0.0.0/24 udp",
				"router rtr fw chain web default action drop",
			},
		},
		{
			name:  "no destination",
			rules: []*v1.Rule{{IDF: 30, ActionF: "accept", ProtocolF: "tcp"}},
			want: []string{
				"router rtr fw chain web action accept 0.0.0.0/0 tcp",
				"router rtr fw chain web default action drop",
			},
		},
		{
			name: "ports with no addresses",
			rules: []*v1.Rule{{
				IDF: 40, ActionF: "accept", ProtocolF: "tcp",
				SourceF:      &v1.AddrPort{PortF: 1024},
				DestinationF: &v1.AddrPort{PortF: 80},
			}},
			want: []string{
				"router rtr fw chain web action accept 0.0.0.0/0:1024 0.0.0.0/0:80 tcp",
				"router rtr fw chain web default action drop",
			},
		},
		{
			name: "two rules",
			rules: []*v1.Rule{
				{IDF: 10, ActionF: "accept", ProtocolF: "tcp", DestinationF: &v1.AddrPort{AddressF: "10.0.0.5", PortF: 80}},
				{IDF: 20, ActionF: "accept", ProtocolF: "tcp", DestinationF: &v1.AddrPort{AddressF: "10.0.0.5", PortF: 443}},
			},
			want: []string{
				"router rtr fw chain web action accept 10.0.0.5:80 tcp",
				"router rtr fw chain web action accept 10.0.0.5:443 tcp",
				"router rtr fw chain web default action drop",
			},
		},
		{
			name: "no rules",
			want: []string{"router rtr fw chain web default action drop"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got []string

			run := func(cmd *mmcli.Command) error {
				got = append(got, cmd.Command)

				return nil
			}

			ruleset := &v1.Ruleset{NameF: "web", DefaultF: "drop", RulesF: tc.rules}

			err := addChainRules(run, mmcli.NewNamespacedCommand("exp"), "rtr", ruleset)
			if err != nil {
				t.Fatalf("addChainRules() error = %v", err)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("addChainRules() commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
