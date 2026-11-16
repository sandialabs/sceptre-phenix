package lumberjack

import (
	"strings"
	"testing"
)

// timeFromName never panics for a sibling of any log name, and it reads a time
// only from a sibling that holds the prefix and the extension apart.
func FuzzTimeFromName(f *testing.F) {
	for _, seed := range [][2]string{
		{"server.log", "server-2016-11-04T18-30-00.000.log"},
		{"server.log", "server.log"},
		{"server", "server-2016-11-04T18-30-00.000"},
		{"a..-", "a.-"},
		{"a..-", "a.-2016-11-04T18-30-00.000.-"},
		{".log", "-.log"},
		{"", ""},
	} {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, logName, sibling string) {
		l := &Logger{Filename: logName}
		prefix, ext := l.prefixAndExt()

		if _, err := l.timeFromName(sibling, prefix, ext); err == nil {
			if len(sibling) < len(prefix)+len(ext) ||
				!strings.HasPrefix(sibling, prefix) || !strings.HasSuffix(sibling, ext) {
				t.Fatalf("timeFromName(%q, %q, %q) error = nil, want an error", sibling, prefix, ext)
			}
		}
	})
}
