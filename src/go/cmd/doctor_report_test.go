package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"text/tabwriter"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/util/termcolor"
)

// doctorReportLines is the text output of report, each line with its runs of
// spaces made one, so the tests do not depend on column widths.
func doctorReportLines(report doctorReport, strict bool) []string {
	var out bytes.Buffer

	printDoctorReport(&out, report, strict, termcolor.New(false))

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.Join(strings.Fields(line), " ")
	}

	return lines
}

// doctorReportBase is a report with the given checks and no settings, of
// phenix 1.0.0 on a Linux host.
func doctorReportBase(status doctorStatus, checks ...doctorResult) doctorReport {
	return doctorReport{
		SchemaVersion: doctorSchemaVersion,
		Status:        status,
		Time:          "2026-10-30T12:00:00Z",
		Version:       doctorVersion{Phenix: "1.0.0", Label: "v1.0.0", Commit: "abc1234", Date: "2026-10-01", Minimega: ""},
		Environment: doctorEnvironment{
			OS: "linux", Kernel: "6.8.0", Arch: "amd64", UID: 1000, GID: 1000, User: "alice",
			WorkingDir: "/home/alice", ConfigFile: "", Container: false, ContainerHint: "", Hostname: "",
		},
		Checks:   checks,
		Settings: []doctorSetting{},
	}
}

