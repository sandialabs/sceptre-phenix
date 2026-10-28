package tap

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"inet.af/netaddr"

	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// name is the tap name of the tests; connect uses its first nine characters.
const name = "abcdefghi-tapapp"

// mesh is the base of the command minimega runs on compute1; shell is the base of a shell command run there.
func mesh(base string) string  { return "mesh send compute1 " + base }
func shell(base string) string { return mesh("shell " + base) }

var (
	createCmds = []string{ //nolint:gochecknoglobals // expected commands
		mesh(`namespace "exp" tap create exp//EXP_1 bridge br0 name ` + name),
		shell("ip netns add " + name),
		shell("ip link set dev " + name + " netns " + name),
		shell("ip netns exec " + name + " ip link set dev " + name + " up"),
		shell("ip netns exec " + name + " ip addr add 172.16.0.1/24 dev " + name),
	}

	deleteCmds = []string{ //nolint:gochecknoglobals // expected commands
		mesh(`namespace "exp" tap delete ` + name),
		shell("ip netns delete " + name),
	}

	// connectCmds and disconnectCmds use the pair 10.213.47.4/30: .5 on the host, .6 in the network namespace.
	connectCmds = []string{ //nolint:gochecknoglobals // expected commands
		shell("ip link add " + name + " type veth peer name abcdefghi_right"),
		shell("ip link set dev abcdefghi_right netns " + name),
		shell("ip netns exec " + name + " ip link set abcdefghi_right name eth0"),
		shell("ip netns exec " + name + " ip link set dev eth0 up"),
		shell("ip netns exec " + name + " ip addr add 10.213.47.6/30 dev eth0"),
		shell("ip netns exec " + name + " ip route add default via 10.213.47.5"),
		shell("ip netns exec " + name + " iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE"),
		shell("iptables -t nat -A POSTROUTING -s 10.213.47.6 ! -o " + name + " -j MASQUERADE"),
		shell("iptables -A FORWARD -i " + name + " ! -o " + name + " -j ACCEPT"),
		shell("iptables -A FORWARD -o " + name + " -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT"),
		shell("ip addr add 10.213.47.5/30 dev " + name),
		shell("ip link set dev " + name + " up"),
	}

	disconnectCmds = []string{ //nolint:gochecknoglobals // expected commands
		shell("iptables -t nat -D POSTROUTING -s 10.213.47.6 ! -o " + name + " -j MASQUERADE"),
		shell("iptables -D FORWARD -i " + name + " ! -o " + name + " -j ACCEPT"),
		shell("iptables -D FORWARD -o " + name + " -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT"),
	}
)

// useMinimega starts the fake minimega, with compute1 as a host that is not the headnode, failing the command whose
// base is failing ("" for none). It returns a function listing the bases sent, without the headnode lookups.
func useMinimega(t *testing.T, failing string) func() []string {
	t.Helper()

	received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		switch {
		case cmd.Base == "host" && cmd.Namespace == "minimega":
			return []*minicli.Response{mmtest.Tabular("head", []string{"name"}, []string{"head"})}
		case failing != "" && cmd.Base == failing:
			return []*minicli.Response{mmtest.Error("compute1", "boom")}
		}

		return nil
	})

	mm.ForgetCluster()
	t.Cleanup(mm.ForgetCluster)

	return func() []string {
		var bases []string

		for _, base := range mmtest.Bases(received()) {
			if base != "host" {
				bases = append(bases, base)
			}
		}

		return bases
	}
}

// newTap returns the tap of the tests, initialized for experiment exp with 10.213.47.0/30 used.
func newTap(external bool) *Tap {
	t := &Tap{VLAN: "EXP_1", IP: "172.16.0.1/24", Name: name, External: External{Enabled: external}}
	t.Init("br0", Experiment("exp"), UsedPairs([]netaddr.IPPrefix{netaddr.MustParseIPPrefix("10.213.47.0/30")}))

	return t
}

func concat(parts ...[]string) []string {
	size := 0
	for _, p := range parts {
		size += len(p)
	}

	out := make([]string, 0, size)

	for _, p := range parts {
		out = append(out, p...)
	}

	return out
}

func TestNewOptions(t *testing.T) {
	t.Parallel()

	if got := NewOptions(); got.subnet != netaddr.MustParseIPPrefix("10.213.47.0/30") || got.exp != "" || got.used != nil {
		t.Errorf("NewOptions() = %+v, want subnet 10.213.47.0/30 and nothing else", got)
	}

	used := []netaddr.IPPrefix{netaddr.MustParseIPPrefix("10.0.0.0/30")}
	got := NewOptions(Experiment("exp"), PairSubnet(netaddr.MustParseIPPrefix("192.168.0.0/30")), UsedPairs(used))

	want := options{exp: "exp", subnet: netaddr.MustParseIPPrefix("192.168.0.0/30"), used: used}
	equalPrefix := cmp.Comparer(func(a, b netaddr.IPPrefix) bool { return a == b })
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(options{}), equalPrefix); diff != "" {
		t.Errorf("NewOptions(...) mismatch (-want +got):\n%s", diff)
	}
}

