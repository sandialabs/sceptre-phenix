package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"phenix/util/termcolor"
	"phenix/version"
)

// doctorColumnPadding is the number of spaces between the columns of a table.
const doctorColumnPadding = 2

// containerMarkers are files a container runtime creates in the container.
//
//nolint:gochecknoglobals // constant list
var containerMarkers = []string{"/.dockerenv", "/run/.containerenv"}

// cgroupContainerHints are words in /proc/1/cgroup that name a container runtime.
//
//nolint:gochecknoglobals // constant list
var cgroupContainerHints = []string{"docker", "kubepods", "containerd", "libpod", "lxc"}

// newDoctorReport builds the report of results: the header from in, and the
// settings, secrets redacted, unless opts leaves them out. With --report, it
// also replaces the host's names, addresses and users in every string.
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
		},
		Checks:       results,
		HiddenChecks: 0,
		Settings:     []doctorSetting{},
	}

	report.Environment.Container = report.Environment.ContainerHint != ""

	for _, r := range results {
		if r.ID == minimegaCheckName && r.Status == doctorPass {
			report.Version.Minimega = r.Detail
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

	if opts.report {
		report = newDoctorRedactor(in).report(report)
	}

	return report
}

// doctorProblems returns checks without the rows that pass or are skipped,
// and the number of rows it left out.
func doctorProblems(checks []doctorResult) ([]doctorResult, int) {
	problems := make([]doctorResult, 0, len(checks))

	for _, c := range checks {
		if c.Status == doctorWarn || c.Status == doctorFail {
			problems = append(problems, c)
		}
	}

	return problems, len(checks) - len(problems)
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

// writeDoctorReport writes report as opts asks: text or JSON, with -p only
// the problem rows, and with --report in a fenced block. Only text without
// --report gets colors.
func writeDoctorReport(out io.Writer, report doctorReport, opts doctorOptions) error {
	var buf bytes.Buffer

	if opts.output == FormatJSON {
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false) // keep tokens such as <host-1> readable

		if opts.problems {
			report.Checks, report.HiddenChecks = doctorProblems(report.Checks)
		}

		if err := enc.Encode(report); err != nil {
			return fmt.Errorf("encoding report: %w", err)
		}
	} else {
		palette := opts.palette
		if opts.report {
			palette = termcolor.New(false)
		}

		printDoctorReport(&buf, report, doctorView{strict: opts.strict, problems: opts.problems, palette: palette})
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

// doctorView is how the text output shows a report.
type doctorView struct {
	strict   bool              // give the hints of warnings too
	problems bool              // show only the rows that warn or fail
	palette  termcolor.Palette // colors the statuses, the summary and the hints
}

// printDoctorReport prints the header, the settings (when the report has any),
// the checks and a summary, as view says. The summary counts every check,
// also those view hides.
func printDoctorReport(out io.Writer, report doctorReport, view doctorView) {
	palette := view.palette

	w := tabwriter.NewWriter(out, 0, 0, doctorColumnPadding, ' ', 0)

	printDoctorHeader(w, report)

	if len(report.Settings) > 0 {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "SETTING\tVALUE\tSOURCE")

		for _, s := range report.Settings {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", s.Key, formatSettingValue(s.Value), s.Source)
		}
	}

	_ = w.Flush()

	checks, hidden := report.Checks, 0
	if view.problems {
		checks, hidden = doctorProblems(report.Checks)
	}

	_, _ = fmt.Fprintln(out)
	printDoctorChecks(out, checks, palette)

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, doctorStatusColor(palette, report.Status, doctorSummary(report, hidden)))

	for _, c := range report.Checks {
		if c.Hint != "" && (c.Status == doctorFail || (view.strict && c.Status == doctorWarn)) {
			_, _ = fmt.Fprintf(out, "  %s: %s\n", c.Name, palette.Faint(c.Hint))
		}
	}
}

// printDoctorChecks prints the checks table, aligned as tabwriter aligns the
// other tables. It pads each cell before it colors the status, since tabwriter
// counts escape codes as text.
func printDoctorChecks(out io.Writer, checks []doctorResult, palette termcolor.Palette) {
	const statusHeader = "STATUS"

	nameWidth := utf8.RuneCountInString("CHECK")
	for _, c := range checks {
		nameWidth = max(nameWidth, utf8.RuneCountInString(c.Name))
	}

	pad := func(text string, width int) string {
		return strings.Repeat(" ", width+doctorColumnPadding-utf8.RuneCountInString(text))
	}

	_, _ = fmt.Fprintf(out, "CHECK%s%s%sDETAIL\n", pad("CHECK", nameWidth), statusHeader, pad(statusHeader, len(statusHeader)))

	for _, c := range checks {
		status := strings.ToUpper(string(c.Status))

		_, _ = fmt.Fprintf(out, "%s%s%s%s%s\n",
			c.Name, pad(c.Name, nameWidth),
			doctorStatusColor(palette, c.Status, status), pad(status, len(statusHeader)),
			c.Detail,
		)
	}
}

// doctorStatusColor colors text as palette colors status: pass green, warn
// yellow, fail bold red and skip faint.
func doctorStatusColor(palette termcolor.Palette, status doctorStatus, text string) string {
	switch status {
	case doctorPass:
		return palette.Green(text)
	case doctorWarn:
		return palette.Yellow(text)
	case doctorFail:
		return palette.BoldRed(text)
	case doctorSkip:
		return palette.Faint(text)
	}

	return text
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
		{"time", report.Time},
	}

	for _, row := range rows {
		_, _ = fmt.Fprintf(w, "%s:\t%s\n", row[0], row[1])
	}
}

// doctorSummary is one line: the overall status, a count of each status,
// the names of the checks that failed and warned, and the number of hidden checks.
func doctorSummary(report doctorReport, hidden int) string {
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

	switch {
	case hidden == 1:
		line += ". 1 passing or skipped check hidden"
	case hidden > 1:
		line += fmt.Sprintf(". %d passing or skipped checks hidden", hidden)
	}

	return line
}
