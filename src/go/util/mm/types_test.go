package mm

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"
)

func TestVMOnAnyHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		host  string
		hosts []string
		want  bool
	}{
		{name: "named host", host: "compute1", hosts: []string{"compute1"}, want: true},
		{name: "any of several", host: "compute2", hosts: []string{"compute1", "compute2"}, want: true},
		{name: "other host", host: "compute1", hosts: []string{"compute2"}, want: false},
		{name: "case matters", host: "compute1", hosts: []string{"COMPUTE1"}, want: false},
		{name: "no prefix match", host: "compute1", hosts: []string{"compute"}, want: false},
		{name: "no hosts", host: "compute1", hosts: nil, want: false},
		{name: "VM without a host", host: "", hosts: []string{""}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := (VM{Host: tt.host}).OnAnyHost(tt.hosts); got != tt.want {
				t.Errorf("VM{Host: %q}.OnAnyHost(%q) = %t, want %t", tt.host, tt.hosts, got, tt.want)
			}
		})
	}
}

func TestVMOnAnyHostProperties(t *testing.T) {
	t.Parallel()

	genHost := rapid.SampledFrom([]string{"", "h1", "H1", "h2"})

	rapid.Check(t, func(t *rapid.T) {
		vm := VM{Host: genHost.Draw(t, "host")}
		first := rapid.SliceOfN(genHost, 0, 3).Draw(t, "first")
		second := rapid.SliceOfN(genHost, 0, 3).Draw(t, "second")

		both := slices.Concat(first, second)
		if got, want := vm.OnAnyHost(both), vm.OnAnyHost(first) || vm.OnAnyHost(second); got != want {
			t.Fatalf("VM{Host: %q}.OnAnyHost(%q) = %t, want %t", vm.Host, both, got, want)
		}

		if got, want := vm.OnAnyHost(first), vm.Host != "" && slices.Contains(first, vm.Host); got != want {
			t.Fatalf("VM{Host: %q}.OnAnyHost(%q) = %t, want %t", vm.Host, first, got, want)
		}
	})
}

// typesHostSorters names each Hosts sort and the key it orders hosts by.
//
//nolint:gochecknoglobals // read-only test table
var typesHostSorters = []struct {
	name string
	sort func(Hosts, bool)
	key  func(Host) int
}{
	{"unallocated CPU", Hosts.SortByUnallocatedCPU, func(h Host) int { return h.CPUs - h.CPUCommit }},
	{"committed CPU", Hosts.SortByCommittedCPU, func(h Host) int { return h.CPUCommit }},
	{"unallocated memory", Hosts.SortByUnallocatedMem, func(h Host) int { return h.MemTotal - h.MemCommit }},
	{"committed memory", Hosts.SortByCommittedMem, func(h Host) int { return h.MemCommit }},
	{"VMs", Hosts.SortByVMs, func(h Host) int { return h.VMs }},
}

func TestHostsSort(t *testing.T) {
	t.Parallel()

	// Each key orders the hosts differently, so a sort by the wrong key fails.
	hosts := Hosts{
		{Name: "a", CPUs: 8, CPUCommit: 1, MemTotal: 100, MemCommit: 90, VMs: 2},
		{Name: "b", CPUs: 4, CPUCommit: 3, MemTotal: 300, MemCommit: 10, VMs: 3},
		{Name: "c", CPUs: 16, CPUCommit: 2, MemTotal: 200, MemCommit: 50, VMs: 1},
	}

	want := map[string][]string{
		"unallocated CPU":    {"b", "a", "c"},
		"committed CPU":      {"a", "c", "b"},
		"unallocated memory": {"a", "c", "b"},
		"committed memory":   {"b", "c", "a"},
		"VMs":                {"c", "a", "b"},
	}

	for _, sorter := range typesHostSorters {
		for _, asc := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s ascending %t", sorter.name, asc), func(t *testing.T) {
				t.Parallel()

				got := slices.Clone(hosts)
				sorter.sort(got, asc)

				names := make([]string, 0, len(got))
				for _, h := range got {
					names = append(names, h.Name)
				}

				expected := slices.Clone(want[sorter.name])
				if !asc {
					slices.Reverse(expected)
				}

				if diff := gocmp.Diff(expected, names); diff != "" {
					t.Errorf("Hosts.SortBy %s(%t) order mismatch (-want +got):\n%s", sorter.name, asc, diff)
				}
			})
		}
	}

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		for _, sorter := range typesHostSorters {
			var none Hosts
			sorter.sort(none, true)

			if none != nil {
				t.Errorf("Hosts(nil) sorted by %s = %v, want nil", sorter.name, none)
			}
		}
	})
}

