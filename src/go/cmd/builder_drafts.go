package cmd

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Statuses of a preflight check that are not a pass.
const (
	preflightFailed      = "failed"
	preflightUnavailable = "unavailable"
)

// builderSuggestionDistance is how many edits a misspelled subcommand may
// be from one that exists for the error to suggest it: what cobra suggests
// within for an unknown command of phenix itself.
const builderSuggestionDistance = 2

// builderPreflightChecks are the checks a preflight runs, as its route names
// them.
func builderPreflightChecks() []string {
	return []string{"capacity", "network", "disks", "apps"}
}

// builderPackageParts are what a Builder package may hold besides the
// document, as the package route names them.
func builderPackageParts() []string {
	return []string{"scenarios", "topologies", "icons", "images"}
}

func newBuilderDraftsCmd() *cobra.Command {
	desc := `Builder drafts on a phenix server

  These subcommands list drafts, export one, check whether one can be
  published, and run the preflight checks of one, through the REST API of
  a running phenix server. They need no browser, so scripts and CI can use
  them.

  With --url (or PHENIX_URL), the requests go to that server and act as
  the user of the API token in --token (or PHENIX_TOKEN): the user's
  permissions decide what they may see and do. A user creates tokens in the
  Users tab of the phenix web UI. A server with sign-in off needs no token.
  Without --url, the requests go to the unix socket of phenix ui on this
  host (the global --unix-socket flag, default /tmp/phenix.sock), where they
  act as global-admin, as phenix workflow apply does; a token without --url
  is refused. A flag given an empty value counts as not given.

  A draft is named <owner>/<draft id>, as drafts list shows it.

  Exit status: 0 on success; 1 when the server answered with findings (a
  draft that cannot be published, a failed preflight check); 2 when the
  request could not be made or was refused (connection, authentication,
  permission, not found, invalid input).`

	cmd := &cobra.Command{
		Use:   "drafts",
		Short: "List, export, validate and preflight Builder drafts on a phenix server",
		Long:  desc,
		Args:  refusedArgs(unknownSubcommand),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		SuggestionsMinimumDistance: builderSuggestionDistance,
	}

	cmd.SetFlagErrorFunc(builderFlagError)

	cmd.AddCommand(
		newBuilderDraftsListCmd(),
		newBuilderDraftsExportCmd(),
		newBuilderDraftsValidateCmd(),
		newBuilderDraftsPreflightCmd(),
	)

	return cmd
}

func newBuilderDraftsListCmd() *cobra.Command {
	desc := `List drafts

  Lists the caller's own drafts. --shared adds the drafts of other users
  the caller may see: those shared with it, and those its role may list.
  --owner lists only the drafts of that user, from either. Drafts are
  sorted by owner, then by ID.`

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List drafts",
		Long:  desc,
		Example: `  phenix builder drafts list
  phenix builder drafts list --shared -o json
  phenix builder drafts list --owner bob --url https://phenix.example --token "$PHENIX_TOKEN"`,
		Args: refusedArgs(cobra.NoArgs),
		RunE: runBuilderDraftsList,
	}

	addBuilderServerFlags(cmd)
	addBuilderOutputFlag(cmd)
	cmd.Flags().String("owner", "", "List only the drafts of this user")
	cmd.Flags().Bool("shared", false, "Also list the drafts of other users the caller may see")

	return cmd
}

func newBuilderDraftsExportCmd() *cobra.Command {
	desc := `Export a draft

  Writes the current document of a draft, as Builder JSON (the default) or
  Builder YAML, to standard output or to the file --output names. The same
  draft always gives the same bytes. With --package, it writes the Builder
  package the server makes of the document instead, holding the parts
  --include names: scenarios, topologies, icons, images.`

	cmd := &cobra.Command{
		Use:   "export <owner>/<draft>",
		Short: "Export a draft as Builder JSON or YAML",
		Long:  desc,
		Example: `  phenix builder drafts export alice/riverside > riverside.builder.json
  phenix builder drafts export alice/riverside --format yaml --output riverside.builder.yaml
  phenix builder drafts export alice/riverside --package --include scenarios,icons`,
		Args: refusedArgs(cobra.ExactArgs(1)),
		RunE: runBuilderDraftsExport,
	}

	addBuilderServerFlags(cmd)
	cmd.Flags().String("format", FormatJSON, "Format of the export: json or yaml")
	cmd.Flags().String("output", "", "File to write (default: standard output)")
	cmd.Flags().Bool("package", false, "Export the Builder package of the document")
	cmd.Flags().StringSlice("include", nil, "Parts a package holds besides the document: scenarios, topologies, icons, images")

	return cmd
}

