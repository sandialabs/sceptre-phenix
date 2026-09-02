package app

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
)

const (
	syslogTestHost = "soc"
	syslogTestAddr = "10.1.101.60"
)

// newSyslogTestNode builds a node with an addressed eth0 and an address-less eth1.
func newSyslogTestNode(t *testing.T, hostname, osType, addr string) *v1.Node {
	t.Helper()

	node := newStartupTestNode(t, hostname, osType, false, 0)
	node.NetworkF.InterfacesF = []*v1.Interface{
		{NameF: "eth0", AddressF: addr, MaskF: 24},
		{NameF: "eth1"},
	}

	return node
}

// newVrouterTestExperiment builds an experiment from the given nodes and scenario.
func newVrouterTestExperiment(t *testing.T, scenario *v2.ScenarioSpec, nodes ...*v1.Node) *types.Experiment {
	t.Helper()

	spec := &v1.ExperimentSpec{
		ExperimentNameF: "exp1",
		BaseDirF:        filepath.Join(t.TempDir(), "experiments", "exp1"),
		TopologyF:       &v1.TopologySpec{NodesF: nodes},
		ScenarioF:       scenario,
	}
	if err := spec.Init(); err != nil {
		t.Fatalf("initializing test experiment spec: %v", err)
	}

	status := &v1.ExperimentStatus{}
	if err := status.Init(); err != nil {
		t.Fatalf("initializing test experiment status: %v", err)
	}

	return &types.Experiment{
		Metadata: store.ConfigMetadata{Name: "exp1"},
		Spec:     spec,
		Status:   status,
	}
}

// runVrouterPreStart runs vrouter PreStart for a router with the given syslog metadata and returns its boot config.
func runVrouterPreStart(t *testing.T, osType string, syslog []map[string]any) (string, error) {
	t.Helper()

	router := newSyslogTestNode(t, "rtr", osType, "10.1.101.1")
	router.TypeF = "Router"

	scenario := &v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{
		{
			NameF: appNameVrouter,
			HostsF: []*v2.ScenarioAppHost{
				{HostnameF: "rtr", MetadataF: map[string]any{"syslog": syslog}},
			},
		},
	}}

	exp := newVrouterTestExperiment(t, scenario, router, newSyslogTestNode(t, syslogTestHost, osLinux, syslogTestAddr))

	if err := new(Vrouter).PreStart(t.Context(), exp); err != nil {
		return "", err
	}

	out, err := os.ReadFile(filepath.Join(exp.Spec.BaseDir(), "vrouter", "rtr.boot"))
	if err != nil {
		t.Fatalf("reading rendered router config: %v", err)
	}

	return string(out), nil
}

