package mm

import "testing"

// TestVNCEndpointCommandFiltersKVM checks that the VNC lookup keeps its
// type=kvm filter: only KVM VMs have VNC.
func TestVNCEndpointCommandFiltersKVM(t *testing.T) {
	got := vncEndpointCommand(NewOptions(NS("exp"), VMName("vm"))).String()
	want := `.record false namespace "exp" .columns "host","vnc_port" .filter name=vm .filter type=kvm vm info`

	if got != want {
		t.Errorf("vncEndpointCommand(exp, vm) = %q, want %q", got, want)
	}
}

func TestVMIsFirecracker(t *testing.T) {
	tests := []struct {
		vmType string
		want   bool
	}{
		{vmType: "firecracker", want: true},
		{vmType: "kvm", want: false},
		{vmType: "container", want: false},
		{vmType: "", want: false},
	}

	for _, test := range tests {
		t.Run(test.vmType, func(t *testing.T) {
			if got := (VM{VMType: test.vmType}).IsFirecracker(); got != test.want {
				t.Errorf("VM{VMType: %q}.IsFirecracker() = %t, want %t", test.vmType, got, test.want)
			}
		})
	}
}
