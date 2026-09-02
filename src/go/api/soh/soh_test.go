package soh

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/mitchellh/mapstructure"

	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
)

func TestHostStateAllStatesIncludesFiles(t *testing.T) {
	t.Parallel()

	networking := State{Success: "network configured"}
	file := State{Success: "file found"}
	fileAbsent := State{Success: "file not found"}
	service := State{Success: "service running"}
	process := State{Success: "process running"}
	container := State{Success: "container running"}

	state := HostState{
		Networking:  []State{networking},
		Files:       []State{file},
		FilesAbsent: []State{fileAbsent},
		Services:    []State{service},
		Processes:   []State{process},
		Containers:  []State{container},
	}

	want := []State{networking, file, fileAbsent, service, process, container}
	if got := state.AllStates(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AllStates() = %#v, want %#v", got, want)
	}
}

func TestSOHMetadataDecodesHostFiles(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"hostFiles": map[string][]string{
			"server": {"/etc/injected.conf", "/opt/data.txt"},
		},
	}

	var metadata sohMetadata
	if err := mapstructure.Decode(input, &metadata); err != nil {
		t.Fatalf("decoding metadata: %v", err)
	}

	if !reflect.DeepEqual(metadata.HostFiles, input["hostFiles"]) {
		t.Fatalf("HostFiles = %#v, want %#v", metadata.HostFiles, input["hostFiles"])
	}
}

func TestSOHMetadataDecodesHostFilesAbsent(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"hostFilesAbsent": map[string][]string{
			"server": {"/tmp/should-not-exist.txt", "/opt/removed.txt"},
		},
	}

	var metadata sohMetadata
	if err := mapstructure.Decode(input, &metadata); err != nil {
		t.Fatalf("decoding metadata: %v", err)
	}

	if !reflect.DeepEqual(metadata.HostFilesAbsent, input["hostFilesAbsent"]) {
		t.Fatalf(
			"HostFilesAbsent = %#v, want %#v",
			metadata.HostFilesAbsent,
			input["hostFilesAbsent"],
		)
	}
}

func TestSOHMetadataDecodesDockerContainers(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"dockerContainers": map[string][]string{
			"server": {"nginx", "redis"},
		},
	}

	var metadata sohMetadata
	if err := mapstructure.Decode(input, &metadata); err != nil {
		t.Fatalf("decoding metadata: %v", err)
	}

	if !reflect.DeepEqual(metadata.HostContainers, input["dockerContainers"]) {
		t.Fatalf(
			"HostContainers = %#v, want %#v",
			metadata.HostContainers,
			input["dockerContainers"],
		)
	}
}

func TestFileCheckCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		osType string
		path   string
		want   string
	}{
		{
			name:   "linux",
			osType: "linux",
			path:   "/opt/user's file",
			want:   `stat -c present -- '/opt/user'"'"'s file'`,
		},
		{
			name:   "windows",
			osType: "windows",
			path:   `C:\Users\O'Brien\data.txt`,
			want:   `powershell -NoProfile -Command "if (Test-Path -LiteralPath 'C:\Users\O''Brien\data.txt' -PathType Leaf) { 'present' }"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := fileCheckCommand(test.osType, test.path); got != test.want {
				t.Fatalf("fileCheckCommand() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSOHMetadataDecodesHostServices(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"hostServices": map[string][]string{
			"server": {"sshd", "nginx"},
		},
	}

	var metadata sohMetadata
	if err := mapstructure.Decode(input, &metadata); err != nil {
		t.Fatalf("decoding metadata: %v", err)
	}

	if !reflect.DeepEqual(metadata.HostServices, input["hostServices"]) {
		t.Fatalf("HostServices = %#v, want %#v", metadata.HostServices, input["hostServices"])
	}
}

func TestServiceCheckCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		osType  string
		service string
		want    string
	}{
		{
			name:    "linux",
			osType:  "linux",
			service: "user's service",
			want:    `systemctl is-active -- 'user'"'"'s service'`,
		},
		{
			name:    "windows",
			osType:  "windows",
			service: `O'Brien Service`,
			want:    `powershell -NoProfile -Command "if ((Get-Service -Name 'O''Brien Service' -ErrorAction SilentlyContinue).Status -eq 'Running') { 'active' }"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := serviceCheckCommand(test.osType, test.service); got != test.want {
				t.Fatalf("serviceCheckCommand() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestContainerCheckCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		osType    string
		container string
		want      string
	}{
		{
			name:      "linux",
			osType:    "linux",
			container: "o'brien's-app",
			want: `docker inspect --format='{{if .State.Health}}{{.State.Health.Status}}` +
				`{{else}}{{if .State.Running}}running{{else}}{{.State.Status}}{{end}}{{end}}' ` +
				`-- 'o'"'"'brien'"'"'s-app'`,
		},
		{
			name:      "windows",
			osType:    "windows",
			container: "o'brien's-app",
			want: `powershell -NoProfile -Command "docker inspect --format='` +
				`{{if .State.Health}}{{.State.Health.Status}}` +
				`{{else}}{{if .State.Running}}running{{else}}{{.State.Status}}{{end}}{{end}}' ` +
				`-- 'o''brien''s-app' 2>$null"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := containerCheckCommand(test.osType, test.container); got != test.want {
				t.Fatalf("containerCheckCommand() = %q, want %q", got, test.want)
			}
		})
	}
}

// fakeNode covers the slice of ifaces.NodeSpec the checks read.
type fakeNode struct {
	ifaces.NodeSpec

	hostname, osType string
}

func (n fakeNode) General() ifaces.NodeGeneral { //nolint:ireturn // test double
	return fakeGeneral{hostname: n.hostname}
}

func (n fakeNode) Hardware() ifaces.NodeHardware { //nolint:ireturn // test double
	return fakeHardware{osType: n.osType}
}

type fakeGeneral struct {
	ifaces.NodeGeneral

	hostname string
}

func (g fakeGeneral) Hostname() string { return g.hostname }

type fakeHardware struct {
	ifaces.NodeHardware

	osType string
}

func (h fakeHardware) OSType() string { return h.osType }

type fakeInterface struct {
	ifaces.NodeNetworkInterface

	name, vlan string
}

func (i fakeInterface) Name() string { return i.name }
func (i fakeInterface) VLAN() string { return i.vlan }

// fakeMM answers every C2 command with the same response. Embedding the (nil)
// mm.MM interface makes any other method call panic, which these tests never
// trigger.
type fakeMM struct {
	mm.MM

	vms      mm.VMs
	response string
}

func (f fakeMM) GetVMInfo(...mm.Option) mm.VMs                    { return f.vms }
func (f fakeMM) ExecC2Command(...mm.C2Option) (string, error)     { return "1", nil }
func (f fakeMM) WaitForC2Response(...mm.C2Option) (string, error) { return f.response, nil }

func useFakeMM(t *testing.T, fake mm.MM) {
	t.Helper()

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = fake //nolint:reassign // install test double
}

// TestWaitForCPULoadRecordsResults drives the parallel CPU load probes for
// several hosts. Results must reach the status map from the joining goroutine
// only (run with -race), and a host whose C2 client went away must be reported
// rather than left out.
func TestWaitForCPULoadRecordsResults(t *testing.T) {
	useFakeMM(t, fakeMM{response: "0.10 0.20 0.30 1/200 12345"})

	s := newSOH()
	s.md.c2Timeout = time.Second

	for i := range 8 {
		host := fmt.Sprintf("host%d", i)

		s.nodes[host] = fakeNode{hostname: host, osType: "linux"}
		s.c2Hosts[host] = struct{}{}
	}

	s.markC2Dead("gone")

	if errs := s.waitForCPULoad(context.Background(), "exp"); !errs {
		t.Fatal("expected the host without C2 to be reported as an error")
	}

	for host := range s.c2Hosts {
		if got := s.status[host].CPULoad; got != "0.10" {
			t.Fatalf("%s: CPULoad = %q, want %q", host, got, "0.10")
		}
	}

	if got := s.status["gone"].CPULoad; got != errC2Dead.Error() {
		t.Fatalf("CPULoad for the host without C2 = %q, want %q", got, errC2Dead.Error())
	}
}

// TestWaitForDHCPReportsAddress checks the DHCP wait hands the address back
// through the state group, to be recorded after the join so the goroutines
// never touch the shared maps, and tolerates minimega listing fewer addresses
// than the interface index.
func TestWaitForDHCPReportsAddress(t *testing.T) {
	useFakeMM(t, fakeMM{vms: mm.VMs{{Name: "host0", IPv4: []string{"10.0.0.5"}}}})

	s := newSOH()
	s.md.c2Timeout = 100 * time.Millisecond

	var (
		ctx   = context.Background()
		wg    = new(mm.StateGroup)
		iface = fakeInterface{name: "IF0", vlan: "OT"}
	)

	wg.Add(2)

	go s.waitForDHCP(ctx, wg, "exp", "host0", 0, iface)
	go s.waitForDHCP(ctx, wg, "exp", "host0", 3, iface)

	wg.Wait()

	if wg.ErrCount != 1 {
		t.Fatalf("ErrCount = %d, want 1 (index past the address list must time out)", wg.ErrCount)
	}

	for _, state := range wg.States {
		if state.Err != nil {
			continue
		}

		var (
			name, _ = state.Meta["iface"].(string)
			vlan, _ = state.Meta["vlan"].(string)
			ip, _   = state.Meta["ip"].(string)
		)

		s.recordHostIP("host0", name, vlan, ip)
	}

	if got := s.hostIPs["host0"]["IF0"]; got != "10.0.0.5" {
		t.Fatalf("hostIPs = %q, want %q", got, "10.0.0.5")
	}

	if got := s.vlans["OT"]; !reflect.DeepEqual(got, []string{"10.0.0.5"}) {
		t.Fatalf("vlans[OT] = %v, want [10.0.0.5]", got)
	}

	if got := s.addrHosts["10.0.0.5"]; got != "host0" {
		t.Fatalf("addrHosts = %q, want %q", got, "host0")
	}
}

// TestSkipHostRecordsDeadHosts: hosts skipped by configuration stay silent,
// hosts whose C2 client went away get an error state per skipped check.
func TestSkipHostRecordsDeadHosts(t *testing.T) {
	t.Parallel()

	s := newSOH()
	s.c2Hosts["live"] = struct{}{}
	s.markC2Dead("gone")

	wg := new(mm.StateGroup)

	if s.skipHost(wg, "live", nil) {
		t.Fatal("host with C2 was skipped")
	}

	if !s.skipHost(wg, "never-added", nil) || wg.ErrCount != 0 {
		t.Fatal("host skipped by configuration must be skipped silently")
	}

	if !s.skipHost(wg, "gone", map[string]any{"host": "gone"}) || wg.ErrCount != 1 {
		t.Fatal("host without C2 must be skipped with an error state")
	}
}

// TestMetadataHelpersAcceptQueryValues: context metadata arrives as strings
// from scorch and the CLI but as url.Values slices from the UI.
func TestMetadataHelpersAcceptQueryValues(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		in   any
		want string
	}{
		{"5m", "5m"},
		{[]string{"10m"}, "10m"},
		{[]string{}, ""},
		{42, ""},
	} {
		if got := metadataString(test.in); got != test.want {
			t.Fatalf("metadataString(%#v) = %q, want %q", test.in, got, test.want)
		}
	}

	for _, test := range []struct {
		in   any
		want []string
	}{
		{[]string{"files", "services"}, []string{"files", "services"}},
		{[]string{"files, services"}, []string{"files", "services"}},
		{"files,services,", []string{"files", "services"}},
		{nil, nil},
	} {
		if got := metadataStrings(test.in); !reflect.DeepEqual(got, test.want) {
			t.Fatalf("metadataStrings(%#v) = %v, want %v", test.in, got, test.want)
		}
	}
}

func TestSOHMetadataParsesC2Tuning(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		input       map[string]any
		appear      *time.Duration
		client      *time.Duration
		concurrency int
		wantErr     bool
	}{
		{name: "defaults", input: map[string]any{}, concurrency: defaultC2Concurrency},
		{
			name: "explicit",
			input: map[string]any{
				"c2AppearGrace": "0s",
				"c2ClientGrace": "2m",
				"c2Concurrency": 0,
			},
			appear: durationPtr(0),
			client: durationPtr(2 * time.Minute),
		},
		{name: "malformed", input: map[string]any{"c2ClientGrace": "soon"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var md sohMetadata
			if err := mapstructure.Decode(tc.input, &md); err != nil {
				t.Fatalf("decoding metadata: %v", err)
			}

			err := md.init()
			if tc.wantErr != (err != nil) {
				t.Fatalf("init() err = %v, wantErr %v", err, tc.wantErr)
			}

			if tc.wantErr {
				return
			}

			if !reflect.DeepEqual(md.c2AppearGrace, tc.appear) ||
				!reflect.DeepEqual(md.c2ClientGrace, tc.client) {
				t.Fatalf("graces = %v/%v, want %v/%v", md.c2AppearGrace, md.c2ClientGrace, tc.appear, tc.client)
			}

			if md.c2Concurrency != tc.concurrency {
				t.Fatalf("c2Concurrency = %d, want %d", md.c2Concurrency, tc.concurrency)
			}
		})
	}
}

func durationPtr(d time.Duration) *time.Duration { return &d }
