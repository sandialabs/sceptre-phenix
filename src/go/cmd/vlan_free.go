package cmd

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"strconv"
	"strings"
	"unicode"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"phenix/api/vlan"
	"phenix/util"
)

var (
	// errBadVLANAlias is returned for an --alias value that is not name=id.
	errBadVLANAlias = errors.New("not a VLAN alias pin")
	// errBadVLANRange is returned for a --range value that is not min-max.
	errBadVLANRange = errors.New("not a VLAN range")
)

// parseVLANAliasPin parses an --alias value of phenix vlan check: an alias
// name, "=", and a VLAN ID, or 0 for an alias with no pin. The name is not
// empty and has no space, control character or "=".
func parseVLANAliasPin(value string) (string, int, error) {
	alias, raw, ok := strings.Cut(value, "=")
	if !ok || alias == "" || strings.ContainsFunc(alias, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return "", 0, fmt.Errorf("%w: %q is not <alias>=<vlan id>, such as EXP_1=101", errBadVLANAlias, value)
	}

	id, err := strconv.Atoi(raw)
	if err != nil || (id != 0 && !vlan.ValidID(id)) {
		return "", 0, fmt.Errorf("%w: the VLAN ID of %q is not 0 (no pin) or a whole number in %d–%d",
			errBadVLANAlias, value, vlan.MinID, vlan.MaxID)
	}

	return alias, id, nil
}

// parseVLANRange parses a --range value of phenix vlan check: two VLAN IDs
// joined by "-", lowest first.
func parseVLANRange(value string) (int, int, error) {
	bad := fmt.Errorf("%w: %q is not <min>-<max> with VLAN IDs in %d–%d, lowest first, such as 100-200",
		errBadVLANRange, value, vlan.MinID, vlan.MaxID)

	rawLo, rawHi, ok := strings.Cut(value, "-")
	if !ok {
		return 0, 0, bad
	}

	lo, errLo := strconv.Atoi(rawLo)
	hi, errHi := strconv.Atoi(rawHi)

	if errLo != nil || errHi != nil || !vlan.ValidID(lo) || !vlan.ValidID(hi) || lo > hi {
		return 0, 0, bad
	}

	return lo, hi, nil
}

// vlanProposal is the VLAN check of the experiment name: its stored pins and
// range, when in has the experiment, with each of the aliases pinned and the
// range replaced when lo is not 0.
func vlanProposal(in vlan.Inputs, name string, aliases map[string]int, lo, hi int) vlan.Proposal {
	p := vlan.Proposal{Experiment: name, Aliases: map[string]int{}, Min: 0, Max: 0}

	for _, e := range in.Experiments {
		if e.Name == name {
			maps.Copy(p.Aliases, e.Pins)
			p.Min, p.Max = e.Min, e.Max
		}
	}

	maps.Copy(p.Aliases, aliases)

	if lo != 0 {
		p.Min, p.Max = lo, hi
	}

	return p
}

// vlanBlock is a block of free VLAN IDs, as GET /vlans/free gives it.
type vlanBlock struct {
	Start int `json:"start" yaml:"start"`
	End   int `json:"end"   yaml:"end"`
}

// vlanIssues is the issues of a VLAN check, as POST /vlans/check gives them.
type vlanIssues struct {
	Issues []vlan.Issue `json:"issues" yaml:"issues"`
}

func newVlanFreeCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "free",
		Short: "Find a block of free VLAN IDs",
		Long: `Find a block of free VLAN IDs

  Prints the first block of --count VLAN IDs in a row, from --min to --max,
  that no experiment pins, minimega does not use or blacklist, and, with
  --outside-ranges, no VLAN range of an experiment holds. -o json and
  -o yaml print the body of GET /vlans/free.`,
		Example: `
  phenix vlan free
  phenix vlan free --count 4 --min 100 --max 199
  phenix vlan free --outside-ranges`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var (
				count   = mustGet(cmd.Flags().GetInt, "count")
				lo      = mustGet(cmd.Flags().GetInt, "min")
				hi      = mustGet(cmd.Flags().GetInt, "max")
				outside = mustGet(cmd.Flags().GetBool, "outside-ranges")
			)

			if count < 1 || count > vlan.MaxID {
				return fmt.Errorf("--count must be a whole number from 1 to %d, not %d", vlan.MaxID, count)
			}

			if !vlan.ValidID(lo) || !vlan.ValidID(hi) || lo > hi {
				return fmt.Errorf("--min %d and --max %d must be VLAN IDs in %d–%d, lowest first", lo, hi, vlan.MinID, vlan.MaxID)
			}

			in, _, err := vlan.Read(commandContext(cmd))
			if err != nil {
				return util.HumanizeError(err, "Unable to read the VLANs").Humanized()
			}

			start, end, err := vlan.FindFree(in, count, lo, hi, outside)
			if err != nil {
				return fmt.Errorf("no %d free VLAN IDs in a row in %d–%d: %w", count, lo, hi, err)
			}

			return out.printAPI(cmd, vlanBlock{Start: start, End: end}, outputViews{
				formatTable: func(w io.Writer) error {
					table := tablewriter.NewWriter(w)
					table.SetHeader([]string{"Start", "End"})
					table.Append([]string{strconv.Itoa(start), strconv.Itoa(end)})
					table.Render()

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	cmd.Flags().Int("count", 1, "Number of VLAN IDs in a row to find")
	cmd.Flags().Int("min", vlan.MinID, "Lowest VLAN ID to give")
	cmd.Flags().Int("max", vlan.MaxID, "Highest VLAN ID to give")
	cmd.Flags().Bool("outside-ranges", false, "Give only VLAN IDs outside the VLAN range of each experiment")

	return cmd
}

func newVlanCheckCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "check <experiment name>",
		Short: "Check VLAN pins and a VLAN range of an experiment against the cluster",
		Long: `Check VLAN pins and a VLAN range of an experiment against the cluster

  Starts from the VLAN pins and the VLAN range that the experiment has, pins
  each --alias, and replaces the range with --range. Then it lists the issues
  that the experiment would have in the cluster: duplicate pins, blacklisted
  pins, range overlaps and pins outside the range. The command changes
  nothing. The experiment does not have to exist. An --alias with the VLAN
  ID 0 has no pin. -o json and -o yaml print the body of POST /vlans/check.`,
		Example: `
  phenix vlan check my-exp
  phenix vlan check my-exp --alias EXP_1=101 --alias EXP_2=102
  phenix vlan check my-exp --range 100-199`,
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: expNameCompletion(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			aliases := map[string]int{}

			for _, value := range mustGet(cmd.Flags().GetStringArray, "alias") {
				alias, id, err := parseVLANAliasPin(value)
				if err != nil {
					return err
				}

				aliases[alias] = id
			}

			var lo, hi int

			if value := mustGet(cmd.Flags().GetString, "range"); value != "" {
				var err error
				if lo, hi, err = parseVLANRange(value); err != nil {
					return err
				}
			}

			in, _, err := vlan.Read(commandContext(cmd))
			if err != nil {
				return util.HumanizeError(err, "Unable to read the VLANs").Humanized()
			}

			p := vlanProposal(in, args[0], aliases, lo, hi)
			if err := vlan.CheckProposal(p); err != nil {
				return err //nolint:wrapcheck // the reason is the answer
			}

			issues := nonNil(vlan.Check(in, cliView(), p))

			return out.printAPI(cmd, vlanIssues{Issues: issues}, outputViews{
				formatTable: func(w io.Writer) error {
					if len(issues) == 0 {
						fmt.Fprintf(w, "The VLAN pins and range of %s have no issues.\n", args[0])

						return nil
					}

					printVLANIssues(w, issues)

					return nil
				},
			})
		},
	}

	cmd.Flags().StringArray("alias", nil, "Pin a VLAN alias to a VLAN ID, as <alias>=<vlan id> (repeatable)")
	cmd.Flags().String("range", "", "Replace the VLAN range, as <min>-<max>")

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	_ = cmd.RegisterFlagCompletionFunc("alias", completeVLANAliasPins)

	return cmd
}

// completeVLANAliasPins completes the VLAN aliases of the experiment in the
// first argument as <alias>=, for an --alias of phenix vlan check.
func completeVLANAliasPins(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	aliases, _ := completeVLANAliases(cmd, args, toComplete)

	pins := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		pins = append(pins, alias+"=")
	}

	return pins, cobra.ShellCompDirectiveNoSpace | cobra.ShellCompDirectiveNoFileComp
}

func init() { //nolint:gochecknoinits // cobra command
	subcommandOf("vlan").AddCommand(newVlanFreeCmd(), newVlanCheckCmd())
}
