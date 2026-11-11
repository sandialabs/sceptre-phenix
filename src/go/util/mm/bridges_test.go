package mm

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"

	"phenix/util/mm/mmcli"
	"phenix/util/mm/mmtest"
)

// bridgeResponses is a closed channel of one answer holding resps, as
// mmcli.Run returns it.
func bridgeResponses(resps ...*minicli.Response) chan *miniclient.Response {
	responses := make(chan *miniclient.Response, 1)
	responses <- &miniclient.Response{Resp: resps, Rendered: "", More: false}

	close(responses)

	return responses
}

// bridgeOutput is what minimega's shell command answers on host when
// ovs-vsctl prints the given bridges, one per line.
func bridgeOutput(host string, bridges ...string) *minicli.Response {
	output := ""
	if len(bridges) > 0 {
		output = strings.Join(bridges, "\n") + "\n"
	}

	return &minicli.Response{Host: host, Response: output}
}

func TestBridgeNames(t *testing.T) {
	// Two answers, each in a response of its own, as a mesh send gives them.
	twoResponses := make(chan *miniclient.Response, 2)
	twoResponses <- &miniclient.Response{Resp: minicli.Responses{bridgeOutput("h1", "phenix")}}
	twoResponses <- &miniclient.Response{Resp: minicli.Responses{bridgeOutput("h2", "lab", "phenix")}}

	close(twoResponses)

	empty := make(chan *miniclient.Response)
	close(empty)

	notFound := &minicli.Response{Host: "h2", Error: "exec: \"ovs-vsctl\": not found\n"}

	for _, tt := range []struct {
		name      string
		responses chan *miniclient.Response
		want      []string
		err       string
		is        error
	}{
		{name: "sorted and unique", responses: bridgeResponses(bridgeOutput("h1", "phenix", "lab", "phenix")), want: []string{"lab", "phenix"}},
		{name: "no bridges", responses: bridgeResponses(bridgeOutput("h1")), want: nil},
		{name: "spaces and tabs", responses: bridgeResponses(&minicli.Response{Response: " lab\tphenix \r\n"}), want: []string{"lab", "phenix"}},
		{name: "two responses", responses: twoResponses, want: []string{"lab", "phenix"}},
		{name: "an error", responses: bridgeResponses(notFound), err: `exec: "ovs-vsctl": not found`},
		{name: "an error and an answer", responses: bridgeResponses(bridgeOutput("h1", "phenix"), notFound), err: `exec: "ovs-vsctl": not found`},
		{
			name: "two errors", responses: bridgeResponses(notFound, &minicli.Response{Host: "h3", Error: "h3 not found in mesh"}),
			err: "exec: \"ovs-vsctl\": not found\nh3 not found in mesh",
		},
		{name: "no answer", responses: empty, err: errNoBridgeList.Error(), is: errNoBridgeList},
	} {
		t.Run(tt.name, func(t *testing.T) {
			names, err := bridgeNames(tt.responses)

			switch {
			case tt.err == "" && err != nil:
				t.Errorf("bridgeNames() error = %v, want nil", err)
			case tt.err != "" && (err == nil || err.Error() != tt.err):
				t.Errorf("bridgeNames() error = %v, want %q", err, tt.err)
			case tt.is != nil && !errors.Is(err, tt.is):
				t.Errorf("bridgeNames() error = %v, want %v", err, tt.is)
			}

			if diff := cmp.Diff(tt.want, names); diff != "" {
				t.Errorf("bridgeNames() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The head node runs ovs-vsctl through minimega's shell, other hosts get it over the
// mesh, and nothing else is sent, so listing bridges changes no host. The hosts are
// listed at once.
func TestListBridgesSendsOnlyTheListing(t *testing.T) {
	var (
		mu   sync.Mutex
		sent = map[string]mmcli.ReadLine{}
	)

	run := func(line mmcli.ReadLine, cmd *mmcli.Command) chan *miniclient.Response {
		// What minimega receives, with any namespace, filter or column.
		mu.Lock()
		sent[cmd.String()] = line
		mu.Unlock()

		if strings.HasPrefix(cmd.Command, "mesh send h2 ") {
			return bridgeResponses(bridgeOutput("h2", "lab", "phenix"))
		}

		return bridgeResponses(bridgeOutput("head", "phenix"))
	}

	bridges, err := listBridges([]string{"head", "h2"}, func(host string) bool { return host == "head" }, run)
	if err != nil {
		t.Fatalf("listBridges(head, h2) returned error: %v", err)
	}

	wantSent := map[string]mmcli.ReadLine{
		".record false shell ovs-vsctl --timeout=5 list-br":              mmcli.HeadRead,
		".record false mesh send h2 shell ovs-vsctl --timeout=5 list-br": mmcli.MeshRead,
	}
	if diff := cmp.Diff(wantSent, sent); diff != "" {
		t.Errorf("listBridges(head, h2) sent mismatch (-want +got):\n%s", diff)
	}

	want := map[string][]string{"head": {"phenix"}, "h2": {"lab", "phenix"}}
	if diff := cmp.Diff(want, bridges); diff != "" {
		t.Errorf("listBridges(head, h2) mismatch (-want +got):\n%s", diff)
	}
}

// A host that answers with an error fails the listing, with the error of the
// first host that failed in the order given.
func TestListBridgesFailsOnAnyHost(t *testing.T) {
	var (
		mu   sync.Mutex
		sent []string
	)

	run := func(_ mmcli.ReadLine, cmd *mmcli.Command) chan *miniclient.Response {
		mu.Lock()
		sent = append(sent, cmd.String())
		mu.Unlock()

		return bridgeResponses(&minicli.Response{Host: "h1", Error: "h1 not found in mesh"})
	}

	_, err := listBridges([]string{"h1", "h2"}, func(string) bool { return false }, run)
	if want := "listing the bridges of host h1: h1 not found in mesh"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("listBridges(h1, h2) error = %v, want one containing %q", err, want)
	}

	slices.Sort(sent)

	want := []string{
		".record false mesh send h1 shell ovs-vsctl --timeout=5 list-br",
		".record false mesh send h2 shell ovs-vsctl --timeout=5 list-br",
	}
	if !slices.Equal(sent, want) {
		t.Errorf("listBridges(h1, h2) sent %q, want %q", sent, want)
	}
}

// TestGetBridges asserts GetBridges of no host lists nothing and sends no
// command. [listBridges] tests check the listing of hosts.
func TestGetBridges(t *testing.T) {
	bridges, err := (Minimega{}).GetBridges(t.Context())
	if err != nil {
		t.Fatalf("GetBridges() error = %v, want nil", err)
	}

	if diff := cmp.Diff(map[string][]string{}, bridges); diff != "" {
		t.Errorf("GetBridges() mismatch (-want +got):\n%s", diff)
	}
}

// The head node gets the bridge destroy command itself, and each other host
// gets it over the mesh, in the order of the hosts.
func TestDestroyBridgeSendsTheDestroy(t *testing.T) {
	var sent []string

	run := func(cmd *mmcli.Command) chan *miniclient.Response {
		sent = append(sent, cmd.String())

		return bridgeResponses(&minicli.Response{Host: "head"})
	}

	err := destroyBridge("exp", []string{"h2", "head", "h3"}, func(host string) bool { return host == "head" }, run)
	if err != nil {
		t.Fatalf("destroyBridge(exp) error = %v, want nil", err)
	}

	want := []string{
		".record false mesh send h2 bridge destroy exp",
		".record false bridge destroy exp",
		".record false mesh send h3 bridge destroy exp",
	}
	if diff := cmp.Diff(want, sent); diff != "" {
		t.Errorf("destroyBridge(exp) sent mismatch (-want +got):\n%s", diff)
	}
}

// A host that does not know the bridge is not an error. A host that fails is
// reported, and the hosts after it still get the command.
func TestDestroyBridgeReportsFailedHosts(t *testing.T) {
	notFound := &minicli.Response{Host: "h2", Error: "bridge not found: exp"}
	failed := &minicli.Response{Host: "h2", Error: "ovs-vsctl: database connection failed\n"}

	tests := []struct {
		name     string
		bridge   string
		hosts    []string
		answers  map[string]*minicli.Response // by host, success when missing
		noAnswer string                       // a host that gives no answer
		wantSent []string                     // the hosts that get the command
		wantErr  []string                     // parts of the error, none for nil
		wantIs   error
	}{
		{name: "every host destroys", bridge: "exp", hosts: []string{"h1", "h2"}, wantSent: []string{"h1", "h2"}},
		{
			name: "a host that does not know the bridge", bridge: "exp", hosts: []string{"h1", "h2", "h3"},
			answers: map[string]*minicli.Response{"h2": notFound}, wantSent: []string{"h1", "h2", "h3"},
		},
		{
			name: "a host that fails", bridge: "exp", hosts: []string{"h1", "h2", "h3"},
			answers: map[string]*minicli.Response{"h2": failed}, wantSent: []string{"h1", "h2", "h3"},
			wantErr: []string{"destroying bridge exp on host h2: ovs-vsctl: database connection failed"},
		},
		{
			name: "two hosts that fail", bridge: "exp", hosts: []string{"h1", "h2"},
			answers:  map[string]*minicli.Response{"h1": failed, "h2": failed},
			wantSent: []string{"h1", "h2"},
			wantErr:  []string{"destroying bridge exp on host h1: ", "destroying bridge exp on host h2: "},
		},
		{
			name: "a host that gives no answer", bridge: "exp", hosts: []string{"h1", "h2"}, noAnswer: "h1",
			wantSent: []string{"h1", "h2"}, wantErr: []string{"destroying bridge exp on host h1"}, wantIs: errNoBridgeDestroy,
		},
		{
			name: "a bridge minimega cannot take", bridge: "a b", hosts: []string{"h1"},
			wantErr: []string{"destroying bridge: "}, wantIs: ErrUnsafeArg,
		},
		{
			name: "a host minimega cannot take", bridge: "exp", hosts: []string{"h 1", "h2"}, wantSent: []string{"h2"},
			wantErr: []string{`destroying bridge exp on host "h 1"`}, wantIs: ErrUnsafeArg,
		},
		{name: "no hosts", bridge: "exp"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sent []string

			run := func(cmd *mmcli.Command) chan *miniclient.Response {
				host := strings.Fields(cmd.Command)[2]
				sent = append(sent, host)

				if host == tc.noAnswer {
					empty := make(chan *miniclient.Response)
					close(empty)

					return empty
				}

				if answer, ok := tc.answers[host]; ok {
					return bridgeResponses(answer)
				}

				return bridgeResponses(&minicli.Response{Host: host})
			}

			err := destroyBridge(tc.bridge, tc.hosts, func(string) bool { return false }, run)

			if len(tc.wantErr) == 0 && err != nil {
				t.Errorf("destroyBridge(%s, %v) error = %v, want nil", tc.bridge, tc.hosts, err)
			}

			for _, part := range tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), part) {
					t.Errorf("destroyBridge(%s, %v) error = %v, want one containing %q", tc.bridge, tc.hosts, err, part)
				}
			}

			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("destroyBridge(%s, %v) error = %v, want %v", tc.bridge, tc.hosts, err, tc.wantIs)
			}

			if diff := cmp.Diff(tc.wantSent, sent); diff != "" {
				t.Errorf("destroyBridge(%s, %v) hosts sent to mismatch (-want +got):\n%s", tc.bridge, tc.hosts, diff)
			}
		})
	}
}

// DestroyBridge sends the destroy to a minimega, the head node found as phenix finds it.
func TestDestroyBridgeThroughMinimega(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, answering(map[string][]*minicli.Response{
		"mesh send compute1 bridge destroy exp": {mmtest.Error("compute1", "bridge not found: exp")},
	}))

	if err := DestroyBridge("exp", "head", "compute1"); err != nil {
		t.Fatalf("DestroyBridge(exp, head, compute1) error = %v, want nil", err)
	}

	want := []string{bare("bridge destroy exp"), bare("mesh send compute1 bridge destroy exp")}
	if diff := cmp.Diff(want, sentRaw(received())); diff != "" {
		t.Errorf("DestroyBridge(exp, head, compute1) sent mismatch (-want +got):\n%s", diff)
	}
}
