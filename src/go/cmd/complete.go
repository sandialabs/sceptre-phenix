package cmd

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"phenix/api/config"
	"phenix/api/disk"
	"phenix/api/experiment"
	"phenix/api/job"
	"phenix/api/runtimesettings"
	"phenix/api/vlan"
	"phenix/api/vm"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/web/rbac"
)

// vmArgs is the number of arguments that name a VM: the experiment and the VM.
const vmArgs = 2

// completionTimeout limits each request to minimega or to the server that a
// shell completion makes, so a completion does not hang the shell.
const completionTimeout = 3 * time.Second

// expState selects the experiments that [completeExperiments] completes.
type expState int

const (
	expAny expState = iota
	expRunning
	expStopped
)

// The completion functions below are cobra.CompletionFunc values for
// ValidArgsFunction and RegisterFlagCompletionFunc. Each one is cheap, and
// gives no completions with cobra.ShellCompDirectiveNoFileComp when it cannot
// get its data, so the shell shows no error. Combine them by argument
// position with [completeArgs] or [completePositional]. A function for a part
// of an experiment, such as completeVMs, reads the experiment name from the
// first argument and the VM name from the second argument:
//
//	ValidArgsFunction: completeArgs(completeExperiments(expAny, false), completeVMs, completeVMInterfaces),
//	_ = cmd.RegisterFlagCompletionFunc("experiment", completeExperiments(expAny, false))

// completeArgs completes the argument at position i with fns[i], and gives no
// completions after the last one.
func completeArgs(fns ...cobra.CompletionFunc) cobra.CompletionFunc {
	return completePositional(nil, fns...)
}

// completePositional completes the argument at position i with fns[i], and
// each argument after them with rest. A nil rest gives no completions.
func completePositional(rest cobra.CompletionFunc, fns ...cobra.CompletionFunc) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		fn := rest
		if len(args) < len(fns) {
			fn = fns[len(args)]
		}

		if fn == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return fn(cmd, args, toComplete)
	}
}

