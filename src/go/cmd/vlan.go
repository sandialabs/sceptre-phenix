package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"phenix/api/vlan"
	"phenix/util"
	"phenix/util/plog"
	"phenix/util/printer"
)

const (
	aliasArgs       = 3
	aliasLookupArgs = 2
	rangeArgs       = 3
)

func newVlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vlan",
		Short: "Used to manage VLANs",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	return cmd
}

// errVLANArgs is returned for a form of arguments or flags that phenix vlan
// alias or range does not accept.
var errVLANArgs = errors.New("unexpected number of arguments")

// vlanAliasAction is what phenix vlan alias does for its arguments.
type vlanAliasAction int

const (
	vlanAliasListAll vlanAliasAction = iota
	vlanAliasListExperiment
	vlanAliasLookup
	vlanAliasPin
	vlanAliasUnpin
)

// vlanAliasRequest is a parsed phenix vlan alias command line.
type vlanAliasRequest struct {
	action vlanAliasAction
	exp    string
	alias  string
	id     int
}

// parseVLANAliasArgs parses the arguments of phenix vlan alias. With unpin
// set, it takes only an experiment name and an alias name.
func parseVLANAliasArgs(args []string, unpin, force bool) (vlanAliasRequest, error) {
	var req vlanAliasRequest

	if unpin {
		if len(args) != aliasLookupArgs {
			return req, fmt.Errorf("%w: --unpin needs an experiment name and an alias name", errVLANArgs)
		}

		if force {
			return req, fmt.Errorf("%w: use --force only to pin an alias", errVLANArgs)
		}

		return vlanAliasRequest{action: vlanAliasUnpin, exp: args[0], alias: args[1], id: 0}, nil
	}

	if force && len(args) != aliasArgs {
		return req, fmt.Errorf("%w: use --force only to pin an alias", errVLANArgs)
	}

	switch len(args) {
	case 0:
		return vlanAliasRequest{action: vlanAliasListAll, exp: "", alias: "", id: 0}, nil
	case 1:
		return vlanAliasRequest{action: vlanAliasListExperiment, exp: args[0], alias: "", id: 0}, nil
	case aliasLookupArgs:
		return vlanAliasRequest{action: vlanAliasLookup, exp: args[0], alias: args[1], id: 0}, nil
	case aliasArgs:
		id, err := parseCLIVLANID(args[2])
		if err != nil {
			return req, err
		}

		return vlanAliasRequest{action: vlanAliasPin, exp: args[0], alias: args[1], id: id}, nil
	default:
		return req, fmt.Errorf(
			"%w: give at most an experiment name, an alias name and a VLAN ID",
			errVLANArgs,
		)
	}
}

// vlanAliasRow is a VLAN alias of an experiment and its VLAN ID, as phenix
// vlan alias -o json shows it.
type vlanAliasRow struct {
	Experiment string `json:"experiment" yaml:"experiment"`
	Alias      string `json:"alias"      yaml:"alias"`
	ID         int    `json:"id"         yaml:"id"`
}

// vlanAliasRows returns the aliases of info, by experiment and then alias, in
// the order of the table.
func vlanAliasRows(info map[string]map[string]int) []vlanAliasRow {
	rows := []vlanAliasRow{}

	for _, exp := range slices.Sorted(maps.Keys(info)) {
		for _, alias := range slices.Sorted(maps.Keys(info[exp])) {
			rows = append(rows, vlanAliasRow{Experiment: exp, Alias: alias, ID: info[exp][alias]})
		}
	}

	return rows
}

// printVLANAliases writes the aliases of info as rows in the format of out.
func printVLANAliases(cmd *cobra.Command, out *outputFlag, info map[string]map[string]int) error {
	return out.printAPI(cmd, vlanAliasRows(info), outputViews{
		formatTable: func(w io.Writer) error {
			printer.PrintTableOfVLANAliases(w, info)

			return nil
		},
	})
}

// errVLANViewOnly is returned for a flag of phenix vlan alias or range that
// works only with some forms of the arguments.
var errVLANViewOnly = errors.New("flag not valid here")

