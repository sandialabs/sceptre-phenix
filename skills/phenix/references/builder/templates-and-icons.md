# Node templates and custom icons

Part of the [Builder references](../builder.md). The template library, its
server collections and template files, and the server's icon library.

User docs: [Node Templates](https://phenix.sceptre.dev/latest/builder/templates/)
(with its limits), [Custom icons](https://phenix.sceptre.dev/latest/builder/diagrams/#custom-icons),
[Template files on the server](https://phenix.sceptre.dev/latest/builder/administration/#template-files-on-the-server).

## Template library

Code: `types/builder/template.go`, `api/builder/templates.go`,
`web/builder_templates.go`, `builder/templates.js`, `BuilderTemplates.vue`.

- Diagram templates live in the document (`templates`, `bdoc.Template`).
  Library templates live in one record per user: `builder.templates` key
  `lib/<OwnerScope(user)>` (`LibraryKey`), at most 512 KiB. A template
  names its custom icon. A library carries no icons.
- The five built-in templates (`BuiltinTemplates()` in Go,
  `BUILTIN_TEMPLATES` in `catalog.js`, fixture
  `types/builder/testdata/builtin-templates.json`) are the library of a
  user with no record. The first change stores them.
- Every write is `Service.UpdateLibrary(ctx, owner, actor, change)`: read,
  run `change`, stamp versions and times, validate, and write against the
  read revision. It tries up to 5 times, then answers `ErrBusy` (503,
  `Retry-After: 1`). Refusals are `*LibraryError`. A record a newer phenix
  wrote is `damaged`: listed without its own items, and every change
  answers 409.
- `POST /builder/templates/{owner}/restore` (`restoreTemplates`,
  `TemplateLibrary.RestoreBuiltins`, `configs` `create`) adds back the
  built-in templates the library lacks, with their original IDs, in one
  write. It takes `{templates: [ids]}` (none means all) and answers
  `{restored: [ids]}`. It ignores a held template or an unknown ID, so a
  repeat is harmless. The UI calls `store.restoreBuiltinTemplates`. User
  docs: [Restoring built-in templates](https://phenix.sceptre.dev/latest/builder/templates/#restoring-built-in-templates).
- Sharing and server-wide publishing give a live, read-only view. They add
  hint records `in/<OwnerScope(recipient)>/<OwnerScope(owner)>` and
  `pub/<OwnerScope(owner)>` (value `{}`, never removed), so a listing reads
  only the libraries the hints name. A share binds to the recipient's
  account. With authentication off, the one library is `global-admin`'s.
- `store.fetchTemplates` reads the library again when the lists load, when
  a draft opens, when the window comes to the front while the editor is
  open, after Sign in again, and after each library change.
- Save to library and template import add the template's icons to the
  icon library with an upload's rules (`ingestSavedTemplateIcons`,
  `ingestTemplateIcons`).

## Template files

`TemplateFile`, `ParseTemplateFile` (`types/builder/templatefile.go`) and
`templateFile.js` read one collection as YAML or JSON, strictly. The schema
is `TemplateFileSchema()`, served at `GET /schemas/builder/templates/v1`.
Names are unique ignoring case and surrounding white space (shared cases in
`testdata/template-file-names.json`). The docs example
`docs/content/builder/examples/node-templates.yaml` must load
(`TestDocsTemplateFileExampleLoads`). `IsDocumentText` is false for a
template file.

### Server collections

`newBuilderAPI` reads the directory `--base-dir.builder-templates`
(`common.BuilderTemplatesDir()`) once at start (`Service.LoadServerTemplates`,
`ReadTemplateDirectory` in `api/builder/templatefiles.go`). Tests pass the
option `withBuilderTemplateFiles("")` to read nothing.

- It opens files through `os.Root`, so a link that leaves the directory is
  refused. It logs and skips a bad file (`skipping builder template file`).
  The Builder starts in all cases.
- Each file is a `ServerCollection` held in memory, never in the store.
  IDs are `server-` plus 24 hex of a SHA-256 of the file name (and of the
  template's lowercase name). Every write route that names one answers 409
  (`refusePreloaded`). `GET /builder/templates` lists them in `preloaded`.
- Icons in the files that the library lacks are added with owner ""
  (`ServerIconOwner`), without per-user limits, only while the library
  holds fewer than `MaxIcons - ServerIconReserve` (1000) icons. No account owns
  them, so only `builder-icons` holders rename or delete them.

## Icon library

Code: `types/builder/customicons.go`, `api/builder/icons.go`,
`web/builder_icons.go`, `builder/icons.js`, `iconLibrary.js`,
`dialogs/IconDialog.vue`.

- One library for the whole server. A name is unique ignoring case, and the
  first upload of a name wins. A later upload of the name with other bytes
  answers 409 and names the uploader. An icon's `id` is the SHA-256 of its
  bytes.
- Icon rules that Go (`customicons.go`: `ValidateIcons`, `IconNameProblem`,
  `ValidateIconPNG`) and JS (`validateIcons` and `iconNameProblem` in
  `validate.js`, `iconPNGProblem` in `icons.js`) must apply the same way:
  - A document's `icons` map holds at most 50 entries (`MaxDocumentIcons`,
    `MAX_DOCUMENT_ICONS`).
  - An icon is a PNG of 1 to 96 pixels a side and at most 40960 bytes. It
    holds only the chunks IHDR, PLTE, tRNS, IDAT and IEND.
  - An icon name matches `^[A-Za-z0-9_@.-]+$`, is 1 to 64 bytes, and is
    not `.` or `..`.
- Records in `builder.icons`: `name/<lowercase name>` holds an icon
  (`{kind: "icon", name, id, owner, …, aliases, data}`) or an alias
  (`{kind: "alias", name, target}`). A rename keeps the old name as an
  alias and points every alias at the new name (`retargetAliases`), so an
  alias is one hop from its icon. Creates are create-if-absent and renames
  compare-and-swap, so two uploads of one name never both win.
- At start, `CleanupLegacyIcons` deletes records of an earlier per-user
  layout (`<OwnerScope>/<hex>`).
- A draft names icons and carries no image data. `settleIcons(doc, library)`
  in `store.commit` drops copies that nothing names and copies the library
  holds with the same bytes. A download embeds every icon the document
  names (`embedIcons` in `exporters.js`). An upload with copies
  (`ingestIcons`) adds each name the library lacks and warns for each copy
  it keeps.
- Every `/builder/` response has `X-Content-Type-Options: nosniff`. The
  icon routes also send `Content-Security-Policy: default-src 'none';
  frame-ancestors 'none'` (`builderResponseHeaders`). No CSP is set on the
  application page.
