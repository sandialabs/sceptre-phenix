package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// loadBuilderExchange reads the request and recorded answer of an exchange
// file (the JS tests read the same answers): the request compacted, the answer decoded.
func loadBuilderExchange(t *testing.T, file string) (string, any) {
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

	var answer any
	if err := json.Unmarshal(exchange.Answer, &answer); err != nil {
		t.Fatalf("decoding the recorded answer: %v", err)
	}

	return request.String(), answer
}

// builderExchangeDiff returns how an answer differs from the recorded one,
// or an error when it is not a 200 answer of JSON.
func builderExchangeDiff(recorder *httptest.ResponseRecorder, want any) (string, error) {
	if recorder.Code != http.StatusOK {
		return "", fmt.Errorf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var got any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		return "", fmt.Errorf("decoding the answer: %w", err)
	}

	return cmp.Diff(want, got), nil
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

	const file = "testdata/builder-preflight-exchange.json"

	request, want := loadBuilderExchange(t, file)

	diff, err := builderExchangeDiff(postBuilderPreflight(harness, draft, nil, request), want)
	if err != nil {
		t.Fatalf("POST preflight of %s: %v", file, err)
	}

	if diff != "" {
		t.Errorf("POST preflight answer mismatch with %s (-want +got):\n%s", file, diff)
	}
}

// TestBuilderPackageAnswersAsRecorded packages a document that names a
// Scenario config the server lacks.
func TestBuilderPackageAnswersAsRecorded(t *testing.T) {
	harness := newBuilderHarness(t)

	const file = "testdata/builder-package-exchange.json"

	request, want := loadBuilderExchange(t, file)

	diff, err := builderExchangeDiff(harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/package", body: request, user: builderAlice,
	}), want)
	if err != nil {
		t.Fatalf("POST /builder/package of %s: %v", file, err)
	}

	if diff != "" {
		t.Errorf("POST /builder/package answer mismatch with %s (-want +got):\n%s", file, diff)
	}
}
