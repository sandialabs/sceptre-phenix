package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

// builderPackageExchangeFile holds a request of the package route and the
// route's answer to it, which TestBuilderPackageAnswersAsRecorded checks.
// The test of phenix builder drafts export --package in phenix/cmd reads the
// same file, so the command is tested against what the route answers.
const builderPackageExchangeFile = "testdata/builder-package-exchange.json"

// TestBuilderPackageAnswersAsRecorded sends the request of
// builderPackageExchangeFile, the package of a document that names a
// Scenario config the server lacks, with the scenarios section, to the
// package route, and compares the answer with the answer recorded there: the
// package and the warning about the config it cannot carry. The same request
// gets the same answer on the unix socket's router, which phenix builder
// drafts export --package uses without --url.
func TestBuilderPackageAnswersAsRecorded(t *testing.T) {
	// The socket's NoAuth middleware reads the global-admin role from the
	// config store.
	useUsersTestStore(t)

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

	onAPI := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/package", body: string(exchange.Request), user: builderTestOwner,
	})

	onSocket := httptest.NewRecorder()
	newSocketRouter(harness.api).ServeHTTP(onSocket, httptest.NewRequest(
		http.MethodPost, "/api/v1/builder/package", bytes.NewReader(exchange.Request),
	))

	for _, answer := range []struct {
		router   string
		recorder *httptest.ResponseRecorder
	}{
		{router: "the API router", recorder: onAPI},
		{router: "the unix socket's router", recorder: onSocket},
	} {
		if answer.recorder.Code != http.StatusOK {
			t.Fatalf("package on %s: status = %d: %s", answer.router, answer.recorder.Code, answer.recorder.Body.String())
		}

		var got any

		harness.decode(answer.recorder, &got)

		if !reflect.DeepEqual(got, want) {
			t.Errorf("the package route's answer on %s is not the answer recorded in %s; it answered\n%s",
				answer.router, builderPackageExchangeFile, answer.recorder.Body.String())
		}
	}
}
