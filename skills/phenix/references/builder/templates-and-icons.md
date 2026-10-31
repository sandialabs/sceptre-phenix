# Node templates, layer sets and icons

Part of the [Builder references](../builder.md). User docs:
[Node Templates](https://phenix.sceptre.dev/latest/builder/templates/)
(with its limits), [Layers](https://phenix.sceptre.dev/latest/builder/diagrams/#layers),
[Custom icons](https://phenix.sceptre.dev/latest/builder/diagrams/#custom-icons),
[Template files on the server](https://phenix.sceptre.dev/latest/builder/administration/#template-files-on-the-server).

## Template library

Code: `types/builder/template.go`, `api/builder/templates.go`,
`web/builder_templates.go`, `builder/templates.js`, `BuilderTemplates.vue`,
`BuilderPalette.vue`, `inspector/InspectorTemplateLinks.vue`.

- One library per server: one record, `builder.templates` key `library`
  (`LibraryKey`), at most 512 KiB (`MaxMetadataBytes`). Every user with
  `builder` `use` reads and changes it. Items have no owner and are never
  published. A template names its custom icon, and the library holds no
  icons.
- The library holds only the templates that users added. The five built-in
  templates (`BuiltinTemplates()` in Go, `BUILTIN_TEMPLATES` in
  `catalog.js`, fixture `types/builder/testdata/builtin-templates.json`)
  and the templates of server collections are read only and never in the
  record. With no record, the library is empty. A write route that names a
  read-only item answers 409 `template.item.read-only` (`refuseReadOnly`).
  The UI offers Duplicate for them.
- Each built-in spec is a complete, valid phenix node spec. The vrouter app
  configures routing and rulesets only on Router or Firewall nodes with a
  router OS type (minirouter, vyatta or vyos). For the deprecated `linux`
  OS type it writes a Vyatta config into the image. So the Router template
  runs minirouter and the Firewall template runs VyOS, each on the image
  named for its OS type. Built-in ids are names, not UUIDs. A copy in
  a diagram gets its own id (`addTemplate` in `model.js`).
- Each template and collection keeps `version` (1, then one more per
  content change), `createdBy` and `updatedBy`. `stamp` sets them from the
  `actor` of a content change. A read-only item is version 1
  (`ReadOnlyTemplateVersion`) with no times and no users.
- Each write is `Service.UpdateLibrary(ctx, actor, change)`: read, run
  `change`, stamp versions, times and users, check, and write against the
  read revision. It tries up to 5 times, then answers `ErrBusy` (503,
  `Retry-After: 1`). Refusals are `*LibraryError`. A record that a newer
  phenix wrote is `damaged`: listed without its items, and each change
  answers 409. The two `PUT` routes need `If-Match`, and
  `POST /builder/templates/delete` needs an `etag` per item. A stale tag
  answers 412 `template.item.stale` with the current `ETag`.
- `GET /builder/templates` answers `templates`, `collections`, `builtins`,
  `preloaded` (the server collections with their templates), `damaged` and
  `limits`. Each template has `source`: `library`, `builtin` or
  `preloaded`. Every caller gets the same answer.
- `store.fetchTemplates` reads the library again when the lists load, when
  a draft opens, when the window comes to the front with the editor open,
  and after each library change. A failed read sets `templates.error` and
  keeps the last result.
- Save to library and template import add the template's icons to the icon
  library under the upload rules (`ingestSavedTemplateIcons`,
  `ingestTemplateIcons`).

### Template copies and updates

- A diagram's template can name its origin in `source`
  (`{kind, id, version, digest}`, see
  [document.md](document.md#keys-and-validation)). `templateSource` and
  `linkedCopy` (`templates.js`) make it. `digest` is `templateCopyDigest`,
  the content digest of the copy's name, description and device.
- A library or server template placed on the canvas is copied into the
  diagram, linked (`withLinkedCopy` in the store's `addNode`), unless a
  linked copy exists. A built-in template never changes, so it is never
  copied. When the diagram holds `MAX_TEMPLATES` (50) templates, the device
  gets no link and the announcement says so. Save to library links the
  diagram's template to the new library template (`store.linkTemplateCopy`).
- `templateLink(copy, library)` gives the state of a copy: `current`,
  `update`, `deleted` or `unknown` (library not read or damaged). `update`
  means a later version, or for a `preloaded` copy a different digest,
  because a server template stays at version 1. `edited` is true when the
  copy changed in the diagram. `templateLinks(doc, library)` lists the
  copies with their state. `templateLinkText` and `templateLinkMark` give
  the words.
- `store.updateTemplateCopies(ids)` copies the latest versions in one undo
  step (`updatedCopy`). `store.unlinkTemplate(id)` deletes `source` and
  keeps the copy. A device made from a template never changes.
- The palette marks a copy that needs attention under its entry (test ids
  `palette-template-link-<id>`, and `palette-template-update-<id>` or
  `palette-template-unlink-<id>`). The template menu has Update and Unlink
  too. While a copy needs attention, the Inspector's Diagram section shows
  `InspectorTemplateLinks.vue` ("Template updates": Update, Update all,
  Unlink).

### Lists of templates

- The editor's Templates list (`BuilderPalette.vue`) has one section per
  group (`paletteTemplateGroups` in `templates.js`): "This diagram", then
  "Library" for templates in no collection ("Other library templates" when
  the library has collections), then one per collection (library
  collections, then "Server: <name>"), and always "Built-in". Section ids
  are `diagram`, `library`, `builtin` or the collection's `templateKey`
  (`library:<id>` or `preloaded:<id>`). A template in several collections
  gets `-in-<section id>` on its test id after the first. Add device in the
  command palette lists each template once. localStorage
  `phenix.builder.palette` (`paletteSections.js`) keeps the collapsed
  sections, and logout clears it.
- The drafts page's Node Templates tab (`BuilderTemplates.vue`) has a Show
  select (`showChoices`, `shownList`): all library templates, the library
  templates with missing images (`SHOW_MISSING_IMAGES`), each library
  collection, the built-in templates (`SHOW_BUILTIN`) and each server
  collection. A read-only card has a "Read only" tag and View, Duplicate
  and Download. Its test ids name the source: `template-<part>-<source>-<id>`
  (such as `template-card-builtin-router`). A library card's test ids are
  `template-<part>-<id>`. The collection buttons and the row's Delete,
  Duplicate and Download selected are item actions too.
- `missingImages` (`templates.js`) finds the drive images of each template
  that are not in `store.disks`, with `unknownDriveImages` (`validate.js`),
  the rule of `device.image.unknown`. A card with one shows a warning
  (`template-missing-images-<id>`) as its `aria-describedby`. The tab reads
  the disk images with `useDiskReads` (`diskReads.js`) while it shows (its
  `active` prop). While `store.disks` is null, nothing is marked and Show
  does not offer the list. A failed re-read sets `store.disks` to null. If
  Show is on the list then, the choice stays (disabled, "(disk images
  unknown)"), the list is empty with a hint, and the tab announces it.

## Template files

`TemplateFile`, `ParseTemplateFile` (`types/builder/templatefile.go`) and
`templateFile.js` read one collection as YAML or JSON, strictly. The schema
is `TemplateFileSchema()`, served at `GET /schemas/builder/templates/v1`.
Names are unique, ignoring case and outer white space (shared cases in
`testdata/template-file-names.json`). The docs example
`docs/content/builder/examples/node-templates.yaml` must load
(`TestDocsTemplateFileExampleLoads`). `FileKindOf` gives
`FileKindTemplateFile` for a template file.

Upload refuses a template file and names Import templates
(`isTemplateFileValue` in `templateFile.js`, checked after the raw parse,
as `isPackageValue` is). A value is a template file when it names the
template file schema, or when it names no schema, has a `templates` list
and has no key that only a diagram has. A diagram with templates names its
own schema.

### Server collections

`newBuilderAPI` reads the directory `--base-dir.builder-templates`
(`common.BuilderTemplatesBase`) once at start
(`Service.LoadServerTemplates`, `ReadTemplateDirectory` in
`api/builder/templatefiles.go`). Tests pass `withBuilderTemplateFiles("")`
to read nothing.

- Files open through `os.Root`, so a link out of the directory is refused.
  A bad file is logged and skipped (`skipping builder template file`). The
  Builder starts in all cases.
- Each file is a read-only `ServerCollection` in memory, never in the
  store. IDs are `server-` plus 24 hex of a SHA-256 of the file name (and
  of the template's lowercase name). A write route that names one answers
  409 `template.item.read-only`. `GET /builder/templates` lists them in
  `preloaded`. After a restart, a diagram's copy finds a changed file by
  its digest (see [Template copies and updates](#template-copies-and-updates)).
- Icons in the files that the library lacks are added with owner ""
  (`ServerIconOwner`), only while the library holds fewer than
  `MaxIcons - ServerIconReserve` (1000) icons. No account owns them, and no
  user renames or deletes them (`MayChangeIcon`).

## Layer set library

Code: `types/builder/layerset.go`, `api/builder/layersets.go`,
`web/builder_layersets.go`, `builder/layerSetStore.js`,
`BuilderLayerSets.vue`, `dialogs/LayersDialog.vue`, `layers/`.

- One library per server, in `builder.layersets`: one record per set, key
  `set/<id>` (`LibraryLayerSet`, at most `MaxMetadataBytes`). The record
  `default` exists when a user chose the server's default set
  (`DefaultLayerSet`, `SetDefaultLayerSet`). At most 200 sets
  (`MaxLayerSets`), not counting the built-in sets. Any user with `builder`
  `use` adds, changes or deletes any set, and sets the default. There is no
  other role gating.
- `BuiltinLayerSets()` gives the built-in sets, which have no record. The
  one built-in set is the Purdue model, ID `builtin:purdue`
  (`BuiltinPurdueLayerSetID`). A change or delete of it answers 403
  `layerset.read-only`.
- A set (`LayerSet`) has `name`, `description`, `iconKey`, `icon`,
  `matchConflict`, `layerOutlines` and 1 to 32 `layers`, under the rules of
  a document's layers. Each stored set has `version` and the users and
  times that a template has.
- Routes: `GET` and `POST /builder/layersets`, `GET`, `PUT` and
  `DELETE /builder/layersets/{layerset}`, and `GET` and
  `PUT /builder/layersets/default` (registered before `{layerset}`). A
  set's `PUT` and `DELETE` need `If-Match` (`"<version>"`). The default's
  `PUT` needs the tag of the choice (`defaultEtag`). Answers: 412
  `layerset.stale` with the current `ETag`, 413 `layerset.library.full`,
  422 `layerset.invalid` with `issues`, 404 for an unknown set, 409 for a
  record this server cannot read, and 503 after 5 tries. A default that
  names no set answers 422 `layerset.default.unknown`. A default whose set
  was deleted reads as null. The listing gives the built-in sets first,
  then the others by name, with `defaultId`, `damaged` (the count of
  unreadable records, which it omits) and `limits`.
- A document holds a full copy of the layers and links to a set by
  `layerSet.source` (`withLayerSetCopy` in `model.js`). The store's
  `layerSetLink` getter is `unknown` until the library is read. Actions
  (`layerSetActions`): `fetchLayerSets`, `createLayerSet`, `saveLayerSet`,
  `deleteLayerSet`, `deleteLayerSets`, `duplicateLayerSet`,
  `setDefaultLayerSet`, `uploadLayerSetFile`, `applyLayerSet`,
  `updateFromLayerSet`, `unlinkLayerSet`, `saveToLayerSet`,
  `saveAsNewLayerSet` and `withDefaultLayerSet`. `saveToLayerSet` refuses
  when the set has a newer version than the link. `saveLayerSet` sends the
  version the editor opened, so a change made meanwhile answers 412.
- `fetchLayerSets` runs where `fetchTemplates` does, and when the layers
  editor opens in diagram mode.
- The Layers tab (`BuilderLayerSets.vue`, the `layers` slot of
  `BuilderDrafts.vue`, between Node Templates and Icons):
  - Filter layer sets is `BuilderCardFilter.vue` with `filterItems` and
    `layerSetSearch` (name and description), kept as
    `phenix.builder.filter.layers`.
  - The shown sets are a `useListSelection` list with the drafts page's
    keys, and each set is selectable. Download selected saves one file per set
    (`layerSetsDownloadedMessage`).
  - Delete selected calls `deleteLayerSets`: one request at a time, one
    library read at the end. It never sends a built-in set and returns it
    as a failure with `BUILTIN_NOT_DELETED`. With only built-in sets
    selected, the tab shows the summary without a question.
  - Card buttons are item actions (Edit or View, Duplicate, Download,
    Delete) in `.builder-card__actions`. Then comes Set default or Clear
    default (`layer-set-default-toggle`): a `BuilderBusyButton.vue` with
    the `star` icon and both labels in `.builder-button__swap`. Only the
    built-in sets open read only (View in place of Edit).
  - Cards are at least 20.5rem wide (`.builder-layer-sets__cards`), so
    Clear default fits half a card. A card narrower than 18.5rem inside
    gives the toggle the whole row.
  - Edit, View and New layer set open `LayersDialog` in `library` mode.
- Layer icons use `LayerIconPicker.vue`, with the same menu button and
  Custom icons dialog as the Inspector's Icon field. The dialog provides
  `INSPECTOR_ICONS` and `INSPECTOR_ICON_LIBRARY` for it. `LayerName.vue`
  shows a layer's icon and its label in its color, or a swatch. `layerInk`
  (`layerLook.js`) decides per theme whether the label can take the color
  (4.5:1 against `LAYER_TEXT_BACKGROUNDS`).

### Layer set files

`layerSetFile.js` reads one set as YAML or JSON, strictly, with copies of
the custom icons it names. Go keeps only the schema
(`types/builder/layerset_schema.go`) and `FileKindLayerSetFile`. The schema
is `LayerSetFileSchema()`, served at `GET /schemas/builder/layersets/v1`.
Code: `layerset.file.schema-mismatch`. Download writes YAML named
`<name>.layers.yaml` (`layerSetFileName`). Upload layer set… adds the file
as a new set and its icons to the icon library (`ingestLayerSetIcons`).
Upload of a diagram refuses a layer set file and names the Layers tab
(`isLayerSetFileValue`, checked after the raw parse, as
`isTemplateFileValue` is).

## Built-in icons

Code: `types/builder/icons.go` (`iconKeys`), `builder/catalog.js`
(`ICON_KEYS`, `KIND_META`), `builder/iconLabels.js`,
`builder/iconPaths.js` (`PATHS`, which `BuilderIcon.vue` draws).

- A new key goes in `iconKeys` (sorted) and `ICON_KEYS`, with a label and
  a place in `ICON_LABELS`, and a drawing in `PATHS`. Then run
  `make generate-builder-schema` for the schema enum.
  `TestIconKeysRegistry`, `catalog.contract.test.js` and
  `icon-render.test.js` check that the lists agree and that each key has
  its own drawing.
- Stored keys are lowercase and never renamed: documents, templates and
  drafts store them, and validation has no aliases. The UI shows
  `iconLabel(key)`, never the key.
- A drawing is stroke only on a 24-unit grid (stroke 1.6, round caps). A
  logo or a solid part can fill with `currentColor` (such as `phenix` and
  `lizard`). Export copies `fill` with the other paint
  (`SVG_PAINT_PROPERTIES` in `exporters.js`).
- `blackhat` is the only drawing with its own color. Its hat and glasses
  (the `builder-icon__ink` group) fill `#111111` with a thin `currentColor`
  outline, so the hat stays black in the dark theme. In forced colors, the
  `BuilderIcon.vue` style fills that group with `currentColor`.
  `icon-render.test.js` permits this one color only.
- A drawing from another work, or a mark with terms, needs an entry in
  `components/builder/ICON_NOTICES.md`. A product icon (`ubuntu`, `kali`,
  `wireshark` and similar) is an original drawing, not the product's logo,
  unless the owner's terms allow a one-color copy.
- Notes, shapes and lines use the chrome icons `sticky-note`, `rectangle`
  and `line` as their kind icon, not a registry key.
- Node type to key (`iconKeyForSpec` in Go and JS): a node type that is a
  key gives that key, so type `PLC` gives `plc`. The shared fixture is
  `testdata/icon-keys.json`.

## Icon library

Code: `types/builder/customicons.go`, `api/builder/icons.go`,
`web/builder_icons.go`, `builder/icons.js`, `iconLibrary.js`,
`dialogs/IconDialog.vue`.

- Two UIs edit the library:
  - The Custom icons dialog (`IconDialog.vue`) opens from Custom icon… in
    the menu of the Inspector's Icon field
    (`inspector/InspectorIconControl.vue`) and of a layer's icon
    (`layers/LayerIconPicker.vue`). It picks (Use, a
    `BuilderBusyButton.vue` with the `check` icon to match Rename),
    uploads, adds the diagram's copies (one, or Add selected to server) and
    renames (Rename is the `edit` item action with its own label). Its
    server icon rows have no checkbox and no selection: `libraryStop` holds
    their Tab stop, and the keys only move focus.
  - The drafts page's Icons tab (`BuilderIcons.vue`, the `icons` slot of
    `BuilderDrafts.vue`). Only the tab deletes (Delete, Delete selected).
    Its View (`dialogs/IconPreviewDialog.vue`) is on every card, because
    the preview never edits. The view's
    `openIcons(name)` (VIEW_API) shows the tab and calls its
    `showIcon(name)`.
  - Both use `IconUpload.vue` for Upload icon…, each with its own id
    `prefix`, because the template editor can open the dialog over the
    tab. A server icon's Download is `saveIconFile` in `icons.js`: the
    stored bytes, never drawn again. The tab's bulk buttons and the
    preview's Download are item actions too.
- The tab's Show select (`showing`) shows the built-in icons by default
  (`builtinIcons()` in `builder/builtinIcons.js`: `ICON_ORDER` filtered by
  `isIconKey`), so a key added to the registry and to `ICON_LABELS` shows
  there. Download (`saveBuiltinIconFile`) passes `builtinIconSvg` of
  `iconPath` (fixed color `BUILTIN_ICON_COLOR`, `BUILTIN_ICON_FILE_SIZE`
  96 px) to the tab's `rasterize` prop (`rasterizeIcon`), and saves the PNG
  as `<key>.png`. An upload (`onUploaded`) and `showIcon` set `showing` to
  `'server'`.
- The tab's count (`iconsTabCount` in `builtinIcons.js`, null until the
  library is read) and its usage line (`usageText(state, builtin)`) count
  the built-in icons plus the library's icons. `MaxIcons` and its
  enforcement count only uploaded and server icons. The Custom icons
  dialog's usage line counts the library only, as its Server icons list
  does.
- One selection (`useListSelection` over `listed`, the cards Show shows,
  keyed `builtin:<key>` or `server:<name>`) covers both lists. So every
  card has a checkbox, and a Show change clears the selection and the bulk
  summary (`outcome`). The one bulk row (id `icons`) has Download selected
  (`runBulk` with `limit: 1`, one PNG per icon, "Downloaded N icons.") and
  Delete selected. Delete selected deletes only `canDelete` icons. The bulk
  summary lists built-in and server icons as not deletable, after the
  question, or with no question when nothing selected is deletable.
- One library per server. Any user with `builder` `use` renames or deletes
  any uploaded icon. `canRename` and `canDelete` in the list response are
  false only for a server icon (`MayChangeIcon`), whose rename or delete
  answers 403. The Icons tab card, the icon preview's Source and the `i:`
  palette choice name the uploader as "Uploaded by <user>", or "Server"
  (`iconOwnerText` in `icons.js`). A built-in icon's card and preview say
  "Built-in" (`BUILTIN_ICON_OWNER`).
- The library holds at most `MaxIcons` (2000) icons of all users together.
  The list response gives `maxIcons`, and `usageText` shows "The library
  holds N of 2000 icons." A name is unique ignoring case, and the first
  upload of a name wins. A later upload of the name with other bytes
  answers 409 and names the uploader. An icon's `id` is the SHA-256 of its
  bytes.
- Icon rules that Go (`customicons.go`: `ValidateIcons`, `IconNameProblem`,
  `ValidateIconPNG`) and JS (`validateIcons` and `iconNameProblem` in
  `validate.js`, `iconPNGProblem` in `icons.js`) apply the same way:
  - A document's `icons` map holds at most 50 entries (`MaxDocumentIcons`,
    `MAX_DOCUMENT_ICONS`).
  - An icon is a PNG of 1 to 96 pixels a side and at most 40960 bytes,
    with only the chunks IHDR, PLTE, tRNS, IDAT and IEND.
  - An icon name matches `^[A-Za-z0-9_@.-]+$`, is 1 to 64 bytes, and is
    not `.` or `..`.
- Records in `builder.icons`: `name/<lowercase name>` holds an icon
  (`{kind: "icon", name, id, owner, …, aliases, data}`) or an alias
  (`{kind: "alias", name, target}`). A rename keeps the old name as an
  alias and points every alias at the new name (`retargetAliases`), so an
  alias is one hop from its icon. A rename that gives an icon more than
  `maxIconAliases` (16, also the resolution hop bound) old names is a 422
  `ValidationError`. No alias is dropped. `encodeIconRecord` refuses a
  record over `MaxMetadataBytes`. Creates are create-if-absent and renames
  compare-and-swap, so two uploads of one name never both win. An icon
  keeps `createdBy` (the uploader) and `updatedBy` (who renamed it last). A
  server icon has no `createdBy`, and has `updatedBy` only after a rename.
- Server reads:
  - `GET /builder/icons` lists the icons without `data`
    (`builderIconSummary`). `ListIcons` checks each PNG with
    `builder.IconPNGSize` (size, signature, chunks, ID) and decodes no
    pixels. `GetIcon` and every other read use the full `ValidateIconPNG`.
  - The list gives each icon's `revision` and `etag`, and the list's
    `etag` (`builderIconListETag`, a hash of the list). It answers 304 to
    an `If-None-Match` of that tag (`builderNoneMatch`, weak comparison).
  - `GET /builder/icons/{icon}` gives one icon with `data`, and 304 for
    the icon's tag. POST and PUT answer with `data`.
- Client reads (`iconLibrary.js`):
  - `state.icons` and `state.index` hold the listing without images.
    `load()` sends the last list tag.
  - `lookup(name)` gives a listed icon with `data` once its image is read,
    else with `pending: true`, and starts the read (at most 4 at once,
    `getIcon`). A reactive map keeps images by `id` (the image hash), so
    each image is read once, also across renames and icons of one image.
  - An image that could not be read gives the icon with neither, which
    draws the built-in icon. It is read again after the next list read.
    `iconSrc` draws a `pending` icon from `PENDING_ICON_SRC`, a transparent
    PNG, so a node keeps its square and shows no other icon meanwhile.
  - Code that saves icons into a file first awaits `ensureImages(names)`:
    Download (JSON, YAML, PNG, SVG and draw.io), `downloadedDiagram`
    (`diagramFile.js`: drafts downloaded in bulk, another tab's version),
    template export, layer set download and the Icons tab's Download.
    After PNG, SVG and draw.io, `pictureIconNote` (`exporters.js`) names
    the drawn icons whose image could not be read (`subject: 'file'` for
    draw.io, whose `toDrawio` draws the built-in icon in its place).
  - Upload, rename and delete take the server's answer into the list
    (`put`, `drop`), with no list read. They clear the list tag, so the
    next `load()` reads the full list, because the hash can name an older
    list again. A 404 delete drops the icon, and a 412 reads the list
    again.
  - The bulk summary of `BuilderIcons.vue` also shows in the empty-list
    branch, so a run that deletes the last icons still lists its failures.
  - When an image is not read, copies are compared with the library by
    image ID (`holdsImage`, `iconImageId` with `sha256Hex` of `digest.js`).
- A draft names icons and carries no image data. `settleIcons(doc, library)`
  in `store.commit` drops copies that nothing names, and copies that the
  library holds with the same bytes (`holdsImage`). A download embeds each
  icon that the document names by its nodes, templates and layers
  (`embedIcons` in `exporters.js`, `IconNames` in Go). An upload with
  copies (`ingestIcons`) adds each name that the library lacks, and warns
  for each copy it keeps.
- Each `/builder/` response, also an error, a 304 and the router's own 404
  and 405, has `X-Content-Type-Options: nosniff` and
  `Cache-Control: no-store`. The `/assets/` files keep their own caching
  (`builderAssetHandler`). The icon list and icon reads still answer 304 to
  the `If-None-Match` that `api.js` sends. The icon routes also send
  `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`
  (`builderResponseHeaders`). The application page has no CSP.