// The header names what doctor could not find out, the summary counts each
// status and names each failed and warned check, and hints follow for each
// failure, and with --strict for each warning.
func TestPrintDoctorReport(t *testing.T) {
	t.Parallel()

	check := func(id string, status doctorStatus, hint string) doctorResult {
		return doctorResult{ID: id, Group: "g", Name: id, Status: status, Detail: id + " detail", Hint: hint}
	}

	answered := doctorReportBase(doctorPass, check(minimegaCheckName, doctorPass, ""))
	answered.Version.Minimega = "minimega 3.0.1"
	answered.Environment.ConfigFile = "/etc/phenix/config.yaml"
	answered.Environment.Hostname = "host1"

	inContainer := doctorReportBase(doctorFail, check("config-file", doctorFail, "fix the file"))
	inContainer.Environment.ConfigFile = "/etc/phenix/config.yaml"
	inContainer.Environment.Container, inContainer.Environment.ContainerHint = true, "/.dockerenv"

	withSettings := doctorReportBase(doctorPass)
	withSettings.Settings = []doctorSetting{
		{Key: "a", Value: nil, Source: sourceDefault},
		{Key: "b", Value: "", Source: sourceEnv},
		{Key: "c", Value: []string{"x", "y"}, Source: sourceFlag},
		{Key: "d", Value: []any{1, "z"}, Source: "config file"},
		{Key: "e", Value: 5, Source: sourceDefault},
	}

	header := func(minimega, config, container string) []string {
		return []string{
			"phenix: 1.0.0 (v1.0.0, commit abc1234, built 2026-10-01)", "minimega: " + minimega,
			"os: linux 6.8.0 (amd64)", "user: alice (uid 1000, gid 1000)", "directory: /home/alice",
			"config: " + config, "container: " + container,
		}
	}

	tests := map[string]struct {
		report doctorReport
		strict bool
		want   []string
	}{
		"minimega skipped, no config file": {
			report: doctorReportBase(doctorPass, check(minimegaCheckName, doctorSkip, "")),
			want: append(header("not checked (--skip)", "none found", "no"),
				"time: 2026-10-30T12:00:00Z", "", "CHECK STATUS DETAIL", "minimega SKIP minimega detail", "",
				"PASS: 0 failed, 0 warned, 0 passed, 1 skipped"),
		},
		"minimega answers, with a hostname": {
			report: answered,
			want: append(header("minimega 3.0.1", "/etc/phenix/config.yaml", "no"),
				"hostname: host1", "time: 2026-10-30T12:00:00Z", "", "CHECK STATUS DETAIL", "minimega PASS minimega detail", "",
				"PASS: 0 failed, 0 warned, 1 passed, 0 skipped"),
		},
		"config does not parse, in a container": {
			report: inContainer,
			want: append(header("unknown (see the minimega check)", "/etc/phenix/config.yaml (does not parse)", "yes (/.dockerenv)"),
				"time: 2026-10-30T12:00:00Z", "", "CHECK STATUS DETAIL", "config-file FAIL config-file detail", "",
				"FAIL: 1 failed, 0 warned, 0 passed, 0 skipped. Failed: config-file", "config-file: fix the file"),
		},
		"failures and warnings": {
			report: doctorReportBase(doctorFail,
				check("a", doctorFail, "fix a"), check("b", doctorWarn, "fix b"), check("c", doctorFail, "fix c"),
				check("d", doctorWarn, "fix d"), check("e", doctorPass, ""), check("f", doctorSkip, "")),
			want: append(header("unknown (see the minimega check)", "none found", "no"),
				"time: 2026-10-30T12:00:00Z", "", "CHECK STATUS DETAIL",
				"a FAIL a detail", "b WARN b detail", "c FAIL c detail", "d WARN d detail", "e PASS e detail", "f SKIP f detail", "",
				"FAIL: 2 failed, 2 warned, 1 passed, 1 skipped. Failed: a, c. Warned: b, d", "a: fix a", "c: fix c"),
		},
		"failures and warnings, strict": {
			report: doctorReportBase(doctorFail, check("a", doctorFail, "fix a"), check("b", doctorWarn, "fix b")),
			strict: true,
			want: append(header("unknown (see the minimega check)", "none found", "no"),
				"time: 2026-10-30T12:00:00Z", "", "CHECK STATUS DETAIL", "a FAIL a detail", "b WARN b detail", "",
				"FAIL: 1 failed, 1 warned, 0 passed, 0 skipped. Failed: a. Warned: b", "a: fix a", "b: fix b"),
		},
		"settings": {
			report: withSettings,
			want: append(header("unknown (see the minimega check)", "none found", "no"),
				"time: 2026-10-30T12:00:00Z", "", "SETTING VALUE SOURCE",
				`a "" default`, `b "" env`, "c x,y flag", "d 1,z config file", "e 5 default", "",
				"CHECK STATUS DETAIL", "", "PASS: 0 failed, 0 warned, 0 passed, 0 skipped"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, doctorReportLines(tc.report, tc.strict)); diff != "" {
				t.Errorf("printDoctorReport() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// With -x the JSON report's settings list is empty, not null; a skipped check
// has its id, status and detail and no hint, and no passing check has a hint.
func TestDoctorJSONNoSettings(t *testing.T) {
	t.Parallel()

	in := fakeDoctorInput(newDoctorTestDir(t), nil)
	opts := doctorOptions{output: FormatJSON, noSettings: true}
	report := runDoctor(t.Context(), in, doctorChecks(), map[string]bool{"env": true, minimegaCheckName: true}, opts)

	var out bytes.Buffer
	if err := writeDoctorReport(&out, report, opts); err != nil {
		t.Fatalf("writeDoctorReport() error = %v", err)
	}

	if !strings.Contains(out.String(), `"settings": []`) {
		t.Errorf("JSON report has no empty settings list:\n%s", out.String())
	}

	var decoded struct {
		Version map[string]string `json:"version"`
		Checks  []map[string]any  `json:"checks"`
	}

	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if decoded.Version["minimega"] != "" {
		t.Errorf("version minimega = %q, want empty with minimega skipped", decoded.Version["minimega"])
	}

	skipped := make(map[string]map[string]any)

	for _, c := range decoded.Checks {
		if _, ok := c["hint"]; ok && c["status"] == string(doctorPass) {
			t.Errorf("passing check %v has a hint", c)
		}

		if c["status"] == string(doctorSkip) {
			skipped[c["id"].(string)] = c //nolint:forcetypeassert // every check has a string id
		}
	}

	want := map[string]map[string]any{
		"env":             {"id": "env", "group": "env", "name": "environment variables", "status": "skip", "detail": "skipped by --skip"},
		minimegaCheckName: {"id": "minimega", "group": "minimega", "name": "minimega", "status": "skip", "detail": "skipped by --skip"},
	}

	for id, c := range want {
		if diff := cmp.Diff(c, skipped[id]); diff != "" {
			t.Errorf("skipped check %s mismatch (-want +got):\n%s", id, diff)
		}
	}
}

// The checks table and summary, in full, with colors off and on: the columns
// line up the same, each status has its color, the summary has the color of
// the worst status, and the hints are faint.
func TestPrintDoctorReportColors(t *testing.T) {
	t.Parallel()

	report := doctorReportBase(doctorFail,
		doctorResult{ID: "minimega", Group: "g", Name: "minimega", Status: doctorFail, Detail: "no answer", Hint: "start minimega"},
		doctorResult{ID: "env", Group: "g", Name: "PHENIX_X", Status: doctorWarn, Detail: "unknown", Hint: "unset it"},
		doctorResult{ID: "disk", Group: "g", Name: "disk", Status: doctorPass, Detail: ""},
		doctorResult{ID: "vmdb2", Group: "g", Name: "vmdb2", Status: doctorSkip, Detail: "skipped by --skip"},
	)

	plain := "\nCHECK     STATUS  DETAIL\n" +
		"minimega  FAIL    no answer\n" +
		"PHENIX_X  WARN    unknown\n" +
		"disk      PASS    \n" +
		"vmdb2     SKIP    skipped by --skip\n" +
		"\nFAIL: 1 failed, 1 warned, 1 passed, 1 skipped. Failed: minimega. Warned: PHENIX_X\n" +
		"  minimega: start minimega\n" +
		"  PHENIX_X: unset it\n"
	colored := "\nCHECK     STATUS  DETAIL\n" +
		"minimega  \x1b[1;31mFAIL\x1b[22;0m    no answer\n" +
		"PHENIX_X  \x1b[33mWARN\x1b[0m    unknown\n" +
		"disk      \x1b[32mPASS\x1b[0m    \n" +
		"vmdb2     \x1b[2mSKIP\x1b[22m    skipped by --skip\n" +
		"\n\x1b[1;31mFAIL: 1 failed, 1 warned, 1 passed, 1 skipped. Failed: minimega. Warned: PHENIX_X\x1b[22;0m\n" +
		"  minimega: \x1b[2mstart minimega\x1b[22m\n" +
		"  PHENIX_X: \x1b[2munset it\x1b[22m\n"

	for name, tc := range map[string]struct {
		palette termcolor.Palette
		want    string
	}{
		"off": {palette: termcolor.New(false), want: plain},
		"on":  {palette: termcolor.New(true), want: colored},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			printDoctorReport(&out, report, true, tc.palette)

			_, checks, _ := strings.Cut(out.String(), "2026-10-30T12:00:00Z\n")
			if diff := cmp.Diff(tc.want, checks); diff != "" {
				t.Errorf("printDoctorReport() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The checks table aligns its columns as tabwriter did before it had colors,
// and without its codes a colored report is the plain report.
func TestPrintDoctorChecksAlignment(t *testing.T) {
	t.Parallel()

	statuses := []doctorStatus{doctorPass, doctorWarn, doctorFail, doctorSkip}

	rapid.Check(t, func(t *rapid.T) {
		names := rapid.SliceOf(rapid.StringMatching(`[a-zé.-]{1,24}`)).Draw(t, "names")

		checks := make([]doctorResult, 0, len(names))

		for i, name := range names {
			status := rapid.SampledFrom(statuses).Draw(t, "status")
			checks = append(checks, doctorResult{
				ID: name, Group: "g", Name: name, Status: status,
				Detail: rapid.StringMatching(`[a-z ]{0,12}`).Draw(t, "detail"), Hint: strconv.Itoa(i),
			})
		}

		var want, plain, colored bytes.Buffer

		tw := tabwriter.NewWriter(&want, 0, 0, doctorColumnPadding, ' ', 0)
		_, _ = fmt.Fprintln(tw, "CHECK\tSTATUS\tDETAIL")

		for _, c := range checks {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", c.Name, strings.ToUpper(string(c.Status)), c.Detail)
		}

		_ = tw.Flush()

		printDoctorChecks(&plain, checks, termcolor.New(false))
		printDoctorChecks(&colored, checks, termcolor.New(true))

		if diff := cmp.Diff(want.String(), plain.String()); diff != "" {
			t.Fatalf("plain table differs from tabwriter's (-want +got):\n%s", diff)
		}

		if diff := cmp.Diff(plain.String(), stripColor(colored.String())); diff != "" {
			t.Fatalf("colored table without its codes differs (-plain +stripped):\n%s", diff)
		}
	})
}

// -o json and --report never color, even when --color asks for it.
func TestWriteDoctorReportColors(t *testing.T) {
	t.Parallel()

	report := doctorReportBase(doctorWarn, doctorResult{ID: "a", Group: "g", Name: "a", Status: doctorWarn, Detail: "d", Hint: "h"})

	tests := map[string]struct {
		opts      doctorOptions
		wantColor bool
	}{
		"text":            {opts: doctorOptions{output: "text", palette: termcolor.New(true)}, wantColor: true},
		"text, report":    {opts: doctorOptions{output: "text", report: true, palette: termcolor.New(true)}},
		"json":            {opts: doctorOptions{output: FormatJSON, palette: termcolor.New(true)}},
		"json, report":    {opts: doctorOptions{output: FormatJSON, report: true, palette: termcolor.New(true)}},
		"text, colorless": {opts: doctorOptions{output: "text", palette: termcolor.New(false)}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			if err := writeDoctorReport(&out, report, tc.opts); err != nil {
				t.Fatalf("writeDoctorReport() error = %v", err)
			}

			if got := strings.Contains(out.String(), "\x1b["); got != tc.wantColor {
				t.Errorf("output has escape codes = %v, want %v:\n%q", got, tc.wantColor, out.String())
			}
		})
	}
}

// phenix doctor colors its text output only when --color says so: auto
// leaves a writer that is not a terminal plain, and -o json is never colored.
func TestDoctorCommandColors(t *testing.T) { //nolint:paralleltest // sets environment variables
	t.Setenv("PHENIX_BASE_DIR_MINIMEGA", t.TempDir())
	t.Setenv("PHENIX_STORE_ENDPOINT", "bolt://"+filepath.Join(t.TempDir(), "store.bdb"))
	t.Setenv("PHENIX_UNIX_SOCKET", filepath.Join(t.TempDir(), "phenix.sock"))

	tests := map[string]struct {
		color     string
		args      []string
		wantColor bool
	}{
		"auto":     {color: "auto"},
		"off":      {color: "off"},
		"on":       {color: "on", wantColor: true},
		"on, json": {color: "on", args: []string{"-o", "json"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PHENIX_COLOR", tc.color)

			cmd := newDoctorCmd()
			cmd.SilenceUsage, cmd.SilenceErrors = true, true // as under the root command

			var out bytes.Buffer

			cmd.SetOut(&out)
			cmd.SetArgs(append([]string{"--skip", "minimega,server", "-x"}, tc.args...))
			cmd.SetContext(t.Context())

			_ = cmd.Execute()

			if got := strings.Contains(out.String(), "\x1b["); got != tc.wantColor {
				t.Errorf("doctor output has escape codes = %v, want %v:\n%q", got, tc.wantColor, out.String())
			}
		})
	}
}
