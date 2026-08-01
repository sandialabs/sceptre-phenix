package app_test

import (
	"bytes"
	"strings"
	"testing"

	"phenix/tmpl"
	v1 "phenix/types/version/v1"
)

// TestVyattaTemplateHostnameVerbatim verifies that vyatta.tmpl sets the router's
// system host-name to the topology hostname exactly as written, so the guest
// hostname matches the minimega VM name used by cc filters and tag matching.
func TestVyattaTemplateHostnameVerbatim(t *testing.T) {
	const hostname = "Branch-Router-01"

	tests := []struct {
		name string
		vyos bool
		want string
	}{
		{name: "vyos script", vyos: true, want: "set system host-name " + hostname + "\n"},
		{name: "vyatta config.boot", vyos: false, want: "host-name " + hostname + "\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &v1.Node{
				TypeF:    "Router",
				GeneralF: &v1.General{HostnameF: hostname},
				NetworkF: &v1.Network{},
			}

			data := map[string]any{"node": node, "vyos": tt.vyos}

			var buf bytes.Buffer

			if err := tmpl.GenerateFromTemplate("vyatta.tmpl", data, &buf); err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(buf.String(), tt.want) {
				t.Fatalf("expected %q in rendered config:\n%s", tt.want, buf.String())
			}
		})
	}
}
