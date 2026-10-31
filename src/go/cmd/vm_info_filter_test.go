package cmd

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

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

// genVMInfoFilterSpelling draws a --filter of column (or alias) spelled in any
// case, with spaces, dashes and underscores in it, and the canonical column.
func genVMInfoFilterSpelling() *rapid.Generator[[2]string] {
	columns := map[string]string{
		vmInfoColumnHost: vmInfoColumnHost, vmInfoColumnName: vmInfoColumnName,
		vmInfoColumnRunning: vmInfoColumnRunning, vmInfoColumnDisk: vmInfoColumnDisk,
		vmInfoColumnInterfaces: vmInfoColumnInterfaces, vmInfoColumnUptime: vmInfoColumnUptime,
		vmInfoColumnMemory: vmInfoColumnMemory, vmInfoColumnVCPUs: vmInfoColumnVCPUs,
		vmInfoColumnOSType: vmInfoColumnOSType, vmInfoColumnTaps: vmInfoColumnTaps,
		"ram": vmInfoColumnMemory, "cpus": vmInfoColumnVCPUs,
	}

	return rapid.Custom(func(t *rapid.T) [2]string {
		name := rapid.SampledFrom(slices.Sorted(maps.Keys(columns))).Draw(t, "column")
		separator := rapid.SampledFrom([]string{"", "", " ", "-", "_"})

		var spelled strings.Builder

		spelled.WriteString(rapid.SampledFrom([]string{"", " ", "\t"}).Draw(t, "lead"))

		for i, r := range name {
			if rapid.Bool().Draw(t, "upper") {
				r -= 'a' - 'A'
			}

			spelled.WriteRune(r)

			if i < len(name)-1 {
				spelled.WriteString(separator.Draw(t, "separator"))
			}
		}

		spelled.WriteString(rapid.SampledFrom([]string{"", " ", "\t"}).Draw(t, "trail"))

		return [2]string{spelled.String(), columns[name]}
	})
}

func TestParseVMInfoFiltersProperties(t *testing.T) {
	t.Parallel()

	genValue := rapid.StringOfN(rapid.RuneFrom([]rune("a1= \t")), 0, 6, -1)

	t.Run("spelling", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			spelling := genVMInfoFilterSpelling().Draw(t, "spelling")
			value := genValue.Draw(t, "value")
			raw := spelling[0] + "=" + value

			got, err := parseVMInfoFilters([]string{raw})
			want := []vmInfoFilter{{column: spelling[1], value: strings.TrimSpace(value)}}

			if err != nil {
				t.Fatalf("parseVMInfoFilters([%q]) error = %v", raw, err)
			}

			if diff := cmp.Diff(want, got, cmp.AllowUnexported(vmInfoFilter{})); diff != "" {
				t.Fatalf("parseVMInfoFilters([%q]) mismatch (-want +got):\n%s", raw, diff)
			}
		})
	})

	// each --filter parses on its own: a list fails as soon as one does
	t.Run("concatenation", func(t *testing.T) {
		t.Parallel()

		genRaw := rapid.OneOf(
			rapid.Map(genVMInfoFilterSpelling(), func(s [2]string) string { return s[0] + "=x" }),
			rapid.SampledFrom([]string{"STATE=on", "RUNNING", "=true", " =1", "Taps"}),
		)
		parseEach := func(raw []string) ([]vmInfoFilter, bool) {
			var each []vmInfoFilter

			for _, r := range raw {
				one, err := parseVMInfoFilters([]string{r})
				if err != nil {
					return nil, false
				}

				each = append(each, one...)
			}

			return each, true
		}

		rapid.Check(t, func(t *rapid.T) {
			raw := rapid.SliceOfN(genRaw, 0, 4).Draw(t, "raw")

			got, err := parseVMInfoFilters(raw)
			want, ok := parseEach(raw)

			if (err == nil) != ok {
				t.Fatalf("parseVMInfoFilters(%q) error = %v, want an error %t", raw, err, !ok)
			}

			if diff := cmp.Diff(want, got, cmp.AllowUnexported(vmInfoFilter{}), cmpopts.EquateEmpty()); ok && diff != "" {
				t.Fatalf("parseVMInfoFilters(%q) mismatch (-each +all):\n%s", raw, diff)
			}
		})
	})
}

