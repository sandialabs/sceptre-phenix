package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"phenix/version"
)

// containerMarkers are files a container runtime creates in the container.
//
//nolint:gochecknoglobals // constant list
var containerMarkers = []string{"/.dockerenv", "/run/.containerenv"}

// cgroupContainerHints are words in /proc/1/cgroup that name a container runtime.
//
//nolint:gochecknoglobals // constant list
var cgroupContainerHints = []string{"docker", "kubepods", "containerd", "libpod", "lxc"}

// newDoctorReport builds the report of results: the header from in, and the
// settings, secrets redacted, unless opts leaves them out.
func newDoctorReport(in *doctorInput, results []doctorResult, opts doctorOptions) doctorReport {
	report := doctorReport{
		SchemaVersion: doctorSchemaVersion,
		Status:        worstStatus(results),
		Time:          in.now().UTC().Format(time.RFC3339),
		Version: doctorVersion{
			Phenix:   version.API(),
			Label:    version.Label(),
			Commit:   version.Commit,
			Date:     version.Date,
			Minimega: "",
		},
		Environment: doctorEnvironment{
			OS:            in.system.os,
			Kernel:        in.system.kernel,
			Arch:          in.system.arch,
			UID:           in.uid,
			GID:           in.gid,
			User:          in.userName,
			WorkingDir:    in.cwd,
			ConfigFile:    in.config.used,
			Container:     false,
			ContainerHint: containerHint(in.env),
			Hostname:      "",
		},
		Checks:   results,
		Settings: []doctorSetting{},
	}

	report.Environment.Container = report.Environment.ContainerHint != ""

	for _, r := range results {
		if r.ID == minimegaCheckName && r.Status == doctorPass {
			report.Version.Minimega = r.Detail
		}
	}

	if opts.report {
		report.Environment.Hostname = "unknown"
		if name, err := in.hostname(); err == nil {
			report.Environment.Hostname = name
		}
	}

	if !opts.noSettings {
		for _, key := range in.known {
			s := in.settings[key]
			report.Settings = append(report.Settings, doctorSetting{
				Key: key, Value: redactSetting(key, s.value), Source: s.source,
			})
		}
	}

	return report
}

// containerHint names what shows doctor runs in a container, or is "" when
// nothing does.
func containerHint(env doctorEnv) string {
	for _, marker := range containerMarkers {
		if _, err := env.stat(marker); err == nil {
			return marker
		}
	}

	cgroup, err := env.readFile("/proc/1/cgroup")
	if err != nil {
		return ""
	}

	for _, hint := range cgroupContainerHints {
		if bytes.Contains(cgroup, []byte(hint)) {
			return "cgroup " + hint
		}
	}

	return ""
}

// writeDoctorReport writes report as opts asks: text or JSON, and with
// --report in a fenced block.
func writeDoctorReport(out io.Writer, report doctorReport, opts doctorOptions) error {
	var buf bytes.Buffer

	if opts.output == FormatJSON {
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")

		if err := enc.Encode(report); err != nil {
			return fmt.Errorf("encoding report: %w", err)
		}
	} else {
		printDoctorReport(&buf, report, opts.strict)
	}

	if opts.report {
		lang := "text"
		if opts.output == FormatJSON {
			lang = FormatJSON
		}

		_, _ = fmt.Fprintf(out, "```%s\n%s```\n", lang, buf.String())

		return nil
	}

	_, _ = buf.WriteTo(out)

	return nil
}

// printDoctorReport prints the header, the settings (when the report has any),
// the checks and a summary; with strict, the summary gives warnings' hints too.
func printDoctorReport(out io.Writer, report doctorReport, strict bool) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0) //nolint:mnd // column padding

	printDoctorHeader(w, report)

	if len(report.Settings) > 0 {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "SETTING\tVALUE\tSOURCE")

		for _, s := range report.Settings {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", s.Key, formatSettingValue(s.Value), s.Source)
		}
	}

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "CHECK\tSTATUS\tDETAIL")

	for _, c := range report.Checks {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", c.Name, strings.ToUpper(string(c.Status)), c.Detail)
	}

	_ = w.Flush()

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, doctorSummary(report))

	for _, c := range report.Checks {
		if c.Hint != "" && (c.Status == doctorFail || (strict && c.Status == doctorWarn)) {
			_, _ = fmt.Fprintf(out, "  %s: %s\n", c.Name, c.Hint)
		}
	}
}

func printDoctorHeader(w io.Writer, report doctorReport) {
	v, env := report.Version, report.Environment

	minimega := v.Minimega
	if minimega == "" {
		minimega = "unknown (see the minimega check)"
	}

	config := env.ConfigFile
	if config == "" {
		config = "none found"
	}

	for _, c := range report.Checks {
		switch {
		case c.ID == minimegaCheckName && c.Status == doctorSkip:
			minimega = "not checked (--skip)"
		case c.ID == "config-file" && c.Status == doctorFail:
			config += " (does not parse)"
		}
	}

	container := "no"
	if env.Container {
		container = "yes (" + env.ContainerHint + ")"
	}

	rows := [][2]string{
		{"phenix", fmt.Sprintf("%s (%s, commit %s, built %s)", v.Phenix, v.Label, v.Commit, v.Date)},
		{"minimega", minimega},
		{"os", fmt.Sprintf("%s %s (%s)", env.OS, env.Kernel, env.Arch)},
		{"user", fmt.Sprintf("%s (uid %d, gid %d)", env.User, env.UID, env.GID)},
		{"directory", env.WorkingDir},
		{"config", config},
		{"container", container},
	}

	if env.Hostname != "" {
		rows = append(rows, [2]string{"hostname", env.Hostname})
	}

	rows = append(rows, [2]string{"time", report.Time})

	for _, row := range rows {
		_, _ = fmt.Fprintf(w, "%s:\t%s\n", row[0], row[1])
	}
}

// doctorSummary is one line: the overall status, a count of each status,
// and the names of the checks that failed and warned.
func doctorSummary(report doctorReport) string {
	var (
		counts         = make(map[doctorStatus]int)
		failed, warned []string
	)

	for _, c := range report.Checks {
		counts[c.Status]++

		switch c.Status {
		case doctorFail:
			failed = append(failed, c.Name)
		case doctorWarn:
			warned = append(warned, c.Name)
		case doctorPass, doctorSkip:
		}
	}

	line := fmt.Sprintf("%s: %d failed, %d warned, %d passed, %d skipped",
		strings.ToUpper(string(report.Status)),
		counts[doctorFail], counts[doctorWarn], counts[doctorPass], counts[doctorSkip],
	)

	if len(failed) > 0 {
		line += ". Failed: " + strings.Join(failed, ", ")
	}

	if len(warned) > 0 {
		line += ". Warned: " + strings.Join(warned, ", ")
	}

	return line
}
