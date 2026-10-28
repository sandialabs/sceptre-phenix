package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/mitchellh/mapstructure"

	"phenix/api/scorch"
	"phenix/app"
	"phenix/store"
	"phenix/types"
	"phenix/types/version"
	v1 "phenix/types/version/v1"
	"phenix/util/file"
)

const (
	scenarioAppsName  = "scenario-apps"
	injectSourcesName = "inject-sources"
	imageConfigsName  = "image-configs"
	scenarioHostsName = "scenario-hosts"
	rbacUsersName     = "rbac-users"

	// Phrases in the details of rows, which their hints look for.
	noRolePhrase   = "is not in the store and is not built in"
	goneNamePhrase = "which the store does not have"
)

// postbuildScript matches the name of a script that phenix image create adds itself, which is not a file.
var postbuildScript = regexp.MustCompile(`^POSTBUILD_[A-Z_]+$`)

// scenarioAppsCheck warns about each app and SCORCH component type that a stored scenario or experiment names, but
// that phenix does not build in and finds no program for on PATH.
func scenarioAppsCheck(ctx context.Context, in *doctorInput) doctorCheck {
	scenarios, why := in.storedKind(ctx, "Scenario")
	if why != "" {
		return hostSkip(why)
	}

	experiments, why := in.storedExperiments(ctx)
	if why != "" {
		return hostSkip(why)
	}

	apps, components := make(map[string][]string), make(map[string][]string)
	add := func(where string, names, types []string) {
		for _, name := range names {
			apps[name] = append(apps[name], where)
		}

		for _, typ := range types {
			components[typ] = append(components[typ], where)
		}
	}

	for _, c := range scenarios {
		spec, err := types.DecodeScenarioFromConfig(c)
		if err != nil {
			continue
		}

		names, comps, _ := doctorScenarioApps(spec)
		add("scenario "+c.Metadata.Name, names, comps)
	}

	for _, exp := range experiments {
		add("experiment "+exp.name, exp.apps, exp.components)
	}

	missingApps := in.missingPrograms(apps, app.BuiltinApps(), userAppPrefix)
	missingComponents := in.missingPrograms(components, scorch.BuiltinComponents(), scorchComponentPrefix)

	var problems []string
	if len(missingApps) > 0 {
		problems = append(problems, countVerb(len(missingApps),
			"this app is not built in and is not on PATH, so phenix cannot run it: ",
			"these apps are not built in and are not on PATH, so phenix cannot run them: ")+shortList(missingApps))
	}

	if len(missingComponents) > 0 {
		sentence := countVerb(len(missingComponents),
			"this SCORCH component is not built in and is not on PATH, so SCORCH cannot run it: ",
			"these SCORCH components are not built in and are not on PATH, so SCORCH cannot run them: ") +
			shortList(missingComponents)
		if len(problems) > 0 {
			sentence = capitalize(sentence)
		}

		problems = append(problems, sentence)
	}

	if len(problems) > 0 {
		return warnCheck(strings.Join(problems, ". "))
	}

	if len(apps) == 0 {
		return passCheck("no stored scenario or experiment names an app")
	}

	return passCheck(fmt.Sprintf("the stored scenarios and experiments name %s and %s, and each is built in or on PATH",
		plural(len(apps), "app", "apps"), plural(len(components), "SCORCH component type", "SCORCH component types")))
}

// missingPrograms names the program of each name in uses that is not in builtin and not on PATH, with the configs
// that use it.
func (in *doctorInput) missingPrograms(uses map[string][]string, builtin []string, prefix string) []string {
	var missing []string

	for _, name := range slices.Sorted(maps.Keys(uses)) {
		if slices.Contains(builtin, name) {
			continue
		}

		if _, err := in.env.lookPath(prefix + name); err == nil {
			continue
		}

		where := slices.Compact(slices.Sorted(slices.Values(uses[name])))
		missing = append(missing, fmt.Sprintf("%s%s (%s)", prefix, name, strings.Join(where, ", ")))
	}

	return missing
}

// scenarioAppsHint is what to do about a scenario-apps row that warns.
func scenarioAppsHint(c doctorCheck) string {
	if c.Status == doctorSkip {
		return ""
	}

	return "install the app, or add its directory to PATH for the user that runs phenix. phenix runs " +
		userAppPrefix + "<app> and " + scorchComponentPrefix + "<type>"
}