// checkVLANViewFlags refuses -o/--output for a change, and --dry-run for
// anything but the change that dryChange says.
func checkVLANViewFlags(cmd *cobra.Command, dry dryRun, view, dryChange bool, dryFlag string) error {
	if !view && cmd.Flags().Changed(outputFlagName) {
		return fmt.Errorf("%w: -o/--output works only to view, not to change", errVLANViewOnly)
	}

	if dry.enabled() && !dryChange {
		return fmt.Errorf("%w: --dry-run works only with %s", errVLANViewOnly, dryFlag)
	}

	return nil
}

func newVlanAliasCmd() *cobra.Command {
	var (
		out *outputFlag
		dry dryRun
	)

	desc := `View, pin or unpin VLAN aliases

  With no arguments, lists the VLAN aliases of every experiment. With an
  experiment name, lists that experiment's VLAN aliases. With an experiment
  name and an alias name, prints the VLAN ID of that alias. With a VLAN ID as
  well, pins the alias to that VLAN ID. With --unpin, clears the pin of the
  alias, so minimega gives it an ID when the experiment starts.

  The experiment must be stopped to pin or unpin an alias, and the alias must
  be in the experiment. A pin is refused when another experiment or a
  minimega namespace uses the VLAN ID, when minimega blacklists it, or when
  minimega does not answer. Use --force to pin it anyway.

  -o json and -o yaml print each alias as an object with its experiment,
  alias and VLAN ID; an alias lookup prints one object. --dry-run with
  --unpin checks the unpin and changes nothing.`

	example := `
  phenix vlan alias
  phenix vlan alias <experiment name>
  phenix vlan alias <experiment name> <alias name>
  phenix vlan alias <experiment name> <alias name> <vlan id>
  phenix vlan alias <experiment name> <alias name> --unpin`

	cmd := &cobra.Command{
		Use:               "alias [experiment name] [alias name] [vlan id]",
		Short:             "View, pin or unpin VLAN aliases",
		Long:              desc,
		Example:           example,
		ValidArgsFunction: vlanAliasArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := parseVLANAliasArgs(
				args, mustGet(cmd.Flags().GetBool, "unpin"), mustGet(cmd.Flags().GetBool, "force"),
			)
			if err != nil {
				return err
			}

			view := req.action == vlanAliasListAll || req.action == vlanAliasListExperiment || req.action == vlanAliasLookup
			if err := checkVLANViewFlags(cmd, dry, view, req.action == vlanAliasUnpin, "--unpin"); err != nil {
				return err
			}

			switch req.action {
			case vlanAliasListAll, vlanAliasListExperiment, vlanAliasLookup:
				return viewVLANAliases(cmd, out, req)
			case vlanAliasPin:
				if err := vlan.PinAlias(
					context.Background(), cliView(), req.exp, req.alias, req.id, mustGet(cmd.Flags().GetBool, "force"),
				); err != nil {
					return vlanChangeRefusal(err, req.exp, "Unable to pin the alias for the "+req.exp+" experiment")
				}

				plog.Info(plog.TypeSystem, "vlan alias pinned", "alias", req.alias, "vlan", req.id, "exp", req.exp)
			case vlanAliasUnpin:
				return unpinVLANAlias(dry, req)
			}

			return nil
		},
	}

	cmd.Flags().BoolP(
		"force", "f", false,
		"Pin the alias even when another experiment or namespace uses the VLAN ID, minimega blacklists it, "+
			"or minimega does not answer",
	)
	cmd.Flags().Bool("unpin", false, "Clear the pin of the alias, so minimega gives it an ID at start")

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())
	dry = addDryRunFlag(cmd)

	return cmd
}