func newBuilderDraftsValidateCmd() *cobra.Command {
	desc := `Check whether a draft can be published

  Checks the current document of a draft as the Builder's Topology YAML
  download does: errors are what would block publishing it, warnings what
  publishing would warn about. Nothing is written. It exits 1 when the
  draft has errors.`

	cmd := &cobra.Command{
		Use:   "validate <owner>/<draft>",
		Short: "Check whether a draft can be published",
		Long:  desc,
		Example: `  phenix builder drafts validate alice/riverside
  phenix builder drafts validate alice/riverside --url https://phenix.example --token "$PHENIX_TOKEN" -o json`,
		Args: refusedArgs(cobra.ExactArgs(1)),
		RunE: runBuilderDraftsValidate,
	}

	addBuilderServerFlags(cmd)
	addBuilderOutputFlag(cmd)

	return cmd
}

func newBuilderDraftsPreflightCmd() *cobra.Command {
	desc := `Run the preflight checks of a draft

  Asks the server whether an experiment of the draft could run: whether
  the cluster has the capacity, the networks, the disk images and the apps
  it needs. Each check passes, fails, or is unavailable (the server could
  not run it). It exits 1 when a check fails, and with --strict also when a
  check is unavailable.`

	cmd := &cobra.Command{
		Use:   "preflight <owner>/<draft>",
		Short: "Run the preflight checks of a draft",
		Long:  desc,
		Example: `  phenix builder drafts preflight alice/riverside
  phenix builder drafts preflight alice/riverside --check capacity,disks --experiment riverside --strict -o json`,
		Args: refusedArgs(cobra.ExactArgs(1)),
		RunE: runBuilderDraftsPreflight,
	}

	addBuilderServerFlags(cmd)
	addBuilderOutputFlag(cmd)
	cmd.Flags().StringSlice("check", builderPreflightChecks(), "Checks to run: capacity, network, disks, apps")
	cmd.Flags().String("experiment", "", "Experiment the checks are for")
	cmd.Flags().Bool("strict", false, "Exit 1 when a check is unavailable too")

	return cmd
}

// addBuilderOutputFlag adds -o, the output format of a report.
func addBuilderOutputFlag(cmd *cobra.Command) {
	cmd.Flags().StringP("output", "o", formatTable, "Output format: table, json or yaml")
}

// builderFlagError makes an error of the flags end phenix with
// [exitRefused]. A request for help stays as it is, which cobra answers
// with the help.
func builderFlagError(_ *cobra.Command, err error) error {
	if errors.Is(err, pflag.ErrHelp) {
		return err
	}

	return refused(err)
}

// unknownSubcommand refuses every argument of a command that only groups
// subcommands, as cobra refuses an unknown command of phenix itself: one
// that reaches the group names no subcommand of it, such as a misspelled
// one. Without it, cobra would print the group's help and exit 0.
func unknownSubcommand(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}

	message := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())

	if suggestions := cmd.SuggestionsFor(args[0]); len(suggestions) > 0 {
		message += "\n\nDid you mean this?\n\t" + strings.Join(suggestions, "\n\t")
	}

	return errors.New(message)
}

// refusedArgs checks the arguments with validate, and makes its error end
// phenix with [exitRefused].
func refusedArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	check := argsWithUsage(validate)

	return func(cmd *cobra.Command, args []string) error {
		return refused(check(cmd, args))
	}
}

// builderReportSetup returns the output format -o names and the client of
// the server cmd's flags name.
func builderReportSetup(cmd *cobra.Command) (string, *builderClient, error) {
	format, err := builderOutputFormat(cmd)
	if err != nil {
		return "", nil, err
	}

	client, err := builderClientFor(cmd)
	if err != nil {
		return "", nil, err
	}

	return format, client, nil
}

