package cmd

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	bdoc "phenix/types/builder"
)

// Sources of an item of the template listing, as the server names them.
const (
	listedSourceOwn       = "own"
	listedSourceShared    = "shared"
	listedSourceServer    = "server"
	listedSourcePreloaded = "preloaded"
)

// Sources of a template or collection, as templates list prints them.
const (
	// templateSourceMine: the caller's own library.
	templateSourceMine = "mine"
	// templateSourceBuiltin: one of the built-in templates in the caller's
	// own library.
	templateSourceBuiltin = "built-in"
	// templateSourceShared: another user's, shared with the caller.
	templateSourceShared = "shared"
	// templateSourceServerWide: another user's, published to every user.
	templateSourceServerWide = "server-wide"
	// templateSourceServer: read by the server from its template files.
	templateSourceServer = "server"
)

const (
	// builderTemplatesFileName names the collection of a template file that
	// exports templates of no one collection.
	builderTemplatesFileName = "Node templates"

	// firstNumberedName is the number of the first name numbered apart from
	// another: the second of that name.
	firstNumberedName = 2
)

func newBuilderTemplatesCmd() *cobra.Command {
	desc := `Builder Node Template libraries on a phenix server

  These subcommands list the Node Templates a user can use, export them as
  a template file, and import a template file into the user's library,
  through the REST API of a running phenix server.

  With --url (or PHENIX_URL), the requests go to that server and act as
  the user of the API token in --token (or PHENIX_TOKEN). A server with
  sign-in off needs no token. Without --url, the requests go to the unix
  socket of phenix ui on this host (the global --unix-socket flag), where
  they act as global-admin; a token without --url is refused. A flag given
  an empty value counts as not given.

  Exit status: 0 on success; 2 when the request could not be made or was
  refused (connection, authentication, permission, not found, invalid
  input).`

	cmd := &cobra.Command{
		Use:   "templates",
		Short: "List, export and import Builder Node Templates on a phenix server",
		Long:  desc,
		Args:  refusedArgs(unknownSubcommand),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		SuggestionsMinimumDistance: builderSuggestionDistance,
	}

	cmd.SetFlagErrorFunc(builderFlagError)

	cmd.AddCommand(newBuilderTemplatesListCmd(), newBuilderTemplatesExportCmd(), newBuilderTemplatesImportCmd())

	return cmd
}

func newBuilderTemplatesListCmd() *cobra.Command {
	desc := `List Node Templates and their collections

  Lists the collections and templates the caller can use, each with its
  source: mine (the caller's library), built-in (a built-in template in the
  caller's library), shared (another user's, shared with the caller),
  server-wide (another user's, published to every user) or server (read by
  the server from its template files). --owner lists only the items of
  that user.`

	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List Node Templates and their collections",
		Long:    desc,
		Example: "  phenix builder templates list\n  phenix builder templates list --owner bob -o json",
		Args:    refusedArgs(cobra.NoArgs),
		RunE:    runBuilderTemplatesList,
	}

	addBuilderServerFlags(cmd)
	addBuilderOutputFlag(cmd)
	cmd.Flags().String("owner", "", "List only the items of this user")

	return cmd
}

func newBuilderTemplatesExportCmd() *cobra.Command {
	desc := `Export Node Templates as a template file

  Writes the templates the caller can use as a template file, the format
  the Builder exports and phenix ui reads from its template directory: the
  templates of the collection --collection names (by name or ID), else
  every template, of --owner only when it is given. The file carries the
  custom icons the templates name, from the server's icon library. Names
  that differ only in case are numbered apart, as the Builder numbers
  them.`

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export Node Templates as a template file",
		Long:  desc,
		Example: `  phenix builder templates export --collection Substation > substation.templates.yaml
  phenix builder templates export --owner alice --output alice.templates.json --format json`,
		Args: refusedArgs(cobra.NoArgs),
		RunE: runBuilderTemplatesExport,
	}

	addBuilderServerFlags(cmd)
	cmd.Flags().String("owner", "", "Export only the templates of this user")
	cmd.Flags().String("collection", "", "Export the templates of this collection, by name or ID")
	cmd.Flags().String("output", "", "File to write (default: standard output)")
	cmd.Flags().String("format", FormatYAML, "Format of the file: yaml or json")

	return cmd
}

