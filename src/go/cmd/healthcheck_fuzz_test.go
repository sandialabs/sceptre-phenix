package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"phenix/web/health"
)

// printHealth prints one line for the status of any body a server sends, then one indented line for each check
// that does not pass. The model counts them from the decoded body.
func FuzzPrintHealth(f *testing.F) {
	for _, body := range []string{
		"", "<html>bad gateway</html>", `{"checks":{}}`, `{"status":"pass","checks":[]}`,
		`{"status":"pass","checks":{"uptime":[{"status":"pass"}]}}`,
		`{"status":"fail","checks":{"store":[{"status":"fail","output":"locked"}],` +
			`"disk":[{"componentId":"files-dir","status":"warn","output":"91% used"},{"status":"pass"}]}}`,
	} {
		f.Add(503, []byte(body))
	}

	f.Fuzz(func(t *testing.T, code int, body []byte) {
		var out bytes.Buffer

		printHealth(&out, code, body)

		var resp health.Response

		wantLines := []string{fmt.Sprintf("unknown (%d)", code)}
		if err := json.Unmarshal(body, &resp); err == nil && resp.Status != "" {
			wantLines[0] = fmt.Sprintf("%s (%d)", resp.Status, code)

			for _, results := range resp.Checks {
				for _, r := range results {
					if r.Status != health.Pass {
						wantLines = append(wantLines, "  ")
					}
				}
			}
		}

		got := out.String()
		if !strings.HasSuffix(got, "\n") {
			t.Fatalf("printHealth(%d, %q) = %q, want lines that end in a newline", code, body, got)
		}

		if first, _, _ := strings.Cut(got, "\n"); first != wantLines[0] && !strings.Contains(wantLines[0], "\n") {
			t.Fatalf("printHealth(%d, %q) first line = %q, want %q", code, body, first, wantLines[0])
		}

		// a status or output that holds a newline adds lines of its own
		if bytes.ContainsAny(body, "\\\n") {
			return
		}

		lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
		if len(lines) != len(wantLines) {
			t.Fatalf("printHealth(%d, %q) = %d lines, want %d:\n%s", code, body, len(lines), len(wantLines), got)
		}

		for _, line := range lines[1:] {
			if !strings.HasPrefix(line, "  ") {
				t.Fatalf("printHealth(%d, %q) line %q is not indented", code, body, line)
			}
		}
	})
}
