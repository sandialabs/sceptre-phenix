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

func TestDiskDeleteCommand(t *testing.T) {
	tests := []struct {
		osType string
		want   string
	}{
		{
			osType: "linux",
			want:   `disk inject vm_snapshot:1 delete files "/etc/old-config","/var/tmp/stale dir"`,
		},
		{
			osType: "windows",
			want: `disk inject vm_snapshot:1 options "-t lowntfs-3g -o ignore_case" ` +
				`delete files "/etc/old-config","/var/tmp/stale dir"`,
		},
	}

	for _, test := range tests {
		t.Run(test.osType, func(t *testing.T) {
			got := diskDeleteCommand(
				"vm_snapshot", 1, test.osType, "/etc/old-config", "/var/tmp/stale dir",
			)
			if got != test.want {
				t.Fatalf("diskDeleteCommand returned %q, want %q", got, test.want)
			}
		})
	}
}
