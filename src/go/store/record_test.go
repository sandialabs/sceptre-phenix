package store

import (
	"errors"
	"testing"
)

func TestValidateRecordNamespace(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		valid     bool
	}{
		{name: "simple", namespace: "builder-chunks", valid: true},
		{name: "dotted", namespace: "builder.drafts_v1", valid: true},
		{name: "empty", namespace: "", valid: false},
		{name: "with slash", namespace: "builder/drafts", valid: false},
		{name: "parent traversal", namespace: "..", valid: false},
		{name: "leading dot", namespace: ".hidden", valid: false},
		{name: "with space", namespace: "builder drafts", valid: false},
		{name: "with percent", namespace: "builder%2Fdrafts", valid: false},
		{name: "too long", namespace: longString(maxRecordNamespaceLen + 1), valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRecordNamespace(tt.namespace)

			if tt.valid {
				if err != nil {
					t.Errorf("ValidateRecordNamespace(%q) error = %v, want nil", tt.namespace, err)
				}

				return
			}

			if !errors.Is(err, ErrInvalidRecordNamespace) {
				t.Errorf("ValidateRecordNamespace(%q) error = %v, want ErrInvalidRecordNamespace", tt.namespace, err)
			}
		})
	}
}

func TestValidateRecordKey(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		valid bool
	}{
		{name: "simple", key: "draft-1", valid: true},
		{name: "hierarchical", key: "draft-1/chunks/0001", valid: true},
		{name: "spaces allowed", key: "draft 1", valid: true},
		{name: "empty", key: "", valid: false},
		{name: "leading slash", key: "/draft-1", valid: false},
		{name: "trailing slash", key: "draft-1/", valid: false},
		{name: "empty segment", key: "draft-1//chunks", valid: false},
		{name: "dot segment", key: "draft-1/./chunks", valid: false},
		{name: "parent segment", key: "draft-1/../chunks", valid: false},
		{name: "control character", key: "draft\x00-1", valid: false},
		{name: "too long", key: longString(maxRecordKeyLen + 1), valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRecordKey(tt.key)

			if tt.valid {
				if err != nil {
					t.Errorf("ValidateRecordKey(%q) error = %v, want nil", tt.key, err)
				}

				return
			}

			if !errors.Is(err, ErrInvalidRecordKey) {
				t.Errorf("ValidateRecordKey(%q) error = %v, want ErrInvalidRecordKey", tt.key, err)
			}
		})
	}
}

func TestCopyRecordValue(t *testing.T) {
	value := []byte("draft")

	copied := CopyRecordValue(value)
	copied[0] = 'D'

	if string(value) != "draft" {
		t.Errorf("after changing CopyRecordValue(%q), the original = %q, want draft", "draft", value)
	}

	if got := CopyRecordValue(nil); got != nil {
		t.Errorf("CopyRecordValue(nil) = %q, want nil", got)
	}
}

func longString(n int) string {
	b := make([]byte, n)

	for i := range b {
		b[i] = 'a'
	}

	return string(b)
}
