package store

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestValidateRecordNamespace(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		valid     bool
	}{
		{name: "simple", namespace: "builder-chunks", valid: true},
		{name: "dotted", namespace: "builder.drafts_v1", valid: true},
		{name: "starts with a digit", namespace: "1drafts", valid: true},
		{name: "longest", namespace: longString(maxRecordNamespaceLen), valid: true},
		{name: "empty", namespace: "", valid: false},
		{name: "with slash", namespace: "builder/drafts", valid: false},
		{name: "parent traversal", namespace: "..", valid: false},
		{name: "leading dot", namespace: ".hidden", valid: false},
		{name: "with space", namespace: "builder drafts", valid: false},
		{name: "with percent", namespace: "builder%2Fdrafts", valid: false},
		{name: "leading dash", namespace: "-drafts", valid: false},
		{name: "leading underscore", namespace: "_drafts", valid: false},
		{name: "Unicode letter", namespace: "brouillons-é", valid: false},
		{name: "Unicode start", namespace: "épreuves", valid: false},
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
		{name: "Unicode", key: "brouillon-é/Ωmega/🚀", valid: true},
		{name: "dots in a segment", key: "draft-1/.../.hidden", valid: true},
		{name: "longest", key: longString(maxRecordKeyLen), valid: true},
		{name: "empty", key: "", valid: false},
		{name: "leading slash", key: "/draft-1", valid: false},
		{name: "trailing slash", key: "draft-1/", valid: false},
		{name: "empty segment", key: "draft-1//chunks", valid: false},
		{name: "dot segment", key: "draft-1/./chunks", valid: false},
		{name: "parent segment", key: "draft-1/../chunks", valid: false},
		{name: "control character", key: "draft\x00-1", valid: false},
		{name: "newline", key: "draft\n1", valid: false},
		{name: "delete character", key: "draft\x7f", valid: false},
		{name: "invalid UTF-8", key: "draft\xff", valid: false},
		{name: "only a dot", key: ".", valid: false},
		{name: "only two dots", key: "..", valid: false},
		{name: "only a slash", key: "/", valid: false},
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

// TestValidateRecordPrefixes checks that a scan prefix and a delete prefix
// may end with '/', and that only a scan prefix may be empty.
func TestValidateRecordPrefixes(t *testing.T) {
	for _, tt := range []struct {
		prefix       string
		scan, delete bool
	}{
		{prefix: "", scan: true, delete: false},
		{prefix: "draft-1/", scan: true, delete: true},
		{prefix: "draft-1/chunks/00", scan: true, delete: true},
		{prefix: "/draft-1", scan: false, delete: false},
		{prefix: "draft-1//", scan: false, delete: false},
		{prefix: "draft-1/../", scan: false, delete: false},
		{prefix: "draft\t", scan: false, delete: false},
		{prefix: longString(maxRecordKeyLen + 1), scan: false, delete: false},
	} {
		t.Run(tt.prefix, func(t *testing.T) {
			for name, check := range map[string]struct {
				validate func(string) error
				valid    bool
			}{
				"ValidateRecordPrefix":       {ValidateRecordPrefix, tt.scan},
				"ValidateRecordDeletePrefix": {ValidateRecordDeletePrefix, tt.delete},
			} {
				err := check.validate(tt.prefix)

				if check.valid && err != nil {
					t.Errorf("%s(%q) error = %v, want nil", name, tt.prefix, err)
				} else if !check.valid && !errors.Is(err, ErrInvalidRecordKey) {
					t.Errorf("%s(%q) error = %v, want ErrInvalidRecordKey", name, tt.prefix, err)
				}
			}
		})
	}
}

func TestRecordErrorMessages(t *testing.T) {
	for _, tt := range []struct {
		name   string
		err    error
		want   string
		target error
	}{
		{
			name: "not exist", err: NewRecordNotExistError("drafts", "draft-1"), target: ErrRecordNotExist,
			want: `record does not exist: namespace "drafts", key "draft-1"`,
		},
		{
			name: "exist", err: NewRecordExistError("drafts", "draft-1"), target: ErrRecordExist,
			want: `record already exists: namespace "drafts", key "draft-1"`,
		},
		{
			name: "conflict", err: NewRecordConflictError("drafts", "draft-1", 3, 5), target: ErrRecordConflict,
			want: `record revision conflict: namespace "drafts", key "draft-1" (expected revision 3, actual revision 5)`,
		},
		{
			name: "invalid namespace", err: ValidateRecordNamespace("a/b"), target: ErrInvalidRecordNamespace,
			want: `invalid record namespace: namespace "a/b": namespace contains invalid character '/'`,
		},
		{
			name: "empty namespace", err: ValidateRecordNamespace(""), target: ErrInvalidRecordNamespace,
			want: `invalid record namespace: namespace "": namespace must not be empty`,
		},
		{
			name: "invalid key", err: ValidateRecordKey("a/"), target: ErrInvalidRecordKey,
			want: `invalid record key: key "a/": key must not end with '/'`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}

			if !errors.Is(tt.err, tt.target) {
				t.Errorf("errors.Is(%v, %v) = false, want true", tt.err, tt.target)
			}
		})
	}
}

