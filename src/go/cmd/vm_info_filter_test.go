package cmd

import (
	"strings"
	"testing"

	"phenix/util/mm"
)

func TestParseVMInfoFilters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		raw        string
		wantColumn string
		wantValue  string
	}{
		{name: "standard column", raw: "RUNNING=true", wantColumn: "running", wantValue: "true"},
		{name: "column with spaces", raw: "OS TYPE=linux", wantColumn: "ostype", wantValue: "linux"},
		{name: "column with underscore", raw: "OS_TYPE=linux", wantColumn: "ostype", wantValue: "linux"},
		{name: "RAM alias", raw: "RAM=1024", wantColumn: "memory", wantValue: "1024"},
		{name: "CPUs alias", raw: "CPUS=2", wantColumn: "vcpus", wantValue: "2"},
		{name: "empty value", raw: "UPTIME=", wantColumn: "uptime", wantValue: ""},
		{name: "value containing equals", raw: "DISK=disk=name", wantColumn: "disk", wantValue: "disk=name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseVMInfoFilters([]string{tt.raw})
			if err != nil {
				t.Fatalf("parseVMInfoFilters() error = %v", err)
			}

			if len(got) != 1 {
				t.Fatalf("parseVMInfoFilters() returned %d filters, want 1", len(got))
			}

			if got[0].column != tt.wantColumn || got[0].value != tt.wantValue {
				t.Fatalf("parseVMInfoFilters() = %+v, want column %q and value %q", got[0], tt.wantColumn, tt.wantValue)
			}
		})
	}
}

func TestParseVMInfoFiltersRejectsInvalidFilters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "missing separator", raw: "RUNNING", want: "expected COLUMN=VALUE"},
		{name: "missing column", raw: "=true", want: "expected COLUMN=VALUE"},
		{name: "unknown column", raw: "STATE=running", want: "unknown VM info filter column"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseVMInfoFilters([]string{tt.raw})
			if err == nil {
				t.Fatal("parseVMInfoFilters() expected an error")
			}

			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseVMInfoFilters() error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestFilterVMInfo(t *testing.T) {
	t.Parallel()

	vms := []mm.VM{
		{
			Name:    "server",
			Host:    "node-1",
			Running: true,
			CPUs:    2,
			RAM:     2048,
			Disk:    "/phenix/images/jammy.qc2",
			OSType:  "Linux",
		},
		{
			Name:    "client",
			Host:    "node-2",
			Running: false,
			CPUs:    1,
			RAM:     1024,
			OSType:  "Linux",
		},
		{
			Name:    "router",
			Host:    "node-1",
			Running: true,
			CPUs:    1,
			RAM:     1024,
			OSType:  "VyOS",
		},
	}

	tests := []struct {
		name    string
		filters []string
		want    []string
	}{
		{name: "no filters", want: []string{"server", "client", "router"}},
		{name: "running VMs", filters: []string{"RUNNING=true"}, want: []string{"server", "router"}},
		{name: "case insensitive value", filters: []string{"OS_TYPE=linux"}, want: []string{"server", "client"}},
		{name: "numeric value", filters: []string{"MEMORY=1024"}, want: []string{"client", "router"}},
		{name: "disk full path", filters: []string{"DISK=/phenix/images/jammy.qc2"}, want: []string{"server"}},
		{name: "disk filename", filters: []string{"DISK=jammy.qc2"}, want: []string{"server"}},
		{name: "disk filename stem", filters: []string{"DISK=jammy"}, want: []string{"server"}},
		{name: "disk filename case insensitive", filters: []string{"DISK=JAMMY.QC2"}, want: []string{"server"}},
		{name: "multiple filters use AND", filters: []string{"HOST=node-1", "VCPUS=1"}, want: []string{"router"}},
		{name: "no matches", filters: []string{"NAME=missing"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := filterVMInfo(vms, tt.filters)
			if err != nil {
				t.Fatalf("filterVMInfo() error = %v", err)
			}

			if len(got) != len(tt.want) {
				t.Fatalf("filterVMInfo() returned %d VMs, want %d: %+v", len(got), len(tt.want), got)
			}

			for idx, vmInfo := range got {
				if vmInfo.Name != tt.want[idx] {
					t.Fatalf("filterVMInfo()[%d].Name = %q, want %q", idx, vmInfo.Name, tt.want[idx])
				}
			}
		})
	}
}

func TestVMInfoColumnValue(t *testing.T) {
	t.Parallel()

	vmInfo := mm.VM{
		Host:     "node-1",
		Name:     "server",
		Running:  true,
		Disk:     "server.qcow2",
		Networks: []string{"users"},
		IPv4:     []string{"10.0.0.2"},
		Uptime:   90,
		RAM:      2048,
		CPUs:     2,
		OSType:   "linux",
		Taps:     []string{"mega_tap1", "mega_tap2"},
	}

	tests := []struct {
		column string
		want   string
	}{
		{column: "host", want: "node-1"},
		{column: "name", want: "server"},
		{column: "running", want: "true"},
		{column: "disk", want: "server.qcow2"},
		{column: "interfaces", want: "ID: 0, IP: 10.0.0.2, VLAN: users"},
		{column: "uptime", want: "1m30s"},
		{column: "memory", want: "2048"},
		{column: "vcpus", want: "2"},
		{column: "ostype", want: "linux"},
		{column: "taps", want: "mega_tap1, mega_tap2"},
	}

	for _, tt := range tests {
		t.Run(tt.column, func(t *testing.T) {
			t.Parallel()

			if got := vmInfoColumnValue(vmInfo, tt.column); got != tt.want {
				t.Fatalf("vmInfoColumnValue(%q) = %q, want %q", tt.column, got, tt.want)
			}
		})
	}
}

func TestVMInfoCommandFilterFlag(t *testing.T) {
	t.Parallel()

	cmd := newVMInfoCmd()
	flag := cmd.Flags().Lookup("filter")
	if flag == nil {
		t.Fatal("newVMInfoCmd() does not define --filter")
	}

	if flag.Shorthand != "f" {
		t.Fatalf("--filter shorthand = %q, want %q", flag.Shorthand, "f")
	}
}