func newBuilderTemplatesImportCmd() *cobra.Command {
	desc := `Import a template file into the caller's library

  Reads a template file (YAML or JSON) strictly, and adds its templates to
  the caller's library as a new collection, named as the file names it or
  as --name gives, with " (2)", " (3)" and so on when one of the caller's
  collections has that name. The custom icons the file carries are added
  to the server's icon library when it lacks them; an icon whose name the
  library holds with another image is left out with a warning, and the
  templates show the library's.`

	cmd := &cobra.Command{
		Use:     "import <file>",
		Short:   "Import a template file into the caller's library",
		Long:    desc,
		Example: "  phenix builder templates import substation.templates.yaml\n  phenix builder templates import lab.yaml --name Lab",
		Args:    refusedArgs(cobra.ExactArgs(1)),
		RunE:    runBuilderTemplatesImport,
	}

	addBuilderServerFlags(cmd)
	cmd.Flags().String("name", "", "Name of the new collection (default: the file's)")

	return cmd
}

// builderListedTemplate is what the commands read of a template the server
// lists.
type builderListedTemplate struct {
	ID          string          `json:"id"`
	Owner       string          `json:"owner"`
	Source      string          `json:"source"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Device      json.RawMessage `json:"device"`
	Collections []string        `json:"collections"`
}

// builderListedCollection is what the commands read of a collection the
// server lists.
type builderListedCollection struct {
	ID          string   `json:"id"`
	Owner       string   `json:"owner"`
	Source      string   `json:"source"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	TemplateIDs []string `json:"templateIds"`
}

// builderPreloadedCollection is a collection the server read from one of its
// template files, with its templates.
type builderPreloadedCollection struct {
	Collection builderListedCollection `json:"collection"`
	Templates  []builderListedTemplate `json:"templates"`
}

// builderTemplateListing is what the commands read of the answer to
// GET /builder/templates.
type builderTemplateListing struct {
	Owner       string                       `json:"owner"`
	Templates   []builderListedTemplate      `json:"templates"`
	Collections []builderListedCollection    `json:"collections"`
	Preloaded   []builderPreloadedCollection `json:"preloaded"`
	Limits      struct {
		Templates   int `json:"templates"`
		Collections int `json:"collections"`
	} `json:"limits"`
}

// fetchBuilderTemplates reads the templates and collections the caller can
// use.
func fetchBuilderTemplates(ctx context.Context, client *builderClient) (*builderTemplateListing, error) {
	var listing builderTemplateListing

	if err := client.get(ctx, "/builder/templates", &listing); err != nil {
		return nil, refused(err)
	}

	return &listing, nil
}

// all returns every template and every collection of the listing: those of
// the libraries, then those the server read from its files.
func (l *builderTemplateListing) all() ([]builderListedTemplate, []builderListedCollection) {
	templates := slices.Clone(l.Templates)
	collections := slices.Clone(l.Collections)

	for _, preloaded := range l.Preloaded {
		templates = append(templates, preloaded.Templates...)
		collections = append(collections, preloaded.Collection)
	}

	return templates, collections
}

// own counts the caller's own templates and collections.
func (l *builderTemplateListing) own() (int, []string) {
	templates := 0

	for _, template := range l.Templates {
		if template.Source == listedSourceOwn {
			templates++
		}
	}

	collections := make([]string, 0, len(l.Collections))

	for _, collection := range l.Collections {
		if collection.Source == listedSourceOwn {
			collections = append(collections, collection.Name)
		}
	}

	return templates, collections
}

// builderItemSource is how templates list names the source of an item:
// builtin is set for a template of the caller's library with the ID of a
// built-in template.
func builderItemSource(source string, builtin bool) string {
	switch source {
	case listedSourceOwn:
		if builtin {
			return templateSourceBuiltin
		}

		return templateSourceMine
	case listedSourceShared:
		return templateSourceShared
	case listedSourceServer:
		return templateSourceServerWide
	case listedSourcePreloaded:
		return templateSourceServer
	}

	return source
}

// builderTemplateRow is one template of templates list. Collections are the
// IDs of the collections that hold it.
type builderTemplateRow struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	Source      string   `json:"source"`
	Collections []string `json:"collections"`
}

