# Node templates, layer sets and icons

Part of the [Builder references](../builder.md). The template library, the
template copies of a diagram, server collections and template files, the
layer set library and layer set files, the built-in icons, and the
server's icon library.

User docs: [Node Templates](https://phenix.sceptre.dev/latest/builder/templates/)
(with its limits), [Layers](https://phenix.sceptre.dev/latest/builder/diagrams/#layers),
[Custom icons](https://phenix.sceptre.dev/latest/builder/diagrams/#custom-icons),
[Template files on the server](https://phenix.sceptre.dev/latest/builder/administration/#template-files-on-the-server).

## Template library

Code: `types/builder/template.go`, `api/builder/templates.go`,
`web/builder_templates.go`, `builder/templates.js`, `BuilderTemplates.vue`,
`BuilderPalette.vue`, `inspector/InspectorTemplateLinks.vue`.

- One library for the whole server: one record, `builder.templates` key
  `library` (`LibraryKey`), at most 512 KiB (`MaxMetadataBytes`). Every
  user with `builder` `use` reads and changes it. Nothing in it has an
  owner, and nothing is published. A
  template names its custom icon. The library carries no icons.
- The library holds only the templates users added. The five built-in
  templates (`BuiltinTemplates()` in Go, `BUILTIN_TEMPLATES` in
  `catalog.js`, fixture `types/builder/testdata/builtin-templates.json`)
  and the templates of server collections are read only and never in the
  record. A server with no record has an empty library. Every write route
  that names a read-only item answers 409 `template.item.read-only`
  (`refuseReadOnly`). The UI offers Duplicate for them.
- Each template and collection keeps `version` (1, then one more for each
  change of its content), `createdBy` and `updatedBy`. `stamp` sets them
  from the `actor` of a content change. A read-only item is version 1
  (`ReadOnlyTemplateVersion`) with no times and no users.
- Every write is `Service.UpdateLibrary(ctx, actor, change)`: read, run
  `change`, stamp versions, times and users, validate, and write against
  the read revision. It tries up to 5 times, then answers `ErrBusy` (503,
  `Retry-After: 1`). Refusals are `*LibraryError`. A record a newer phenix
  wrote is `damaged`: listed without its items, and every change answers
  409. The two `PUT` routes need `If-Match`. A stale tag answers 412
  `template.item.stale` with the current `ETag`.
- `GET /builder/templates` answers `templates`, `collections`, `builtins`,
  `preloaded` (the server collections, each with its templates),
  `damaged` and `limits`. Each template has `source`: `library`, `builtin`
  or `preloaded`. Every caller gets the same answer.
- `store.fetchTemplates` reads the library again when the lists load, when
  a draft opens, when the window comes to the front while the editor is
  open, after Sign in again, and after each library change. A failed read
  says why in `templates.error` and keeps what it read before.
- Save to library and template import add the template's icons to the
  icon library with an upload's rules (`ingestSavedTemplateIcons`,
  `ingestTemplateIcons`).

### Template copies and updates

- A diagram's template can name the template it was copied from in
  `source` (`{kind, id, version, digest}`, see
  [document.md](document.md#keys-and-validation)). `templateSource` and
  `linkedCopy` in `templates.js` make it. `digest` is
  `templateCopyDigest`: the content digest of the copy's name,
  description and device.
- Placing a library or server template on the canvas copies it into the
  diagram, linked (`withLinkedCopy`, in the store's `addNode`), unless the
  diagram has a copy linked to it already. A built-in template is never
  copied, because it never changes. When the diagram holds `MAX_TEMPLATES`
  (50) templates, the device has no link and the announcement says so.
  Save to library links the diagram's template to the new library
  template (`store.linkTemplateCopy`).
- `templateLink(copy, library)` gives the state of a copy: `current`,
  `update` (a later version, or for a `preloaded` copy a digest that
  differs, because a server template stays at version 1), `deleted` or
  `unknown` (the library was not read or is damaged). `edited` is true when
  the copy changed in the diagram after it was copied.
  `templateLinks(doc, library)` lists the copies with their state.
  `templateLinkText` and `templateLinkMark` give the words.
- `store.updateTemplateCopies(ids)` copies the latest versions in one undo
  step (`updatedCopy`), and `store.unlinkTemplate(id)` removes `source`
  and keeps the copy. A device made from a template never changes.
- The palette marks a copy that needs attention under its entry (test
  ids `palette-template-link-<id>`, and `palette-template-update-<id>` or
  `palette-template-unlink-<id>`). The template menu has Update and Unlink
  too. The Inspector's Diagram section shows `InspectorTemplateLinks.vue`
  ("Template updates", with Update, Update all and Unlink) while a copy
  needs attention.

### Lists of templates

- The editor's Templates list (`BuilderPalette.vue`) has one section per
  group (`paletteTemplateGroups` in `templates.js`): "This diagram", then
  "Library" (or "Other library templates" when the library has
  collections) for templates in no collection, then one section per
  collection (library collections, then "Server: <name>"), and "Built-in"
  always. A section's id is `diagram`, `library`, `builtin` or the
  collection's `templateKey` (`library:<id>` or `preloaded:<id>`). A
  template in several collections gets `-in-<section id>` on its test id
  after the first. Add device in the command palette lists each template
  once. Collapsed sections are kept in localStorage
  `phenix.builder.palette` (`paletteSections.js`), which logout clears.
- The drafts page's Node Templates tab (`BuilderTemplates.vue`) has a Show
  select (`showChoices`, `shownList`): all library templates, each library
  collection, the built-in templates (`SHOW_BUILTIN`) and each server
  collection. A read-only card has a "Read only" tag and View, Duplicate
  and Export. Its test ids name the source: `template-<part>-<source>-<id>`
  (for example `template-card-builtin-router`). A library card's test ids
  are `template-<part>-<id>`.

## Template files

`TemplateFile`, `ParseTemplateFile` (`types/builder/templatefile.go`) and
`templateFile.js` read one collection as YAML or JSON, strictly. The schema
is `TemplateFileSchema()`, served at `GET /schemas/builder/templates/v1`.
Names are unique ignoring case and surrounding white space (shared cases in
`testdata/template-file-names.json`). The docs example
`docs/content/builder/examples/node-templates.yaml` must load
(`TestDocsTemplateFileExampleLoads`). `IsDocumentText` is false for a
template file.

Upload refuses a template file and names Import templates
(`isTemplateFileValue` in `templateFile.js`, checked after the raw parse,
as `isPackageValue` is). A value is one when it names the template file
schema, or names no schema and has a `templates` list and no key that
only a diagram has. A diagram that carries templates names its own schema.

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
  `template.item.read-only` (`refuseReadOnly`). `GET /builder/templates`
  lists them in `preloaded`. A diagram's copy of a server template sees a
  changed file after a restart by its digest (see
  [Template copies and updates](#template-copies-and-updates)).
- Icons in the files that the library lacks are added with owner ""
  (`ServerIconOwner`), only while the library holds fewer than
  `MaxIcons - ServerIconReserve` (1000) icons. No account
  owns them, and no user renames or deletes them (`MayChangeIcon`).

## Layer set library

Code: `types/builder/layerset.go`, `api/builder/layersets.go`,
`web/builder_layersets.go`, `builder/layerSetStore.js`,
`BuilderLayerSets.vue`, `dialogs/LayersDialog.vue`, `layers/`.

- One library for the whole server, in `builder.layersets`: one record per
  set, key `set/<id>` (`LibraryLayerSet`, at most `MaxMetadataBytes`), and
  the record `default` when a user chose the one default set of the server
  (`DefaultLayerSet`, `SetDefaultLayerSet`). At most 200 sets
  (`MaxLayerSets`), apart from the built-in sets. Any user with `builder`
  `use` adds, changes or deletes any set, and sets the default.
- `BuiltinLayerSets()` gives the built-in sets, which have no record. The
  one built-in set is the Purdue model, ID `builtin:purdue`
  (`BuiltinPurdueLayerSetID`). A change or a delete of it answers 403
  `layerset.read-only`.
- A set (`LayerSet`) has `name`, `description`, `iconKey`, `icon`,
  `matchConflict`, `layerOutlines` and 1 to 32 `layers`, which follow the
  rules of a document's layers. Each stored set has `version`, and the
  users and times that a template has.
- Routes: `GET` and `POST /builder/layersets`, `GET`, `PUT` and
  `DELETE /builder/layersets/{layerset}`, and `GET` and
  `PUT /builder/layersets/default` (registered before `{layerset}`). `PUT`
  of a set needs `If-Match` (`"<version>"`). Answers: 412 `layerset.stale`
  with the current `ETag`, 413 `layerset.library.full`, 422
  `layerset.invalid` with `issues`, 404 for an unknown set, 409 for a
  record this server cannot read, and 503 after 5 tries. A default that
  names no set answers 422 `layerset.default.unknown`. A default whose set
  was deleted reads as null. The listing gives the built-in sets first,
  then the others by name, with `defaultId`, `damaged` (the count of
  records it cannot read, which it leaves out) and `limits`.
- A document holds a full copy of the layers and links to a set by
  `layerSet.source` (`withLayerSetCopy` in `model.js`). The store's
  `layerSetLink` getter is `unknown` while the library is not read.
  Actions (`layerSetActions`): `fetchLayerSets`, `createLayerSet`,
  `saveLayerSet`, `deleteLayerSet`, `duplicateLayerSet`,
  `setDefaultLayerSet`, `uploadLayerSetFile`, `applyLayerSet`,
  `updateFromLayerSet`, `unlinkLayerSet`, `saveToLayerSet`,
  `saveAsNewLayerSet`, `useMatchedLayer` and `withDefaultLayerSet`.
  `saveToLayerSet` refuses when the set has a newer version than the link.
  `saveLayerSet` sends the version the editor opened, so a change made
  meanwhile answers 412.
- `fetchLayerSets` runs where `fetchTemplates` does, and when the layers
  editor opens in diagram mode.
- The Layers tab (`BuilderLayerSets.vue`, the `layers` slot of
  `BuilderDrafts.vue`, between Node Templates and Icons) has no
  multi-select. Each card has its own buttons. Edit, View and New layer set
  open `LayersDialog` in `library` mode.
- The Layers tab has no role gating: every Builder user may use each
  control. Only the built-in sets open read only (View in place of Edit).
- Layer icons use `LayerIconPicker.vue`: the same menu button and Custom
  icons dialog as the Inspector's Icon field. The dialog provides
  `INSPECTOR_ICONS` and `INSPECTOR_ICON_LIBRARY` for it. `LayerName.vue`
  shows a layer's icon and its label in its color, or a swatch.
  `layerInk` (`layerLook.js`) decides for each theme whether the label can
  take the color (4.5:1 against `LAYER_TEXT_BACKGROUNDS`).

### Layer set files

`layerSetFile.js` reads one set as YAML or JSON, strictly, with the copies
of the custom icons it names. Go keeps only the schema
(`types/builder/layerset_schema.go`) and `IsLayerSetFileText`. The schema is `LayerSetFileSchema()`, served
at `GET /schemas/builder/layersets/v1`. Codes: `layerset.file.invalid` and
`layerset.file.schema-mismatch`. Download writes YAML named
`<name>.layers.yaml` (`layerSetFileName`). Upload layer set… adds the file
as a new set, and its icons go into the icon library
(`ingestLayerSetIcons`). Upload of a diagram refuses a layer set file and
names the Layers tab (`isLayerSetFileValue`, checked after the raw parse,
as `isTemplateFileValue` is).

## Built-in icons

Code: `types/builder/icons.go` (`iconKeys`), `builder/catalog.js`
(`ICON_KEYS`, `KIND_META`), `builder/iconLabels.js`,
`components/builder/BuilderIcon.vue` (`PATHS`).

- A new key goes in `iconKeys` (sorted) and `ICON_KEYS`, with a label and
  a place in `ICON_LABELS`, and a drawing in `PATHS`. Then run
  `make generate-builder-schema` for the schema enum.
  `TestIconKeysRegistry`, `catalog.test.js` and `icon-render.test.js`
  check that the lists agree and that each key has its own drawing.
- Stored keys are lowercase and never renamed: documents, templates and
  drafts store them, and validation has no aliases. The UI shows
  `iconLabel(key)`, never the key.
- A drawing is stroke only on a 24-unit grid (stroke 1.6, round caps). A
  logo or a solid part can fill with `currentColor` (for example
  `phenix` and `lizard`). Export copies `fill` with the other paint
  (`SVG_PAINT_PROPERTIES` in `exporters.js`).
- `blackhat` is the only drawing with a color of its own. Its hat and
  glasses (the `builder-icon__ink` group) fill `#111111` with a thin
  `currentColor` outline, so the hat stays black in the dark theme. In
  forced colors, the `BuilderIcon.vue` style fills that group with
  `currentColor`. `icon-render.test.js` permits this one color only.
- A drawing from another work, or a mark with terms, needs an entry in
  `components/builder/ICON_NOTICES.md`. A product icon (`ubuntu`, `kali`,
  `wireshark` and the like) is an original drawing, not the product's
  logo, unless the owner's terms allow a one-color copy.
- Notes, shapes and lines use the chrome icons `sticky-note`, `rectangle`
  and `line` as their kind icon, not a registry key.
- Node type to key (`iconKeyForSpec` in Go and JS): any node type that is
  a key gives that key, so type `PLC` gives `plc`. The shared fixture is
  `testdata/icon-keys.json`.

## Icon library

Code: `types/builder/customicons.go`, `api/builder/icons.go`,
`web/builder_icons.go`, `builder/icons.js`, `iconLibrary.js`,
`dialogs/IconDialog.vue`.

- Two UIs edit the library: the Custom icons dialog (`IconDialog.vue`,
  from Custom icon… in the menu of the Inspector's Icon field,
  `inspector/InspectorIconControl.vue`, and of a layer's icon,
  `layers/LayerIconPicker.vue`) and the drafts page's Icons tab (`BuilderIcons.vue`,
  in the `icons` slot of `BuilderDrafts.vue`). Both use `IconUpload.vue`
  for Upload icon…, with an id `prefix` each, since the template editor
  can open the dialog over the tab. The tab's Preview is
  `dialogs/IconPreviewDialog.vue`. Download is `saveIconFile` in
  `icons.js`: the stored bytes, never drawn again. The view's
  `openIcons(name)` (VIEW_API) shows the tab and calls the tab's
  `showIcon(name)`.
- The tab's Show select (`showing`) shows the built-in icons by default
  (`builtinIcons()` in `builder/builtinIcons.js`: `ICON_ORDER` filtered by
  `isIconKey`), so a key added to the registry and to `ICON_LABELS` shows
  there. Their cards are not selectable (`selectable: () => false`, keys only move focus).
  Download renders `BuilderIcon` into a detached element, serializes its
  `<g>` and saves `builtinIconSvg` (fixed color `BUILTIN_ICON_COLOR`, 96
  px). The tab's count is the server library only. An upload
  (`onUploaded`) and `showIcon` set `showing` to `'server'`, where the
  icon is.
- One library for the whole server. Any user with `builder` `use` renames
  or deletes any uploaded icon. `canRename` and `canDelete` in the list
  response are false only for a server icon (`MayChangeIcon`), whose
  rename or delete answers 403. The Icons tab card, the icon preview's
  Source and the `i:` palette choice name the uploader as "Uploaded by
  <user>", or "Server" (`iconOwnerText` in `icons.js`). A built-in icon's
  card and preview say "Built-in" (`BUILTIN_ICON_OWNER`). The library
  holds at most `MaxIcons` (2000) icons of all users together. The list
  response gives `maxIcons`, and `usageText` in `icons.js` shows "The
  library holds N of 2000 icons." A name is unique ignoring case, and the first upload
  of a name wins. A later upload of the name with other bytes
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
  compare-and-swap, so two uploads of one name never both win. An icon
  keeps `createdBy` (the uploader) and `updatedBy` (who renamed it last).
  A server icon has no `createdBy`, and has `updatedBy` only after a
  rename.
- A draft names icons and carries no image data. `settleIcons(doc, library)`
  in `store.commit` drops copies that nothing names and copies the library
  holds with the same bytes. A download embeds every icon the document
  names, by its nodes, templates and layers (`embedIcons` in
  `exporters.js`, `IconNames` in Go). An upload with copies
  (`ingestIcons`) adds each name the library lacks and warns for each copy
  it keeps.
- Every `/builder/` response has `X-Content-Type-Options: nosniff`. The
  icon routes also send `Content-Security-Policy: default-src 'none';
  frame-ancestors 'none'` (`builderResponseHeaders`). No CSP is set on the
  application page.
