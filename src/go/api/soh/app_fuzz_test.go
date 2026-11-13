package soh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/olivere/elastic/v7"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
)

// wantFlows returns, for an Elasticsearch search result, the bytes each IP
// sent to each IP, or nil when the result has no hits or a hit that is not a flow.
func wantFlows(resp string) map[string]map[string]int {
	var result elastic.SearchResult
	if json.Unmarshal([]byte(resp), &result) != nil || result.Hits == nil || len(result.Hits.Hits) == 0 {
		return nil
	}

	sent := make(map[[2]string]int)
	ips := make(map[string]bool)

	for _, hit := range result.Hits.Hits {
		type end struct {
			IP    string `json:"ip"`
			Bytes int    `json:"bytes"`
		}

		var flow struct {
			Source      end `json:"source"`
			Destination end `json:"destination"`
		}

		if json.Unmarshal(hit.Source, &flow) != nil {
			return nil
		}

		src, dst := flow.Source, flow.Destination
		sent[[2]string{src.IP, dst.IP}] += src.Bytes
		sent[[2]string{dst.IP, src.IP}] += dst.Bytes
		ips[src.IP], ips[dst.IP] = true, true
	}

	matrix := make(map[string]map[string]int, len(ips))

	for src := range ips {
		matrix[src] = make(map[string]int, len(ips))

		for dst := range ips {
			matrix[src][dst] = sent[[2]string{src, dst}]
		}
	}

	return matrix
}

// getFlows records, for any response to query-flows.sh, a square matrix of
// distinct hosts that holds the bytes each host sent to each host.
func FuzzGetFlows(f *testing.F) {
	f.Add(flowsResponse(flowHit{"10.0.0.1", 100, "10.0.0.2", 40}, flowHit{"10.0.0.2", 1, "10.0.0.1", 2}))
	f.Add(flowsResponse(flowHit{"10.0.0.3", 9, "10.0.0.3", 4}, flowHit{"", 0, "", 0}))
	f.Add(`{"hits":{"hits":[{"_source":{"source":{"ip":"a","bytes":1}}}]}}`)
	f.Add(`{"hits":{"hits":[{"_source":"flow"}]}}`)
	f.Add(`{"hits":{"hits":[]}}`)
	f.Add(`{}`)
	f.Add(`not json`)

	elasticNode := sohNode("elastic", "linux")
	elasticNode.LabelsF = map[string]string{"soh-elastic-server": "true"}

	exp := &types.Experiment{
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec: &v1.ExperimentSpec{
			ExperimentNameF: "exp",
			TopologyF:       &v1.TopologySpec{NodesF: []*v1.Node{elasticNode}},
		},
	}

	f.Fuzz(func(t *testing.T, resp string) {
		useSOHMM(t, func(c2Call) (string, error) { return resp, nil })

		s := newSOH()
		s.getFlows(context.Background(), exp)

		hosts, _ := s.packetCapture["hosts"].([]string)
		flows, _ := s.packetCapture["flows"].([][]int)

		if len(slices.Compact(slices.Sorted(slices.Values(hosts)))) != len(hosts) {
			t.Fatalf("getFlows(%q) hosts = %q, want distinct hosts", resp, hosts)
		}

		for i, row := range flows {
			if len(row) != len(hosts) || len(flows) != len(hosts) {
				t.Fatalf("getFlows(%q) flows row %d = %v of %d rows, want %d of %d", resp, i, row, len(flows), len(hosts), len(hosts))
			}
		}

		if diff := cmp.Diff(wantFlows(resp), packetFlows(t, s)); diff != "" {
			t.Errorf("getFlows(%q) flows mismatch (-want +got):\n%s", resp, diff)
		}
	})
}

