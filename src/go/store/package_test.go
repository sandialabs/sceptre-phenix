package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitEndpointScheme(t *testing.T) {
	boltPath := filepath.Join(t.TempDir(), "store.bdb")

	tests := []struct {
		name     string
		endpoint string
		wantErr  bool
	}{
		{name: "bolt", endpoint: "bolt://" + boltPath},
		{name: "unknown", endpoint: "foo://localhost", wantErr: true},
	}

	orig := DefaultStore

	t.Cleanup(func() { DefaultStore = orig })

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Init(Endpoint(tt.endpoint))
			if tt.wantErr && err == nil {
				t.Fatalf("Init(%q) returned no error", tt.endpoint)
			}

			if !tt.wantErr && err != nil {
				t.Fatalf("Init(%q) returned error: %v", tt.endpoint, err)
			}
		})
	}

	if _, err := os.Stat(boltPath); err != nil {
		t.Fatalf("Init did not create the BoltDB file: %v", err)
	}
}
