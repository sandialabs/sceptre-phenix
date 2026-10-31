package cmd

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/util/common"
)

// settingIDs are the checks of the settings every phenix command reads.
//
//nolint:gochecknoglobals // constant list
var settingIDs = []string{
	"bridge-mode", "deploy-mode", "experiment.disk-check", "log.level", "log.system.max-size", "log.system.max-backups",
	"log.system.max-age",
}

// settingRows runs the checks of settingIDs against in.
func settingRows(t interface {
	Helper()
	Fatalf(format string, args ...any)
}, in *doctorInput,
) map[string]doctorCheck {
	t.Helper()

	rows := make(map[string]doctorCheck)

	for _, def := range doctorChecks() {
		for _, id := range settingIDs {
			if def.id != id {
				continue
			}

			if def.group != doctorGroupSettings {
				t.Fatalf("check %s is in group %s, want %s", id, def.group, doctorGroupSettings)
			}

			checks := def.run(context.Background(), in)
			if len(checks) != 1 {
				t.Fatalf("check %s gave %d rows, want 1", id, len(checks))
			}

			rows[id] = checks[0]
		}
	}

	return rows
}

// The settings every command reads are checked whatever sets them: a mode
// phenix refuses and a negative log size fail, a log level plog ignores and
// a log number that reads as 0 warn, and an unset mode passes.
func TestSettingChecks(t *testing.T) {
	t.Parallel()

	dir := newDoctorTestDir(t)

	tests := map[string]struct {
		settings map[string]string
		want     map[string]doctorCheck
	}{
		"defaults": {
			settings: map[string]string{},
			want: map[string]doctorCheck{
				"bridge-mode":            passCheck("manual"),
				"deploy-mode":            passCheck("not set, so phenix uses the mode of phenix ui, or no-headnode"),
				"experiment.disk-check":  passCheck("error"),
				"log.level":              passCheck("info"),
				"log.system.max-size":    passCheck("100"),
				"log.system.max-backups": passCheck("3"),
				"log.system.max-age":     passCheck("90"),
			},
		},
		"bad values": {
			settings: map[string]string{
				"bridge-mode": "", "deploy-mode": "Only-Headnode", "experiment.disk-check": "WARN", "log.level": "loud",
				"log.system.max-size": "-5", "log.system.max-backups": "five", "log.system.max-age": "0",
			},
			want: map[string]doctorCheck{
				"bridge-mode":           passCheck("not set, so phenix uses manual, or auto when phenix ui runs in auto"),
				"deploy-mode":           passCheck("only-headnode"),
				"experiment.disk-check": passCheck("warn"),
				"log.level": warnCheck(
					`"loud" is not a log level (debug, info, warn or error), so phenix ignores it and logs at info`),
				"log.system.max-size":    failCheck("-5 is negative, so every write to the system log fails"),
				"log.system.max-backups": warnCheck(`"five" is not a whole number, so phenix reads it as 0 (no limit)`),
				"log.system.max-age":     passCheck("0 (no limit)"),
			},
		},
		"refused modes": {
			settings: map[string]string{"bridge-mode": "sideways", "deploy-mode": "some", "experiment.disk-check": "loose"},
			want: map[string]doctorCheck{
				"bridge-mode": failCheck(
					"unknown bridge mode provided: sideways, so phenix commands refuse it (options: manual | auto)"),
				"deploy-mode": failCheck(
					"unknown deploy mode provided: some, so phenix commands refuse it (options: all | no-headnode | only-headnode)"),
				"experiment.disk-check": failCheck(
					"unknown disk check mode provided: loose, so phenix commands refuse it (options: error | warn | off)"),
				"log.level":              passCheck("info"),
				"log.system.max-size":    passCheck("100"),
				"log.system.max-backups": passCheck("3"),
				"log.system.max-age":     passCheck("90"),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := settingRows(t, fakeDoctorInput(dir, tc.settings))
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("setting checks mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Each setting check fails exactly the values the root command refuses or
// that break the system log, and warns about a log level plog ignores.
func TestSettingChecksMatchConsumers(t *testing.T) {
	t.Parallel()

	dir := newDoctorTestDir(t)

	rapid.Check(t, func(t *rapid.T) {
		value := rapid.OneOf(
			rapid.SampledFrom([]string{
				"", "manual", "AUTO", "all", "no-headnode", "none", "debug", "WARN", "error+2", "Off", "error",
			}),
			rapid.StringMatching(`[-+]?(0x)?[0-9a-z]{0,6}`),
			rapid.String(),
		).Draw(t, "value")

		settings := make(map[string]string)
		for _, id := range settingIDs {
			settings[id] = value
		}

		rows := settingRows(t, fakeDoctorInput(dir, settings))

		_, bridgeErr := common.ParseBridgeMode(value)
		if got := rows["bridge-mode"].Status == doctorFail; got != (bridgeErr != nil) {
			t.Fatalf("bridge-mode %q: check %+v, ParseBridgeMode error %v", value, rows["bridge-mode"], bridgeErr)
		}

		_, deployErr := common.ParseDeployMode(value)
		if got := rows["deploy-mode"].Status == doctorFail; got != (deployErr != nil) {
			t.Fatalf("deploy-mode %q: check %+v, ParseDeployMode error %v", value, rows["deploy-mode"], deployErr)
		}

		_, diskErr := common.ParseDiskCheck(value)
		if got := rows["experiment.disk-check"].Status == doctorFail; got != (diskErr != nil) {
			t.Fatalf("experiment.disk-check %q: check %+v, ParseDiskCheck error %v", value, rows["experiment.disk-check"], diskErr)
		}

		var level slog.Level

		ignored := !strings.EqualFold(value, "none") && level.UnmarshalText([]byte(value)) != nil
		if got := rows["log.level"].Status == doctorWarn; got != ignored {
			t.Fatalf("log.level %q: check %+v, plog ignores it %t", value, rows["log.level"], ignored)
		}

		size := viperWith("k", value).GetInt("k")
		if got := rows["log.system.max-size"].Status == doctorFail; got != (size < 0) {
			t.Fatalf("log.system.max-size %q: check %+v, viper reads %d", value, rows["log.system.max-size"], size)
		}

		for _, id := range []string{"log.system.max-backups", "log.system.max-age"} {
			if rows[id].Status == doctorFail {
				t.Fatalf("%s %q: check %+v, want no failure", id, value, rows[id])
			}
		}
	})
}
