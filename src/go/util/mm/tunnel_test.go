package mm

import (
	"strings"
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

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
		t.Fatalf("GetTunnels(vm1, 50001 to 127.0.0.1:22) sent %v, want one tunnel listing", cmds)
	}

	for _, part := range []string{
		`.filter 'src port'=50001 `, `.filter 'dst port'=22 `, `.filter dst=127.0.0.1 `, " cc tunnel list vm1",
	} {
		if !strings.Contains(cmds[0].Raw, part) {
			t.Errorf("GetTunnels(vm1, 50001 to 127.0.0.1:22) sent %q, want it to contain %q", cmds[0].Raw, part)
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
		t.Errorf("CreateTunnel(vm1, 50001 to 127.0.0.1:22) error = %v", err)
	}

	if err := (Minimega{}).CloseTunnel(opts...); err != nil {
		t.Errorf("CloseTunnel(vm1, 50001 to 127.0.0.1:22) error = %v", err)
	}

	var closed bool

	for _, cmd := range received() {
		if strings.TrimSpace(cmd.Base) == "cc tunnel close vm1 3" && cmd.Namespace == "exp" {
			closed = true
		}
	}

	if !closed {
		t.Errorf("CloseTunnel(vm1, 50001 to 127.0.0.1:22) sent %v, want tunnel 3 closed in namespace exp", received())
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
		t.Errorf("CreateTunnel(vm1, 50001 to :22) error = %v, want only the headnode's bind error", err)
	}
}

func TestMeshSend(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		ns, host string
		remote   *minicli.Response // the answer to a mesh send, nil for success
		want     string            // the command sent
		wantErr  string            // substring of the error, "" for none
	}{
		"namespace goes inside the mesh send": {
			ns: "exp", host: "compute1", want: `.record false mesh send compute1 namespace "exp" tap delete t1`,
		},
		"no namespace":               {ns: "", host: "compute1", want: `.record false mesh send compute1 tap delete t1`},
		"headnode runs it itself":    {ns: "exp", host: "head", want: `.record false namespace "exp" tap delete t1`},
		"no host means the headnode": {ns: "exp", host: "", want: `.record false namespace "exp" tap delete t1`},
		"reports the remote node's error": {
			ns: "exp", host: "compute1", remote: mmtest.Error("compute1", "tap not found"),
			want:    `.record false mesh send compute1 namespace "exp" tap delete t1`,
			wantErr: "tap not found",
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if strings.HasPrefix(cmd.Base, "mesh send ") && tc.remote != nil {
					return []*minicli.Response{tc.remote}
				}

				return vm1OnHeadnode(cmd)
			})

			err := (Minimega{}).MeshSend(tc.ns, tc.host, "tap delete t1")

			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("MeshSend(%q, %q) error = %v, want nil", tc.ns, tc.host, err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("MeshSend(%q, %q) error = %v, want one containing %q", tc.ns, tc.host, err, tc.wantErr)
			}

			cmds := received()
			if len(cmds) == 0 {
				t.Fatalf("MeshSend(%q, %q) sent nothing", tc.ns, tc.host)
			}

			if got := cmds[len(cmds)-1].Raw; got != tc.want {
				t.Errorf("MeshSend(%q, %q) sent %q, want %q", tc.ns, tc.host, got, tc.want)
			}
		})
	}
}
