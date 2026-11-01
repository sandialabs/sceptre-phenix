package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"phenix/store"
	"phenix/web"
	"phenix/web/health"
)

const (
	// The executables phenix runs for user apps and SCORCH components.
	userAppPrefix         = "phenix-app-"
	scorchComponentPrefix = "phenix-scorch-component-"

	diskRowPrefix = "disk "
)

// autoFeatures are the ui.features phenix ui turns on by itself.
//
//nolint:gochecknoglobals // constant list
var autoFeatures = []string{web.FeatureWebShark, web.FeatureTunnelerDownload, web.FeatureFileServer}

// qemuImgCheck reports qemu-img's version as the readiness check does, but
// fails without it: phenix commits VMs to disk images with it.
func qemuImgCheck(
	ctx context.Context,
	timeout time.Duration,
	lookPath func(string) (string, error),
	version func(context.Context, string) (string, error),
) doctorCheck {
	if _, err := lookPath("qemu-img"); err != nil {
		return failCheck("qemu-img is not a program on PATH, so phenix cannot commit VMs to disk images")
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result := health.QemuImg(lookPath, version).Run(ctx)[health.KeyQemuImg][0]
	if result.Status == health.Pass {
		return passCheck(result.Output)
	}

	return failCheck(result.Output + " (phenix commits VMs to disk images with it)")
}

// toolCheck checks that name is a program on PATH; missing is the status and
// without what phenix cannot do when it is not.
func toolCheck(lookPath func(string) (string, error), name string, missing doctorStatus, without string) doctorCheck {
	path, err := lookPath(name)
	if err != nil {
		return doctorCheck{Name: "", Status: missing, Detail: name + " is not a program on PATH, so " + without}
	}

	return passCheck(path)
}

// appsCheck lists the user apps and SCORCH components on PATH, by the name a
// config gives them, each the first executable of its name.
func (e doctorEnv) appsCheck() doctorCheck {
	path, _ := e.lookup("PATH")

	var (
		apps, components []string
		seen             = make(map[string]bool)
	)

	for _, dir := range filepath.SplitList(path) {
		entries, err := e.readDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			name := entry.Name()
			if seen[name] {
				continue
			}

			seen[name] = true

			app, isApp := strings.CutPrefix(name, userAppPrefix)
			component, isComponent := strings.CutPrefix(name, scorchComponentPrefix)

			if !isApp && !isComponent {
				continue
			}

			if _, err := e.lookPath(name); err != nil {
				continue
			}

			if isApp {
				apps = append(apps, app)
			} else {
				components = append(components, component)
			}
		}
	}

	if len(apps) == 0 && len(components) == 0 {
		return passCheck(fmt.Sprintf("no %s* or %s* programs on PATH", userAppPrefix, scorchComponentPrefix))
	}

	list := func(names []string) string {
		if len(names) == 0 {
			return "none"
		}

		slices.Sort(names)

		return strings.Join(names, ", ")
	}

	return passCheck(fmt.Sprintf("apps: %s. SCORCH components: %s", list(apps), list(components)))
}

// diskChecks measures, as the readiness check does, the filesystems of
// base-dir.phenix, the store and minimega's files directory, when found.
func diskChecks(ctx context.Context, in *doctorInput) []doctorCheck {
	candidates := []health.DiskPath{{ID: "phenix-base", Path: in.text("base-dir.phenix")}}

	if path, err := store.BoltPath(in.text("store.endpoint")); err == nil && path != "" {
		candidates = append(candidates, health.DiskPath{ID: "store", Path: filepath.Dir(path)})
	}

	if files := in.found.minimegaArgs["filepath"]; filepath.IsAbs(files) {
		candidates = append(candidates, health.DiskPath{ID: "files-dir", Path: files})
	}

	// the dirs and files checks report a directory that is not there
	paths := slices.DeleteFunc(candidates, func(p health.DiskPath) bool {
		info, err := in.env.stat(p.Path)

		return err != nil || !info.IsDir()
	})

	if len(paths) == 0 {
		return []doctorCheck{passCheck("no directory to measure")}
	}

	disk := health.Disk(func() []health.DiskPath { return paths }, in.statfs)

	ctx, cancel := context.WithTimeout(ctx, in.timeout)
	defer cancel()

	results := disk.Run(ctx)[health.KeyDisk]
	checks := make([]doctorCheck, 0, len(results))

	for _, r := range results {
		detail := r.Output
		if used, ok := r.ObservedValue.(health.Float); ok {
			detail = fmt.Sprintf("%.1f%% used, %s", float64(used), r.Output)
		}

		status := doctorPass

		switch r.Status {
		case health.Warn:
			status = doctorWarn
		case health.Fail:
			status = doctorFail
		case health.Pass:
		}

		checks = append(checks, doctorCheck{Name: diskRowPrefix + r.ComponentID, Status: status, Detail: detail})
	}

	return checks
}

// relativeToCwd names path, relative to the working directory cwd when it is relative.
func relativeToCwd(path, cwd string) string {
	if filepath.IsAbs(path) {
		return path
	}

	return fmt.Sprintf("%s (under %s)", path, cwd)
}

