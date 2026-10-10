package web

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"
)

// builderPackageExchangeFile holds a request of the package route and the
// route's answer to it, which TestBuilderPackageAnswersAsRecorded checks.
const builderPackageExchangeFile = "testdata/builder-package-exchange.json"

// TestBuilderPackageAnswersAsRecorded sends the request of
// builderPackageExchangeFile, the package of a document that names a
// Scenario config the server lacks, with the scenarios section, to the
// package route, and compares the answer with the answer recorded there: the
// package and the warning about the config it cannot carry.
func TestBuilderPackageAnswersAsRecorded(t *testing.T) {
	data, err := os.ReadFile(builderPackageExchangeFile)
	if err != nil {
		t.Fatalf("reading %s: %v", builderPackageExchangeFile, err)
	}

	var exchange struct {
		Request json.RawMessage `json:"request"`
		Answer  json.RawMessage `json:"answer"`
	}

	if err := json.Unmarshal(data, &exchange); err != nil {
		t.Fatalf("decoding %s: %v", builderPackageExchangeFile, err)
	}

	var want any

	if err := json.Unmarshal(exchange.Answer, &want); err != nil {
		t.Fatalf("decoding the recorded answer: %v", err)
	}

	harness := newBuilderHarness(t)

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/package", body: string(exchange.Request), user: builderTestOwner,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("package status = %d: %s", recorder.Code, recorder.Body.String())
	}

	var got any

	harness.decode(recorder, &got)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("the package route's answer is not the answer recorded in %s; it answered\n%s",
			builderPackageExchangeFile, recorder.Body.String())
	}
}