// genVMInfoFilterVMs draws VMs with IDs 0..n-1 whose columns take few values.
func genVMInfoFilterVMs() *rapid.Generator[[]mm.VM] {
	genVM := rapid.Custom(func(t *rapid.T) mm.VM {
		return mm.VM{
			Name:    rapid.SampledFrom([]string{"a", "b"}).Draw(t, "name"),
			Host:    rapid.SampledFrom([]string{"n1", "n2"}).Draw(t, "host"),
			Running: rapid.Bool().Draw(t, "running"),
			CPUs:    rapid.IntRange(1, 2).Draw(t, "cpus"),
			Disk:    rapid.SampledFrom([]string{"", "/i/x.qc2", "y.qc2"}).Draw(t, "disk"),
			OSType:  rapid.SampledFrom([]string{"linux", "Linux", "vyos"}).Draw(t, "os"),
		}
	})

	return rapid.Map(rapid.SliceOfN(genVM, 0, 6), func(vms []mm.VM) []mm.VM {
		for i := range vms {
			vms[i].ID = i
		}

		return vms
	})
}

func vmInfoFilterIDs(vms []mm.VM) []int {
	ids := make([]int, 0, len(vms))
	for _, v := range vms {
		ids = append(ids, v.ID)
	}

	return ids
}

func TestFilterVMInfoProperties(t *testing.T) {
	t.Parallel()

	// what each filter keeps, spelled out per VM
	model := map[string]func(v mm.VM) bool{
		"name=a":        func(v mm.VM) bool { return v.Name == "a" },
		"HOST=n1":       func(v mm.VM) bool { return v.Host == "n1" },
		"running=true":  func(v mm.VM) bool { return v.Running },
		"Running=FALSE": func(v mm.VM) bool { return !v.Running },
		"vcpus=1":       func(v mm.VM) bool { return v.CPUs == 1 },
		"cpus=2":        func(v mm.VM) bool { return v.CPUs == 2 },
		"disk=x":        func(v mm.VM) bool { return v.Disk == "/i/x.qc2" },
		"DISK=y.qc2":    func(v mm.VM) bool { return v.Disk == "y.qc2" },
		"disk=/i/x.qc2": func(v mm.VM) bool { return v.Disk == "/i/x.qc2" },
		"disk=":         func(v mm.VM) bool { return v.Disk == "" },
		"os_type=LINUX": func(v mm.VM) bool { return v.OSType != "vyos" },
		"OS TYPE=vyos":  func(v mm.VM) bool { return v.OSType == "vyos" },
	}
	genFilters := rapid.SliceOfN(rapid.SampledFrom(slices.Sorted(maps.Keys(model))), 0, 4)

	rapid.Check(t, func(t *rapid.T) {
		vms := genVMInfoFilterVMs().Draw(t, "vms")
		filters := genFilters.Draw(t, "filters")

		got, err := filterVMInfo(vms, filters)
		if err != nil {
			t.Fatalf("filterVMInfo(vms, %q) error = %v", filters, err)
		}

		// filters are ANDed, and the VMs keep their order
		want := make([]int, 0, len(vms))

		for _, v := range vms {
			if !slices.ContainsFunc(filters, func(f string) bool { return !model[f](v) }) {
				want = append(want, v.ID)
			}
		}

		if diff := cmp.Diff(want, vmInfoFilterIDs(got), cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("filterVMInfo(vms, %q) IDs mismatch (-want +got):\n%s", filters, diff)
		}
	})
}