// viewVLANAliases writes the aliases that req asks for: those of every
// experiment, of one experiment, or the ID of one alias.
func viewVLANAliases(cmd *cobra.Command, out *outputFlag, req vlanAliasRequest) error {
	if req.action == vlanAliasLookup {
		id, err := vlanAliasID(req.exp, req.alias)
		if err != nil {
			return err
		}

		return out.printAPI(cmd, vlanAliasRow{Experiment: req.exp, Alias: req.alias, ID: id}, outputViews{
			formatTable: func(w io.Writer) error {
				fmt.Fprintln(w, id)

				return nil
			},
		})
	}

	var opts []vlan.Option

	msg := "Unable to display all aliases"

	if req.action == vlanAliasListExperiment {
		opts = append(opts, vlan.Experiment(req.exp))
		msg = "Unable to display aliases for the " + req.exp + " experiment"
	}

	info, err := vlan.Aliases(opts...)
	if err != nil {
		return util.HumanizeError(err, "%s", msg).Humanized()
	}

	return printVLANAliases(cmd, out, info)
}

// unpinVLANAlias unpins the alias of req, or, with --dry-run, checks the
// unpin and writes it.
func unpinVLANAlias(dry dryRun, req vlanAliasRequest) error {
	msg := "Unable to unpin the alias for the " + req.exp + " experiment"

	if dry.enabled() {
		if err := vlan.CheckUnpinAlias(req.exp, req.alias); err != nil {
			return vlanChangeRefusal(err, req.exp, msg)
		}

		dry.skip("unpin", "VLAN alias "+req.alias+" of experiment "+req.exp)

		return nil
	}

	if err := vlan.UnpinAlias(req.exp, req.alias); err != nil {
		return vlanChangeRefusal(err, req.exp, msg)
	}

	plog.Info(plog.TypeSystem, "vlan alias unpinned", "alias", req.alias, "exp", req.exp)

	return nil
}

// vlanConflictError is a refused VLAN change of an experiment, with every issue
// that caused the refusal.
type vlanConflictError struct {
	exp      string
	conflict *vlan.ConflictError
}

func (e vlanConflictError) Error() string {
	msgs := make([]string, 0, len(e.conflict.Issues))

	for _, i := range e.conflict.Issues {
		switch {
		case i.VLAN != nil && i.Alias != "":
			msgs = append(msgs, fmt.Sprintf("VLAN ID %d of alias %s: %s", *i.VLAN, i.Alias, i.Message))
		case i.Kind == vlan.KindRangeOverlap:
			msgs = append(msgs, "VLAN range "+i.Message)
		default:
			msgs = append(msgs, i.Message)
		}
	}

	return fmt.Sprintf(
		"VLAN conflict in experiment %s: %s Use --force to save the change anyway.",
		e.exp, strings.Join(msgs, " "),
	)
}

func (e vlanConflictError) Unwrap() error { return e.conflict }

// vlanChangeRefusal returns a conflict with all its issues, and other
// refusals as they are, since their message is the answer. It humanizes any
// other error.
func vlanChangeRefusal(err error, exp, msg string) error {
	var conflict *vlan.ConflictError
	if errors.As(err, &conflict) {
		return vlanConflictError{exp: exp, conflict: conflict}
	}

	if errors.Is(err, vlan.ErrMinimegaDown) || errors.Is(err, vlan.ErrAliasNotFound) {
		return err
	}

	return vlanRefusal(err, msg)
}

// vlanAliasID returns the VLAN ID of the given alias in the given
// experiment. A missing or unassigned alias is reported as is, since its
// message is the answer the user asked for.
func vlanAliasID(exp, alias string) (int, error) {
	id, err := vlan.AliasID(vlan.Experiment(exp), vlan.Alias(alias))
	if err != nil {
		if errors.Is(err, vlan.ErrAliasNotFound) || errors.Is(err, vlan.ErrAliasUnassigned) {
			return 0, err
		}

		err := util.HumanizeError(
			err,
			"%s",
			"Unable to get VLAN alias "+alias+" for the "+exp+" experiment",
		)

		return 0, err.Humanized()
	}

	return id, nil
}

// vlanRangeAction is what phenix vlan range does for its arguments.
type vlanRangeAction int

const (
	vlanRangeListAll vlanRangeAction = iota
	vlanRangeListExperiment
	vlanRangeSet
	vlanRangeClear
)

