package health

import (
	"context"
	"encoding/json"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

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
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()

			w := serve(t, http.MethodGet, "/readyz", true, fixed("a", Pass), fixed("b", status))

			if w.Code != code {
				t.Errorf("GET /readyz with a %s check = %d, want %d", status, w.Code, code)
			}

			if got := w.Header().Get("Content-Type"); got != MediaType {
				t.Errorf("GET /readyz with a %s check: Content-Type = %q, want %q", status, got, MediaType)
			}

			if got := w.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("GET /readyz with a %s check: Cache-Control = %q, want no-store", status, got)
			}

			if got := decode(t, w)["status"]; got != string(status) {
				t.Errorf("GET /readyz with a %s check: body status = %v, want %s", status, got, status)
			}
		})
	}
}

func TestHandlerHeadHasNoBody(t *testing.T) {
	t.Parallel()

	w := serve(t, http.MethodHead, "/healthz", true, fixed("a", Fail))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("HEAD /healthz = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	if w.Body.Len() != 0 {
		t.Errorf("HEAD /healthz body = %q, want none", w.Body.String())
	}
}

func TestHandlerShowsOutputOnlyWhenAuthenticated(t *testing.T) {
	t.Parallel()

	body := decode(t, serve(t, http.MethodGet, "/readyz", false, fixed("a", Pass), fixed("b", Warn)))

	if hasOutput(body) {
		t.Errorf("unauthenticated GET /readyz = %v, want no output", body)
	}

	if diff := cmp.Diff([]any{OutputOmitted}, body["notes"]); diff != "" {
		t.Errorf("unauthenticated GET /readyz notes mismatch (-want +got):\n%s", diff)
	}

	// the values are not output
	if checks, _ := body["checks"].(map[string]any); len(checks) != 2 {
		t.Errorf("unauthenticated GET /readyz checks = %v, want both", body["checks"])
	}

	body = decode(t, serve(t, http.MethodGet, "/readyz", true, fixed("a", Pass)))

	if !hasOutput(body) {
		t.Errorf("authenticated GET /readyz = %v, want output", body)
	}

	if notes, ok := body["notes"]; ok {
		t.Errorf("authenticated GET /readyz notes = %v, want none", notes)
	}
}

func TestHandlerExcludesChecks(t *testing.T) {
	t.Parallel()

	for name, target := range map[string]string{
		"one":      "/readyz?exclude=minimega",
		"list":     "/readyz?exclude=other,minimega",
		"repeated": "/readyz?exclude=other&exclude=minimega",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			body := decode(t, serve(t, http.MethodGet, target, true, fixed("minimega", Fail), fixed("store", Pass)))

			checks, _ := body["checks"].(map[string]any)
			if diff := cmp.Diff([]string{"store:value"}, slices.Sorted(maps.Keys(checks))); diff != "" {
				t.Errorf("GET %s check keys mismatch (-want +got):\n%s", target, diff)
			}

			if got := body["status"]; got != string(Pass) {
				t.Errorf("GET %s status = %v, want %s", target, got, Pass)
			}
		})
	}
}

//nolint:paralleltest // sets the version variables
func TestHandlerReportsVersion(t *testing.T) {
	tag, commit := version.Tag, version.Commit

	t.Cleanup(func() { version.Tag, version.Commit = tag, commit }) //nolint:reassign // restore build info

	version.Tag, version.Commit = "v1.2.3", "abc123" //nolint:reassign // test build info

	want := map[string]any{
		"version":     "v1.2.3",
		"releaseId":   "abc123",
		"serviceId":   "phenix",
		"description": "test readiness",
		"status":      "pass",
		"notes":       []any{OutputOmitted},
		"checks":      map[string]any{},
	}

	if diff := cmp.Diff(want, decode(t, serve(t, http.MethodGet, "/healthz", false))); diff != "" {
		t.Errorf("GET /healthz mismatch (-want +got):\n%s", diff)
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
			t.Errorf("json.Marshal(Result{ObservedValue: Float(%v)}) error = %v", float64(f), err)

			continue
		}

		// the bytes are the contract: a whole value keeps its decimal point
		if !strings.Contains(string(got), `"observedValue":`+want+`,`) {
			t.Errorf("json.Marshal(Result{ObservedValue: Float(%v)}) = %s, want observedValue %s", float64(f), got, want)
		}
	}

	if got, err := json.Marshal(Float(math.NaN())); err == nil {
		t.Errorf("json.Marshal(Float(NaN)) = %s, want an error", got)
	}
}
