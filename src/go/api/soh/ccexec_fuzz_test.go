package soh

import (
	"testing"
)

// Any value that a check accepts reaches miniccc unchanged, as minicli and the
// minimega preprocessor split and expand the cc exec command.
func FuzzCheckCommands(f *testing.F) {
	for _, seed := range []string{
		"nginx", "python3 server.py", `O'Brien "x"`, `^python3 .*\.py$`, "$HOME/app",
		"cost$", "/opt/user's file", `C:\Users\O'Brien\data.txt`, "o'brien\u2019s-app",
		"#1", "a\nb\tc", "file:x", "http://x", "a\x00b", "a\xffb", "", "${x}", "a$1",
	} {
		f.Add(seed)
	}

	checks := linuxChecks()

	f.Fuzz(func(t *testing.T, value string) {
		for _, c := range checks {
			if err := checkRoundTrip(c, "linux", value); err != nil {
				t.Error(err)
			}
		}

		if err := windowsRoundTrip(value); err != nil {
			t.Error(err)
		}
	})
}