// vlanRangeRequest is a parsed phenix vlan range command line.
type vlanRangeRequest struct {
	action vlanRangeAction
	exp    string
	lo, hi int
}

// parseVLANRangeArgs parses the arguments of phenix vlan range: none, an
// experiment name, or an experiment name with a range minimum and maximum.
// With clearRange set, it takes only an experiment name.
func parseVLANRangeArgs(args []string, clearRange, force bool) (vlanRangeRequest, error) {
	var req vlanRangeRequest

	if clearRange {
		if len(args) != 1 {
			return req, fmt.Errorf("%w: --clear needs only an experiment name", errVLANArgs)
		}

		if force {
			return req, fmt.Errorf("%w: use --force only to set a range", errVLANArgs)
		}

		return vlanRangeRequest{action: vlanRangeClear, exp: args[0], lo: 0, hi: 0}, nil
	}

	if force && len(args) != rangeArgs {
		return req, fmt.Errorf("%w: use --force only to set a range", errVLANArgs)
	}

	switch len(args) {
	case 0:
		return vlanRangeRequest{action: vlanRangeListAll, exp: "", lo: 0, hi: 0}, nil
	case 1:
		return vlanRangeRequest{action: vlanRangeListExperiment, exp: args[0], lo: 0, hi: 0}, nil
	case rangeArgs:
		vmin, err := parseCLIVLANID(args[1])
		if err != nil {
			return req, fmt.Errorf("the VLAN range minimum: %w", err)
		}

		vmax, err := parseCLIVLANID(args[2])
		if err != nil {
			return req, fmt.Errorf("the VLAN range maximum: %w", err)
		}

		if vmin > vmax {
			return req, fmt.Errorf("%w: the VLAN range minimum %d is greater than the maximum %d", errBadVLANID, vmin, vmax)
		}

		return vlanRangeRequest{action: vlanRangeSet, exp: args[0], lo: vmin, hi: vmax}, nil
	default:
		return req, fmt.Errorf(
			"%w: give no arguments, an experiment name, or an experiment name with a range minimum and maximum",
			errVLANArgs,
		)
	}
}

// vlanRangeRow is the VLAN range of an experiment, as phenix vlan range -o
// json shows it.
type vlanRangeRow struct {
	Experiment string `json:"experiment" yaml:"experiment"`
	Min        int    `json:"min"        yaml:"min"`
	Max        int    `json:"max"        yaml:"max"`
}

// printVLANRanges writes the ranges of info, by experiment, in the format of out.
func printVLANRanges(cmd *cobra.Command, out *outputFlag, info map[string][2]int) error {
	rows := make([]vlanRangeRow, 0, len(info))

	for _, exp := range slices.Sorted(maps.Keys(info)) {
		rows = append(rows, vlanRangeRow{Experiment: exp, Min: info[exp][0], Max: info[exp][1]})
	}

	return out.printAPI(cmd, rows, outputViews{
		formatTable: func(w io.Writer) error {
			printer.PrintTableOfVLANRanges(w, info)

			return nil
		},
	})
}

// viewVLANRanges writes the ranges that req asks for: those of every
// experiment, or of one.
func viewVLANRanges(cmd *cobra.Command, out *outputFlag, req vlanRangeRequest) error {
	if req.action == vlanRangeListAll {
		info, err := vlan.Ranges()
		if err != nil {
			err := util.HumanizeError(err, "Unable to display VLAN range(s)")

			return err.Humanized()
		}

		return printVLANRanges(cmd, out, info)
	}

	info, err := vlan.Ranges(vlan.Experiment(req.exp))
	if err != nil {
		err := util.HumanizeError(
			err,
			"%s",
			"Unable to display VLAN range(s) for "+req.exp+" experiment",
		)

		return err.Humanized()
	}

	return printVLANRanges(cmd, out, info)
}

