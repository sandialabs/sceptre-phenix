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
		{name: "simple", namespace: "builder-drafts", valid: true},
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

			if tt.valid && err != nil {
				t.Fatalf("ValidateRecordNamespace(%q) returned error: %v", tt.namespace, err)
			}

			if !tt.valid {
				if err == nil {
					t.Fatalf("ValidateRecordNamespace(%q) should have returned an error", tt.namespace)
				}

				if !errors.Is(err, ErrInvalidRecordNamespace) {
					t.Fatalf("ValidateRecordNamespace(%q) error = %v, want ErrInvalidRecordNamespace", tt.namespace, err)
				}
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

			if tt.valid && err != nil {
				t.Fatalf("ValidateRecordKey(%q) returned error: %v", tt.key, err)
			}

			if !tt.valid {
				if err == nil {
					t.Fatalf("ValidateRecordKey(%q) should have returned an error", tt.key)
				}

				if !errors.Is(err, ErrInvalidRecordKey) {
					t.Fatalf("ValidateRecordKey(%q) error = %v, want ErrInvalidRecordKey", tt.key, err)
				}
			}
		})
	}
}

func TestValidateRecordPrefixAllowsEmptyButDeletePrefixDoesNot(t *testing.T) {
	if err := ValidateRecordPrefix(""); err != nil {
		t.Fatalf("ValidateRecordPrefix(\"\") returned error: %v", err)
	}

	if err := ValidateRecordPrefix("draft-1/chunks/"); err != nil {
		t.Fatalf("ValidateRecordPrefix with trailing separator returned error: %v", err)
	}

	err := ValidateRecordDeletePrefix("")
	if err == nil {
		t.Fatal("ValidateRecordDeletePrefix(\"\") should have returned an error")
	}

	if !errors.Is(err, ErrInvalidRecordKey) {
		t.Fatalf("ValidateRecordDeletePrefix(\"\") error = %v, want ErrInvalidRecordKey", err)
	}
}

func TestRecordErrorsAreErrorsIsCompatible(t *testing.T) {
	notExist := error(NewRecordNotExistError("ns", "key"))
	if !errors.Is(notExist, ErrRecordNotExist) {
		t.Fatal("RecordNotExistError should match ErrRecordNotExist")
	}

	exist := error(NewRecordExistError("ns", "key"))
	if !errors.Is(exist, ErrRecordExist) {
		t.Fatal("RecordExistError should match ErrRecordExist")
	}

	conflict := error(NewRecordConflictError("ns", "key", 2, 3))
	if !errors.Is(conflict, ErrRecordConflict) {
		t.Fatal("RecordConflictError should match ErrRecordConflict")
	}

	var target *RecordConflictError
	if !errors.As(conflict, &target) {
		t.Fatal("RecordConflictError should be extractable with errors.As")
	}

	if target.Expected != 2 || target.Actual != 3 {
		t.Fatalf("conflict revisions = (%d, %d), want (2, 3)", target.Expected, target.Actual)
	}

	if errors.Is(notExist, ErrRecordExist) || errors.Is(conflict, ErrRecordNotExist) {
		t.Fatal("record errors should not match unrelated sentinels")
	}
}

func TestCopyRecordValueAndClone(t *testing.T) {
	value := []byte("draft")

	copied := CopyRecordValue(value)
	copied[0] = 'D'

	if value[0] != 'd' {
		t.Fatal("CopyRecordValue must not share the original backing array")
	}

	if CopyRecordValue(nil) != nil {
		t.Fatal("CopyRecordValue(nil) should return nil")
	}

	record := Record{Namespace: "ns", Key: "key", Value: value}

	clone := record.Clone()
	clone.Value[0] = 'X'

	if record.Value[0] != 'd' {
		t.Fatal("Record.Clone must deep copy the value")
	}
}

func TestCheckRecordRevision(t *testing.T) {
	if err := checkRecordRevision("ns", "key", AnyRevision, 7); err != nil {
		t.Fatalf("AnyRevision should skip the revision check, got: %v", err)
	}

	if err := checkRecordRevision("ns", "key", 7, 7); err != nil {
		t.Fatalf("matching revisions should not conflict, got: %v", err)
	}

	err := checkRecordRevision("ns", "key", 6, 7)
	if !errors.Is(err, ErrRecordConflict) {
		t.Fatalf("mismatched revisions error = %v, want ErrRecordConflict", err)
	}
}

func longString(n int) string {
	b := make([]byte, n)

	for i := range b {
		b[i] = 'a'
	}

	return string(b)
}