// injectSourcesCheck warns about each source of a node injection in a stored experiment or topology that does not
// exist, as phenix resolves it: an absolute path as it is, and a relative one under the experiment's base directory.
// It does not check the files that apps write under the base directory at the start, or the relative sources of a
// topology, which depend on the experiment.
func injectSourcesCheck(ctx context.Context, in *doctorInput) doctorCheck {
	experiments, why := in.storedExperiments(ctx)
	if why != "" {
		return hostSkip(why)
	}

	topologies, why := in.storedKind(ctx, "Topology")
	if why != "" {
		return hostSkip(why)
	}

	var (
		items   []string
		checked int
	)

	check := func(where, path string) {
		checked++

		if _, err := in.env.stat(path); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				path = fmt.Sprintf("%s (%v)", path, unwrapPathError(err))
			}

			items = append(items, where+": "+path)
		}
	}

	for _, exp := range experiments {
		for _, vm := range exp.vms {
			for _, src := range vm.injects {
				path := src

				switch {
				case filepath.IsAbs(src) && exp.baseDir != "" && file.WithinDir(exp.baseDir, src):
					continue // an app writes it at the start
				case !filepath.IsAbs(src) && exp.baseDir == "":
					continue
				case !filepath.IsAbs(src):
					path = exp.baseDir + "/" + src // as the minimega script does
				}

				check(fmt.Sprintf("experiment %s node %s", exp.name, vm.name), path)
			}
		}
	}

	for _, c := range topologies {
		for _, node := range decodeDoctorTopology(c) {
			if node.External() || node.GeneralF == nil {
				continue
			}

			for _, inject := range node.Injections() {
				if filepath.IsAbs(inject.Src()) {
					check(fmt.Sprintf("topology %s node %s", c.Metadata.Name, node.General().Hostname()), inject.Src())
				}
			}
		}
	}

	if len(items) > 0 {
		return warnCheck(fmt.Sprintf("%s of %d %s here, so minimega disk inject cannot copy %s at the start: %s",
			plural(len(items), "inject source", "inject sources"), checked,
			countVerb(len(items), "does not exist", "do not exist"), countVerb(len(items), "it", "them"), shortList(items)))
	}

	if checked == 0 {
		return passCheck("no stored experiment or topology has an inject source to check")
	}

	return passCheck(eachOf(checked, "inject source", "inject sources") + " exists")
}

// decodeDoctorTopology is the nodes of the stored topology config c, without the topologies it includes, which
// phenix reads from the store when it decodes c.
func decodeDoctorTopology(c store.Config) []*v1.Node {
	if c.APIVersion() != version.StoredVersion[c.Kind] {
		return nil
	}

	var spec v1.TopologySpec
	if err := mapstructure.WeakDecode(c.Spec, &spec); err != nil {
		return nil
	}

	return slices.DeleteFunc(spec.NodesF, func(n *v1.Node) bool { return n == nil })
}

// injectSourcesHint is what to do about an inject-sources row that warns.
func injectSourcesHint(c doctorCheck) string {
	if c.Status == doctorSkip {
		return ""
	}

	return "put the file at the path, or change the injection src in the topology or experiment. In a container, " +
		"mount the same path in the containers of phenix and minimega"
}