// builderCount is n and the noun, plural unless n is 1.
func builderCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return strconv.Itoa(n) + " " + noun + "s"
}

// newBuilderTable returns a table with the given header, whose cells are
// never wrapped.
func newBuilderTable(out io.Writer, header ...string) *tablewriter.Table {
	table := tablewriter.NewWriter(out)

	table.SetHeader(header)
	table.SetAutoWrapText(false)

	return table
}

// builderDraftRef names a draft.
type builderDraftRef struct {
	Owner string `json:"owner"`
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
}

// parseBuilderDraftRef reads a draft's name, "<owner>/<draft id>".
func parseBuilderDraftRef(name string) (builderDraftRef, error) {
	owner, id, ok := strings.Cut(name, "/")
	if !ok || owner == "" || id == "" || strings.Contains(id, "/") {
		return builderDraftRef{}, refused(fmt.Errorf("%q does not name a draft as <owner>/<draft id>, such as alice/riverside", name))
	}

	return builderDraftRef{Owner: owner, ID: id, Name: ""}, nil
}

// String is the draft's name, "<owner>/<draft id>".
func (r builderDraftRef) String() string {
	return r.Owner + "/" + r.ID
}

// path is the API path of the draft.
func (r builderDraftRef) path() string {
	return "/builder/drafts/" + url.PathEscape(r.Owner) + "/" + url.PathEscape(r.ID)
}

// titled is the draft's name, with its title after it when it has one.
func (r builderDraftRef) titled() string {
	if r.Name == "" {
		return r.String()
	}

	return r.String() + " (" + r.Name + ")"
}

// builderFetchedDraft is what the commands read of a draft.
type builderFetchedDraft struct {
	Title    string          `json:"title"`
	Document json.RawMessage `json:"document"`
}

// fetchBuilderDraft reads a draft and its current document. The draft's
// title becomes ref's name.
func fetchBuilderDraft(ctx context.Context, client *builderClient, ref *builderDraftRef) (json.RawMessage, error) {
	var draft builderFetchedDraft

	if err := client.get(ctx, ref.path(), &draft); err != nil {
		return nil, refused(err)
	}

	if len(draft.Document) == 0 {
		return nil, refused(fmt.Errorf("the phenix server answered with no document for draft %s", ref))
	}

	ref.Name = draft.Title

	return draft.Document, nil
}

// builderDraftRow is one draft of drafts list.
type builderDraftRow struct {
	Owner     string `json:"owner"`
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	UpdatedBy string `json:"updatedBy,omitempty"`
	Access    string `json:"access,omitempty"`
}

// builderDraftList is the report of drafts list.
type builderDraftList struct {
	Drafts []builderDraftRow `json:"drafts"`
}

// builderListedDraft is what drafts list reads of a draft the server lists.
type builderListedDraft struct {
	ID             string `json:"id"`
	Owner          string `json:"owner"`
	Title          string `json:"title"`
	Updated        string `json:"updated"`
	LastModifiedBy string `json:"lastModifiedBy"`
	Access         string `json:"access"`
}

// newBuilderDraftList returns the report of the caller's own drafts and,
// when withShared is set or owner names a user, of the shared drafts too;
// only those of owner when it is not empty.
func newBuilderDraftList(own, shared []builderListedDraft, owner string, withShared bool) builderDraftList {
	listed := own
	if withShared || owner != "" {
		listed = slices.Concat(own, shared)
	}

	rows := make([]builderDraftRow, 0, len(listed))

	for _, draft := range listed {
		if owner != "" && draft.Owner != owner {
			continue
		}

		rows = append(rows, builderDraftRow{
			Owner:     draft.Owner,
			ID:        draft.ID,
			Name:      draft.Title,
			UpdatedAt: draft.Updated,
			UpdatedBy: draft.LastModifiedBy,
			Access:    draft.Access,
		})
	}

	slices.SortStableFunc(rows, func(a, b builderDraftRow) int {
		return cmp.Or(strings.Compare(a.Owner, b.Owner), strings.Compare(a.ID, b.ID))
	})

	return builderDraftList{Drafts: rows}
}