func TestResolveAddress(t *testing.T) {
	topo := &v1.TopologySpec{NodesF: []*v1.Node{newSyslogTestNode(t, syslogTestHost, osLinux, syslogTestAddr)}}

	tests := []struct {
		addr    string
		want    string
		wantErr bool
	}{
		{addr: syslogTestAddr, want: syslogTestAddr},
		{addr: "fd00::1", want: "fd00::1"},
		{addr: " " + syslogTestAddr + " ", want: syslogTestAddr},
		{addr: syslogTestHost + "|eth0", want: syslogTestAddr},
		{addr: syslogTestHost + "|ETH0", want: syslogTestAddr},
		{addr: syslogTestHost, wantErr: true},
		{addr: syslogTestHost + "|", wantErr: true},
		{addr: "|eth0", wantErr: true},
		{addr: "", wantErr: true},
		{addr: "nope|eth0", wantErr: true},
		{addr: syslogTestHost + "|eth9", wantErr: true},
		{addr: syslogTestHost + "|eth1", wantErr: true},
		{addr: "300.1.1.1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.addr), func(t *testing.T) {
			got, err := resolveAddress(topo, tt.addr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestProcessSyslog(t *testing.T) {
	topo := &v1.TopologySpec{NodesF: []*v1.Node{newSyslogTestNode(t, syslogTestHost, osLinux, syslogTestAddr)}}

	remote := func(fields map[string]any) map[string]any {
		return map[string]any{"syslog": []any{fields}}
	}

	tests := []struct {
		name    string
		md      map[string]any
		want    []SyslogRemote
		wantErr string
	}{
		{name: "absent", md: map[string]any{}},
		{
			name: "defaults",
			md:   remote(map[string]any{"address": syslogTestAddr}),
			want: []SyslogRemote{{
				Address:  syslogTestAddr,
				Protocol: syslogDefaultProtocol,
				Facility: syslogDefaultFacility,
				Level:    syslogDefaultLevel,
			}},
		},
		{
			name: "explicit",
			md: remote(map[string]any{
				"address":  syslogTestHost + "|eth0",
				"protocol": "TCP",
				"port":     1514,
				"facility": "auth",
				"level":    "warning",
			}),
			want: []SyslogRemote{{Address: syslogTestAddr, Protocol: "tcp", Port: 1514, Facility: "auth", Level: "warning"}},
		},
		{
			name:    "missing address",
			md:      map[string]any{"syslog": []any{map[string]any{"address": syslogTestAddr}, map[string]any{"level": "info"}}},
			wantErr: "index 1",
		},
		{name: "unresolvable", md: remote(map[string]any{"address": "nope|eth0"}), wantErr: "not found"},
		{name: "bad protocol", md: remote(map[string]any{"address": syslogTestAddr, "protocol": "sctp"}), wantErr: "protocol"},
		{name: "port too high", md: remote(map[string]any{"address": syslogTestAddr, "port": 70000}), wantErr: "port"},
		{name: "negative port", md: remote(map[string]any{"address": syslogTestAddr, "port": -1}), wantErr: "port"},
		{name: "string port", md: remote(map[string]any{"address": syslogTestAddr, "port": "514"}), wantErr: "decoding"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Vrouter{}.processSyslog(tt.md, topo)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("expected %+v, got %+v", tt.want, got)
			}
		})
	}
}

func TestVrouterPreStartSyslog(t *testing.T) {
	const tcpAddr = "10.1.101.61"

	out, err := runVrouterPreStart(t, "vyos", []map[string]any{
		{"address": syslogTestHost + "|eth0"},
		{"address": tcpAddr, "protocol": "tcp", "port": 1514, "facility": "auth", "level": "warning"},
	})
	if err != nil {
		t.Fatalf("running vrouter PreStart: %v", err)
	}

	want := []string{
		fmt.Sprintf("set system syslog remote %s facility %s level %s", syslogTestAddr, syslogDefaultFacility, syslogDefaultLevel),
		fmt.Sprintf("set system syslog remote %s protocol %s", syslogTestAddr, syslogDefaultProtocol),
		fmt.Sprintf("set system syslog remote %s facility auth level warning", tcpAddr),
		fmt.Sprintf("set system syslog remote %s protocol tcp", tcpAddr),
		fmt.Sprintf("set system syslog remote %s port 1514", tcpAddr),
	}

	last := -1

	for _, line := range want {
		idx := strings.Index(out, "\n"+line+"\n")
		if idx < 0 {
			t.Fatalf("expected line %q in rendered config:\n%s", line, out)
		}

		last = max(last, idx)
	}

	if strings.Contains(out, fmt.Sprintf("remote %s port", syslogTestAddr)) {
		t.Fatalf("unexpected port line for remote without a port:\n%s", out)
	}

	if commit := strings.Index(out, "\ncommit\n"); commit < last {
		t.Fatalf("expected syslog lines before commit:\n%s", out)
	}
}

func TestVrouterPreStartSyslogBadReference(t *testing.T) {
	_, err := runVrouterPreStart(t, "vyos", []map[string]any{{"address": syslogTestHost + "|eth9"}})
	if err == nil || !strings.Contains(err.Error(), "rtr") {
		t.Fatalf("expected PreStart error naming the router, got %v", err)
	}
}

func TestVrouterPreStartSyslogIgnoredOnVyatta(t *testing.T) {
	out, err := runVrouterPreStart(t, "vyatta", []map[string]any{{"address": syslogTestAddr}})
	if err != nil {
		t.Fatalf("running vrouter PreStart: %v", err)
	}

	if strings.Contains(out, "syslog") {
		t.Fatalf("unexpected syslog config for vyatta router:\n%s", out)
	}
}