// imageConfigsCheck warns about each overlay directory of a stored image config that does not exist, which
// phenix image build gives vmdb2 to copy into the image. A script path that does not exist only changes the detail,
// because the config holds a copy of each script that the build uses.
func imageConfigsCheck(ctx context.Context, in *doctorInput) doctorCheck {
	configs, why := in.storedKind(ctx, "Image")
	if why != "" {
		return hostSkip(why)
	}

	var overlays, scripts []string

	for _, c := range configs {
		var img v1.Image
		if err := mapstructure.Decode(c.Spec, &img); err != nil {
			continue
		}

		for _, overlay := range img.Overlays {
			if overlay = strings.TrimSpace(overlay); overlay == "" {
				continue
			}

			info, err := in.env.stat(overlay)

			switch {
			case err != nil:
				overlays = append(overlays, fmt.Sprintf("%s: %s", c.Metadata.Name, relativeToCwd(overlay, in.cwd)))
			case !info.IsDir():
				overlays = append(overlays, fmt.Sprintf("%s: %s (not a directory)", c.Metadata.Name, overlay))
			}
		}

		for _, script := range slices.Sorted(maps.Keys(img.Scripts)) {
			if postbuildScript.MatchString(script) {
				continue
			}

			if strings.HasPrefix(script, "http://") || strings.HasPrefix(script, "https://") {
				continue // a URL, not a file on disk
			}

			if _, err := in.env.stat(strings.TrimPrefix(script, "file://")); err != nil {
				scripts = append(scripts, fmt.Sprintf("%s: %s", c.Metadata.Name, script))
			}
		}
	}

	var parts []string
	if len(overlays) > 0 {
		parts = append(parts, fmt.Sprintf("%s %s, so phenix image build fails: %s",
			plural(len(overlays), "overlay directory", "overlay directories"),
			countVerb(len(overlays), "does not exist", "do not exist"), shortList(overlays)))
	}

	if len(scripts) > 0 {
		parts = append(parts, fmt.Sprintf("%s not on disk, so phenix image update keeps %s: %s",
			plural(len(scripts), "script file is", "script files are"),
			countVerb(len(scripts), "its stored copy", "their stored copies"), shortList(scripts)))
	}

	switch {
	case len(overlays) > 0:
		return warnCheck(strings.Join(parts, ". "))
	case len(parts) > 0:
		return passCheck(strings.Join(parts, ". "))
	}

	if len(configs) == 0 {
		return passCheck("the store has no image configs")
	}

	return passCheck(fmt.Sprintf("the overlays and scripts of the %s exist",
		plural(len(configs), "stored image config", "stored image configs")))
}

// imageConfigsHint is what to do about an image-configs row that warns.
func imageConfigsHint(c doctorCheck) string {
	if c.Status == doctorSkip {
		return ""
	}

	return "create the overlay directory, or remove it with phenix image remove <config> --overlays <path>. " +
		"In a container, mount the same path in the container"
}

// scenarioHostsCheck warns about each host that an app of a stored experiment's scenario names, but that is not a
// node of the experiment's topology. The apps of phenix look up their hosts by exact host name, and skip the others.
func scenarioHostsCheck(ctx context.Context, in *doctorInput) doctorCheck {
	experiments, why := in.storedExperiments(ctx)
	if why != "" {
		return hostSkip(why)
	}

	var (
		items []string
		hosts int
	)

	for _, exp := range experiments {
		for _, ah := range exp.appHosts {
			hosts++

			if !slices.Contains(exp.nodes, ah[1]) {
				items = append(items, fmt.Sprintf("experiment %s app %s host %s", exp.name, ah[0], ah[1]))
			}
		}
	}

	if len(items) > 0 {
		return warnCheck(fmt.Sprintf("apps name hosts that are not nodes of the topology, so the apps skip them: %s",
			shortList(slices.Compact(items))))
	}

	if hosts == 0 {
		return passCheck("no scenario app of a stored experiment names a host")
	}

	return passCheck(eachOf(hosts, "host", "hosts") + " that the scenario apps of the stored experiments name is a node")
}

// scenarioHostsHint is what to do about a scenario-hosts row that warns.
func scenarioHostsHint(c doctorCheck) string {
	if c.Status == doctorSkip {
		return ""
	}

	return "change the host names in the scenario to the node host names, with phenix experiment edit <experiment>"
}

