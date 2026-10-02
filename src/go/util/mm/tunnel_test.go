package mm

import (
	"strings"
	"testing"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/util/mm/mmtest"
)

// vm1OnHeadnode answers `host` in the default namespace with the headnode
// "head", and `vm info` with vm1 on the headnode.
func vm1OnHeadnode(cmd mmtest.Command) []*minicli.Response {
	switch {
	case cmd.Base == "host" && cmd.Namespace == "minimega":
		return []*minicli.Response{mmtest.Tabular("head", []string{"name"}, []string{"head"})}
	case cmd.Base == vmInfoCmd:
		return []*minicli.Response{mmtest.Tabular("head", []string{"name"}, []string{"vm1"})}
	}

	return nil
}

func TestGetTunnelsFiltersBySourcePort(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, nil)

	_ = (Minimega{}).GetTunnels(
		NS("exp"), VMName("vm1"),
		TunnelSourcePort(50001), TunnelDestinationPort(22), TunnelDestinationHost("127.0.0.1"),
	)

	cmds := received()
	if len(cmds) != 1 {
		t.Fatalf("sent %v, want one tunnel listing", cmds)
	}

	for _, part := range []string{
		`.filter 'src port'=50001 `, `.filter 'dst port'=22 `, `.filter dst=127.0.0.1 `, " cc tunnel list vm1",
	} {
		if !strings.Contains(cmds[0].Raw, part) {
			t.Errorf("tunnel listing %q lacks %q", cmds[0].Raw, part)
		}
	}
}

func TestTunnelsOfHeadnodeVMIgnoreOtherNodes(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		// For a headnode VM, phenix sends the command with a leading space.
		switch base := strings.TrimSpace(cmd.Base); {
		case strings.HasPrefix(base, "cc tunnel list "):
			return []*minicli.Response{
				mmtest.Tabular("head", []string{"vm", "id", "src port", "dst", "dst port"},
					[]string{"vm1", "3", "50001", "127.0.0.1", "22"}),
				{Host: "compute1", Error: "vm not found: vm1"},
			}
		case strings.HasPrefix(base, "cc tunnel "):
			// The command runs on every node in the namespace.
			return []*minicli.Response{{Host: "head"}, {Host: "compute1", Error: "vm not found: vm1"}}
		}

		return vm1OnHeadnode(cmd)
	})

	opts := []Option{
		NS("exp"), VMName("vm1"),
		TunnelSourcePort(50001), TunnelDestinationPort(22), TunnelDestinationHost("127.0.0.1"),
	}

	if err := (Minimega{}).CreateTunnel(opts...); err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	if err := (Minimega{}).CloseTunnel(opts...); err != nil {
		t.Fatalf("CloseTunnel: %v", err)
	}

	var closed bool

	for _, cmd := range received() {
		if strings.TrimSpace(cmd.Base) == "cc tunnel close vm1 3" && cmd.Namespace == "exp" {
			closed = true
		}
	}

	if !closed {
		t.Fatalf("sent %v, want tunnel 3 closed in namespace exp", received())
	}
}

func TestCreateTunnelReportsOwnerError(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if strings.HasPrefix(strings.TrimSpace(cmd.Base), "cc tunnel ") {
			return []*minicli.Response{
				{Host: "head", Error: "listen tcp :50001: bind: address already in use"},
				{Host: "compute1", Error: "vm not found: vm1"},
			}
		}

		return vm1OnHeadnode(cmd)
	})

	err := (Minimega{}).CreateTunnel(
		NS("exp"), VMName("vm1"), TunnelSourcePort(50001), TunnelDestinationPort(22),
	)
	if err == nil || !strings.Contains(err.Error(), "bind: address already in use") ||
		strings.Contains(err.Error(), "vm not found") {
		t.Fatalf("CreateTunnel error = %v, want only the headnode's bind error", err)
	}
}

func TestMeshSendPutsNamespaceInside(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, vm1OnHeadnode)

	for _, tc := range []struct{ ns, host, want string }{
		{"exp", "compute1", `.record false mesh send compute1 namespace "exp" tap delete t1`},
		{"", "compute1", `.record false mesh send compute1 tap delete t1`},
		{"exp", "head", `.record false namespace "exp" tap delete t1`},
	} {
		if err := (Minimega{}).MeshSend(tc.ns, tc.host, "tap delete t1"); err != nil {
			t.Fatalf("MeshSend(%q, %q): %v", tc.ns, tc.host, err)
		}

		cmds := received()
		if got := cmds[len(cmds)-1].Raw; got != tc.want {
			t.Errorf("MeshSend(%q, %q) sent %q, want %q", tc.ns, tc.host, got, tc.want)
		}
	}
}

func TestMeshSendReportsError(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if strings.HasPrefix(cmd.Base, "mesh send ") {
			return []*minicli.Response{{Host: "compute1", Error: "tap not found"}}
		}

		return vm1OnHeadnode(cmd)
	})

	err := (Minimega{}).MeshSend("exp", "compute1", "tap delete t1")
	if err == nil || !strings.Contains(err.Error(), "tap not found") {
		t.Fatalf("MeshSend error = %v, want the remote node's error", err)
	}
}
