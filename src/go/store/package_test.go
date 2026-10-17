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
		{name: "etcd", endpoint: "etcd://localhost:2379", wantErr: true},
		{name: "unknown", endpoint: "foo://localhost", wantErr: true},
	}

	orig := DefaultStore

	t.Cleanup(func() { DefaultStore = orig })

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Init(Endpoint(tt.endpoint)); (err != nil) != tt.wantErr {
				t.Errorf("Init(%q) error = %v, want error %t", tt.endpoint, err, tt.wantErr)
			}
		})
	}

	if _, err := os.Stat(boltPath); err != nil {
		t.Errorf("os.Stat(%s) after Init(bolt://...) error = %v, want the BoltDB file", boltPath, err)
	}
}