// rbacUsersCheck warns, when sign-in is on, about each stored user whose role is not a stored or built-in role, and
// about each literal resource name in a role that names an experiment or a user that the store does not have.
func rbacUsersCheck(ctx context.Context, in *doctorInput) doctorCheck {
	if in.text("ui.jwt-signing-key") == "" {
		return passCheck("unused, because ui.jwt-signing-key is empty, so phenix ui does not check roles")
	}

	users, why := in.storedKind(ctx, "User")
	if why != "" {
		return hostSkip(why)
	}

	roles, why := in.storedKind(ctx, "Role")
	if why != "" {
		return hostSkip(why)
	}

	experiments, why := in.storedExperiments(ctx)
	if why != "" {
		return hostSkip(why)
	}

	known := slices.Clone(in.env.roles)
	names := doctorNames{experiments: nil, users: nil}

	for _, exp := range experiments {
		names.experiments = append(names.experiments, exp.name)
	}

	for _, c := range users {
		names.users = append(names.users, c.Metadata.Name)
	}

	var items []string

	for _, c := range roles {
		var spec v1.RoleSpec
		if err := mapstructure.Decode(c.Spec, &spec); err != nil {
			continue
		}

		known = append(known, c.Metadata.Name, spec.Name)
		items = append(items, names.goneNames("role "+c.Metadata.Name, spec.Policies)...)
	}

	var userItems []string

	for _, c := range users {
		var spec v1.UserSpec
		if err := mapstructure.Decode(c.Spec, &spec); err != nil || spec.Role == nil {
			continue // phenix ui gives a user with no role the disabled role
		}

		label := "user " + c.Metadata.Name
		if !slices.Contains(known, spec.Role.Name) {
			userItems = append(userItems, fmt.Sprintf("the role %q of %s %s", spec.Role.Name, label, noRolePhrase))
		}

		items = append(items, names.goneNames("the role of "+label, spec.Role.Policies)...)

		if in.found != nil {
			in.found.users = append(in.found.users, c.Metadata.Name, spec.Username)
		}
	}

	items = append(userItems, items...)
	if len(items) > 0 {
		return warnCheck(shortList(items))
	}

	if len(users) == 0 {
		return passCheck("the store has no users, and no role names a deleted experiment or user")
	}

	return passCheck(eachOf(len(users), "stored user", "stored users") +
		" has a stored or built-in role, and no role names a deleted experiment or user")
}

// doctorNames are the names of the stored experiments and users, which role policies can name.
type doctorNames struct {
	experiments, users []string
}

// goneNames names each literal resource name of policies that names no stored experiment or user. It judges only a
// policy whose resources are all experiments and VMs (a VM name starts with its experiment, as in exp/vm), or all
// users. A name with a glob or a "!" can be for a later config, so it is never gone.
func (n doctorNames) goneNames(label string, policies []*v1.PolicySpec) []string {
	var gone []string

	for _, p := range policies {
		if p == nil || len(p.Resources) == 0 {
			continue
		}

		var have []string

		switch {
		case allResourcesOf(p.Resources, "experiments", "vms"):
			have = n.experiments
		case allResourcesOf(p.Resources, "users"):
			have = n.users
		default:
			continue
		}

		for _, name := range p.ResourceNames {
			if name == "" || strings.ContainsAny(name, "*?[!") {
				continue
			}

			if first, _, _ := strings.Cut(name, "/"); !slices.Contains(have, first) {
				gone = append(gone, fmt.Sprintf("%s names %s, %s", label, name, goneNamePhrase))
			}
		}
	}

	return slices.Compact(gone)
}

// allResourcesOf reports whether each resource is one of bases or below one, such as experiments/start.
func allResourcesOf(resources []string, bases ...string) bool {
	for _, r := range resources {
		base, _, _ := strings.Cut(r, "/")
		if !slices.Contains(bases, base) {
			return false
		}
	}

	return true
}

// rbacUsersHint is what to do about an rbac-users row that warns.
func rbacUsersHint(c doctorCheck) string {
	switch {
	case c.Status == doctorSkip:
		return ""
	case strings.Contains(c.Detail, noRolePhrase):
		return "give the user a role on the Users page, or fix its rbac.roleName with phenix config edit user/<user>. " +
			"Remove the names of deleted experiments and users from the role in the same way"
	}

	return "remove the names of deleted experiments and users from the role on the Users page, or with " +
		"phenix config edit user/<user> or phenix config edit role/<role>"
}

// countVerb is one when n is 1, else many. It makes a verb or pronoun agree with a count.
func countVerb(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return many
}

// eachOf names n things as the subject of a singular verb: "the 1 host" or "each of the 3 hosts".
func eachOf(n int, one, many string) string {
	if n == 1 {
		return "the 1 " + one
	}

	return fmt.Sprintf("each of the %d %s", n, many)
}
