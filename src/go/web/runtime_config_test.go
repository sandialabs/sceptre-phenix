package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestClientAuthMode(t *testing.T) {
	tests := []struct {
		name   string
		jwtKey string
		mode   string
	}{
		{name: "no key", mode: "disabled"},
		{name: "development key", jwtKey: "dev|tester|global-admin", mode: "disabled"},
		{name: "proxy key", jwtKey: "proxy-jwt", mode: "proxy"},
		{name: "password key", jwtKey: "secret", mode: "enabled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if mode := clientAuthMode(tt.jwtKey); mode != tt.mode {
				t.Fatalf("mode = %q, want %q", mode, tt.mode)
			}
		})
	}
}

func TestRenderRuntimeIndex(t *testing.T) {
	options := newServerOptions(
		ServeBasePath(`custom/"quoted`),
		ServeWithJWTKey("secret"),
	)

	index, err := renderRuntimeIndex(
		[]byte(`<html><head>`+runtimeConfigMarker+`</head></html>`),
		options,
	)
	if err != nil {
		t.Fatalf("rendering runtime index: %v", err)
	}

	rendered := string(index)

	for _, want := range []string{
		`<base href="/custom/&#34;quoted/">`,
		`<meta name="phenix-base-path" content="/custom/&#34;quoted/">`,
		`<meta name="phenix-auth-mode" content="enabled">`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered index missing %q: %s", want, rendered)
		}
	}

	if strings.Contains(rendered, runtimeConfigMarker) {
		t.Errorf("rendered index still contains marker: %s", rendered)
	}
}

func TestRenderRuntimeIndexRequiresMarker(t *testing.T) {
	_, err := renderRuntimeIndex([]byte(`<html></html>`), newServerOptions())
	if err == nil {
		t.Fatal("expected missing marker error")
	}
}

func TestServeRuntimeIndexDisablesCaching(t *testing.T) {
	assets := http.FS(fstest.MapFS{
		"index.html": {
			Data: []byte(`<html><head>` + runtimeConfigMarker + `</head></html>`),
		},
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()

	err := serveRuntimeIndex(response, request, assets, newServerOptions())
	if err != nil {
		t.Fatalf("serving runtime index: %v", err)
	}

	if value := response.Header().Get("Cache-Control"); value != "no-store" {
		t.Fatalf("Cache-Control = %q, want %q", value, "no-store")
	}

	body, err := io.ReadAll(response.Result().Body)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}

	if !strings.Contains(string(body), `content="disabled"`) {
		t.Fatalf("response missing server-selected auth mode: %s", body)
	}
}