// Init keeps a bridge the tap names and sets the given one otherwise.
func TestInit(t *testing.T) {
	t.Parallel()

	var empty Tap

	empty.Init("phenix", Experiment("exp"))

	if empty.Bridge != "phenix" || empty.o.exp != "exp" {
		t.Errorf("Init(phenix, exp) on a tap without a bridge = bridge %q, exp %q, want phenix, exp", empty.Bridge, empty.o.exp)
	}

	named := Tap{Bridge: "br0"}
	named.Init("phenix")

	if named.Bridge != "br0" {
		t.Errorf("Init(phenix) on a tap with bridge br0 = bridge %q, want br0", named.Bridge)
	}
}

func TestCreate(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for _, tc := range []struct {
		name     string
		external bool
		want     []string
		wantPair string
	}{
		{name: "host only", external: false, want: createCmds},
		{name: "external access", external: true, want: concat(createCmds, connectCmds), wantPair: "10.213.47.4/30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sent := useMinimega(t, "")
			tp := newTap(tc.external)

			pair, err := tp.Create("compute1")
			if err != nil {
				t.Fatalf("Create(compute1) error = %v, want nil", err)
			}

			var wantPair netaddr.IPPrefix
			if tc.wantPair != "" {
				wantPair = netaddr.MustParseIPPrefix(tc.wantPair)
			}

			if pair != wantPair || tp.Subnet != tc.wantPair {
				t.Errorf("Create(compute1) = %s, subnet %q, want %s, %q", pair, tp.Subnet, wantPair, tc.wantPair)
			}

			if diff := cmp.Diff(tc.want, sent()); diff != "" {
				t.Errorf("Create(compute1) sent mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A failed Create removes what it made: a failed tap removes the tap, and a failed connection removes the
// connection and then the tap.
func TestCreateRollsBack(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	tests := map[string]struct {
		failing string
		want    []string
	}{
		"tap create":  {failing: createCmds[0], want: concat(createCmds[:1], deleteCmds)},
		"tap address": {failing: createCmds[4], want: concat(createCmds, deleteCmds)},
	}

	for i, cmd := range connectCmds {
		tests[cmd] = struct {
			failing string
			want    []string
		}{failing: cmd, want: concat(createCmds, connectCmds[:i+1], disconnectCmds, deleteCmds)}
	}

	for desc, tc := range tests {
		t.Run(desc, func(t *testing.T) {
			sent := useMinimega(t, tc.failing)

			pair, err := newTap(true).Create("compute1")
			if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "VLAN EXP_1") {
				t.Errorf("Create(compute1) error = %v, want one for VLAN EXP_1 holding boom", err)
			}

			if !pair.IsZero() {
				t.Errorf("Create(compute1) = %s, want the zero prefix", pair)
			}

			if diff := cmp.Diff(tc.want, sent()); diff != "" {
				t.Errorf("Create(compute1) sent mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDelete(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for _, tc := range []struct {
		name     string
		external bool
		subnet   string
		failing  string
		want     []string
		wantErr  string // a part of the error, "" for none
	}{
		{name: "host only", want: deleteCmds},
		{name: "external access", external: true, subnet: "10.213.47.4/30", want: concat(disconnectCmds, deleteCmds)},
		{
			name: "unknown subnet", external: true, subnet: "none", want: deleteCmds,
			wantErr: "parsing tap subnet none",
		},
		{name: "tap delete fails", failing: deleteCmds[0], want: deleteCmds, wantErr: "boom"},
		{
			name: "masquerade delete fails", external: true, subnet: "10.213.47.4/30", failing: disconnectCmds[0],
			want: concat(disconnectCmds, deleteCmds), wantErr: "deleting iptables masquerading",
		},
		{
			name: "forward delete fails", external: true, subnet: "10.213.47.4/30", failing: disconnectCmds[1],
			want: concat(disconnectCmds, deleteCmds), wantErr: "deleting iptables forwarding",
		},
		{
			name: "conntrack delete fails", external: true, subnet: "10.213.47.4/30", failing: disconnectCmds[2],
			want: concat(disconnectCmds, deleteCmds), wantErr: "deleting iptables forwarding",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sent := useMinimega(t, tc.failing)
			tp := newTap(tc.external)
			tp.Subnet = tc.subnet

			err := tp.Delete("compute1")

			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("Delete(compute1) error = %v, want nil", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("Delete(compute1) error = %v, want one holding %q", err, tc.wantErr)
			}

			if diff := cmp.Diff(tc.want, sent()); diff != "" {
				t.Errorf("Delete(compute1) sent mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