// builderCollectionRow is one collection of templates list. Templates are
// the IDs of the templates it holds, in its order.
type builderCollectionRow struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	Source      string   `json:"source"`
	Templates   []string `json:"templates"`
}

// builderTemplateReport is the report of templates list: User is the caller,
// whose library is "mine".
type builderTemplateReport struct {
	User        string                 `json:"user"`
	Collections []builderCollectionRow `json:"collections"`
	Templates   []builderTemplateRow   `json:"templates"`
}

// newBuilderTemplateReport returns the report of the listing's items, of
// owner only when it is not empty, in the order the server lists them.
func newBuilderTemplateReport(listing *builderTemplateListing, owner string) builderTemplateReport {
	templates, collections := listing.all()

	builtin := map[string]bool{}
	for _, template := range bdoc.BuiltinTemplates() {
		builtin[template.ID] = true
	}

	report := builderTemplateReport{
		User:        listing.Owner,
		Collections: make([]builderCollectionRow, 0, len(collections)),
		Templates:   make([]builderTemplateRow, 0, len(templates)),
	}

	for _, collection := range collections {
		if owner == "" || collection.Owner == owner {
			report.Collections = append(report.Collections, builderCollectionRow{
				ID:          collection.ID,
				Name:        collection.Name,
				Description: collection.Description,
				Owner:       collection.Owner,
				Source:      builderItemSource(collection.Source, false),
				Templates:   builderNonNil(collection.TemplateIDs),
			})
		}
	}

	for _, template := range templates {
		if owner == "" || template.Owner == owner {
			report.Templates = append(report.Templates, builderTemplateRow{
				ID:          template.ID,
				Name:        template.Name,
				Description: template.Description,
				Owner:       template.Owner,
				Source:      builderItemSource(template.Source, builtin[template.ID]),
				Collections: builderNonNil(template.Collections),
			})
		}
	}

	return report
}

// table writes the collections, then the templates, as tables. A template's
// collections are named by their names.
func (r builderTemplateReport) table(out io.Writer) {
	names := map[[2]string]string{}

	collections := newBuilderTable(out, "Source", "Owner", "Collection", "Templates", "ID")

	for _, collection := range r.Collections {
		names[[2]string{collection.Owner, collection.ID}] = collection.Name

		collections.Append([]string{
			collection.Source, collection.Owner, collection.Name, strconv.Itoa(len(collection.Templates)), collection.ID,
		})
	}

	fmt.Fprintln(out, "Collections:")
	collections.Render()

	templates := newBuilderTable(out, "Source", "Owner", "Template", "Collections", "ID")

	for _, template := range r.Templates {
		held := make([]string, 0, len(template.Collections))

		for _, id := range template.Collections {
			held = append(held, cmp.Or(names[[2]string{template.Owner, id}], id))
		}

		templates.Append([]string{template.Source, template.Owner, template.Name, strings.Join(held, ", "), template.ID})
	}

	fmt.Fprintln(out, "Templates:")
	templates.Render()
}

// runBuilderTemplatesList runs templates list.
func runBuilderTemplatesList(cmd *cobra.Command, _ []string) error {
	format, client, err := builderReportSetup(cmd)
	if err != nil {
		return err
	}

	listing, err := fetchBuilderTemplates(cmd.Context(), client)
	if err != nil {
		return err
	}

	report := newBuilderTemplateReport(listing, MustGetString(cmd.Flags(), "owner"))

	return writeBuilderOutput(cmd.OutOrStdout(), format, report, report.table)
}

// builderTemplateSelection is what templates export writes: the templates,
// as a collection of that name and description.
type builderTemplateSelection struct {
	name        string
	description string
	templates   []builderListedTemplate
}

