# Node templates

Part of the [Builder references](../builder.md). The built-in device
templates, diagram templates, the per-user template library with sharing
and server-wide publishing, template files, and the server collections read
at start.

## Built-in templates

The five built-in device templates (`server`,
`workstation`, `router`, `firewall`, `external`; fixture
`types/builder/testdata/builtin-templates.json`, `BuiltinTemplates()` in Go,
`BUILTIN_TEMPLATES` in `catalog.js`) write no description into the node.
Router is type `Router` running `minirouter` on `minirouter.qc2`; Firewall
is type `Firewall` running `vyos` on `vyos.qc2`.

## Diagram templates

Two stores. Diagram templates live in the document (`templates`: `{id,
name, description, device: {iconKey?, icon?, iconSize?, outlineColor?,
fillColor?, spec}}`; name 1 to 128 bytes, description at most 1024, both one line;
device at most 16384 bytes as JSON; `bdoc.Template`, `template.go`); they
travel with downloads, shares and publishes, and are never written to a
config. The palette's "+" (New device template) and the template editor
(`dialogs/TemplateDialog.vue`, the Inspector's form code through
`templateEditorHost` in `templates.js`) write them. `TemplateDevice` must
hold every `Device` field but `hostname`, `interfaces` and `includedFrom`
(a reflection test pins it), so a new device field is a template field too.
A device made from a template (`nodeOptionsFromTemplate`) keeps no link to
it. The template editor's document (`templateDocument`) has the root
`iconSize` of the template's diagram, so Icon size's Diagram default names
that size; a library template's has none, and `inspectorTarget(doc,
selection, {template: true})` then names no default size. Save to library
on a diagram template (`store.saveTemplateToLibrary`) first puts the
diagram's copy of the icon it names into the icon library
(`savedTemplateIcons`, `ingestSavedTemplateIcons`: an upload's rules; a
refused or differing icon is a warning announced with the save and shown as
the canvas notice).

## The per-user library

The per-user library (`api/builder/templates.go`, `web/builder_templates.go`):
one record per user in `builder.templates` under `lib/<OwnerScope(user)>`
(`LibraryKey`), at most 512 KiB, holding templates and collections; a
template names its custom icon, which the icon library resolves (a library
carries no icons). A user with no record has the five built-in templates at
version 1; the first change stores them, and a deleted one never comes back.
Limits: 200 templates, 50 collections, 200 templates per collection, 25
users per item. Every write is `Service.UpdateLibrary(ctx, owner, actor,
change)`: read, run `change`, stamp versions and times, validate, write
against the read revision; up to 5 tries, then `ErrBusy` (503,
`Retry-After: 1`).
Refusals are `*LibraryError`. Templates and collections have strong ETags
`"<version>"`; only the two PUT routes need `If-Match`. A record a newer
phenix wrote is `damaged`: listed with no own items, and every change 409.
Sharing (`/share`, `configs` `update` and a user account) and server-wide
publishing (`/publish`, also `builder-templates` `publish`; taking back:
the owner, or anyone with that permission) give recipients a live, read-only
view and add hint records `in/<OwnerScope(recipient)>/<OwnerScope(owner)>`
and `pub/<OwnerScope(owner)>` (value `{}`, never removed), so a listing
reads only the libraries hints name. A share is bound to the recipient's
account (stale after it is deleted or recreated). With authentication off
the one library is `global-admin`'s and sharing is unavailable. The
start-of-server cleanup never touches `builder.templates`, and in
`builder.icons` only records of the earlier per-user layout. UI: the Node Templates tab (`BuilderTemplates.vue`,
`CollectionDialog.vue`, `TemplateShareDialog.vue`), palette groups "This
diagram", "My library", "Shared with me", "Server-wide", "Built-in"
(`paletteTemplateGroups`, `TEMPLATE_GROUP_LABELS`; a group heading shows only
with two groups or more), the library button "Node Templates library",
`store.templates` and its actions (`fetchTemplates`,
`createLibraryTemplates`, `updateLibraryTemplate`, `createCollection`,
`updateCollection`, `deleteLibraryItems`, `shareLibraryItems`,
`publishLibraryItems`).

## Template files

Template files (`TemplateFile`, `ParseTemplateFile` in
`types/builder/templatefile.go`; `templateFile.js`): one collection as YAML
or JSON, `{$schema: "https://phenix.sandia.gov/schemas/builder/templates/v1",
name, description?, templates: [{name, description?, device}], icons?}`,
strict (unknown keys refused), YAML read by `JSONFromYAML` (no anchors,
aliases, merge keys), at most 8 MiB (`MaxTemplateFileBytes`), 1 to 200
templates (`MaxTemplateFileTemplates` = `MaxCollectionTemplates`), names
unique ignoring case and the white space `strings.TrimSpace` trims around
them (`trimSpace` in `text.js`; shared cases in
`testdata/template-file-names.json`), each template as `Template.Issues` checks it, icons
as `ValidateIcons` checks a document's; a template may name an icon the
file does not carry. Schema: `TemplateFileSchema()`
(`templatefile_schema.go`, same title/description/examples rule, checked by
`TestTemplateFileSchemaDocumentsEveryProperty`), served by `GET
/schemas/builder/templates/v1` (`schemas` `get` on `builder`). The docs
example `docs/content/builder/examples/node-templates.yaml` must load
(`TestDocsTemplateFileExampleLoads`). `IsDocumentText` is false for a
template file.

### Export and import

Export (Node Templates tab: a card's Export, Export selected, Export
collection; `exportTemplateFile`) saves `<sanitized name>.templates.yaml`
(`templateFileName`, never a dotfile) as `templateFileText` writes it
(`YAML.dump` with `lineWidth: -1`, so an icon's base64 stays on one line),
embedding each icon the templates name from the icon library (`embedIcons`,
at most 50); every source exports. Template names that differ only in case
(a library may hold them) are numbered apart (`uniqueTemplateNames`, " (2)",
" (3)" … ignoring case and surrounding space, within 128 bytes), and the
export notice names each renamed template. Import (`templates-import`,
`dialogs/TemplateImportDialog.vue`, rendered by `BuilderTemplates.vue`)
reads the file with `parseTemplateFile` (issues listed as `path: message`),
then `store.importTemplateFile`: `importProblem` (200 templates, 50
collections), `ingestTemplateIcons` (an upload's rules; a differing or
refused icon is a warning, and the dialog stays open listing them), and
`createLibraryTemplates` with a new collection named by
`uniqueCollectionName` (" (2)", " (3)", ignoring case, among own
collections).

## Server collections

Server collections: the root flag `--base-dir.builder-templates` (env
`PHENIX_BASE_DIR_BUILDER_TEMPLATES`, config key, default
`<base-dir.phenix>/builder/templates`; `common.BuilderTemplatesBase`,
`common.BuilderTemplatesDir()`) names a directory that `newBuilderAPI`
reads once at start (`Service.LoadServerTemplates`, `ReadTemplateDirectory`
in `api/builder/templatefiles.go`; option `withBuilderTemplateFiles`, ""
reads nothing, as the web test harness does): files directly in it named
`*.yaml`, `*.yml`, `*.json` (any case), not starting with `.`, at most 50
(`MaxServerTemplateFiles`), through `os.Root` (a link leaving the directory
is refused), regular files only. A bad file is logged (`skipping builder
template file`, `file`, `reason`) and skipped; a missing directory is a
debug log, and a path that cannot be opened as a directory (a regular file
there) a warning (`builder template directory cannot be read`); the Builder
starts either way. Each file is a `ServerCollection` held in memory
(`Service.ServerCollections`), with ids `server-` + 24 hex of a SHA-256 of
the file name (and the template's lower-case name). Icons the files carry
that the icon library lacks are added (`addServerIcons`, which lists the
library once per load) with owner "" (`ServerIconOwner`: no phenix user
name is empty, and the service refuses an empty caller, so no account owns
them and only `builder-icons` holders rename or delete them; `readIcon`
accepts the empty owner, and the UI shows it as "Server", `iconOwnerText`)
without per-user limits, only while the library holds fewer than
`MaxIcons - ServerIconReserve` (1000) icons; the rest are skipped with one
warning (`builder icon library has no room for more template file icons`).
A name
held with other bytes keeps the library's and is logged. `GET
/builder/templates` lists them in `preloaded: [{collection,
templates}]` (`source` `preloaded`, `owner` "", version 1, etag `"1"`) for
every caller; every write route that names one of their ids answers 409
(`refusePreloaded`). The UI flattens them into `store.templates` with source
`preloaded`: Show's `Server` optgroup (`preloaded:<id>`, `showChoices`
`preloadedCollections`), cards with View, Copy to my library and Export
(testids `…-preloaded-<id>`), one palette group per collection
(`preloaded:<id>`, labelled `Server: <name>` by `preloadedGroupLabel`, its
entries' tooltips saying the collection is read only, entries
`palette-template-preloaded-<id>`, key `preloaded:<id>`).

## Command line

No `phenix` command converts legacy diagrams, imports configs, manages the
icon library, or shares, publishes or edits library templates; they are web
UI and REST only. `phenix builder templates` lists, exports and imports
templates (see
[CLI: phenix builder drafts and templates](cli.md#cli-phenix-builder-drafts-and-templates)).
