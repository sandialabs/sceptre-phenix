package cmd

import (
	"context"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"phenix/api/experiment"
	"phenix/app"
)

// The completion functions below follow the rules of complete.go.

// completeConfigNamesOrFiles completes the names of the stored configs of
// kind. When no name matches, the shell completes file paths, because the
// flag also takes the path of a config file.
func completeConfigNamesOrFiles(kind string) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if names := completionMatches(configNamesOf(kind), toComplete); len(names) > 0 {
			return names, cobra.ShellCompDirectiveNoFileComp
		}

		return nil, cobra.ShellCompDirectiveDefault
	}
}

// completeAppNames completes the last item of a comma-separated list of app
// names: the default apps and the other apps that phenix knows.
func completeAppNames(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	var (
		cut     = strings.LastIndex(toComplete, ",") + 1
		prefix  = toComplete[:cut]
		given   = strings.Split(prefix, ",")
		matches []string
	)

	for _, name := range completionMatches(append(app.DefaultApps(), app.List()...), toComplete[cut:]) {
		if !slices.Contains(given, name) {
			matches = append(matches, prefix+name)
		}
	}

	return matches, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}

// completeExperimentFiles completes the files of the experiment in the first
// argument, as phenix experiment files list shows them.
func completeExperimentFiles(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) < 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
	defer cancel()

	files, _, err := experiment.FilesObserved(ctx, args[0], "")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	paths := make([]string, 0, len(files))

	for _, f := range files {
		if !slices.Contains(args[1:], f.Path) {
			paths = append(paths, f.Path)
		}
	}

	slices.Sort(paths)

	return completionMatches(paths, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeNetflowCaptureIDs completes the IDs of the recorded netflow
// captures of the experiment in the first argument, which phenix ui keeps.
func completeNetflowCaptureIDs(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) < 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
	defer cancel()

	captures, err := listNetflowCaptures(ctx, args[0])
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var ids []string

	for _, c := range captures {
		if strings.HasPrefix(c.ID, toComplete) {
			ids = append(ids, cobra.CompletionWithDesc(c.ID, c.State))
		}
	}

	return ids, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
}

// completeDoctorChecks completes the last item of a comma-separated list of
// phenix doctor checks and groups of checks.
func completeDoctorChecks(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	var (
		cut    = strings.LastIndex(toComplete, ",") + 1
		prefix = toComplete[:cut]
		defs   = doctorChecks()
		names  = make([]string, 0, len(defs))
	)

	for _, def := range defs {
		names = append(names, def.id)
	}

	for _, def := range defs {
		names = append(names, def.group)
	}

	slices.Sort(names)

	matches := completionMatches(names, toComplete[cut:])
	for i := range matches {
		matches[i] = prefix + matches[i]
	}

	return matches, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}
