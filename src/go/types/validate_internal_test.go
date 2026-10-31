package types

import "testing"

// TestConfigSchemaParsedOnce proves the Config schema is loaded once and
// every validation shares it.
func TestConfigSchemaParsedOnce(t *testing.T) {
	t.Parallel()

	first, err := configSchema()
	if err != nil {
		t.Fatalf("configSchema() error = %v", err)
	}

	if second, err := configSchema(); err != nil || second != first {
		t.Errorf("configSchema() = %p, %v, want %p, nil", second, err, first)
	}
}
