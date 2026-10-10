package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
)

// builderPreflightExchangeFile holds a request of the preflight route and
// the route's answer to it, for the draft TestBuilderPreflightAnswersAsRecorded
// makes.
const builderPreflightExchangeFile = "testdata/builder-preflight-exchange.json"

// TestBuilderPreflightAnswersAsRecorded runs the disks and apps checks of a
// draft whose drive image the server has and whose scenario names an app the
// server lacks, with the request of builderPreflightExchangeFile, and
// compares the answer with the answer recorded there.
func TestBuilderPreflightAnswersAsRecorded(t *testing.T) {
	data, err := os.ReadFile(builderPreflightExchangeFile)
	if err != nil {
		t.Fatalf("reading %s: %v", builderPreflightExchangeFile, err)
	}

	var exchange struct {
		Request json.RawMessage `json:"request"`
		Answer  json.RawMessage `json:"answer"`
	}

	if err := json.Unmarshal(data, &exchange); err != nil {
		t.Fatalf("decoding %s: %v", builderPreflightExchangeFile, err)
	}

	var request bytes.Buffer
	if err := json.Compact(&request, exchange.Request); err != nil {
		t.Fatalf("compacting the recorded request: %v", err)
	}

	node := includeNode("plc")
	node["hardware"] = map[string]any{"os_type": "linux", "drives": []any{map[string]any{"image": "base.qc2"}}}

	topology := builderConfig(t, builderKindTopology, "plant")
	topology.Spec = map[string]any{"nodes": []any{node}}

	scenario := builderConfig(t, builderKindScenario, "ops")
	scenario.Spec = map[string]any{"apps": []any{map[string]any{"name": "scorch"}, map[string]any{"name": "historian"}}}

	var hostReads atomic.Int32

	harness := newBuilderHarnessWith(t,
		[]builderOption{withBuilderPreflightSources(preflightSources(&hostReads)), preflightDisks()}, topology, scenario,
	)

	document := generateBuilderDocument(t, harness, "Topology/plant")
	document.Scenarios = []string{"ops"}
	draft := createBuilderPublishDraft(t, harness, document)

	recorder := postBuilderPreflight(harness, draft, nil, request.String())
	if recorder.Code != http.StatusOK {
		t.Fatalf("preflight status = %d: %s", recorder.Code, recorder.Body.String())
	}

	var got, want any

	harness.decode(recorder, &got)

	if err := json.Unmarshal(exchange.Answer, &want); err != nil {
		t.Fatalf("decoding the recorded answer: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("the preflight route's answer is not the answer recorded in %s; it answered\n%s",
			builderPreflightExchangeFile, recorder.Body.String())
	}
}