// selectBuilderTemplates returns the templates of the listing that
// templates export writes: those of the collection named, by name or ID,
// else every template; of owner only when it is not empty.
func selectBuilderTemplates(listing *builderTemplateListing, owner, collection string) (builderTemplateSelection, error) {
	templates, collections := listing.all()

	selection := builderTemplateSelection{name: builderTemplatesFileName, description: "", templates: nil}

	if collection == "" {
		for _, template := range templates {
			if owner == "" || template.Owner == owner {
				selection.templates = append(selection.templates, template)
			}
		}

		if len(selection.templates) == 0 {
			return selection, refused(errors.New("there is no template to export"))
		}

		return selection, nil
	}

	matches := make([]builderListedCollection, 0, 1)

	for _, candidate := range collections {
		if (owner == "" || candidate.Owner == owner) && (candidate.ID == collection || candidate.Name == collection) {
			matches = append(matches, candidate)
		}
	}

	switch len(matches) {
	case 0:
		return selection, refused(fmt.Errorf("no collection the caller can use is named %q", collection))
	case 1:
	default:
		return selection, refused(fmt.Errorf("%q names %d collections: give --owner, or the collection's ID", collection, len(matches)))
	}

	chosen := matches[0]
	selection.name, selection.description = chosen.Name, chosen.Description

	for _, id := range chosen.TemplateIDs {
		index := slices.IndexFunc(templates, func(template builderListedTemplate) bool {
			return template.Owner == chosen.Owner && template.ID == id
		})

		if index >= 0 {
			selection.templates = append(selection.templates, templates[index])
		}
	}

	if len(selection.templates) == 0 {
		return selection, refused(fmt.Errorf("collection %q holds no template", chosen.Name))
	}

	return selection, nil
}

// builderLibraryIcon is what the commands read of an icon of the icon
// library.
type builderLibraryIcon struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
	Data    string   `json:"data"`
}

// embedBuilderIcons returns copies of the custom icons the templates name,
// from the server's icon library, by the names the templates use: at most
// as many as a template file carries. Each icon left out is a warning.
func embedBuilderIcons(
	ctx context.Context,
	client *builderClient,
	templates []builderListedTemplate,
) (map[string]bdoc.Icon, []string, error) {
	named := map[string]bool{}

	for _, template := range templates {
		var device struct {
			Icon string `json:"icon"`
		}

		if json.Unmarshal(template.Device, &device) == nil && device.Icon != "" {
			named[device.Icon] = true
		}
	}

	if len(named) == 0 {
		return map[string]bdoc.Icon{}, nil, nil
	}

	var library struct {
		Icons []builderLibraryIcon `json:"icons"`
	}

	if err := client.get(ctx, "/builder/icons", &library); err != nil {
		return nil, nil, refused(err)
	}

	data := map[string]string{}

	for _, icon := range library.Icons {
		for _, name := range append([]string{icon.Name}, icon.Aliases...) {
			data[strings.ToLower(name)] = icon.Data
		}
	}

	icons := map[string]bdoc.Icon{}
	warnings := make([]string, 0, len(named))

	for _, name := range slices.Sorted(maps.Keys(named)) {
		icon := map[string]bdoc.Icon{name: {Data: data[strings.ToLower(name)]}}

		switch {
		case icon[name].Data == "":
			warnings = append(warnings, fmt.Sprintf("icon %s is not in the icon library, so the file does not carry it", name))
		case len(icons) == bdoc.MaxDocumentIcons:
			warnings = append(warnings, fmt.Sprintf(
				"icon %s is left out: a template file carries at most %d icons", name, bdoc.MaxDocumentIcons,
			))
		case len(bdoc.ValidateIcons(icon, "icons")) > 0:
			warnings = append(warnings, fmt.Sprintf(
				"icon %s is left out: a template file cannot carry the image the library holds", name,
			))
		default:
			icons[name] = icon[name]
		}
	}

	return icons, warnings, nil
}

// builderTemplateFile returns the template file of the selection, carrying
// icons, decoded and checked as phenix reads one, and a warning for each
// template it numbers apart from another of the same name.
func builderTemplateFile(selection builderTemplateSelection, icons map[string]bdoc.Icon) (*bdoc.TemplateFile, []string, error) {
	type fileTemplate struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Device      json.RawMessage `json:"device"`
	}

	names := make([]string, 0, len(selection.templates))
	for _, template := range selection.templates {
		names = append(names, template.Name)
	}

	names, renamed := uniqueBuilderTemplateNames(names)

	templates := make([]fileTemplate, 0, len(selection.templates))
	for i, template := range selection.templates {
		templates = append(templates, fileTemplate{Name: names[i], Description: template.Description, Device: template.Device})
	}

	data, err := json.Marshal(struct {
		Schema      string               `json:"$schema"`
		Name        string               `json:"name"`
		Description string               `json:"description,omitempty"`
		Templates   []fileTemplate       `json:"templates"`
		Icons       map[string]bdoc.Icon `json:"icons,omitempty"`
	}{
		Schema:      bdoc.TemplateFileSchemaURI,
		Name:        selection.name,
		Description: selection.description,
		Templates:   templates,
		Icons:       icons,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("encoding the template file: %w", err)
	}

	file, err := bdoc.DecodeTemplateFile(data)
	if err == nil {
		err = file.Validate()
	}

	if err != nil {
		return nil, nil, refused(fmt.Errorf("the templates do not make a template file phenix reads: %w", err))
	}

	return file, renamed, nil
}

