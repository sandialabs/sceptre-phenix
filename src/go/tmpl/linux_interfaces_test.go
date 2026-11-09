package tmpl_test

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"phenix/tmpl"
	v1 "phenix/types/version/v1"
)

func TestLinuxInterfacesDNS(t *testing.T) {
	node := &v1.Node{
		NetworkF: &v1.Network{
			InterfacesF: []*v1.Interface{
				{ProtoF: "static", AddressF: "10.0.0.10", MaskF: 24, DNSF: []string{"10.0.0.53"}},
				{ProtoF: "static", AddressF: "10.0.1.10", MaskF: 24, DNSF: []string{"10.0.1.53", "8.8.8.8"}},
			},
		},
	}

	var buf bytes.Buffer
	if err := tmpl.GenerateFromTemplate("linux_interfaces.tmpl", node, &buf); err != nil {
		t.Fatalf("generating linux interfaces script: %v", err)
	}

	script := buf.String()

	for _, server := range []string{"10.0.0.53", "10.0.1.53", "8.8.8.8"} {
		// Once for the ip branch and once for the ifconfig fallback.
		if got := strings.Count(script, `dns+=("`+server+`")`); got != 2 {
			t.Errorf("dns+=(%q) appears %d times, want 2", server, got)
		}
	}

	if !strings.Contains(script, "/etc/systemd/resolved.conf.d/phenix.conf") {
		t.Error("script does not configure systemd-resolved")
	}

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}

	cmd := exec.Command(bash, "-n")
	cmd.Stdin = strings.NewReader(script)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("script is not valid bash: %v\n%s", err, out)
	}
}
