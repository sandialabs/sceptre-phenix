package mm

import (
	"slices"
	"testing"

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
