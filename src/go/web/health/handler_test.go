package health

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"phenix/version"
)

// fixed is a per-request check of one key with the given status and output.
func fixed(name string, status Status) Check {
	key := name + ":value"

	return Check{
		Name: name,
		Keys: []Key{{Name: key, ComponentID: name, ComponentType: TypeComponent}},
		Run: func(context.Context) map[string][]Result {
			return map[string][]Result{key: {{
				ComponentID:   name,
				ObservedValue: 1,
				Status:        status,
				Output:        "detail of " + name,
			}}}
		},
	}
}

// serve answers one request to a handler of the checks.
func serve(t *testing.T, method, target string, authenticated bool, checks ...Check) *httptest.ResponseRecorder {
	t.Helper()

	h := Handler{
		Registry:      NewRegistry(checks...),
		Description:   "test readiness",
		Authenticated: func(*http.Request) bool { return authenticated },
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), method, target, nil))

	return w
}

// decode decodes a response body generically.
func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding %q: %v", w.Body.String(), err)
	}

	return body
}

// hasOutput reports whether any object in v has an "output" key.
func hasOutput(v any) bool {
	switch v := v.(type) {
	case map[string]any:
		if _, ok := v["output"]; ok {
			return true
		}

		for _, child := range v {
			if hasOutput(child) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(v, hasOutput)
	}

	return false
}

func TestHandlerStatusCodes(t *testing.T) {
	t.Parallel()

	tests := map[Status]int{Pass: http.StatusOK, Warn: http.StatusOK, Fail: http.StatusServiceUnavailable}

	for status, code := range tests {
		w := serve(t, http.MethodGet, "/readyz", true, fixed("a", Pass), fixed("b", status))

		if w.Code != code {
			t.Errorf("%s: code %d, want %d", status, w.Code, code)
		}

		if got := w.Header().Get("Content-Type"); got != MediaType {
			t.Errorf("%s: Content-Type %q, want %q", status, got, MediaType)
		}

		if got := w.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control %q, want no-store", status, got)
		}

		if body := decode(t, w); body["status"] != string(status) {
			t.Errorf("%s: body status %v", status, body["status"])
		}
	}
}

func TestHandlerHeadHasNoBody(t *testing.T) {
	t.Parallel()

	w := serve(t, http.MethodHead, "/healthz", true, fixed("a", Fail))

	if w.Code != http.StatusServiceUnavailable || w.Body.Len() != 0 {
		t.Errorf("HEAD = %d with %d bytes, want 503 with none", w.Code, w.Body.Len())
	}
}

func TestHandlerShowsOutputOnlyWhenAuthenticated(t *testing.T) {
	t.Parallel()

	body := decode(t, serve(t, http.MethodGet, "/readyz", false, fixed("a", Pass), fixed("b", Warn)))

	if hasOutput(body) {
		t.Errorf("unauthenticated body has output: %v", body)
	}

	if notes, _ := body["notes"].([]any); !slices.Contains(notes, any(OutputOmitted)) {
		t.Errorf("notes = %v, want %q", body["notes"], OutputOmitted)
	}

	// the values are not output
	if checks, _ := body["checks"].(map[string]any); len(checks) != 2 {
		t.Errorf("checks = %v, want both", body["checks"])
	}

	body = decode(t, serve(t, http.MethodGet, "/readyz", true, fixed("a", Pass)))

	if !hasOutput(body) || body["notes"] != nil {
		t.Errorf("authenticated body = %v, want output and no notes", body)
	}
}

func TestHandlerExcludesChecks(t *testing.T) {
	t.Parallel()

	for _, target := range []string{
		"/readyz?exclude=minimega", "/readyz?exclude=other,minimega", "/readyz?exclude=other&exclude=minimega",
	} {
		body := decode(t, serve(t, http.MethodGet, target, true, fixed("minimega", Fail), fixed("store", Pass)))

		checks, _ := body["checks"].(map[string]any)

		if _, ok := checks["minimega:value"]; ok || checks["store:value"] == nil || body["status"] != "pass" {
			t.Errorf("%s: %v, want only the store check, passing", target, body)
		}
	}
}

//nolint:paralleltest // sets the version variables
func TestHandlerReportsVersion(t *testing.T) {
	tag, commit := version.Tag, version.Commit

	t.Cleanup(func() { version.Tag, version.Commit = tag, commit }) //nolint:reassign // restore build info

	version.Tag, version.Commit = "v1.2.3", "abc123" //nolint:reassign // test build info

	body := decode(t, serve(t, http.MethodGet, "/healthz", false))

	want := map[string]any{
		"version": "v1.2.3", "releaseId": "abc123", "serviceId": "phenix",
		"description": "test readiness", "status": "pass",
	}

	for key, value := range want {
		if body[key] != value {
			t.Errorf("%s = %v, want %v", key, body[key], value)
		}
	}

	if checks, ok := body["checks"].(map[string]any); !ok || len(checks) != 0 {
		t.Errorf("checks = %v, want an empty object", body["checks"])
	}
}

// Fractional values keep a decimal point when they happen to be whole, so an
// index maps them as floats from the first document.
func TestFloatMarshalsWithDecimalPoint(t *testing.T) {
	t.Parallel()

	tests := map[Float]string{0: "0.0", 42: "42.0", 1.27: "1.27", -3: "-3.0", 1e21: "1000000000000000000000.0"}

	for f, want := range tests {
		got, err := json.Marshal(Result{ObservedValue: f, Status: Pass})
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(string(got), `"observedValue":`+want+`,`) {
			t.Errorf("Float(%v) marshals in %s, want observedValue %s", float64(f), got, want)
		}
	}

	if _, err := json.Marshal(Float(math.NaN())); err == nil {
		t.Error("NaN marshals, want an error")
	}
}
