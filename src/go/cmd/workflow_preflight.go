package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"phenix/api/workflow"
	"phenix/util/plog"
)

// newApplyRun resolves the topology directory, the workflow name and the
// workflow config, and finds which inputs the directory holds. It contacts
// nothing.
func newApplyRun(opts applyOptions, arg string) (*applyRun, error) {
	started := opts.Now()

	dir, err := resolveTopologyDir(arg, opts.TopologiesBase)
	if err != nil {
		return nil, err
	}

	name := opts.Name
	if name == "" {
		name = filepath.Base(dir)
	}

	if err := workflow.ValidateName(name); err != nil {
		return nil, fmt.Errorf("choose a valid workflow name with -b: %w", err)
	}

	// Staged under such a name, the injects would make their base look like
	// a topology directory.
	if isMarkerName(name) {
		return nil, fmt.Errorf("the name %q is reserved; pass another with -b", name)
	}

	wfConfig, err := resolveWorkflowConfig(dir, opts.Config, opts.ConfigExplicit)
	if err != nil {
		return nil, err
	}

	if wfConfig == "" {
		warnIgnoredWorkflowConfigs(dir)
	}

	// The walk of the configs would not follow the link, so no config would
	// be deployed.
	configs := filepath.Join(dir, configsDirName)
	if info, err := os.Lstat(configs); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symbolic link; a symlinked configs directory is not supported", configs)
	}

	run := &applyRun{
		opts:     opts,
		client:   newWorkflowClient(opts.Socket),
		started:  started,
		dir:      dir,
		name:     name,
		injects:  existingDir(filepath.Join(dir, injectsDirName)),
		configs:  existingDir(configs),
		wfConfig: wfConfig,
		staged:   false,
		upserted: 0,
	}

	if run.injects == "" && run.configs == "" && run.wfConfig == "" {
		return nil, fmt.Errorf(
			"nothing to apply: %s has no %s/, %s/, %s or %s",
			dir, injectsDirName, configsDirName, workflowConfigName, legacyWorkflowConfig,
		)
	}

	plog.Info(plog.TypeSystem, "applying workflow", "step", stepPreflight, "dir", dir, "name", name)

	return run, nil
}

// warnIgnoredWorkflowConfigs warns about a file named like a workflow config
// that is not read as one without -c, such as phenix.yaml. Without the
// warning, a run logged at the warn level would read as a deploy.
func warnIgnoredWorkflowConfigs(dir string) {
	for _, name := range []string{"phenix.yaml", ".phenix.yaml"} {
		if !isRegularFile(filepath.Join(dir, name)) {
			continue
		}

		plog.Warn(
			plog.TypeSystem, "ignoring a possible workflow config",
			"step", stepPreflight, "dir", dir, "file", name, "hint", "rename it to "+workflowConfigName+", or pass -c "+name,
		)
	}
}

// preflight checks the server and parses every config and the workflow
// config. It changes nothing and leaves every check of what the files say to
// the server. It also runs the staging guards, so that a dry run refuses what
// staging would refuse.
func (r *applyRun) preflight(ctx context.Context) (preflightResult, error) {
	injectsBase, err := r.checkServer(ctx)
	if err != nil {
		return preflightResult{}, err
	}

	if r.injects != "" {
		// Staged, such an entry would make the staged tree look like a
		// topology directory, which checkDestination then refuses to replace.
		if entry := injectsMarkerEntry(r.injects); entry != "" {
			return preflightResult{}, fmt.Errorf(
				"%s holds %s, a name that marks a topology directory; rename it or move it deeper", r.injects, entry,
			)
		}

		// stage looks again, since the destination can change while the dry
		// runs are under way.
		cwd, _ := os.Getwd()

		if err := r.checkDestination(injectsBase, filepath.Join(injectsBase, r.name), cwd); err != nil {
			return preflightResult{}, err
		}
	}

	var configs []configFile

	if r.configs != "" {
		if configs, err = loadConfigs(r.configs); err != nil {
			return preflightResult{}, fmt.Errorf("checking configs: %w", err)
		}
	}

	if len(configs) == 0 && r.injects == "" && r.wfConfig == "" {
		return preflightResult{}, fmt.Errorf(
			"nothing to apply: %s has no %s/, %s or %s, and %s/ holds no .json, .yaml or .yml file that is not hidden",
			r.dir, injectsDirName, workflowConfigName, legacyWorkflowConfig, configsDirName,
		)
	}

	for _, cfg := range configs {
		plog.Info(plog.TypeSystem, "config parsed", "step", stepPreflight, "file", r.rel(cfg.Path), "kind", cfg.Kind)
	}

	if r.wfConfig == "" {
		return preflightResult{injectsBase: injectsBase, configs: configs, wfConfig: nil}, nil
	}

	wfConfig, err := parseConfigFile(r.wfConfig, false)
	if err != nil {
		return preflightResult{}, fmt.Errorf("checking the workflow config: %w", err)
	}

	plog.Info(plog.TypeSystem, "workflow config parsed", "step", stepPreflight, "file", r.rel(wfConfig.Path), "kind", wfConfig.Kind)

	return preflightResult{injectsBase: injectsBase, configs: configs, wfConfig: &wfConfig}, nil
}

