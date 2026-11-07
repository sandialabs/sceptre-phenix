package tap

import (
	"errors"
	"fmt"
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

// steps is the shell command that runs cmds as a MeshShellSteps script, failing step i with exit status 100+i.
func steps(cmds ...string) string {
	parts := make([]string, len(cmds))
	for i, c := range cmds {
		parts[i] = fmt.Sprintf("%s || exit %d", c, 100+i)
	}

	return shell(`sh -c "` + strings.Join(parts, "; ") + `"`)
}

// each is the shell command that runs cmds as a MeshShellEach script, printing i when step i fails.
func each(cmds ...string) string {
	parts := make([]string, len(cmds))
	for i, c := range cmds {
		parts[i] = fmt.Sprintf("%s 2>/dev/null || echo %d", c, i)
	}

	return shell(`sh -c "` + strings.Join(parts, "; ") + `"`)
}

var (
	createCmds = []string{ //nolint:gochecknoglobals // expected commands
		mesh(`namespace "exp" tap create exp//EXP_1 bridge br0 name ` + name),
		steps(
			"ip netns add "+name,
			"ip link set dev "+name+" netns "+name,
			"ip netns exec "+name+" ip link set dev "+name+" up",
			"ip netns exec "+name+" ip addr add 172.16.0.1/24 dev "+name,
		),
	}

	deleteCmds = []string{ //nolint:gochecknoglobals // expected commands
		mesh(`namespace "exp" tap delete ` + name),
		shell("ip netns delete " + name),
	}

	// connectSteps and disconnectSteps use the pair 10.213.47.4/30: .5 on the host, .6 in the network namespace.
	connectSteps = []string{ //nolint:gochecknoglobals // expected commands
		"ip link add " + name + " type veth peer name abcdefghi_right",
		"ip link set dev abcdefghi_right netns " + name,
		"ip netns exec " + name + " ip link set abcdefghi_right name eth0",
		"ip netns exec " + name + " ip link set dev eth0 up",
		"ip netns exec " + name + " ip addr add 10.213.47.6/30 dev eth0",
		"ip netns exec " + name + " ip route add default via 10.213.47.5",
		"ip netns exec " + name + " iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE",
		"iptables -t nat -A POSTROUTING -s 10.213.47.6 ! -o " + name + " -j MASQUERADE",
		"iptables -A FORWARD -i " + name + " ! -o " + name + " -j ACCEPT",
		"iptables -A FORWARD -o " + name + " -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT",
		"ip addr add 10.213.47.5/30 dev " + name,
		"ip link set dev " + name + " up",
	}

	disconnectSteps = []string{ //nolint:gochecknoglobals // expected commands
		"iptables -t nat -D POSTROUTING -s 10.213.47.6 ! -o " + name + " -j MASQUERADE",
		"iptables -D FORWARD -i " + name + " ! -o " + name + " -j ACCEPT",
		"iptables -D FORWARD -o " + name + " -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT",
	}

	connectCmd    = steps(connectSteps...)   //nolint:gochecknoglobals // expected command
	disconnectCmd = each(disconnectSteps...) //nolint:gochecknoglobals // expected command
)

// useMinimega starts the fake minimega, with compute1 as a host that is not the headnode, answering each base in
// answers with its response. It returns a function listing the bases sent, without the headnode lookups.
func useMinimega(t *testing.T, answers map[string]*minicli.Response) func() []string {
	t.Helper()

	received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "host" && cmd.Namespace == "minimega" {
			return []*minicli.Response{mmtest.Tabular("head", []string{"name"}, []string{"head"})}
		}

		if resp, ok := answers[cmd.Base]; ok {
			return []*minicli.Response{resp}
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

// failing answers base with an error holding msg.
func failing(base, msg string) map[string]*minicli.Response {
	return map[string]*minicli.Response{base: mmtest.Error("compute1", msg)}
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
		{name: "external access", external: true, want: concat(createCmds, []string{connectCmd}), wantPair: "10.213.47.4/30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sent := useMinimega(t, nil)
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
// connection and then the tap. The error names the step that failed.
func TestCreateRollsBack(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	tests := map[string]struct {
		answers map[string]*minicli.Response
		want    []string
		wantMsg string
	}{
		"tap create": {answers: failing(createCmds[0], "boom"), want: concat(createCmds[:1], deleteCmds), wantMsg: "boom"},
		"tap address": {
			answers: failing(createCmds[1], "exit status 103"), want: concat(createCmds, deleteCmds),
			wantMsg: "setting IP address for tap in network namespace",
		},
		"connection with an unknown status": {
			answers: failing(connectCmd, "boom"), want: concat(createCmds, []string{connectCmd, disconnectCmd}, deleteCmds),
			wantMsg: "boom",
		},
	}

	for i, step := range []string{
		"creating veth pair", "attaching veth interface", "renaming veth interface", "enabling veth interface",
		"setting IP for veth interface", "setting default route", "configuring iptables masquerading in network",
		"configuring iptables masquerading in system", "configuring iptables forwarding",
		"configuring iptables forwarding", "setting IP for veth interface in system", "enabling veth interface in system",
	} {
		tests[connectSteps[i]] = struct {
			answers map[string]*minicli.Response
			want    []string
			wantMsg string
		}{
			answers: failing(connectCmd, fmt.Sprintf("exit status %d", 100+i)),
			want:    concat(createCmds, []string{connectCmd, disconnectCmd}, deleteCmds),
			wantMsg: step,
		}
	}

	for desc, tc := range tests {
		t.Run(desc, func(t *testing.T) {
			sent := useMinimega(t, tc.answers)

			pair, err := newTap(true).Create("compute1")
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) || !strings.Contains(err.Error(), "VLAN EXP_1") {
				t.Errorf("Create(compute1) error = %v, want one for VLAN EXP_1 holding %q", err, tc.wantMsg)
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

// Create and Delete send nothing for a tap name sh would not read as one word.
func TestRefusesUnsafeName(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	sent := useMinimega(t, nil)

	tp := newTap(true)
	tp.Name = "abcdefghi;reboot"

	if _, err := tp.Create("compute1"); !errors.Is(err, mm.ErrUnsafeArg) {
		t.Errorf("Create(compute1) error = %v, want %v", err, mm.ErrUnsafeArg)
	}

	tp.Subnet = "10.213.47.4/30"

	if err := tp.connect("compute1", netaddr.MustParseIPPrefix("10.213.47.4/30")); !errors.Is(err, mm.ErrUnsafeArg) {
		t.Errorf("connect(compute1) error = %v, want %v", err, mm.ErrUnsafeArg)
	}

	if err := tp.disconnect("compute1"); !errors.Is(err, mm.ErrUnsafeArg) {
		t.Errorf("disconnect(compute1) error = %v, want %v", err, mm.ErrUnsafeArg)
	}

	for _, base := range sent() {
		if strings.Contains(base, "reboot") && strings.Contains(base, "sh -c") {
			t.Errorf("sent %q, want no shell script with the unsafe name", base)
		}
	}
}

func TestDelete(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	external := concat([]string{disconnectCmd}, deleteCmds)

	for _, tc := range []struct {
		name     string
		external bool
		subnet   string
		answers  map[string]*minicli.Response
		want     []string
		wantErr  []string // parts of the error, none for no error
	}{
		{name: "host only", want: deleteCmds},
		{name: "external access", external: true, subnet: "10.213.47.4/30", want: external},
		{
			name: "unknown subnet", external: true, subnet: "none", want: deleteCmds,
			wantErr: []string{"parsing tap subnet none"},
		},
		{name: "tap delete fails", answers: failing(deleteCmds[0], "boom"), want: deleteCmds, wantErr: []string{"boom"}},
		{
			name: "masquerade delete fails", external: true, subnet: "10.213.47.4/30",
			answers: map[string]*minicli.Response{disconnectCmd: mmtest.Text("compute1", "0\n")},
			want:    external, wantErr: []string{"deleting iptables masquerading"},
		},
		{
			name: "forward deletes fail", external: true, subnet: "10.213.47.4/30",
			answers: map[string]*minicli.Response{disconnectCmd: mmtest.Text("compute1", "1\n2\n")},
			want:    external, wantErr: []string{"deleting iptables forwarding", "conntrack"},
		},
		{
			name: "disconnect script fails", external: true, subnet: "10.213.47.4/30",
			answers: failing(disconnectCmd, "boom"), want: external, wantErr: []string{"running 3 shell commands"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sent := useMinimega(t, tc.answers)
			tp := newTap(tc.external)
			tp.Subnet = tc.subnet

			err := tp.Delete("compute1")

			if len(tc.wantErr) == 0 && err != nil {
				t.Errorf("Delete(compute1) error = %v, want nil", err)
			}

			for _, part := range tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), part) {
					t.Errorf("Delete(compute1) error = %v, want one holding %q", err, part)
				}
			}

			if diff := cmp.Diff(tc.want, sent()); diff != "" {
				t.Errorf("Delete(compute1) sent mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