// tunnelerDownloadCheck reports whether phenix ui, started in cwd, offers
// tunneler downloads: it does when the downloads directory is there.
func (e doctorEnv) tunnelerDownloadCheck(cwd string) doctorCheck {
	dir := web.TunnelerDownloadDir

	info, err := e.stat(dir)

	switch {
	case err != nil:
		return passCheck(fmt.Sprintf("off: no %s, and phenix ui looks in the directory it starts in", relativeToCwd(dir, cwd)))
	case !info.IsDir():
		return warnCheck(fmt.Sprintf("on, but %s is not a directory, so listing tunneler downloads fails", relativeToCwd(dir, cwd)))
	}

	return passCheck("on: " + filepath.Join(cwd, dir))
}

// webSharkCheck reports whether phenix ui, started in cwd, serves WebShark: it
// does when one of its directories holds a build.
func (e doctorEnv) webSharkCheck(cwd string) doctorCheck {
	if dir, ok := web.FindWebShark(e.stat); ok {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}

		return passCheck("on: the build in " + dir)
	}

	return passCheck("off: no index.html in " + webSharkDirsText(cwd))
}

func webSharkDirsText(cwd string) string {
	dirs := web.WebSharkDirs()
	for i, dir := range dirs {
		dirs[i] = relativeToCwd(dir, cwd)
	}

	return strings.Join(dirs, " or ")
}

// featuresSettingCheck checks ui.features as phenix ui reads it: unknown names
// warn, and a feature phenix ui turns on by itself warns, set by hand without
// what it needs, or fails when phenix ui then refuses to start.
func (e doctorEnv) featuresSettingCheck(features []string, cwd, fileServer string) doctorCheck {
	if len(features) == 0 {
		return passCheck("none set")
	}

	var (
		problems []string
		status   = doctorPass
	)

	problem := func(s doctorStatus, detail string) {
		problems = append(problems, detail)
		if s == doctorFail || status == doctorPass {
			status = s
		}
	}

	for _, feature := range features {
		switch feature {
		case web.FeatureVMMount:
		case web.FeatureWebShark:
			if _, ok := web.FindWebShark(e.stat); !ok {
				problem(doctorFail, fmt.Sprintf("webshark is set by hand without a WebShark build in %s, so phenix ui refuses to start",
					webSharkDirsText(cwd)))
			}
		case web.FeatureTunnelerDownload:
			if _, err := e.stat(web.TunnelerDownloadDir); err != nil {
				problem(doctorWarn, fmt.Sprintf(
					"tunneler-download is set by hand without %s, so the download menu lists no tunnelers",
					relativeToCwd(web.TunnelerDownloadDir, cwd),
				))
			}
		case web.FeatureFileServer:
			if endpoint, err := web.NormalizeFileServerEndpoint(fileServer); err == nil && endpoint == "" {
				problem(doctorWarn, "file-server is set by hand without ui.file-server-endpoint, so no file server runs")
			}
		default:
			problem(doctorWarn, unknownFeatureDetail(feature))
		}
	}

	if len(problems) == 0 {
		return passCheck(strings.Join(features, " "))
	}

	return doctorCheck{Name: "", Status: status, Detail: strings.Join(problems, ". ")}
}

// unknownFeatureDetail explains a ui.features name phenix ui does not know,
// and suggests the one it may misspell.
func unknownFeatureDetail(feature string) string {
	detail := fmt.Sprintf("unknown feature %q", feature)

	best, bestDistance := "", maxEnvSuggestDistance+1

	for _, known := range append([]string{web.FeatureVMMount}, autoFeatures...) {
		if d := editDistance(feature, known); d < bestDistance {
			best, bestDistance = known, d
		}
	}

	if best != "" {
		detail += " (did you mean " + best + "?)"
	}

	if strings.Contains(feature, ",") {
		detail += ". Separate features with spaces, or list them in YAML"
	}

	return detail
}

// featuresHint is what to do about ui.features.
func featuresHint(c doctorCheck) string {
	if strings.Contains(c.Detail, "unknown feature") {
		return "remove or fix the unknown features (phenix ui knows vm-mount, " + strings.Join(autoFeatures, ", ") + ")"
	}

	return "keep only vm-mount in ui.features, because phenix ui enables " + strings.Join(autoFeatures, ", ") +
		" by itself when it finds what each needs"
}

// minimegaLogCheck checks ui.logs.minimega-path, the minimega log phenix ui
// tails, which waits without a warning for a file that is not there.
// minimegaLog is the file minimega logs to, or "" when not known.
func (e doctorEnv) minimegaLogCheck(path, minimegaLog string) doctorCheck {
	if path != "" {
		check := e.readableFileCheck(path, doctorWarn)
		if check.Status == doctorWarn {
			check.Detail += ", so phenix ui waits for it without a warning, and the Logs page shows no minimega logs"
		}

		return check
	}

	source := "its -logfile"
	if minimegaLog == "" {
		minimegaLog, _ = e.lookup("MM_LOGFILE")
		source = "MM_LOGFILE"
	}

	if minimegaLog != "" {
		return warnCheck(fmt.Sprintf("not set, so the Logs page shows no minimega logs, but minimega logs to %s (%s)",
			minimegaLog, source))
	}

	return passCheck("not set, so the Logs page shows no minimega logs")
}
