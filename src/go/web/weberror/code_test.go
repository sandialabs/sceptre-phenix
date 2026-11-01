package weberror

import (
	"net/http"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

// documentedCodes are the codes openapi.yml's Error schema lists.
var documentedCodes = []Code{ //nolint:gochecknoglobals // test table
	CodeInvalidRequest, CodeUnauthorized, CodeForbidden, CodeNotFound,
	CodeConflict, CodeRateLimited, CodeUnavailable, CodeInternal,
}

// Each status the routes answer has its code.
func TestCodeFor(t *testing.T) {
	got := map[int]Code{}
	want := map[int]Code{
		http.StatusBadRequest:                   CodeInvalidRequest,
		http.StatusUnauthorized:                 CodeUnauthorized,
		http.StatusForbidden:                    CodeForbidden,
		http.StatusNotFound:                     CodeNotFound,
		http.StatusMethodNotAllowed:             CodeInvalidRequest,
		http.StatusConflict:                     CodeConflict,
		http.StatusPreconditionFailed:           CodeConflict,
		http.StatusRequestEntityTooLarge:        CodeInvalidRequest,
		http.StatusUnsupportedMediaType:         CodeInvalidRequest,
		http.StatusRequestedRangeNotSatisfiable: CodeInvalidRequest,
		http.StatusUnprocessableEntity:          CodeInvalidRequest,
		http.StatusTooManyRequests:              CodeRateLimited,
		http.StatusInternalServerError:          CodeInternal,
		http.StatusNotImplemented:               CodeInternal,
		http.StatusBadGateway:                   CodeUnavailable,
		http.StatusServiceUnavailable:           CodeUnavailable,
		http.StatusGatewayTimeout:               CodeUnavailable,
	}

	for status := range want {
		got[status] = CodeFor(status)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("CodeFor mismatch (-want +got):\n%s", diff)
	}
}

// Every error status has a documented code, a client error never one that
// blames the server, and a server error always one that does.
func TestCodeForProperty(t *testing.T) {
	serverCodes := []Code{CodeUnavailable, CodeInternal}

	rapid.Check(t, func(t *rapid.T) {
		status := rapid.IntRange(http.StatusBadRequest, 599).Draw(t, "status")

		code := CodeFor(status)
		if !slices.Contains(documentedCodes, code) {
			t.Fatalf("CodeFor(%d) = %q, want a documented code", status, code)
		}

		if server := status >= http.StatusInternalServerError; server != slices.Contains(serverCodes, code) {
			t.Fatalf("CodeFor(%d) = %q, which blames the server: %t, want %t", status, code, !server, server)
		}
	})
}

// An error's own code wins over its status's.
func TestWithCode(t *testing.T) {
	_, _, body := serve(t, func(http.ResponseWriter, *http.Request) error {
		return NewWebError(nil, "minimega is busy").SetStatus(http.StatusConflict).WithCode(CodeUnavailable)
	})

	if body.Code != CodeUnavailable {
		t.Errorf("code = %q, want %q", body.Code, CodeUnavailable)
	}
}
