package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
)

// assertBuilderExchange reads the request and recorded answer of an exchange
// file (the JS tests read the same answers), asks with send, and compares.
func assertBuilderExchange(t *testing.T, file string, send func(request string) *httptest.ResponseRecorder) {
	t.Helper()

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading %s: %v", file, err)
	}

	var exchange struct {
		Request json.RawMessage `json:"request"`
		Answer  json.RawMessage `json:"answer"`
	}

	if err := json.Unmarshal(data, &exchange); err != nil {
		t.Fatalf("decoding %s: %v", file, err)
	}

	var request bytes.Buffer
	if err := json.Compact(&request, exchange.Request); err != nil {
		t.Fatalf("compacting the recorded request: %v", err)
	}

	recorder := send(request.String())
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body)
	}

	var got, want any

	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}

	if err := json.Unmarshal(exchange.Answer, &want); err != nil {
		t.Fatalf("decoding the recorded answer: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("the answer is not the one recorded in %s; it answered\n%s", file, recorder.Body)
	}
}

// TestBuilderPreflightAnswersAsRecorded runs the disks and apps checks of a
// draft whose scenario names an app the server lacks.
func TestBuilderPreflightAnswersAsRecorded(t *testing.T) {
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

	assertBuilderExchange(t, "testdata/builder-preflight-exchange.json", func(request string) *httptest.ResponseRecorder {
		return postBuilderPreflight(harness, draft, nil, request)
	})
}

// TestBuilderPackageAnswersAsRecorded packages a document that names a
// Scenario config the server lacks.
func TestBuilderPackageAnswersAsRecorded(t *testing.T) {
	harness := newBuilderHarness(t)

	assertBuilderExchange(t, "testdata/builder-package-exchange.json", func(request string) *httptest.ResponseRecorder {
		return harness.do(builderRequest{method: http.MethodPost, path: "/builder/package", body: request, user: builderAlice})
	})
}