// checkServer checks that the server answers and supports workflow dry runs,
// and returns the injects directory to stage into.
func (r *applyRun) checkServer(ctx context.Context) (string, error) {
	if r.opts.Socket == "" {
		return "", errors.New("no phenix unix socket configured; set --unix-socket")
	}

	serverOpts, err := r.client.options(ctx)
	if err != nil {
		return "", r.requestError("reading the phenix server options", err)
	}

	if supported, _ := serverOpts[optionWorkflowDryRun].(bool); !supported {
		return "", fmt.Errorf("the phenix server at %s does not support workflow dry runs; upgrade the phenix server", r.opts.Socket)
	}

	injectsBase := injectsBaseDir(r.opts, serverOpts)

	// A relative directory would be resolved against the working directory.
	serverDir, _ := serverOpts[optionInjectsBase].(string)
	if r.injects != "" && !r.opts.InjectsBaseExplicit && serverDir != "" && !filepath.IsAbs(serverDir) {
		return "", fmt.Errorf("the phenix server reports the relative injects directory %q; pass --base-dir.injects", serverDir)
	}

	plog.Info(plog.TypeSystem, "phenix server ready", "step", stepPreflight, "socket", r.opts.Socket, "injects", injectsBase)

	return injectsBase, nil
}

// resolveTopologyDir returns the absolute topology directory for arg: an
// existing path as is, else a name with no path separator under
// topologiesBase.
func resolveTopologyDir(arg, topologiesBase string) (string, error) {
	if arg == "" {
		return "", errors.New("the topology directory or name must not be empty")
	}

	path := arg

	info, err := os.Stat(path)
	if err != nil && topologiesBase != "" && !strings.ContainsRune(arg, filepath.Separator) {
		path = filepath.Join(topologiesBase, arg)

		info, err = os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("topology directory not found: tried %s and %s", arg, path)
		}
	}

	if err != nil {
		return "", fmt.Errorf("topology directory not found: %w", err)
	}

	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", path)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}

	return abs, nil
}

// resolveWorkflowConfig returns the path of the workflow config in dir, or ""
// when there is none. An explicit config must exist. Otherwise .phenix.yml is
// the fallback, and having both is an error.
func resolveWorkflowConfig(dir, config string, explicit bool) (string, error) {
	path := config
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}

	if explicit {
		if !isRegularFile(path) {
			return "", fmt.Errorf("workflow config %s does not exist or is not a file", path)
		}

		return path, nil
	}

	legacy := filepath.Join(dir, legacyWorkflowConfig)
	primary, fallback := isRegularFile(path), isRegularFile(legacy)

	switch {
	case primary && fallback:
		return "", fmt.Errorf("both %s and %s exist in %s; remove one or pass -c", filepath.Base(path), legacyWorkflowConfig, dir)
	case primary:
		return path, nil
	case fallback:
		return legacy, nil
	default:
		return "", nil
	}
}

// injectsBaseDir returns where injects are staged: --base-dir.injects when it
// was given on the command line, else the server's setting, else the local
// one.
func injectsBaseDir(opts applyOptions, serverOpts map[string]any) string {
	if opts.InjectsBaseExplicit {
		return opts.InjectsBase
	}

	if dir, _ := serverOpts[optionInjectsBase].(string); dir != "" {
		return dir
	}

	return opts.InjectsBase
}

// existingDir returns path when it is a directory, else "".
func existingDir(path string) string {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return path
	}

	return ""
}

// isRegularFile reports whether path is a regular file, following symlinks.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}