// table writes the drafts as a table.
func (l builderDraftList) table(out io.Writer) {
	table := newBuilderTable(out, "Owner", "Draft", "Name", "Updated", "Updated by", "Access")

	for _, row := range l.Drafts {
		table.Append([]string{row.Owner, row.ID, row.Name, row.UpdatedAt, row.UpdatedBy, row.Access})
	}

	table.Render()
}

// runBuilderDraftsList runs drafts list.
func runBuilderDraftsList(cmd *cobra.Command, _ []string) error {
	format, client, err := builderReportSetup(cmd)
	if err != nil {
		return err
	}

	var listing struct {
		Drafts []builderListedDraft `json:"drafts"`
		Shared []builderListedDraft `json:"shared"`
	}

	if err := client.get(cmd.Context(), "/builder/drafts", &listing); err != nil {
		return refused(err)
	}

	list := newBuilderDraftList(
		listing.Drafts, listing.Shared, MustGetString(cmd.Flags(), "owner"), MustGetBool(cmd.Flags(), "shared"),
	)

	return writeBuilderOutput(cmd.OutOrStdout(), format, list, list.table)
}

// builderPackageRequest asks for the Builder package of a document.
type builderPackageRequest struct {
	Document json.RawMessage `json:"document"`
	Include  []string        `json:"include"`
}

// runBuilderDraftsExport runs drafts export.
func runBuilderDraftsExport(cmd *cobra.Command, args []string) error {
	format := MustGetString(cmd.Flags(), "format")
	if format != FormatJSON && format != FormatYAML {
		return refused(fmt.Errorf("--format %q is not json or yaml", format))
	}

	include, err := cmd.Flags().GetStringSlice("include")
	if err != nil {
		return refused(fmt.Errorf("reading --include: %w", err))
	}

	pack := MustGetBool(cmd.Flags(), "package")

	if len(include) > 0 && !pack {
		return refused(errors.New("--include names the parts of a package: give --package too"))
	}

	for _, part := range include {
		if !slices.Contains(builderPackageParts(), part) {
			return refused(fmt.Errorf("--include %q is not one of %s", part, strings.Join(builderPackageParts(), ", ")))
		}
	}

	ref, err := parseBuilderDraftRef(args[0])
	if err != nil {
		return err
	}

	client, err := builderClientFor(cmd)
	if err != nil {
		return err
	}

	data, err := fetchBuilderDraft(cmd.Context(), client, &ref)
	if err != nil {
		return err
	}

	if pack {
		request := builderPackageRequest{Document: data, Include: builderNonNil(include)}

		if err := client.post(cmd.Context(), "/builder/package", request, &data); err != nil {
			return refused(err)
		}
	}

	text, err := builderExportText(data, format)
	if err != nil {
		return err
	}

	return writeBuilderFile(cmd.OutOrStdout(), MustGetString(cmd.Flags(), "output"), text)
}

// builderExportText is the JSON text data as format writes it: JSON indented
// by two spaces, in the server's key order, or YAML in the same order.
func builderExportText(data []byte, format string) ([]byte, error) {
	if format == FormatYAML {
		return builderYAML(data)
	}

	var buffer bytes.Buffer

	if err := json.Indent(&buffer, data, "", "  "); err != nil {
		return nil, fmt.Errorf("the phenix server answered with JSON this phenix cannot read: %w", err)
	}

	buffer.WriteByte('\n')

	return buffer.Bytes(), nil
}

// writeBuilderFile writes text to the file at path, or to out when path is
// empty.
func writeBuilderFile(out io.Writer, path string, text []byte) error {
	if path == "" {
		if _, err := out.Write(text); err != nil {
			return fmt.Errorf("writing the output: %w", err)
		}

		return nil
	}

	// Readable by anyone, as a file a shell redirection writes.
	if err := os.WriteFile(path, text, 0o644); err != nil {
		return refused(fmt.Errorf("writing %s: %w", path, err))
	}

	return nil
}

