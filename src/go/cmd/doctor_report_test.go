package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// doctorReportLines is the text output of report, each line with its runs of
// spaces made one, so the tests do not depend on column widths.
func doctorReportLines(report doctorReport, strict bool) []string {
	var out bytes.Buffer

	printDoctorReport(&out, report, strict)

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
