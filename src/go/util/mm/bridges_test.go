package mm

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/activeshadow/libminimega/miniclient"
	"github.com/google/go-cmp/cmp"

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
	if err != nil {
		t.Errorf("bridgeNames(phenix, lab, phenix) returned error: %v", err)
	} else if want := []string{"lab", "phenix"}; !slices.Equal(names, want) {
		t.Errorf("bridgeNames(phenix, lab, phenix) = %q, want %q", names, want)
	}

	names, err = bridgeNames(bridgeResponses(bridgeOutput("h1")))
	if err != nil {
		t.Errorf("bridgeNames(no bridges) returned error: %v", err)
	} else if len(names) != 0 {
		t.Errorf("bridgeNames(no bridges) = %q, want none", names)
	}

	_, err = bridgeNames(bridgeResponses(&minicli.Response{Host: "h2", Error: "exec: \"ovs-vsctl\": not found\n"}))
	if want := `exec: "ovs-vsctl": not found`; err == nil || err.Error() != want {
		t.Errorf("bridgeNames(ovs-vsctl not found) error = %v, want %q", err, want)
	}

	empty := make(chan *miniclient.Response)
	close(empty)

	if _, err = bridgeNames(empty); !errors.Is(err, errNoBridgeList) {
		t.Errorf("bridgeNames(no answer) error = %v, want %v", err, errNoBridgeList)
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
