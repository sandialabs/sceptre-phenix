package cmd

import "testing"

func TestResolveUIBasePath(t *testing.T) {
	tests := []struct {
		name        string
		configured  string
		flagChanged bool
		standardEnv string
		legacyEnv   string
		baseEnv     string
		expected    string
	}{
		{
			name:        "flag wins",
			configured:  "/flag",
			flagChanged: true,
			standardEnv: "/standard",
			legacyEnv:   "/legacy",
			baseEnv:     "/base",
			expected:    "/flag",
		},
		{
			name:        "standard environment variable wins",
			configured:  "/standard",
			standardEnv: "/standard",
			legacyEnv:   "/legacy",
			baseEnv:     "/base",
			expected:    "/standard",
		},
		{
			name:       "legacy prefixed environment variable",
			configured: "/default",
			legacyEnv:  "/legacy",
			baseEnv:    "/base",
			expected:   "/legacy",
		},
		{
			name:       "base path environment variable",
			configured: "/default",
			baseEnv:    "/base",
			expected:   "/base",
		},
		{
			name:       "configured fallback",
			configured: "/configured",
			expected:   "/configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PHENIX_UI_BASE_PATH", tt.standardEnv)
			t.Setenv("PHENIX_BASE_PATH", tt.legacyEnv)
			t.Setenv("BASE_PATH", tt.baseEnv)

			actual := resolveUIBasePath(tt.configured, tt.flagChanged)
			if actual != tt.expected {
				t.Fatalf("base path = %q, want %q", actual, tt.expected)
			}
		})
	}
}
