package web

import (
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"phenix/store"
)

func TestSaveBuilderTopology(t *testing.T) {
	xml := `<mxGraphModel value="%2B"/>`
	form := url.Values{
		"filename": {`diagram "one".xml`},
		"xml":      {xml},
	}
	req := httptest.NewRequest(http.MethodPost, "/builder/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()

	SaveBuilderTopology(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	if got := res.Body.String(); got != xml {
		t.Fatalf("body = %q, want %q", got, xml)
	}
	if got := res.Header().Get("Content-Type"); got != "application/xml; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}

	_, params, err := mime.ParseMediaType(res.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("parsing Content-Disposition: %v", err)
	}
	if got := params["filename"]; got != `diagram "one".xml` {
		t.Fatalf("filename = %q", got)
	}
}

func TestSaveBuilderTopologySVG(t *testing.T) {
	form := url.Values{
		"filename": {"diagram.svg"},
		"format":   {"svg"},
		"xml":      {"<svg/>"},
	}
	req := httptest.NewRequest(http.MethodPost, "/builder/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()

	SaveBuilderTopology(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	if got := res.Header().Get("Content-Type"); got != "image/svg+xml; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestSaveBuilderTopologyRejectsUnsupportedFormat(t *testing.T) {
	form := url.Values{
		"format": {"png"},
		"xml":    {"<output/>"},
	}
	req := httptest.NewRequest(http.MethodPost, "/builder/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()

	SaveBuilderTopology(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}

func TestSaveBuilderTopologyRequiresXML(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/builder/save", nil)
	res := httptest.NewRecorder()

	SaveBuilderTopology(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}

func TestValidateBuilderRequest(t *testing.T) {
	valid := builder{
		Name:     "test",
		Topology: map[string]any{"nodes": []any{}},
		XML:      "<mxGraphModel/>",
	}

	tests := []struct {
		name string
		req  builder
	}{
		{name: "missing name", req: builder{Topology: valid.Topology, XML: valid.XML}},
		{name: "missing topology", req: builder{Name: valid.Name, XML: valid.XML}},
		{name: "missing XML", req: builder{Name: valid.Name, Topology: valid.Topology}},
	}

	if err := validateBuilderRequest(valid); err != nil {
		t.Fatalf("valid request returned error: %v", err)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateBuilderRequest(test.req); err == nil {
				t.Fatal("validateBuilderRequest() returned no error")
			}
		})
	}
}

func TestAddScenarioTopology(t *testing.T) {
	scenario := &store.Config{}

	addScenarioTopology(scenario, "alpha")
	addScenarioTopology(scenario, "alpha")
	addScenarioTopology(scenario, "beta")

	if got := scenario.Metadata.Annotations["topology"]; got != "alpha,beta" {
		t.Fatalf("topology annotation = %q, want %q", got, "alpha,beta")
	}
}
