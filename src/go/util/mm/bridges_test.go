package mm

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"

	"phenix/util/mm/mmcli"
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
// mesh, and nothing else is sent, so listing bridges changes no host.
func TestListBridgesSendsOnlyTheListing(t *testing.T) {
	var sent []string

	run := func(cmd *mmcli.Command) chan *miniclient.Response {
		// What minimega receives, with any namespace, filter or column.
		sent = append(sent, cmd.String())

		if strings.HasPrefix(cmd.Command, "mesh send h2 ") {
			return bridgeResponses(bridgeOutput("h2", "lab", "phenix"))
		}

		return bridgeResponses(bridgeOutput("head", "phenix"))
	}

	bridges, err := listBridges([]string{"head", "h2"}, func(host string) bool { return host == "head" }, run)
	if err != nil {
		t.Fatalf("listBridges(head, h2) returned error: %v", err)
	}

	wantSent := []string{
		".record false shell ovs-vsctl --timeout=5 list-br",
		".record false mesh send h2 shell ovs-vsctl --timeout=5 list-br",
	}
	if diff := cmp.Diff(wantSent, sent); diff != "" {
		t.Errorf("listBridges(head, h2) sent mismatch (-want +got):\n%s", diff)
	}

	want := map[string][]string{"head": {"phenix"}, "h2": {"lab", "phenix"}}
	if diff := cmp.Diff(want, bridges); diff != "" {
		t.Errorf("listBridges(head, h2) mismatch (-want +got):\n%s", diff)
	}
}

// A host that answers with an error fails the listing, and no host after it
// is asked.
func TestListBridgesFailsOnAnyHost(t *testing.T) {
	var sent []string

	run := func(cmd *mmcli.Command) chan *miniclient.Response {
		sent = append(sent, cmd.String())

		return bridgeResponses(&minicli.Response{Host: "h1", Error: "h1 not found in mesh"})
	}

	_, err := listBridges([]string{"h1", "h2"}, func(string) bool { return false }, run)
	if want := "listing the bridges of host h1: h1 not found in mesh"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("listBridges(h1, h2) error = %v, want one containing %q", err, want)
	}

	if want := []string{".record false mesh send h1 shell ovs-vsctl --timeout=5 list-br"}; !slices.Equal(sent, want) {
		t.Errorf("listBridges(h1, h2) sent %q, want only %q", sent, want)
	}
}

// TestGetBridges asserts GetBridges of no host lists nothing and sends no
// command. [listBridges] tests check the listing of hosts.
func TestGetBridges(t *testing.T) {
	bridges, err := (Minimega{}).GetBridges()
	if err != nil {
		t.Fatalf("GetBridges() error = %v, want nil", err)
	}

	if diff := cmp.Diff(map[string][]string{}, bridges); diff != "" {
		t.Errorf("GetBridges() mismatch (-want +got):\n%s", diff)
	}
}
