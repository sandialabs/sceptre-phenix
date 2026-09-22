package config

import (
	"testing"

	"phenix/store"
)

func builderTopology(t *testing.T, annotations store.Annotations) store.Config {
	t.Helper()

	topo, err := store.NewConfig("topology/test")
	if err != nil {
		t.Fatalf("creating topology config: %v", err)
	}
	topo.Metadata.Annotations = annotations

	return *topo
}

func TestBuilderXML(t *testing.T) {
	const xml = "<mxGraphModel><root/></mxGraphModel>"

	tests := map[string]struct {
		annotations store.Annotations
		want        string
		wantOK      bool
	}{
		"embedded": {
			annotations: store.Annotations{BuilderXMLAnnotation: xml},
			want:        xml,
			wantOK:      true,
		},
		"empty value is still a diagram": {
			annotations: store.Annotations{BuilderXMLAnnotation: ""},
			want:        "",
			wantOK:      true,
		},
		"missing annotation": {
			annotations: store.Annotations{"topology": "branch-office"},
			wantOK:      false,
		},
		"no annotations": {
			annotations: nil,
			wantOK:      false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			topo := builderTopology(t, test.annotations)

			got, ok := BuilderXML(topo)
			if ok != test.wantOK {
				t.Fatalf("BuilderXML() ok = %t, want %t", ok, test.wantOK)
			}
			if ok && string(got) != test.want {
				t.Fatalf("BuilderXML() = %q, want %q", got, test.want)
			}
			if !ok && got != nil {
				t.Fatalf("BuilderXML() = %q, want nil", got)
			}
		})
	}
}

func TestHasBuilderXML(t *testing.T) {
	tests := map[string]struct {
		annotations store.Annotations
		want        bool
	}{
		"embedded":            {store.Annotations{BuilderXMLAnnotation: "<mxGraphModel/>"}, true},
		"other annotation":    {store.Annotations{"topology": "branch-office"}, false},
		"no annotations":      {nil, false},
		"empty diagram value": {store.Annotations{BuilderXMLAnnotation: ""}, true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			topo := builderTopology(t, test.annotations)

			if got := HasBuilderXML(topo); got != test.want {
				t.Fatalf("HasBuilderXML() = %t, want %t", got, test.want)
			}
		})
	}
}