// runBuilderTemplatesExport runs templates export.
func runBuilderTemplatesExport(cmd *cobra.Command, _ []string) error {
	format := MustGetString(cmd.Flags(), "format")
	if format != FormatJSON && format != FormatYAML {
		return refused(fmt.Errorf("--format %q is not yaml or json", format))
	}

	client, err := builderClientFor(cmd)
	if err != nil {
		return err
	}

	listing, err := fetchBuilderTemplates(cmd.Context(), client)
	if err != nil {
		return err
	}

	selection, err := selectBuilderTemplates(
		listing, MustGetString(cmd.Flags(), "owner"), MustGetString(cmd.Flags(), "collection"),
	)
	if err != nil {
		return err
	}

	icons, warnings, err := embedBuilderIcons(cmd.Context(), client, selection.templates)
	if err != nil {
		return err
	}

	file, renamed, err := builderTemplateFile(selection, icons)
	if err != nil {
		return err
	}

	data, err := json.Marshal(file)
	if err != nil {
		return fmt.Errorf("encoding the template file: %w", err)
	}

	text, err := builderExportText(data, format)
	if err != nil {
		return err
	}

	for _, warning := range slices.Concat(renamed, warnings) {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+warning)
	}

	return writeBuilderFile(cmd.OutOrStdout(), MustGetString(cmd.Flags(), "output"), text)
}

// numberedBuilderName is name followed by " (n)", shortened to stay within
// the most bytes a template or collection name holds, as the Builder
// numbers a name apart.
func numberedBuilderName(name string, n int) string {
	suffix := " (" + strconv.Itoa(n) + ")"
	runes := []rune(name)

	for len(runes) > 0 && len(string(runes))+len(suffix) > bdoc.MaxTemplateNameBytes {
		runes = runes[:len(runes)-1]
	}

	return strings.TrimRightFunc(string(runes), unicode.IsSpace) + suffix
}

// uniqueBuilderTemplateNames returns the names, in order, with each that a
// name before it has, ignoring case and the white space around them,
// replaced by the first of " (2)", " (3)" and so on that no name has; and
// a warning for each name replaced. A template file may not hold two
// templates whose names differ only so, which a library may.
func uniqueBuilderTemplateNames(names []string) ([]string, []string) {
	key := func(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

	given := map[string]bool{}
	for _, name := range names {
		given[key(name)] = true
	}

	var (
		used    = map[string]bool{}
		unique  = make([]string, 0, len(names))
		renamed []string
	)

	for _, name := range names {
		if current := key(name); current == "" || !used[current] {
			used[current] = true
			unique = append(unique, name)

			continue
		}

		n := firstNumberedName
		candidate := numberedBuilderName(strings.TrimSpace(name), n)

		for used[key(candidate)] || given[key(candidate)] {
			n++
			candidate = numberedBuilderName(strings.TrimSpace(name), n)
		}

		used[key(candidate)] = true
		unique = append(unique, candidate)
		renamed = append(renamed, fmt.Sprintf("template %q is %q in the file: an earlier template has its name", name, candidate))
	}

	return unique, renamed
}

// uniqueBuilderCollectionName is name, else the first of it with " (2)",
// " (3)" and so on that none of taken is, ignoring case.
func uniqueBuilderCollectionName(name string, taken []string) string {
	used := map[string]bool{}
	for _, collection := range taken {
		used[strings.ToLower(collection)] = true
	}

	if !used[strings.ToLower(name)] {
		return name
	}

	for n := firstNumberedName; ; n++ {
		if candidate := numberedBuilderName(name, n); !used[strings.ToLower(candidate)] {
			return candidate
		}
	}
}

// builderTemplateContent is one template a request adds to a library.
type builderTemplateContent struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Device      bdoc.TemplateDevice `json:"device"`
}