// For any Elastic server address, Configure either rejects it as not IPv4 in
// CIDR notation, or adds the server with that same address and prefix length.
func FuzzConfigureElasticServerAddress(f *testing.F) {
	for _, addr := range []string{
		"10.1.0.1/24", "10.1.0.1/32", "0.0.0.0/0", "10.1.0.1", "10.1.0.1/33", "fd00::1/64",
		"::ffff:10.1.0.1/120", "010.1.0.1/24", "10.1.0.1/024", "", "/", "10.1.0.1/-1", "10.1.0.1%eth0/24",
	} {
		f.Add(addr)
	}

	f.Fuzz(func(t *testing.T, addr string) {
		exp := &types.Experiment{
			Metadata: store.ConfigMetadata{Name: "exp"},
			Spec: &v1.ExperimentSpec{
				ExperimentNameF: "exp",
				BaseDirF:        t.TempDir(),
				DefaultBridgeF:  "phenix",
				TopologyF:       &v1.TopologySpec{NodesF: []*v1.Node{sohNode("web", "linux")}},
				ScenarioF:       &v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{{NameF: "soh", MetadataF: captureMetadata(addr)}}},
			},
			Status: &v1.ExperimentStatus{},
		}

		err := newSOH().Configure(context.Background(), exp)
		if err != nil {
			if !errors.Is(err, errElasticServerAddress) {
				t.Fatalf("Configure(address %q) error = %v, want %v", addr, err, errElasticServerAddress)
			}

			return
		}

		server := exp.Spec.Topology().FindNodeByName("elastic")
		if server == nil {
			t.Fatalf("Configure(address %q) added no Elastic server", addr)
		}

		iface := server.Network().Interfaces()[0]

		got, gotErr := netip.ParsePrefix(fmt.Sprintf("%s/%d", iface.Address(), iface.Mask()))
		want, wantErr := netip.ParsePrefix(addr)

		if gotErr != nil || wantErr != nil || got != want || !got.Addr().Is4() {
			t.Errorf("Configure(address %q) Elastic server address = %s/%d, want the same IPv4 prefix", addr, iface.Address(), iface.Mask())
		}
	})
}

// metadataStringsModel is metadataStrings for one string: the comma-separated
// items without the white space around them, empty items left out.
func metadataStringsModel(s string) []string {
	var items []string

	for rest, more := s, true; more; {
		var item string

		item, rest, more = strings.Cut(rest, ",")
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}

	return items
}

// Run metadata lists from any source read as the model reads them, the same
// as a string and as a query value, and a slice reads as its values in turn.
// No item is empty, holds a comma or has white space around it.
func FuzzMetadataStrings(f *testing.F) {
	f.Add("files,flows", "ports")
	f.Add(" files , flows ", "")
	f.Add(",, ,", "\tcustom\n")
	f.Add("", "")
	f.Add("network-config", "reachability,custom-reachability")
	f.Add(" files ", "a, b")

	f.Fuzz(func(t *testing.T, a, b string) {
		want := metadataStringsModel(a)

		if diff := cmp.Diff(want, metadataStrings(a)); diff != "" {
			t.Fatalf("metadataStrings(%q) mismatch (-want +got):\n%s", a, diff)
		}

		if diff := cmp.Diff(want, metadataStrings([]string{a})); diff != "" {
			t.Fatalf("metadataStrings([%q]) mismatch (-want +got):\n%s", a, diff)
		}

		both := append(slices.Clone(want), metadataStringsModel(b)...)
		if len(both) == 0 {
			both = nil
		}

		got := metadataStrings([]string{a, b})
		if diff := cmp.Diff(both, got); diff != "" {
			t.Fatalf("metadataStrings([%q, %q]) mismatch (-want +got):\n%s", a, b, diff)
		}

		for _, item := range got {
			if item == "" || strings.Contains(item, ",") || strings.TrimSpace(item) != item {
				t.Fatalf("metadataStrings([%q, %q]) item %q is empty, has a comma or has white space around it", a, b, item)
			}
		}
	})
}

// A run metadata duration given as a string or as a query value reads as
// [time.ParseDuration] reads it.
func FuzzMetadataDuration(f *testing.F) {
	for _, seed := range []string{"42s", "1m", "0s", "soon", "", "-1h30m", "9999999999h"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, s string) {
		want, err := time.ParseDuration(s)

		for _, val := range []any{s, []string{s}, []string{s, "1h"}} {
			got, ok := metadataDuration(val)
			if ok != (err == nil) || (ok && got != want) {
				t.Fatalf("metadataDuration(%#v) = %v, %t; want %v, %t", val, got, ok, want, err == nil)
			}
		}
	})
}
