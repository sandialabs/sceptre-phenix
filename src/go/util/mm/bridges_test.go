package mm

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/activeshadow/libminimega/miniclient"

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
	names, err := bridgeNames(bridgeResponses(bridgeOutput("h1", "phenix", "lab", "phenix")))
	if err != nil || !slices.Equal(names, []string{"lab", "phenix"}) {
		t.Fatalf("bridgeNames = %v, %v, want lab and phenix", names, err)
	}

	names, err = bridgeNames(bridgeResponses(bridgeOutput("h1")))
	if err != nil || len(names) != 0 {
		t.Fatalf("bridgeNames of a host without bridges = %v, %v, want none", names, err)
	}

	_, err = bridgeNames(bridgeResponses(&minicli.Response{Host: "h2", Error: "exec: \"ovs-vsctl\": not found\n"}))
	if err == nil || err.Error() != `exec: "ovs-vsctl": not found` {
		t.Fatalf("bridgeNames of an error = %v, want the error", err)
	}

	empty := make(chan *miniclient.Response)
	close(empty)

	if _, err = bridgeNames(empty); !errors.Is(err, errNoBridgeList) {
		t.Fatalf("bridgeNames without an answer = %v, want %v", err, errNoBridgeList)
	}
}

// The head node runs ovs-vsctl through minimega's shell command, every other
// host is sent it over the mesh, and no other command is sent: listing the
// bridges changes nothing on a host.
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
		t.Fatalf("listBridges: %v", err)
	}

	want := []string{
		".record false shell ovs-vsctl --timeout=5 list-br",
		".record false mesh send h2 shell ovs-vsctl --timeout=5 list-br",
	}
	if !slices.Equal(sent, want) {
		t.Fatalf("sent %q, want only %q", sent, want)
	}

	if !maps.EqualFunc(bridges, map[string][]string{"head": {"phenix"}, "h2": {"lab", "phenix"}}, slices.Equal) {
		t.Fatalf("bridges = %v, want phenix on head and lab and phenix on h2", bridges)
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
	if err == nil || !strings.Contains(err.Error(), "listing the bridges of host h1: h1 not found in mesh") {
		t.Fatalf("listBridges = %v, want h1's error", err)
	}

	if want := []string{".record false mesh send h1 shell ovs-vsctl --timeout=5 list-br"}; !slices.Equal(sent, want) {
		t.Fatalf("sent %q, want only %q", sent, want)
	}
}