// completeValues completes one of values.
func completeValues(values ...string) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completionMatches(values, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// completeExperiments completes the names of the stored experiments in state,
// after "all" when includeAll is set.
func completeExperiments(state expState, includeAll bool) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		exps, err := experiment.List()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		names := make([]string, 0, len(exps))

		for _, exp := range exps {
			if state == expAny || exp.Running() == (state == expRunning) {
				names = append(names, exp.Metadata.Name)
			}
		}

		names = completionMatches(names, toComplete)

		if includeAll && strings.HasPrefix(allExperiments, toComplete) {
			names = append([]string{allExperiments}, names...)
		}

		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeVMs completes the names of the VMs of the experiment in the first argument.
func completeVMs(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) < 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
	defer cancel()

	vms, err := vm.List(ctx, args[0])
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	names := make([]string, 0, len(vms))
	for _, v := range vms {
		names = append(names, v.Name)
	}

	return completionMatches(names, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeVMInterfaces completes the interfaces of the VM in the second
// argument, of the experiment in the first argument: the name of each
// interface that has one, and the index of each other interface.
func completeVMInterfaces(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) < vmArgs {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
	defer cancel()

	v, err := vm.Get(ctx, args[0], args[1])
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ifaces := make([]string, 0, len(v.Networks))

	for i := range v.Networks {
		if i < len(v.IfaceNames) && v.IfaceNames[i] != "" {
			ifaces = append(ifaces, v.IfaceNames[i])
		} else {
			ifaces = append(ifaces, strconv.Itoa(i))
		}
	}

	return completionMatches(ifaces, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeVMInterfaceIndexes completes the interface indexes of the VM in
// the second argument, of the experiment in the first argument, with the
// name of each interface that has one.
func completeVMInterfaceIndexes(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) < vmArgs {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
	defer cancel()

	v, err := vm.Get(ctx, args[0], args[1])
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var indexes []string

	for i := range v.Networks {
		index := strconv.Itoa(i)
		if !strings.HasPrefix(index, toComplete) {
			continue
		}

		if i < len(v.IfaceNames) && v.IfaceNames[i] != "" {
			index = cobra.CompletionWithDesc(index, v.IfaceNames[i])
		}

		indexes = append(indexes, index)
	}

	return indexes, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
}

// completeVMSnapshots completes the snapshot names of the VM in the second
// argument, of the experiment in the first argument.
func completeVMSnapshots(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) < vmArgs {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
	defer cancel()

	snapshots, err := vm.Snapshots(ctx, args[0], args[1])
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	slices.Sort(snapshots)

	return completionMatches(snapshots, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeVLANAliases completes the VLAN aliases of the experiment in the first argument.
func completeVLANAliases(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) < 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	info, err := vlan.Aliases(vlan.Experiment(args[0]))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	aliases := slices.Sorted(maps.Keys(info[args[0]]))

	return completionMatches(aliases, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeConfigNames completes kind/name of a stored config: first the kind
// and a slash, then the names of the configs of that kind.
func completeConfigNames(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	kind, name, ok := strings.Cut(toComplete, "/")
	if !ok {
		kinds := completionMatches(configKinds(), toComplete)
		for i, k := range kinds {
			kinds[i] = k + "/"
		}

		return kinds, cobra.ShellCompDirectiveNoSpace | cobra.ShellCompDirectiveNoFileComp
	}

	var stored string

	for _, k := range config.AllKinds {
		if strings.EqualFold(k, kind) {
			stored = k
		}
	}

	if stored == "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var matches []string

	for _, n := range completionMatches(configNamesOf(stored), name) {
		matches = append(matches, kind+"/"+n)
	}

	return matches, cobra.ShellCompDirectiveNoFileComp
}

// completeConfigNamesOf completes the names of the stored configs of kind,
// such as "Image" or "Topology".
func completeConfigNamesOf(kind string) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completionMatches(configNamesOf(kind), toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// completeDisks completes the disk images in the minimega files directory,
// as paths relative to it.
func completeDisks(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	images, err := disk.GetImages("")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	paths := make([]string, 0, len(images))

	for _, image := range images {
		if image.RelativePath != "" {
			paths = append(paths, image.RelativePath)
		}
	}

	return completionMatches(paths, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeJobIDs completes the IDs of the background jobs of the phenix ui
// server at the unix socket, with the title of each job.
func completeJobIDs(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
	defer cancel()

	filter := job.Filter{Owner: "", Experiment: "", Kind: "", Active: false}

	jobs, err := newJobClient(common.UnixSocket).list(ctx, filter)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var ids []string

	for _, j := range jobs {
		if strings.HasPrefix(j.ID, toComplete) {
			ids = append(ids, cobra.CompletionWithDesc(j.ID, j.Title))
		}
	}

	return ids, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
}

// completeClusterHosts completes the names of the cluster hosts that minimega reports.
func completeClusterHosts(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
	defer cancel()

	hosts, _, err := mm.GetClusterHostsObserved(ctx, false)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	names := make([]string, 0, len(hosts))
	for _, h := range hosts {
		names = append(names, h.Name)
	}

	return completionMatches(names, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeUsers completes the user names of the phenix users.
func completeUsers(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	users, err := rbac.GetUsers()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	names := make([]string, 0, len(users))
	for _, u := range users {
		names = append(names, u.Username())
	}

	return completionMatches(names, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeRoles completes the names of the phenix roles.
func completeRoles(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	roles, err := rbac.GetRoles()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	names := make([]string, 0, len(roles))
	for _, r := range roles {
		names = append(names, r.Spec.Name)
	}

	return completionMatches(names, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeLogFiles completes the names of the log files that phenix logs download takes.
func completeLogFiles(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completeValues(logFileNames...)(cmd, args, toComplete)
}

// completeSettingsKeys completes the dotted keys of the settings in v.
func completeSettingsKeys(v *viper.Viper) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		flat := make(map[string]any)
		runtimesettings.Flatten("", v.AllSettings(), flat)

		return completionMatches(slices.Sorted(maps.Keys(flat)), toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// expNameCompletion completes the first argument with the names of the
// stored experiments, after "all" when includeAll is set.
func expNameCompletion(includeAll bool) cobra.CompletionFunc {
	return completeArgs(completeExperiments(expAny, includeAll))
}

// vmArgsCompletion completes <experiment name> <vm name>.
func vmArgsCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completeArgs(completeExperiments(expAny, false), completeVMs)(cmd, args, toComplete)
}

// vmSnapshotCompletion completes <experiment name> <vm name> <snapshot name>.
func vmSnapshotCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completeArgs(
		completeExperiments(expAny, false), completeVMs, completeVMSnapshots,
	)(cmd, args, toComplete)
}

// vlanAliasArgsCompletion completes <experiment name> <alias name>.
func vlanAliasArgsCompletion(
	cmd *cobra.Command,
	args []string,
	toComplete string,
) ([]string, cobra.ShellCompDirective) {
	return completeArgs(completeExperiments(expAny, false), completeVLANAliases)(cmd, args, toComplete)
}

// configGetArgsCompletion completes one <kind/name> argument.
func configGetArgsCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completeArgs(completeConfigNames)(cmd, args, toComplete)
}

// settingsKeyCompletion completes the first argument with the dotted keys of
// the settings in v.
func settingsKeyCompletion(v *viper.Viper) cobra.CompletionFunc {
	return completeArgs(completeSettingsKeys(v))
}

// configNamesOf returns the names of the stored configs of kind, or none
// when the store cannot list them.
func configNamesOf(kind string) []string {
	configs, err := config.List(kind)
	if err != nil {
		return nil
	}

	names := make([]string, 0, len(configs))
	for _, c := range configs {
		names = append(names, c.Metadata.Name)
	}

	return names
}

// completionMatches returns the candidates that start with prefix, in their
// order and without repeats, or nil when none does.
func completionMatches(candidates []string, prefix string) []string {
	var matches []string

	for _, c := range candidates {
		if strings.HasPrefix(c, prefix) && !slices.Contains(matches, c) {
			matches = append(matches, c)
		}
	}

	return matches
}
