package mm

import "testing"

func TestConnectVMInterfaceCommand(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
		want string
	}{
		{
			name: "with bridge",
			opts: []Option{
				VMName("test-vm"),
				ConnectInterface(1),
				ConnectVLAN("EXP_1"),
				Bridge("phenix"),
			},
			want: "vm net connect test-vm 1 EXP_1 phenix",
		},
		{
			name: "without bridge",
			opts: []Option{
				VMName("test-vm"),
				ConnectInterface(1),
				ConnectVLAN("EXP_1"),
			},
			want: "vm net connect test-vm 1 EXP_1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := connectVMInterfaceCommand(NewOptions(test.opts...)); got != test.want {
				t.Fatalf("connectVMInterfaceCommand returned %q, want %q", got, test.want)
			}
		})
	}
}
