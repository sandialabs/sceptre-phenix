package soh

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/app"
	v1 "phenix/types/version/v1"
)

// A process check passes only when each line of the response is a process
// ID, and a response line that is not an ID is an error, not a pass.
func TestProcRunning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		resp        string
		wantRunning bool
		wantErr     string
	}{
		{resp: ""},
		{resp: "\n \r\n"},
		{resp: "321", wantRunning: true},
		{resp: "321\n322\n", wantRunning: true},
		{resp: "412\r\n\r\n", wantRunning: true},
		{resp: "pgrep: regex error\n", wantErr: `process check failed: "pgrep: regex error"`},
		{resp: "321\npgrep: warning\n", wantErr: `process check failed: "pgrep: warning"`},
		{resp: "svchost 412", wantErr: `process check failed: "svchost 412"`},
		{resp: "-1", wantErr: `process check failed: "-1"`},
	}

	for _, tt := range tests {
		running, err := procRunning(tt.resp)

		gotErr := ""
		if err != nil {
			gotErr = err.Error()
		}

		if running != tt.wantRunning || gotErr != tt.wantErr {
			t.Errorf("procRunning(%q) = %t, %q, want %t, %q", tt.resp, running, gotErr, tt.wantRunning, tt.wantErr)
		}
	}
}

// A pgrep or PowerShell error in the process check response is stored as an
// error at once, and an empty response is retried and then stored as not
// running.
func TestRunChecksReportsProcessCheckErrors(t *testing.T) { //nolint:paralleltest // replaces package globals
	const psWildcardErr = "Get-Process : The specified wildcard character pattern is not valid: bad[\r\n" +
		"At line:1 char:1\r\n"

	exp := sohExperiment(t, map[string]any{
		"c2Timeout": "1m",
		"hostProcesses": map[string]any{
			"web": []any{"nginx", "ghost", "["},
			"win": []any{"svchost", "bad["},
		},
	}, []*v1.Node{
		sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", "")),
		sohNode("win", "windows", sohIface("eth0", "A", "static", "10.0.0.2", "")),
	})

	winCmd := func(name string) string {
		return `exp/win: powershell -NoProfile -Command "Get-Process -Name '` + name +
			`' -ErrorAction SilentlyContinue` + psID + `"`
	}

	answers := map[string]string{
		"exp/web: pgrep -f -- nginx": "321\n322\n",
		"exp/web: pgrep -f -- ghost": "",
		"exp/web: pgrep -f -- '['":   "pgrep: regex error: brackets ([ ]) not balanced\n",
		winCmd("svchost"):            "412\r\n",
		winCmd("bad["):               psWildcardErr,
	}

	fake := useSOHMM(t, answerFrom(answers))

	s := newSOH(withRetryDelay(time.Millisecond))
	if err := s.decodeMetadata(exp); err != nil {
		t.Fatalf("decodeMetadata(exp) error = %v", err)
	}

	ctx := app.SetContextMetadata(t.Context(), map[string]any{"checks": []string{"processes"}})

	if err := s.runChecks(ctx, exp); err == nil {
		t.Error("runChecks(processes) error = nil, want an error")
	}

	retried := []string{"exp/web: pgrep -f -- ghost"}
	once := []string{"exp/web: pgrep -f -- nginx", "exp/web: pgrep -f -- '['", winCmd("svchost"), winCmd("bad[")}
	checkSent(t, "runChecks(processes)", fake, once, retried)

	want := map[string]hostResult{
		"web": {Checks: sorted(
			"process map[host:web proc:nginx] = process running",
			"process map[host:web proc:ghost] = error: process not running",
			`process map[host:web proc:[] = error: process check failed: "pgrep: regex error: brackets ([ ]) not balanced"`,
		)},
		"win": {Checks: sorted(
			"process map[host:win proc:svchost] = process running",
			`process map[host:win proc:bad[] = error: process check failed: `+
				`"Get-Process : The specified wildcard character pattern is not valid: bad["`,
		)},
	}
	if diff := cmp.Diff(want, storedResults(t)); diff != "" {
		t.Errorf("runChecks(processes) stored results mismatch (-want +got):\n%s", diff)
	}
}
