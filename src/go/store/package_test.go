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
			before := DefaultStore

			err := Init(Endpoint(tt.endpoint))
			if !tt.wantErr {
				if err != nil {
					t.Errorf("Init(%q) error = %v, want nil", tt.endpoint, err)
				}

				return
			}

			if err == nil {
				t.Errorf("Init(%q) error = nil, want an error", tt.endpoint)
			}

			if DefaultStore != before {
				t.Errorf("Init(%q) failed but replaced DefaultStore", tt.endpoint)
			}
		})
	}

	if _, err := os.Stat(boltPath); err != nil {
		t.Errorf("os.Stat(BoltDB file) error = %v, want the file Init creates", err)
	}
}
