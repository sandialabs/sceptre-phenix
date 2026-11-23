package app_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/app"
	"phenix/store"
	"phenix/tmpl"
	"phenix/types"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
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

// TestVrouterPreStartNTPServerLabel verifies that routers use the address of
// the interface that the ntp-server label names, as the ntp app does.
func TestVrouterPreStartNTPServerLabel(t *testing.T) {
	ntpApp := &v2.ScenarioApp{
		NameF:     "ntp",
		MetadataF: map[string]any{"defaultSource": map[string]any{"address": "10.9.9.9"}},
		HostsF: []*v2.ScenarioAppHost{
			{HostnameF: "rtr1", MetadataF: map[string]any{"client": "ntp"}},
		},
	}

	tests := []struct {
		name     string
		labels   map[string]string
		eth1Addr string
		external bool
		want     []string
		wantErr  error
	}{
		{
			name:     "label names non-mgmt interface",
			labels:   map[string]string{"ntp-server": "eth1"},
			eth1Addr: "10.1.0.5",
			want:     []string{"set service ntp server 10.1.0.5 prefer"},
		},
		{
			name:     "label names unknown interface",
			labels:   map[string]string{"ntp-server": "eth9"},
			eth1Addr: "10.1.0.5",
			wantErr:  app.ErrNTPServerAddressNotFound,
		},
		{
			name:    "labeled interface has no address",
			labels:  map[string]string{"ntp-server": "eth1"},
			wantErr: app.ErrNTPServerAddressNotFound,
		},
		{
			name:     "external labeled node without a network",
			labels:   map[string]string{"ntp-server": "eth1"},
			external: true,
			wantErr:  app.ErrNTPServerAddressNotFound,
		},
		{
			name:     "no label falls back to ntp app metadata",
			eth1Addr: "10.1.0.5",
			want:     []string{"set service ntp server 10.9.9.9 prefer"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &v1.Node{
				LabelsF:  tt.labels,
				TypeF:    "VirtualMachine",
				GeneralF: &v1.General{HostnameF: "ntp1"},
				NetworkF: &v1.Network{InterfacesF: []*v1.Interface{
					{NameF: "mgmt", VLANF: "MGMT", AddressF: "172.16.0.5"},
					{NameF: "eth1", VLANF: "EXP", AddressF: tt.eth1Addr},
				}},
			}
			if tt.external {
				server.ExternalF = &tt.external
				server.NetworkF = nil
			}
			router := &v1.Node{
				TypeF:     "Router",
				GeneralF:  &v1.General{HostnameF: "rtr1"},
				HardwareF: &v1.Hardware{OSTypeF: "vyos"},
				NetworkF:  &v1.Network{},
			}

			baseDir := t.TempDir()
			exp := &types.Experiment{
				Metadata: store.ConfigMetadata{Name: "exp1"},
				Spec: &v1.ExperimentSpec{
					ExperimentNameF: "exp1",
					BaseDirF:        baseDir,
					TopologyF:       &v1.TopologySpec{NodesF: []*v1.Node{server, router}},
					ScenarioF:       &v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{ntpApp}},
				},
			}

			err := new(app.Vrouter).PreStart(context.Background(), exp)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("PreStart() error = %v, want %v", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("PreStart() error = %v", err)
			}

			script, err := os.ReadFile(filepath.Join(baseDir, "vrouter", "rtr1.boot"))
			if err != nil {
				t.Fatalf("reading rendered router config: %v", err)
			}

			var got []string

			for line := range strings.SplitSeq(string(script), "\n") {
				if strings.Contains(line, "service ntp") {
					got = append(got, strings.TrimSpace(line))
				}
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("NTP lines in rendered router config mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