// Every Hosts sort keeps the same hosts and leaves them ordered by its key.
func TestHostsSortProperties(t *testing.T) {
	t.Parallel()

	genHost := rapid.Custom(func(t *rapid.T) Host {
		return Host{
			Name:      rapid.StringMatching(`[a-z]{1,3}`).Draw(t, "name"),
			CPUs:      rapid.IntRange(0, 64).Draw(t, "cpus"),
			CPUCommit: rapid.IntRange(0, 64).Draw(t, "cpucommit"),
			MemTotal:  rapid.IntRange(0, 1024).Draw(t, "memtotal"),
			MemCommit: rapid.IntRange(0, 1024).Draw(t, "memcommit"),
			VMs:       rapid.IntRange(0, 10).Draw(t, "vms"),
		}
	})

	rapid.Check(t, func(t *rapid.T) {
		hosts := Hosts(rapid.SliceOfN(genHost, 0, 8).Draw(t, "hosts"))
		sorter := rapid.SampledFrom(typesHostSorters).Draw(t, "sorter")
		asc := rapid.Bool().Draw(t, "asc")

		got := slices.Clone(hosts)
		sorter.sort(got, asc)

		byName := func(a, b Host) int { return strings.Compare(a.Name+fmt.Sprint(a), b.Name+fmt.Sprint(b)) }
		if diff := gocmp.Diff(hosts, got, cmpopts.SortSlices(func(a, b Host) bool { return byName(a, b) < 0 })); diff != "" {
			t.Fatalf("Hosts sorted by %s changed its hosts (-want +got):\n%s", sorter.name, diff)
		}

		ordered := slices.IsSortedFunc(got, func(a, b Host) int {
			if asc {
				return cmp.Compare(sorter.key(a), sorter.key(b))
			}

			return cmp.Compare(sorter.key(b), sorter.key(a))
		})
		if !ordered {
			t.Fatalf("Hosts sorted by %s (ascending %t) = %+v, not in key order", sorter.name, asc, got)
		}
	})
}

func TestHostsFindHostByName(t *testing.T) {
	t.Parallel()

	hosts := Hosts{{Name: "head", VMs: 1}, {Name: "compute1", VMs: 2}, {Name: "compute1", VMs: 3}}

	tests := []struct {
		name  string
		hosts Hosts
		find  string
		want  *Host
	}{
		{name: "found", hosts: hosts, find: "head", want: &Host{Name: "head", VMs: 1}},
		{name: "first of two with the name", hosts: hosts, find: "compute1", want: &Host{Name: "compute1", VMs: 2}},
		{name: "case matters", hosts: hosts, find: "HEAD", want: nil},
		{name: "missing", hosts: hosts, find: "compute2", want: nil},
		{name: "empty name", hosts: hosts, find: "", want: nil},
		{name: "no hosts", hosts: nil, find: "head", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := gocmp.Diff(tt.want, tt.hosts.FindHostByName(tt.find)); diff != "" {
				t.Errorf("Hosts.FindHostByName(%q) mismatch (-want +got):\n%s", tt.find, diff)
			}
		})
	}
}

