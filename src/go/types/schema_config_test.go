package types_test

import (
	"errors"
	"strings"
	"testing"

	"phenix/store"
	"phenix/types"
)

// TestConfigSchemaBuilderDocumentAnnotation is a regression guard on the
// Config schema's builder-doc annotation: a map of the text sub-keys digest,
// id and path, with at least one of them. Every other annotation is text.
func TestConfigSchemaBuilderDocumentAnnotation(t *testing.T) {
	t.Parallel()

	config := func(t *testing.T, annotations string) store.Config {
		t.Helper()

		c, err := store.NewConfigFromYAML([]byte(
			"apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: site\n  annotations:\n" +
				annotations + "spec:\n  nodes: []\n",
		))
		if err != nil {
			t.Fatalf("NewConfigFromYAML returned error: %v", err)
		}

		return *c
	}

	for _, test := range []struct {
		name        string
		annotations string
		// refused is part of the validation error, or empty when the config
		// is valid.
		refused string
	}{
		{
			name: "digest, id and path",
			annotations: "    keep: v\n    builder-doc:\n      path: /phenix/topologies/site/builder.yaml\n" +
				"      id: 0f1e2d\n      digest: sha256:aa\n",
		},
		{name: "digest", annotations: "    builder-doc:\n      digest: sha256:aa\n"},
		{name: "id", annotations: "    builder-doc:\n      id: 0f1e2d\n"},
		{name: "path", annotations: "    builder-doc:\n      path: /phenix/builder.json\n"},
		{name: "the string of a map", annotations: "    builder-doc: '{\"id\":\"0f1e2d\"}'\n"},
		{name: "no builder-doc", annotations: "    keep: v\n    builder-experiment: '{\"draftId\":\"d\"}'\n"},
		{
			name: "an unknown sub-key", annotations: "    builder-doc:\n      file: /phenix/builder.json\n",
			refused: `property "file" is unsupported`,
		},
		{
			name: "a sub-key an earlier Builder wrote", annotations: "    builder-doc:\n      id: 0f1e2d\n      draftId: d\n",
			refused: `property "draftId" is unsupported`,
		},
		{name: "no sub-key", annotations: "    builder-doc: {}\n", refused: "at least 1 properties"},
		{name: "a string", annotations: "    builder-doc: not a map\n", refused: "value must be an object"},
		{name: "an empty string", annotations: "    builder-doc: ''\n", refused: "value must be an object"},
		{
			name:        "the reference an earlier Builder wrote",
			annotations: "    builder-doc: '{\"id\":\"0f1e2d\",\"digest\":\"sha256:aa\",\"size\":10,\"chunks\":1}'\n",
			refused:     "value must be an object",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := types.ValidateConfigSpec(config(t, test.annotations))

			switch {
			case test.refused == "" && err != nil:
				t.Fatalf("ValidateConfigSpec returned error: %v", err)
			case test.refused != "" && (!errors.Is(err, types.ErrValidationFailed) ||
				!strings.Contains(err.Error(), test.refused) || !strings.Contains(err.Error(), "builder-doc")):
				t.Fatalf("ValidateConfigSpec error = %v, want a validation failure of builder-doc holding %q", err, test.refused)
			}
		})
	}
}