// clearVLANRange clears the range of the experiment of req, or, with
// --dry-run, checks the clear and writes it.
func clearVLANRange(dry dryRun, req vlanRangeRequest) error {
	msg := "Unable to clear the VLAN range for the " + req.exp + " experiment"

	if dry.enabled() {
		if err := vlan.CheckClearRange(req.exp); err != nil {
			return vlanChangeRefusal(err, req.exp, msg)
		}

		dry.skip("clear", "the VLAN range of experiment "+req.exp)

		return nil
	}

	if err := vlan.ClearRange(req.exp); err != nil {
		return vlanChangeRefusal(err, req.exp, msg)
	}

	plog.Info(plog.TypeSystem, "vlan range cleared", "exp", req.exp)

	return nil
}

func newVlanRangeCmd() *cobra.Command {
	var (
		out *outputFlag
		dry dryRun
	)

	desc := `View, set or clear the VLAN range of an experiment

  With no arguments, lists the VLAN range of every experiment. With an
  experiment name, lists the VLAN range of that experiment. With a range
  minimum and maximum as well, sets the VLAN range of the experiment and
  replaces its current range. With --clear, clears the VLAN range of the
  experiment.

  The experiment must be stopped to set or clear its range. A range that
  leaves out a pinned alias is refused. A range that overlaps the VLAN range
  of another experiment is refused. Use --force to set it anyway.

  -o json and -o yaml print each range as an object with its experiment,
  minimum and maximum. --dry-run with --clear checks the clear and changes
  nothing.`

	example := `
  phenix vlan range
  phenix vlan range <experiment name>
  phenix vlan range <experiment name> <range minimum> <range maximum>
  phenix vlan range <experiment name> --clear`

	cmd := &cobra.Command{
		Use:               "range [experiment name [range minimum range maximum]]",
		Short:             "View, set or clear the VLAN range of an experiment",
		Long:              desc,
		Example:           example,
		ValidArgsFunction: vlanRangeArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := parseVLANRangeArgs(
				args, mustGet(cmd.Flags().GetBool, "clear"), mustGet(cmd.Flags().GetBool, "force"),
			)
			if err != nil {
				return err
			}

			view := req.action == vlanRangeListAll || req.action == vlanRangeListExperiment
			if err := checkVLANViewFlags(cmd, dry, view, req.action == vlanRangeClear, "--clear"); err != nil {
				return err
			}

			switch req.action {
			case vlanRangeListAll, vlanRangeListExperiment:
				return viewVLANRanges(cmd, out, req)
			case vlanRangeSet:
				if err := vlan.ChangeRange(
					context.Background(), cliView(), req.exp, req.lo, req.hi, mustGet(cmd.Flags().GetBool, "force"),
				); err != nil {
					return vlanChangeRefusal(err, req.exp, "Unable to set the VLAN range for the "+req.exp+" experiment")
				}

				plog.Info(plog.TypeSystem, "vlan range set", "exp", req.exp, "min", req.lo, "max", req.hi)
			case vlanRangeClear:
				return clearVLANRange(dry, req)
			}

			return nil
		},
	}

	cmd.Flags().BoolP("force", "f", false, "Set the range even when it overlaps the VLAN range of another experiment")
	cmd.Flags().Bool("clear", false, "Clear the VLAN range of the experiment")

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())
	dry = addDryRunFlag(cmd)

	return cmd
}

// vlanRangeArgsCompletion completes the experiment name only.
func vlanRangeArgsCompletion(
	cmd *cobra.Command,
	args []string,
	toComplete string,
) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return expNameCompletion(false)(cmd, args, toComplete)
	}

	return nil, cobra.ShellCompDirectiveNoFileComp
}

func init() { //nolint:gochecknoinits // cobra command
	vlanCmd := newVlanCmd()

	vlanCmd.AddCommand(newVlanAliasCmd())
	vlanCmd.AddCommand(newVlanRangeCmd())
	vlanCmd.AddCommand(newVlanListCmd(), newVlanBlacklistCmd(), newVlanReleaseCmd(), newVlanPinLiveCmd())

	addCommandToRoot(vlanCmd, true)
}