// FuzzValidateRecordNames checks that each name the validators accept has the
// properties a store relies on, and that each refusal is an InvalidRecordError.
func FuzzValidateRecordNames(f *testing.F) {
	for _, seed := range []string{"draft-1/chunks/0001", "builder.drafts_v1", "a/../b", "draft-1/", "drâfts/é\xff"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, name string) {
		if err := ValidateRecordNamespace(name); err == nil {
			if len(name) > maxRecordNamespaceLen || !isRecordNameStart(rune(name[0])) ||
				strings.ContainsFunc(name, func(r rune) bool { return !isRecordNameRune(r) }) {
				t.Errorf("ValidateRecordNamespace(%q) = nil, want a refusal", name)
			}

			if err := ValidateRecordKey(name); err != nil {
				t.Errorf("ValidateRecordKey(namespace %q) error = %v, want a valid key", name, err)
			}
		} else if !errors.Is(err, ErrInvalidRecordNamespace) {
			t.Errorf("ValidateRecordNamespace(%q) error = %v, want ErrInvalidRecordNamespace", name, err)
		}

		keyErr := ValidateRecordKey(name)
		if keyErr == nil && !safeRecordKeyForTest(name) {
			t.Errorf("ValidateRecordKey(%q) = nil, want a refusal", name)
		}

		var invalid *InvalidRecordError
		if keyErr != nil && (!errors.As(keyErr, &invalid) || invalid.Key != name || !errors.Is(keyErr, ErrInvalidRecordKey)) {
			t.Errorf("ValidateRecordKey(%q) error = %v, want an InvalidRecordError of the key", name, keyErr)
		}

		// A prefix differs from a key only in that it may end with '/'.
		prefixErr := ValidateRecordPrefix(name)
		if keyErr == nil && prefixErr != nil {
			t.Errorf("ValidateRecordPrefix(valid key %q) error = %v, want nil", name, prefixErr)
		} else if keyErr != nil && prefixErr == nil && name != "" && !strings.HasSuffix(name, "/") {
			t.Errorf("ValidateRecordPrefix(%q) = nil, but ValidateRecordKey error = %v", name, keyErr)
		}

		if name != "" && (ValidateRecordDeletePrefix(name) == nil) != (prefixErr == nil) {
			t.Errorf("ValidateRecordDeletePrefix(%q) and ValidateRecordPrefix(%q) disagree", name, name)
		}
	})
}

// safeRecordKeyForTest reports whether key cannot escape or collide with
// another key: the properties that ValidateRecordKey promises.
func safeRecordKeyForTest(key string) bool {
	if key == "" || len(key) > maxRecordKeyLen || !utf8.ValidString(key) ||
		strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") ||
		strings.ContainsFunc(key, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return false
	}

	for segment := range strings.SplitSeq(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}

	return true
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