func TestHostsIncr(t *testing.T) {
	t.Parallel()

	incrs := map[string]struct {
		incr func(Hosts, string, int) error
		set  func(*Host, int)
	}{
		"VMs":        {Hosts.IncrHostVMs, func(h *Host, n int) { h.VMs = n }},
		"CPU commit": {Hosts.IncrHostCPUCommit, func(h *Host, n int) { h.CPUCommit = n }},
		"mem commit": {Hosts.IncrHostMemCommit, func(h *Host, n int) { h.MemCommit = n }},
	}

	tests := []struct {
		name    string
		host    string
		by      int
		want    []int // the field of each host afterwards; each starts at 10
		wantErr error
	}{
		{name: "adds to the named host only", host: "b", by: 3, want: []int{10, 13, 10}},
		{name: "adds to the first host with the name", host: "a", by: 2, want: []int{12, 10, 10}},
		{name: "subtracts", host: "b", by: -4, want: []int{10, 6, 10}},
		{name: "adds zero", host: "b", by: 0, want: []int{10, 10, 10}},
		{name: "missing host", host: "c", by: 1, want: []int{10, 10, 10}, wantErr: ErrHostNotFound},
		{name: "empty name", host: "", by: 1, want: []int{10, 10, 10}, wantErr: ErrHostNotFound},
	}

	for field, f := range incrs {
		for _, tt := range tests {
			t.Run(field+" "+tt.name, func(t *testing.T) {
				t.Parallel()

				hosts := Hosts{{Name: "a"}, {Name: "b"}, {Name: "a"}}
				for i := range hosts {
					f.set(&hosts[i], 10)
				}

				if err := f.incr(hosts, tt.host, tt.by); !errors.Is(err, tt.wantErr) {
					t.Errorf("Hosts.Incr %s(%q, %d) error = %v, want %v", field, tt.host, tt.by, err, tt.wantErr)
				}

				want := Hosts{{Name: "a"}, {Name: "b"}, {Name: "a"}}
				for i, n := range tt.want {
					f.set(&want[i], n)
				}

				if diff := gocmp.Diff(want, hosts); diff != "" {
					t.Errorf("Hosts after Incr %s(%q, %d) mismatch (-want +got):\n%s", field, tt.host, tt.by, diff)
				}
			})
		}
	}

	t.Run("no hosts", func(t *testing.T) {
		t.Parallel()

		for field, f := range incrs {
			if err := f.incr(nil, "a", 1); !errors.Is(err, ErrHostNotFound) {
				t.Errorf("Hosts(nil).Incr %s(a, 1) error = %v, want %v", field, err, ErrHostNotFound)
			}
		}
	})
}

func TestVMsSortBy(t *testing.T) {
	t.Parallel()

	vms := VMs{
		{Name: "b", Host: "Compute2", Uptime: 5},
		{Name: "C", Host: "compute1", Uptime: 1},
		{Name: "a", Host: "head", Uptime: 9},
	}

	tests := []struct {
		col  string
		asc  bool
		want []string // names in order
	}{
		{col: "name", asc: true, want: []string{"a", "b", "C"}},
		{col: "name", asc: false, want: []string{"C", "b", "a"}},
		{col: hostColumn, asc: true, want: []string{"C", "b", "a"}},
		{col: hostColumn, asc: false, want: []string{"a", "b", "C"}},
		{col: "uptime", asc: true, want: []string{"C", "b", "a"}},
		{col: "uptime", asc: false, want: []string{"a", "b", "C"}},
		{col: "unknown", asc: true, want: []string{"b", "C", "a"}},
		{col: "", asc: false, want: []string{"b", "C", "a"}},
		{col: "Name", asc: true, want: []string{"b", "C", "a"}},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s ascending %t", tt.col, tt.asc), func(t *testing.T) {
			t.Parallel()

			got := slices.Clone(vms)
			got.SortBy(tt.col, tt.asc)

			if diff := gocmp.Diff(tt.want, typesVMNames(got)); diff != "" {
				t.Errorf("VMs.SortBy(%q, %t) order mismatch (-want +got):\n%s", tt.col, tt.asc, diff)
			}
		})
	}
}