// builderValidation is the report of drafts validate: Errors would block
// publishing the draft, and Warnings are what publishing it warns about.
type builderValidation struct {
	Draft    builderDraftRef `json:"draft"`
	Valid    bool            `json:"valid"`
	Errors   []builderIssue  `json:"errors"`
	Warnings []builderIssue  `json:"warnings"`
}

// builderExportRequest asks for the Topology config a document publishes as.
type builderExportRequest struct {
	Document json.RawMessage `json:"document"`
}

// runBuilderDraftsValidate runs drafts validate.
func runBuilderDraftsValidate(cmd *cobra.Command, args []string) error {
	ref, err := parseBuilderDraftRef(args[0])
	if err != nil {
		return err
	}

	format, client, err := builderReportSetup(cmd)
	if err != nil {
		return err
	}

	document, err := fetchBuilderDraft(cmd.Context(), client, &ref)
	if err != nil {
		return err
	}

	report, err := validateBuilderDocument(cmd.Context(), client, ref, document)
	if err != nil {
		return refused(err)
	}

	if err := writeBuilderOutput(cmd.OutOrStdout(), format, report, report.table); err != nil {
		return err
	}

	if !report.Valid {
		return &exitError{
			code: exitFindings,
			err:  fmt.Errorf("draft %s has %s that block publishing", ref, builderCount(len(report.Errors), builderSeverityError)),
		}
	}

	return nil
}

// validateBuilderDocument checks document, the current document of the
// draft ref names, through POST /builder/export/topology: its publish
// blockers are the report's errors, and its warnings the report's warnings.
// A document the route refuses with 422 is invalid: the refusal's issues are
// the report's errors and warnings, by their severity, and when none is an
// error, the refusal's message and cause are its one error.
func validateBuilderDocument(
	ctx context.Context,
	client *builderClient,
	ref builderDraftRef,
	document json.RawMessage,
) (builderValidation, error) {
	var answer struct {
		Warnings        []json.RawMessage `json:"warnings"`
		PublishBlockers []json.RawMessage `json:"publishBlockers"`
	}

	report := builderValidation{Draft: ref, Valid: false, Errors: []builderIssue{}, Warnings: []builderIssue{}}

	var apiErr *builderAPIError

	err := client.post(ctx, "/builder/export/topology", builderExportRequest{Document: document}, &answer)

	switch {
	case errors.As(err, &apiErr) && apiErr.Status == http.StatusUnprocessableEntity:
		for _, issue := range apiErr.Issues {
			if issue.Severity == builderSeverityWarning {
				report.Warnings = append(report.Warnings, issue)
			} else {
				report.Errors = append(report.Errors, issue)
			}
		}

		if len(report.Errors) == 0 {
			report.Errors = append(report.Errors, newBuilderIssue(builderSeverityError, apiErr.Code, apiErr.reason()))
		}
	case err != nil:
		return report, err
	default:
		report.Errors = builderIssues(answer.PublishBlockers, builderSeverityError)
		report.Warnings = builderIssues(answer.Warnings, builderSeverityWarning)
	}

	report.Valid = len(report.Errors) == 0

	return report, nil
}

// table writes the verdict, then the errors and the warnings as a table.
func (v builderValidation) table(out io.Writer) {
	verdict := "can be published"
	if !v.Valid {
		verdict = "cannot be published"
	}

	fmt.Fprintf(
		out, "Draft %s %s: %s, %s.\n", v.Draft.titled(), verdict,
		builderCount(len(v.Errors), builderSeverityError), builderCount(len(v.Warnings), builderSeverityWarning),
	)

	if len(v.Errors)+len(v.Warnings) == 0 {
		return
	}

	table := newBuilderTable(out, "Severity", "Code", "Element", "Message")

	for _, issue := range slices.Concat(v.Errors, v.Warnings) {
		table.Append([]string{issue.Severity, issue.Code, issue.element(), issue.Message})
	}

	table.Render()
}

// builderPreflightRequest asks for preflight checks of a draft.
type builderPreflightRequest struct {
	Checks     []string `json:"checks"`
	Experiment string   `json:"experiment,omitempty"`
}

