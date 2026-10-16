package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestBoltDBOpenFailure(t *testing.T) {
	cfg := &Config{Kind: "Topology", Metadata: ConfigMetadata{Name: "foo"}}

	tests := []struct {
		name string
		call func(*BoltDB) error
	}{
		{name: "get", call: func(b *BoltDB) error { return b.Get(cfg) }},
		{name: "update", call: func(b *BoltDB) error { return b.Update(cfg) }},
		{name: "delete", call: func(b *BoltDB) error { return b.Delete(cfg) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The parent directory does not exist, so every open fails.
			b := &BoltDB{path: filepath.Join(t.TempDir(), "missing", "store.bdb")}

			done := make(chan error, 2)

			go func() {
				done <- tt.call(b)
				done <- tt.call(b)
			}()

			for range 2 {
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("expected an error opening the BoltDB file")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("call blocked after a failed open")
				}
			}
		})
	}
}