// typesVMNames returns the names of vms in order.
func typesVMNames(vms VMs) []string {
	names := make([]string, 0, len(vms))
	for _, vm := range vms {
		names = append(names, vm.Name)
	}

	return names
}

// VMs.SortBy keeps the same VMs and leaves them in the column's order, ignoring case for text.
func TestVMsSortByProperties(t *testing.T) {
	t.Parallel()

	genVM := rapid.Custom(func(t *rapid.T) VM {
		return VM{
			Name:   rapid.StringMatching(`[a-cA-C]{0,3}`).Draw(t, "name"),
			Host:   rapid.StringMatching(`[a-cA-C]{0,3}`).Draw(t, "host"),
			Uptime: float64(rapid.IntRange(0, 5).Draw(t, "uptime")),
		}
	})

	keys := map[string]func(a, b VM) int{
		"name":     func(a, b VM) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) },
		hostColumn: func(a, b VM) int { return strings.Compare(strings.ToLower(a.Host), strings.ToLower(b.Host)) },
		"uptime":   func(a, b VM) int { return cmp.Compare(a.Uptime, b.Uptime) },
	}

	rapid.Check(t, func(t *rapid.T) {
		vms := VMs(rapid.SliceOfN(genVM, 0, 8).Draw(t, "vms"))
		col := rapid.SampledFrom([]string{"name", hostColumn, "uptime"}).Draw(t, "col")
		asc := rapid.Bool().Draw(t, "asc")

		got := slices.Clone(vms)
		got.SortBy(col, asc)

		less := func(a, b VM) bool { return fmt.Sprint(a) < fmt.Sprint(b) }
		if diff := gocmp.Diff(vms, got, cmpopts.SortSlices(less), cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("VMs.SortBy(%q, %t) changed its VMs (-want +got):\n%s", col, asc, diff)
		}

		ordered := slices.IsSortedFunc(got, func(a, b VM) int {
			if asc {
				return keys[col](a, b)
			}

			return keys[col](b, a)
		})
		if !ordered {
			t.Fatalf("VMs.SortBy(%q, %t) = %+v, not in column order", col, asc, got)
		}
	})
}

func TestVMsPaginate(t *testing.T) {
	t.Parallel()

	vms := VMs{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}, {Name: "e"}}

	tests := []struct {
		name       string
		vms        VMs
		page, size int
		want       []string
	}{
		{name: "first page", vms: vms, page: 1, size: 2, want: []string{"a", "b"}},
		{name: "middle page", vms: vms, page: 2, size: 2, want: []string{"c", "d"}},
		{name: "short last page", vms: vms, page: 3, size: 2, want: []string{"e"}},
		{name: "past the end", vms: vms, page: 4, size: 2, want: []string{}},
		{name: "exactly full", vms: vms, page: 1, size: 5, want: []string{"a", "b", "c", "d", "e"}},
		{name: "page after exactly full", vms: vms, page: 2, size: 5, want: []string{}},
		{name: "size larger than the list", vms: vms, page: 1, size: 100, want: []string{"a", "b", "c", "d", "e"}},
		{name: "page of one", vms: vms, page: 5, size: 1, want: []string{"e"}},
		{name: "no VMs", vms: nil, page: 1, size: 10, want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.vms.Paginate(tt.page, tt.size)
			if got == nil {
				t.Fatalf("VMs.Paginate(%d, %d) = nil, want a non-nil list", tt.page, tt.size)
			}

			if diff := gocmp.Diff(tt.want, typesVMNames(got)); diff != "" {
				t.Errorf("VMs.Paginate(%d, %d) mismatch (-want +got):\n%s", tt.page, tt.size, diff)
			}
		})
	}
}