// builderNewCollection is the collection a request adds templates to.
type builderNewCollection struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// builderTemplateImport adds templates to a library as a new collection.
type builderTemplateImport struct {
	Templates  []builderTemplateContent `json:"templates"`
	Collection builderNewCollection     `json:"collection"`
}

// builderIconUpload adds an icon to the icon library.
type builderIconUpload struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

// readBuilderTemplateFile reads the template file at path: no more than one
// byte past the most a template file may hold, so a larger one is refused
// without reading all of it.
func readBuilderTemplateFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, refused(fmt.Errorf("reading the template file: %w", err))
	}
	defer file.Close()

	text, err := io.ReadAll(io.LimitReader(file, bdoc.MaxTemplateFileBytes+1))
	if err != nil {
		return nil, refused(fmt.Errorf("reading the template file: %w", err))
	}

	return text, nil
}

// addBuilderIcons adds the icons to the server's icon library, in the order
// of their names. One the library already holds with the same image is
// left as it is; one it holds with another image, or refuses, is a warning.
// A request that got no answer stops it.
func addBuilderIcons(ctx context.Context, client *builderClient, icons map[string]bdoc.Icon) ([]string, error) {
	var warnings []string

	for _, name := range slices.Sorted(maps.Keys(icons)) {
		var apiErr *builderAPIError

		err := client.post(ctx, "/builder/icons", builderIconUpload{Name: name, Data: icons[name].Data}, nil)

		switch {
		case err == nil:
		case errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict:
			warnings = append(warnings, fmt.Sprintf(
				"icon %s was not added: %s; the templates show the icon library's", name, apiErr.reason(),
			))
		case errors.As(err, &apiErr):
			warnings = append(warnings, fmt.Sprintf("icon %s was not added: %s", name, apiErr.reason()))
		default:
			return warnings, refused(err)
		}
	}

	return warnings, nil
}

// builderImportProblem says why the file's templates cannot be added to the
// caller's library, as the Builder says it before it adds any icon, or nil.
func builderImportProblem(listing *builderTemplateListing, adding int) error {
	templates, collections := listing.own()

	if limit := listing.Limits.Templates; limit > 0 && templates+adding > limit {
		return fmt.Errorf("your library can hold %d templates, and this file would take it past that; delete some first", limit)
	}

	if limit := listing.Limits.Collections; limit > 0 && len(collections) >= limit {
		return fmt.Errorf("your library holds %d collections, the most it can; delete one first", limit)
	}

	return nil
}

// runBuilderTemplatesImport runs templates import.
func runBuilderTemplatesImport(cmd *cobra.Command, args []string) error {
	text, err := readBuilderTemplateFile(args[0])
	if err != nil {
		return err
	}

	file, err := bdoc.ParseTemplateFile(text)
	if err != nil {
		return refused(fmt.Errorf("%s cannot be imported: %w", args[0], err))
	}

	client, err := builderClientFor(cmd)
	if err != nil {
		return err
	}

	listing, err := fetchBuilderTemplates(cmd.Context(), client)
	if err != nil {
		return err
	}

	if err := builderImportProblem(listing, len(file.Templates)); err != nil {
		return refused(err)
	}

	warnings, err := addBuilderIcons(cmd.Context(), client, file.Icons)
	if err != nil {
		return err
	}

	_, taken := listing.own()
	name := uniqueBuilderCollectionName(cmp.Or(MustGetString(cmd.Flags(), "name"), file.Name), taken)

	request := builderTemplateImport{
		Templates:  make([]builderTemplateContent, 0, len(file.Templates)),
		Collection: builderNewCollection{Name: name, Description: file.Description},
	}

	for _, template := range file.Templates {
		request.Templates = append(request.Templates, builderTemplateContent{
			Name: template.Name, Description: template.Description, Device: template.Device,
		})
	}

	for _, warning := range warnings {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+warning)
	}

	if err := client.post(cmd.Context(), "/builder/templates/"+url.PathEscape(listing.Owner)+"/items", request, nil); err != nil {
		return refused(err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Imported %s as collection %s.\n", builderCount(len(file.Templates), "template"), name)

	return nil
}
