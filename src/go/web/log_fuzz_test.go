package web

import (
	"testing"
	"time"

	"phenix/web/internal/webgen"
)

// parseLogRange takes start and end exactly when [time.Parse] takes them in the
// RFC 3339 layout (end may be empty), gives the same times, and refuses any
// other with 400.
func FuzzParseLogRange(f *testing.F) {
	for _, seed := range [][2]string{
		{"2024-01-01T00:00:00Z", ""}, {"2024-01-01T00:00:00Z", "2024-01-02T00:00:00+02:00"},
		{"2024-01-01T00:00:00.123456789-07:00", ""}, {"", ""}, {"yesterday", ""}, {"2024-01-01", ""},
		{"2024-01-01T00:00:00Z", "soon"}, {"2024-01-01T24:00:00Z", ""}, {"2024-02-30T00:00:00Z", ""},
		{"2024-01-01 00:00:00Z", ""}, {"2024-01-01T00:00:00Z", "2023-01-01T00:00:00Z"},
	} {
		f.Add(seed[0], seed[1])
	}

	now := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

	f.Fuzz(func(t *testing.T, start, end string) {
		wantStart, ok := webgen.TimeModel(start)

		wantEnd := now.Add(DefaultLogDuration)
		if end != "" {
			var endOK bool

			wantEnd, endOK = webgen.TimeModel(end)
			ok = ok && endOK
		}

		gotStart, gotEnd, err := parseLogRange(start, end, now)
		if (err == nil) != ok {
			t.Fatalf("parseLogRange(%q, %q) error = %v, want ok %t", start, end, err, ok)
		}

		if err != nil {
			if why := badRequest(err); why != "" {
				t.Fatalf("parseLogRange(%q, %q) error = %v, want a 400: %s", start, end, err, why)
			}

			return
		}

		if !gotStart.Equal(wantStart) || !gotEnd.Equal(wantEnd) {
			t.Fatalf("parseLogRange(%q, %q) = %v, %v; want %v, %v", start, end, gotStart, gotEnd, wantStart, wantEnd)
		}
	})
}