// Pages 1, 2, ... of any positive size, joined, give back the whole list.
func TestVMsPaginateProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 20).Draw(t, "n")
		size := rapid.IntRange(1, 25).Draw(t, "size")

		vms := make(VMs, n)
		for i := range vms {
			vms[i].ID = i
		}

		var joined VMs

		for page := 1; ; page++ {
			got := vms.Paginate(page, size)
			if len(got) > size {
				t.Fatalf("VMs.Paginate(%d, %d) of %d VMs has %d, want at most %d", page, size, n, len(got), size)
			}

			if len(got) == 0 {
				break
			}

			joined = append(joined, got...)
		}

		if diff := gocmp.Diff(vms, joined, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("VMs.Paginate pages of %d joined mismatch (-want +got):\n%s", size, diff)
		}
	})
}

func TestVMCopy(t *testing.T) {
	t.Parallel()

	newVM := func() VM {
		return VM{
			ID: 7, Name: "vm1", Host: "head", CPUs: 2, RAM: 1024, Uptime: 3.5, UUID: "uuid-1",
			IPv4:        []string{"10.0.0.1"},
			Networks:    []string{"EXP_1"},
			IfaceNames:  []string{"eth0"},
			Taps:        []string{"mega_tap1"},
			Captures:    []Capture{{VM: "vm1", Interface: 0, Filepath: "/a.pcap"}},
			Tags:        map[string]string{"k": "v"},
			Labels:      map[string]string{"k": "v"},
			Interfaces:  map[string]string{"EXP_1": "10.0.0.1"},
			Metadata:    map[string]any{"k": "v"},
			Annotations: map[string]any{"k": "v"},
		}
	}

	original := newVM()

	if diff := gocmp.Diff(original, original.Copy()); diff != "" {
		t.Fatalf("VM.Copy() mismatch (-want +got):\n%s", diff)
	}

	tests := []struct {
		field  string
		change func(cp VM)
	}{
		{field: "IPv4", change: func(cp VM) { cp.IPv4[0] = "x" }},
		{field: "Networks", change: func(cp VM) { cp.Networks[0] = "x" }},
		{field: "IfaceNames", change: func(cp VM) { cp.IfaceNames[0] = "x" }},
		{field: "Taps", change: func(cp VM) { cp.Taps[0] = "x" }},
		{field: "Captures", change: func(cp VM) { cp.Captures[0].Filepath = "x" }},
		{field: "Tags", change: func(cp VM) { cp.Tags["k"], cp.Tags["new"] = "x", "x" }},
		{field: "Labels", change: func(cp VM) { cp.Labels["k"], cp.Labels["new"] = "x", "x" }},
		{field: "Interfaces", change: func(cp VM) { cp.Interfaces["EXP_1"], cp.Interfaces["new"] = "x", "x" }},
		{field: "Metadata", change: func(cp VM) { cp.Metadata["k"], cp.Metadata["new"] = "x", "x" }},
		{field: "Annotations", change: func(cp VM) { cp.Annotations["k"], cp.Annotations["new"] = "x", "x" }},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()

			vm := newVM()
			tt.change(vm.Copy())

			if diff := gocmp.Diff(newVM(), vm); diff != "" {
				t.Errorf("VM after changing %s of its Copy() mismatch (-want +got):\n%s", tt.field, diff)
			}
		})
	}

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		got := VM{Name: "vm1"}.Copy()

		want := VM{
			Name: "vm1", IPv4: []string{}, Networks: []string{}, IfaceNames: []string{}, Taps: []string{},
			Captures: []Capture{}, Tags: map[string]string{},
		}

		if diff := gocmp.Diff(want, got); diff != "" {
			t.Errorf("VM{}.Copy() mismatch (-want +got):\n%s", diff)
		}
	})
}