// builderPreflightCheck is one check of a preflight: its status is
// "passed", "failed" or "unavailable".
type builderPreflightCheck struct {
	Name    string         `json:"name"`
	Status  string         `json:"status"`
	Summary string         `json:"summary"`
	Issues  []builderIssue `json:"issues"`
}

// builderPreflight is the answer of the preflight route, and the report of
// drafts preflight.
type builderPreflight struct {
	Checks      []builderPreflightCheck `json:"checks"`
	Passed      []string                `json:"passed"`
	Failed      []string                `json:"failed"`
	Unavailable []string                `json:"unavailable"`
}

// runBuilderDraftsPreflight runs drafts preflight.
func runBuilderDraftsPreflight(cmd *cobra.Command, args []string) error {
	ref, err := parseBuilderDraftRef(args[0])
	if err != nil {
		return err
	}

	checks, err := cmd.Flags().GetStringSlice("check")
	if err != nil {
		return refused(fmt.Errorf("reading --check: %w", err))
	}

	if len(checks) == 0 {
		return refused(errors.New("--check names no check"))
	}

	for _, check := range checks {
		if !slices.Contains(builderPreflightChecks(), check) {
			return refused(fmt.Errorf("--check %q is not one of %s", check, strings.Join(builderPreflightChecks(), ", ")))
		}
	}

	format, client, err := builderReportSetup(cmd)
	if err != nil {
		return err
	}

	var report builderPreflight

	request := builderPreflightRequest{Checks: checks, Experiment: MustGetString(cmd.Flags(), "experiment")}

	if err := client.post(cmd.Context(), ref.path()+"/preflight", request, &report); err != nil {
		return refused(err)
	}

	report.normalize()

	if err := writeBuilderOutput(cmd.OutOrStdout(), format, report, report.table); err != nil {
		return err
	}

	return report.outcome(ref, MustGetBool(cmd.Flags(), "strict"))
}

// normalize gives every list of the report a value, so the output shows an
// empty list rather than null.
func (p *builderPreflight) normalize() {
	p.Checks = builderNonNil(p.Checks)
	p.Passed = builderNonNil(p.Passed)
	p.Failed = builderNonNil(p.Failed)
	p.Unavailable = builderNonNil(p.Unavailable)

	for i := range p.Checks {
		p.Checks[i].Issues = builderNonNil(p.Checks[i].Issues)
	}
}

// outcome is nil when no check failed, and with strict none was unavailable;
// else an error that ends phenix with [exitFindings] and names the checks.
func (p *builderPreflight) outcome(ref builderDraftRef, strict bool) error {
	failed, unavailable := slices.Clone(p.Failed), slices.Clone(p.Unavailable)

	for _, check := range p.Checks {
		switch check.Status {
		case preflightFailed:
			failed = append(failed, check.Name)
		case preflightUnavailable:
			unavailable = append(unavailable, check.Name)
		}
	}

	slices.Sort(failed)
	slices.Sort(unavailable)

	failed, unavailable = slices.Compact(failed), slices.Compact(unavailable)

	switch {
	case len(failed) > 0:
		return &exitError{
			code: exitFindings,
			err:  fmt.Errorf("preflight of draft %s: failed: %s", ref, strings.Join(failed, ", ")),
		}
	case strict && len(unavailable) > 0:
		return &exitError{
			code: exitFindings,
			err: fmt.Errorf(
				"preflight of draft %s: unavailable, which --strict counts as failed: %s", ref, strings.Join(unavailable, ", "),
			),
		}
	}

	return nil
}

// table writes each check's status and summary, then the issues of the
// checks.
func (p builderPreflight) table(out io.Writer) {
	checks := newBuilderTable(out, "Check", "Status", "Summary")

	for _, check := range p.Checks {
		checks.Append([]string{check.Name, check.Status, check.Summary})
	}

	checks.Render()

	issues := newBuilderTable(out, "Check", "Severity", "Code", "Element", "Message")
	found := false

	for _, check := range p.Checks {
		for _, issue := range check.Issues {
			issues.Append([]string{check.Name, issue.Severity, issue.Code, issue.element(), issue.Message})

			found = true
		}
	}

	if found {
		issues.Render()
	}
}
