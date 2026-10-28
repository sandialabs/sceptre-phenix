package util_test

import (
	"testing"

	"phenix/util"
)

// GetEnv gives a set value, also an empty one, and the fallback only for an unset name.
func TestGetEnv(t *testing.T) {
	t.Setenv("PHENIX_UTIL_TEST_SET", "value")
	t.Setenv("PHENIX_UTIL_TEST_EMPTY", "")

	tests := []struct {
		key  string
		want string
	}{
		{key: "PHENIX_UTIL_TEST_SET", want: "value"},
		{key: "PHENIX_UTIL_TEST_EMPTY", want: ""},
		{key: "PHENIX_UTIL_TEST_UNSET", want: "fallback"},
	}

	for _, tt := range tests {
		if got := util.GetEnv(tt.key, "fallback"); got != tt.want {
			t.Errorf("GetEnv(%q, fallback) = %q, want %q", tt.key, got, tt.want)
		}
	}
}
