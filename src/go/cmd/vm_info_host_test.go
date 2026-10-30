package cmd

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/util/mm"
)

func vmNames(vms []mm.VM) []string {
	names := make([]string, 0, len(vms))
	for _, vmInfo := range vms {
		names = append(names, vmInfo.Name)
	}

	return names
}

func TestFilterVMsByHost(t *testing.T) {
	t.Parallel()

	vms := []mm.VM{
		{Name: "server", Host: "compute1"},
		{Name: "client", Host: "compute2"},
		{Name: "router", Host: "compute1"},
		{Name: "external", Host: ""},
	}

	tests := []struct {
		name    string
		hosts   []string
		want    []string
		wantErr string
	}{
		{name: "no hosts keeps every VM", want: []string{"server", "client", "router", "external"}},
		{name: "one host", hosts: []string{"compute1"}, want: []string{"server", "router"}},
		{name: "two hosts widen the list", hosts: []string{"compute2", "compute1"}, want: []string{"server", "client", "router"}},
		{name: "host names are exact", hosts: []string{"COMPUTE1"}, wantErr: "no VMs matched host(s): COMPUTE1"},
		{name: "no prefix match", hosts: []string{"compute"}, wantErr: "no VMs matched host(s): compute"},
		{name: "unknown hosts", hosts: []string{"a", "b"}, wantErr: "no VMs matched host(s): a, b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := filterVMsByHost(vms, tt.hosts)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("filterVMsByHost(%q) error = %v, want %q", tt.hosts, err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("filterVMsByHost(%q) error = %v", tt.hosts, err)
			}

			if diff := cmp.Diff(tt.want, vmNames(got)); diff != "" {
				t.Errorf("filterVMsByHost(%q) mismatch (-want +got):\n%s", tt.hosts, diff)
			}
		})
	}
}

func TestVMInfoHosts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    []string
		wantErr string
	}{
		{name: "flag not set", args: nil, want: nil},
		{name: "repeated flag", args: []string{"--host", "compute1", "--host", "compute2"}, want: []string{"compute1", "compute2"}},
		{name: "comma separated", args: []string{"--host", "compute1, compute2"}, want: []string{"compute1", "compute2"}},
		{name: "blank value", args: []string{"--host", " , "}, wantErr: "must provide at least one VM host filter"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newVMInfoCmd()
			if err := c.ParseFlags(tt.args); err != nil {
				t.Fatalf("ParseFlags(%q) error = %v", tt.args, err)
			}

			got, err := vmInfoHosts(c)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("vmInfoHosts(%q) error = %v, want %q", tt.args, err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("vmInfoHosts(%q) error = %v", tt.args, err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("vmInfoHosts(%q) mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

func TestVMInfoCmdCompletesHosts(t *testing.T) {
	t.Parallel()

	if _, ok := newVMInfoCmd().GetFlagCompletionFunc("host"); !ok {
		t.Error("newVMInfoCmd() registers no completion for --host, want one")
	}
}

func TestVMHostNames(t *testing.T) {
	t.Parallel()

	vms := []mm.VM{
		{Name: "a", Host: "compute2"},
		{Name: "b", Host: "compute1"},
		{Name: "c", Host: "compute2"},
		{Name: "d", Host: "gpu1"},
		{Name: "e", Host: ""},
	}

	tests := []struct {
		prefix string
		want   []string
	}{
		{prefix: "", want: []string{"compute1", "compute2", "gpu1"}},
		{prefix: "comp", want: []string{"compute1", "compute2"}},
		{prefix: "x", want: nil},
	}

	for _, tt := range tests {
		if diff := cmp.Diff(tt.want, vmHostNames(vms, tt.prefix)); diff != "" {
			t.Errorf("vmHostNames(%q) mismatch (-want +got):\n%s", tt.prefix, diff)
		}
	}
}

func TestFilterVMsByHostProperties(t *testing.T) {
	t.Parallel()

	genHost := rapid.SampledFrom([]string{"", "h1", "H1", "h2", "h3"})
	genHosts := rapid.SliceOfN(rapid.SampledFrom([]string{"h1", "H1", "h2", "h3", "h"}), 0, 3)

	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 6).Draw(t, "n")
		vms := make([]mm.VM, n)

		for i := range vms {
			vms[i] = mm.VM{Name: "vm-" + strings.Repeat("x", i), Host: genHost.Draw(t, "host")}
		}

		first := genHosts.Draw(t, "first")
		second := genHosts.Draw(t, "second")

		filter := func(hosts []string) []string {
			got, err := filterVMsByHost(vms, hosts)
			if err != nil {
				return []string{}
			}

			return vmNames(got)
		}

		// the model: the full list, in order, kept to VMs whose host is named
		want := []string{}
		for _, vmInfo := range vms {
			if len(first) == 0 || (vmInfo.Host != "" && slices.Contains(first, vmInfo.Host)) {
				want = append(want, vmInfo.Name)
			}
		}

		got, err := filterVMsByHost(vms, first)
		if (err != nil) != (len(want) == 0 && len(first) > 0) {
			t.Fatalf("filterVMsByHost(%q) error = %v, want an error only when no VM matches", first, err)
		}

		if err == nil {
			if diff := cmp.Diff(want, vmNames(got)); diff != "" {
				t.Fatalf("filterVMsByHost(%q) mismatch (-want +got):\n%s", first, diff)
			}
		}

		// --host values are ORed: two flags keep the VMs either one keeps
		if len(first) > 0 && len(second) > 0 {
			both := filter(slices.Concat(first, second))
			either := []string{}

			for _, name := range vmNames(vms) {
				if slices.Contains(filter(first), name) || slices.Contains(filter(second), name) {
					either = append(either, name)
				}
			}

			if diff := cmp.Diff(either, both); diff != "" {
				t.Fatalf("filterVMsByHost(%q) mismatch (-either +both):\n%s", slices.Concat(first, second), diff)
			}
		}
	})
}
