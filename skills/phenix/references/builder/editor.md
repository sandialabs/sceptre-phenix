# Builder editor and drafts page

Part of the [Builder references](../builder.md). User docs:
[The Editor](https://phenix.sceptre.dev/latest/builder/editor/),
[Building a Diagram](https://phenix.sceptre.dev/latest/builder/diagrams/)
and [Drafts](https://phenix.sceptre.dev/latest/builder/drafts/). They hold
the UI text, keys and limits.

## Opening a diagram

- Configs page links (`builderAction`, `builderLink`, `builderTagLabel` in
  `builder/configs.js`) go to `/builder?topology=<name>`. `openLinked`
  (`Builder.vue`) picks a draft with
  `draftForPublished(drafts, published, token)` (order:
  [Editing a published topology](https://phenix.sceptre.dev/latest/builder/publishing/#editing-a-published-topology)), else
  opens the Import dialog with `initial: {kind: 'topology', name}`.
- The address names the open draft as `?draft=<id>`, so a reload opens it
  again. An unsaved draft's ID stays out of the link. A link with a `/`
  names no draft: it opens nothing and says "This link does not name a
  draft. Open one from the lists instead."
- Card Publish on the drafts page loads the draft without the editor
  (`store.loadDraft(id, {quiet: true})`) and opens the Publish dialog over
  the page.
- Without `builder` `use` (`store.canUseBuilder`), `Builder.vue` renders
  only the notice "Your role does not include the Builder."
  (`builder-no-access`) and loads no drafts.

## Inspector

- The Inspector builds JSON Forms from the schema (`schema.js`,
  `adapters/forms.js`, `inspector/`).
- Gotcha: a change of `store.doc` reloads the form only while
  `keepsWorkingCopy` (`adapters/forms.js`) is false: no unapplied edits, no
  uncommitted text, no focus on Apply or Cancel. Else `rebasedWorkingCopy`
  moves the working copy onto the current element (`mergeFormData`). A
  stamp-only change (`sameButStamp`) never resets the form, because a reset
  drops typed text (`builder-inspector.spec.js`).
- Gotcha: a text or number field binds its typed text, not its data, until
  its data changes or the form reloads (Cancel, undo, Apply,
  `useInspectorResets`). This is `useFieldText` (`inspector/control.js`).
  Other re-renders (new warnings, disk images, a schema update) patch the
  input with what it binds, so a binding to the data would overwrite
  uncommitted text.
- The host (`host` prop of `BuilderInspector.vue`) is a reactive object
  with the store members that the Inspector uses: `doc`,
  `inspectorSelection`, `schema`, `schemaError`, `readOnly`, `disks`,
  `issues`, `canRedo`, and the actions `commit(doc, label)`,
  `announce(message)`, `addInterface`, `removeInterface`, `remove`,
  `moveNodes` and `setNodeLayer`. Go to needs `focusRequest` and
  `takeFocusRequest`. Assign addresses needs `assignAddresses(networkId)`.
  A host without these features omits them.
- Apply refuses only while a field that the edits changed has an error
  (`workingCopyErrors`). Old errors on unchanged fields show nowhere and
  block nothing. A renderer reports problems that never reach the working
  copy (a key/value row with no name, or a name that a row above has) as
  `{path, message}` entries of `INSPECTOR_LOCAL_PROBLEMS` under its data
  path (`InspectorMapRenderer.vue`). The error summary links them, and
  Apply refuses while any exist, so the edit does not vanish. Renderers
  outside the Inspector report to nothing.
- Locked fields (`useInspectorLocked`): fields of a device from an included
  topology or of a read-only draft, fields that the uischema `readonly`
  option locks (`inspectorLock`), and fields that the schema marks
  `readOnly`. A locked field is a read-only input, not a disabled one, so
  Tab reaches it and it has full contrast. A locked checkbox is
  `aria-readonly` and ignores clicks. A locked select is a read-only text
  input with the chosen option's name.
- `InspectorEnumControl.vue` shows a value that matches no choice as its
  own choice, with a warning. It names the empty choice by the schema's
  `UNSET_KEYWORD` word (`schema.js`), such as "Auto (Dashed)", else
  "Default". `InspectorInputControl.vue` commits no text that a number
  field cannot read, and no number that a whole-number field cannot take
  (1.5, 1e3). A field that phenix takes as a number or text (Memory, VCPUs,
  `numberBranch`) keeps other text, such as `{{ .Memory }}`, until a number
  replaces it. User docs:
  [Editing a node](https://phenix.sceptre.dev/latest/builder/editor/#editing-a-node).
- An interface renamed in the Inspector keeps its handle and connection
  when the spec has as many interfaces as handles and its VLAN still names
  the handle's network (`reconcileDeviceHandles`, `model.js`). A removal
  plus an addition in one Apply is not a rename and keeps no handle. Two
  spec entries with one name each get their own handle.
- Instant fields (`instantKeys(kind)` in `adapters/forms.js`: colors,
  icon, icon size, border pattern, a switch's Line style, a line's or a
  connection's Label and Line style) apply without Apply. Each change is
  one `host.commit` named by `instantChangeLabel`. `holdLook`
  (`BuilderInspector.vue`) picks them out of a form change and skips a
  field with a working-copy error. `commitLook` merges them into the
  element's data (`applyFormData`). A select in `HELD_LOOK_PATHS` holds a
  choice stepped to with keys until it is made (`heldCommit.js`). Typing in
  such a field does not show Apply (`typingInstant`), and Escape puts back
  the committed text (`restoreText`). Gotcha: a new instant field goes in
  `INSTANT_KEYS`, and also in `HELD_LOOK_PATHS` for a select.
- The description toggle (`inspector/InspectorDescriptionToggle.vue`,
  `inspector-description-toggle`) sits in the wrapper of the Description
  field of a device (`spec.general.description`) or a group
  (`description`), by `descriptionPath(kind)` in `adapters/forms.js`.
  `InspectorInputControl.vue` draws it from `useDescriptionToggle`, which
  reads `INSPECTOR_DESCRIPTION` (`{path, shown, locked, set}`) from
  `BuilderInspector.vue`. Like the Layer field, it acts outside the working
  copy: `setDescriptionShown` commits `setDescriptionHidden` (`model.js`)
  through `host.commit`, one undo step named by `descriptionShownLabel`. So
  it works in the canvas Inspector, the Node Editor and the template
  editor. It is an `aria-pressed` button ("Show description on the
  canvas", pressed while shown), disabled in a read-only draft and on an
  included device. While the setting `showNodeDescriptions` is off,
  `INSPECTOR_FIELD_NOTES` adds a note under the field.
- The Icon field (`inspector/InspectorIconControl.vue`, at `iconKey`)
  edits `iconKey` and the custom `icon` with one `BuilderMenuButton.vue`:
  the `ICON_ORDER` icons that the schema offers and `isIconKey` accepts,
  then Custom icon…, which opens `IconDialog`. `withOneIconField`
  (`adapters/forms.js`) drops the `icon` control. A choice sets both fields
  in one task, so JSON Forms sends one change. Default shows only where
  `fieldDefault` names the icon that the node draws (not on an icon
  drawing, whose payload needs exactly one icon).
- Default box and Default button (`inspector/InspectorField.vue`,
  `inspector/control.js`): an unset field with a default shows the Default
  box (`useFieldDefault`, `fallback` prop). While a field with a default
  (`useUnsetValue`) holds another value, `useDefaultReset` gives the
  `reset` prop. Default is then a button (`inspector-field-reset-default`)
  named `Reset <label> to the default, <value>` (booleans read on or off)
  and described by `<note>.`. A reset clears an optional field or sets a
  required one to the default in the working copy (it waits for Apply),
  and focus goes back to the input. A value equal to the default shows
  neither. `useInspectorControl` returns the field `note` once (`note`
  prop) and an `ids.reset` id. Combobox, Color, Icon and OneOf controls
  pass the note only, with no Default box.
- The Layer field (`inspector/InspectorLayer.vue`, `chooseLayer`) acts at
  once, outside the working copy. Its choices are `effectiveLayers(host.doc)`
  plus the node's own ID when the diagram does not define it
  (`layerChoices`), named by `layerTitles` (`model.js`: the label, or
  "Control (ID 3)" when two layers share it). For a device at a matched
  layer, the first choice names that layer and the field that matched, in
  place of None. Use matched layer clears the chosen layer
  (`INSPECTOR_LAYER.useMatched`, which calls `chooseLayer('')`).
  `LayerName.vue` shows the layer's icon and color before the select
  (`layerLook`, `hasLayerLook` in `layerLook.js`), because native options
  cannot show them. The `layer` prop of `BuilderInspector.vue` places the
  field: `section` (canvas default) above the connection points, or `look`
  (template and Node Editor default) as a form field after the look
  fields. `look` passes `layer: true` to `uiSchemaForKind`, which adds a
  `LAYER_UI_TYPE` element that `InspectorLayerRenderer.vue` draws from
  `INSPECTOR_LAYER`. The template editor reads its layers and the conflict
  rule from `templateDocument`. Edit layers… under the field (not in the
  template editor) and in `InspectorDiagram.vue` emit `layers`.
- The layers editor (`dialogs/LayersDialog.vue`) has two modes. `diagram`
  edits `store.doc` through `addLayer`, `relabelLayer`, `updateLayer`,
  `renumberLayer` and `removeLayer` (one undo step each), shows the layer
  set link, and has Update from layer set, Save to layer set, Save as new
  set…, Unlink and Apply. `library` edits a copy of one server set and
  saves it (see
  [templates-and-icons.md](templates-and-icons.md#layer-set-library)). Both
  use `layers/LayerRows.vue`, whose parent passes `ops`: `add()`,
  `renumber(from, to)`, `update(id, changes)` and `remove(id)`, each
  returning what the dialog says. Rows keep a key per layer ID across a
  renumber. After a re-sort, focus goes back to its field when Tab
  finishes. `openLayers` (`Builder.vue`, VIEW_API) opens the dialog in the
  editor, or shows the drafts page's Layers tab. Over the Node Editor it
  opens on top (`layersOver`), as Settings does (`settingsOver`), so the
  Node Editor keeps its edits. Edit layers… in Settings closes Settings
  first (`layersFromSettings`). Settings has no layers of its own.
- Leaving a draft, publishing and downloading first save unapplied
  Inspector changes as a snapshot (`settle`, `saveUnapplied`).
- The Node Editor (`dialogs/NodeEditorDialog.vue`, command `node.edit`)
  mounts a second `BuilderInspector` with `variant="editor"` on
  `nodeEditorHost` (`nodeEditor.js`): the store seen through one fixed node
  id, so its Apply (`settle()`) is one `store.commit`. The variant uses the
  template editor's wide layout (`.builder-inspector--wide`), keeps checks,
  connection points and Position, and hides the heading, subject and Apply
  bar. It prefixes its ids with `node-editor-` (`idPrefix`), because the
  side Inspector stays on the page. Gotcha: `openNodeEditor`
  (`Builder.vue`) settles the side Inspector first. It refuses to open
  while the side Inspector holds edits it cannot apply, so two working
  copies never both hold edits. The view's `saveUnapplied` (leave guard,
  `beforeunload`) also calls the dialog's exposed `saveUnapplied`. The
  dialog mirrors store announcements into its own status region.
- Node Editor field search (`node-editor-search`, before Expand all and
  Collapse all, for every node kind). `builder/fieldSearch.js` holds the
  pure parts (`searchWords`, `searchPath`, `searchFields`,
  `searchResultText`).
  - A query matches a part's label or spec path, case-insensitive, every
    word. `[0]` and a leading `spec.` are normalized. A dotted partial path
    matches when its parts occur in order, and a list index must match a
    whole segment. Values are not searched.
  - `markSearch` sets `data-search` (`match`, `holds`, `inside`, `hidden`)
    on each part. Scoped CSS hides `hidden` parts, and the table of
    contents omits them. A matching section, list or entry shows all of its
    contents. The count line (`node-editor-search-results`) describes the
    box, and the status region announces it 500 ms after typing stops.
  - A search start saves `collapsedState()` (the paths of
    `collapsedPaths` in `collapse.js`, and the closed details) and expands
    all. A clear of the search, or a dialog close, calls
    `restoreCollapsed`. Escape clears the box, and closes the dialog only
    when the box is empty. A failed Apply clears the search and keeps the
    sections expanded.
- Gotcha, sticky headers (Node Editor only): the top-level section legends
  (floated, so they become grid items that can stick) and the open More
  settings summary stick to the top of the scroll area. `STICKY` in
  `NodeEditorDialog.vue` mirrors the scoped CSS selector.
  `scroll-padding-top` and the `uncover` focusin handler scroll a focused
  field clear of a stuck header. Change both together.
- The side Inspector's header (`HEAD_ACTIONS`) has Add connection
  (`dialog.connect`, emits `connect`, devices and switches only, not on an
  included device) and Move to group (`dialog.regroup`, emits `regroup`),
  each only while its command can run. Then comes Open Node Editor
  (`builder-button--primary`, emits `edit`). All three use
  `.builder-inspector__head-button`. `.builder-inspector__open-editor` only
  adds the forced-colors border. Test ids: `inspector-connect`,
  `inspector-regroup`, `inspector-node-editor`. The tooltips name the
  keys. The toolbar has no Add connection or Move to group.
- Collapse (`inspector/collapse.js`): a labeled group
  (`InspectorGroupRenderer.vue`), a list, a map and each list entry have a
  disclosure button (`InspectorDisclosure.vue`) in the header. Collapsed
  paths are kept per variant (`canvas`, `template`, `editor`) for the
  session. Reset view clears them (`resetCollapsed`). `BuilderInspector.vue`
  provides `INSPECTOR_COLLAPSE` and `INSPECTOR_HEADING_LEVEL` (h3, or h4
  under the template editor's h3, for the spec's heading group). A
  collapsed part with a field error marks its header. Go to, the error
  summary, the table of contents, and an Apply or save that errors expand
  the part with the field (`collapse.reveal`).
- The outline contract: an element with `data-outline` (and
  `data-outline-label`, `data-outline-count`, `data-outline-errors`) is an
  entry of the Node Editor's table of contents. `data-collapse-path` marks
  what collapses, and `data-outline-focus` is the control that takes focus
  on Go. `formOutline.js` holds the pure parts (`itemSummary`,
  `entryTitle`, `outlineTree`, `activeEntry`, and the path updates after an
  insert, a removal or a move). `BuilderInspector.vue` exposes
  `collapseAll`, `expandAll`, `reveal(key)`, `collapsedState` and
  `restoreCollapsed`. `dialogs/NodeEditorToc.vue` draws the table of
  contents. The entry gone to stays marked when the fields fit without a
  scroll.
- Gotcha: the first button of a list is its disclosure button, so
  `focusField` tries `.array-list-add` before any other button.
- Field order and titles: `SPEC_FIELD_ORDER` (`schema.js`) orders the
  fields of a drive and an interface. `INTERFACE_FIELDS` puts `address`
  first for a static interface (`address`, `mask`, `gateway`, `proto`,
  `name`, `vlan`, `mac`). Add interface focuses Address (`FIRST_FIELDS` in
  `InspectorArrayRenderer.vue`). New interfaces are `static`. The titles
  "Firewall rulesets" and "miniccc commands" are in `schema.js`, and
  `capitalize` (`text.js`) keeps a lowercase tool name.
- Suggestions: a free-text field whose schema has `x-builder-suggestions`
  (`SPEC_SUGGESTIONS` in `schema.js`) renders as
  `InspectorComboboxControl.vue`. Its values come from
  `INSPECTOR_SUGGESTIONS` for its kind: `disks` for a drive's image, and
  `rulesets` (the working copy's ruleset names, `rulesetNames` in
  `validate.js`) for `ruleset_in` and `ruleset_out`. With no values it is a
  plain text field. While its list is open, a visually hidden status
  announces the match count, as the command palette and the card filter
  do. In a map of labels or annotations, `x-builder-values`
  (`MAP_VALUES_KEYWORD`) gives the value kind for a key: `interfaces`
  (`interfaceNames` in `validate.js`) for `ntp-server` and
  `vrouter/enable-ssh`. `InspectorMapRenderer.vue` lists them in a
  `datalist`, as it lists the suggested keys. Its ids replace each
  character of the base id (`#/properties/labels`) that is not a word
  character or `-`, because happy-dom refuses such an id in `list`.
- Addresses: `INSPECTOR_RANDOM_MAC` gives the MAC address field a Generate
  button (`randomMac` in `addressing.js`: a MAC that no interface of the
  diagram or the form uses). A switch's Inspector shows Assign addresses
  while the host has `assignAddresses` (`addressesShown`). While the
  setting `assignAddressesOnConnect` is on, the store's `connect` gives a
  new connection's static interface the next free address of the network's
  subnet (`connectOptions` in `store.js`, model
  `connect(doc, connection, {assign})`). Paste and Duplicate follow the
  setting too. Otherwise, or with no subnet, it announces that no address
  was set.
- The Diagram section (`InspectorDiagram.vue`) has Grid size
  (`inputmode="numeric"`, `inspector/gridSizeField.js`). `isGridSize`
  (`model.js`) takes a whole number from `MIN_GRID_SIZE` to
  `MAX_GRID_SIZE` (4 to 200), one undo step through `store.setGrid`.
  Refused text stays in the field with `GRID_SIZE_ERROR` ("Grid size must
  be a whole number from 4 to 200."). The section also
  has the layer list with the layer set link and Update from layer set,
  and `InspectorTemplateLinks.vue`. While the setting `nodeIconSize` is not
  Off, the Icon size fields show `iconSizeOverrideNote()` (`settings.js`).
- Slow work shows on its control with `BuilderBusyButton.vue` (props
  `busy`, `icon`, `busyLabel`, `disabled` for aria-disabled, `locked` for
  native disabled, `type`):
  - With `icon`, the ring replaces the icon and the label stays. Without
    it, the ring turns over the label. The label stays in the
    accessibility tree (opacity 0) unless `busyLabel` names the button.
  - It keeps focus and size, sets aria-busy, ignores a press while busy,
    and never submits a form then. It exposes `focus()`.
  - In the Scenarios dialog, Create new scenario has the `plus` icon and a
    `busyLabel`. Its View and Edit are item actions whose `aria-label`
    while busy is "Loading" and the scenario name.
  - `builder/busy.js` holds the shared state. `runBusy(group, task, work)`
    runs one task per group after `afterPaint()`, and `busyTask(group)` is
    the reactive task, so a control shows work that its keys or the
    palette started. `BUSY_GROUPS` names the groups that `Builder.vue`
    runs: `experiment` (the task is the experiment name, for Exp in the
    toolbar and on a card), `combine` (the Inspector's Combine into a new
    draft), `blank` (Blank diagram) and `editPublished` (Edit as a draft).
    The Download dialog runs group `download` (one format at a time,
    `PREPARING` status texts), and template exports run
    `template-export`.
  - Other dialogs pass their own busy flag, keep the label constant, and
    put the wait text in their own status region, because the page's live
    region does not speak through a modal dialog. A row button (icon Add to
    server or Delete) turns only for its icon (`busyIcon`), and a list's
    bulk button only for its list.
  - Gotcha: work in `runBusy` starts a frame later, so a test waits
    (`vi.waitFor`), not only `flushPromises`.
- `dialogs/ScenarioEditorDialog.vue` opens over `ScenarioDialog.vue` for
  View, Edit and Create new scenario (see
  [publishing.md](publishing.md#the-scenario-stage)). Enter in Filter
  scenarios filters and does not save the dialog.
- Diagram notes: `store.setDiagramNotes`, `diagramNoteProblem`
  (`model.js`). A note that breaks a rule is never written.
- A multi-line text box (a `multi` control in `InspectorInputControl.vue`,
  a diagram note) uses `v-fit-text` (`inspector/fitText.js`): two rows,
  grown to its text up to `MAX_LINES`, and never shorter than the user
  dragged it. It refits on input, on a Vue update that changes its value,
  and on a width change.
- The diagram's Scenarios list (`inspector/InspectorDiagram.vue`, after
  Notes) gives each scenario an `h4` with `scenarioCounts`, and each app
  `scenarioAppNote` (`builder/scenarioSummary.js`).

## Notes, layouts and the canvas

- Fit, the least zoom, "bring into view", group sizing and image downloads
  use `nodesBounds` (`model.js`), the box around the node boxes. Fit uses
  `getTransformForBounds` to stay clear of the minimap and the zoom
  controls.
- Layouts are registered in `layouts/index.js`: `cards`, `elk` (ELK
  layered, the default), `tiers` (By layer, from each node's `layer`),
  `dagre` and `standard`. The order of `LAYOUT_ALGORITHMS` is the order of
  the layout menu, the Default layout setting and the palette. ELK runs in
  a Web Worker that loads on first use.
  - The ids `sfdp` (Yifan Hu), `force` and `radial` no longer exist, but
    old drafts and settings can name them. `ownLayout`, `runLayout` and the
    `layoutAlgorithm` setting treat an id not in `LAYOUT_ALGORITHMS` as
    none, so the default runs. The Go schema takes any string for
    `layout`.
  - In `tiers`, the selected devices and switches are the roots. With no
    selection, the roots are firewalls, then routers, then the switch with
    the most connections. An external device (`spec.external`, node type
    `hil` or the `external` icon, `tierKind`) is a root only when
    selected.
  - Layouts leave drawings in place and grow groups around them
    (`growAroundDrawings`). The document's `layout` records the last
    layout that ran. User docs:
    [Layouts](https://phenix.sceptre.dev/latest/builder/diagrams/#layouts).
- The layout contract (`layoutScopes` in `layouts/common.js`):
  `arrange({items, edges, networks})` gets the items of one scope, the
  connections between them, and the networks by id. An item is a node that
  joins a network (with its `primary` network, `multi`, `prefix` and
  `roles`), or a group as one item sized for its members. A connection has
  source and target ports `{id, y}`. `arrange` returns top-left corners (a
  Map, or a Promise of one), or `{positions, routes}` with each route from
  source port to target port. Routes between two nodes of the scope are
  kept, and routes to a group are not. Kept routes move onto the snapped
  handles (`fitRoute`).
- ELK places clusters in layer columns. So `placeClusters` (`elk.js`) cuts
  the columns into slices, wraps tall layers (`packRanks`), and moves a
  cluster to the slice of a later link into it. It keeps the slicings near
  the best aspect ratio, then those within `ROOM` times the most compact
  space. Then it takes the one with the shortest lines between clusters.
- In `tiers.js`, the breadth-first distance from a root orders the nodes
  in a tier, and kind only breaks ties. Every connection joins a device and
  a switch, so kind first would put every switch above its devices.
- Stacking: the canvas disables Vue Flow's `elevateNodesOnSelect` and sets
  z-index itself (`nodeZIndex` in `adapters/vueflow.js`). A selected shape
  or line is never lifted, so it never covers a device or its handles.
  Resize and line handles are in Vue Flow's edge label layer (z-index
  2000). They reach the store through `nodes/canvasEditing.js`, so a node
  rendered alone imports no store.
- PNG and SVG downloads (`exporters.js`) are at most `MAX_IMAGE_SIZE`
  (4096) pixels each way (`computeExportViewport`). `exportImage` passes
  `imagePixelRatio`, because html-to-image multiplies the size by
  `devicePixelRatio`.
- The coordinate rulers (setting `showCoordinates`) are siblings of
  `<VueFlow>` in `BuilderCanvas.vue`, outside the pane. `.has-rulers` gives
  the pane a top and left margin, so Vue Flow's `dimensions` and each
  overlay or fit computation see the smaller pane. Exports (the
  transformation pane) never hold the rulers. Tick math is in `rulers.js`
  (1-2-5 steps, small ticks as a CSS background moved by
  `--minor-offset`). The rulers count absolute flow coordinates, which
  match a node's Position only outside a group.
- Node icons: a device, switch or group gets `useNodeIcon(size)`
  (`nodes/nodeIconSize.js`). While the setting `showNodeIcons` is off, it
  draws no icon and takes no icon layout, and the document keeps the icon.
  The size is `nodeIconSize(doc, node, override)` (`model.js`). The
  override is `iconSizeOverride()` from the setting `nodeIconSize` (`off`,
  `small`, `medium` or `large`). It applies only to the viewer's canvas
  and to PNG, SVG and draw.io downloads, and the document never keeps it.
  The class of a Medium or Large icon sets `--bx-node-icon` (24px or 32px),
  the icon's side.
- Node descriptions: the canvas draws `data.comment`, which `toFlowNodes`
  sets from `drawnNodeComment(node, showDescriptions)` (`model.js`). A
  device or a group draws its description only while the setting
  `showNodeDescriptions` is on and its payload has no
  `hideDescription: true` (`nodeDescriptionHidden`). A note always draws
  its text. The accessible name (`nodeComment` in `diagramText.js`) and the
  info tooltip keep the description. PNG and SVG follow the canvas DOM.
  `toDrawio` takes `showNodeDescriptions` and applies the same rule. Copy
  and paste (`clipboard.js`) and templates (`templateDevice`, `addNode`)
  keep `hideDescription`.
- Node colors (`useNodeColors(payload, outlines)`, `nodes/nodeColors.js`):
  - The outline is the node's own outline color, else its layer's color
    while `layerSet.layerOutlines` is on (`layerOutlineColor`), else for a
    switch its network's color (`defaultOutline`). A custom network color
    that fades into the canvas gets a ring (`needsCasing`).
  - The icon of a device, switch or group is in `--bx-node-outline`, or in
    `--bx-accent` with no outline (`builder.css`). `iconInk(outline, fill)`
    (`colors.js`) sets `is-icon-ink-light` or `is-icon-ink-dark`, which
    draw the icon in the text color in a theme where the outline keeps
    less than 3:1.
  - The colors that `addNetwork` picks draw with the theme's token at the
    same position (`--bx-net-N` for the Nth). These tokens keep 3:1
    against the canvas in both themes (`colors.js`). Other network colors
    draw as chosen, with a casing in the theme's text color where they keep
    less than 3:1.
  - Note and group colors are decorative accent bars, because their names
    tell what they are.
- A switch shows only its icon and its name (its network's name). Its
  accessible name gives its network and VLAN alias (`nodeAccessibleName`
  in `diagramText.js`).
- The header's counts (`BuilderCounts.vue`, `diagramCounts`) include
  `layers` only while `someNodeOnLayer(doc)` (a device or switch at a
  chosen or matched layer). They then count `effectiveLayers`,
  placeholders included, as the Inspector lists them. A press selects every
  node on a layer (`kindSelection` with `nodeHasLayer` in `selection.js`).
- The resource totals (`BuilderResources.vue`: `builder-resources`,
  `resources-selection`) sit in `.builder-header__start` after the name,
  not in `.builder-counts`. They come from `builder/resources.js`:
  - `startsDevice`: every device except `spec.external`, included devices
    too, as `preflightDevices` in `api/builder/preflight.go` counts.
  - `specWhole`: the server's reading of `hardware.vcpus` and
    `hardware.memory`.
  - `selectionResources`: adds the devices of selected groups at any
    depth.
  - Gotcha: `DEFAULT_VCPUS` and `DEFAULT_MEMORY_MB` must equal
    `preflightDefaultVCPUs`, `preflightDefaultMemory` and the phenix
    schema's defaults. `resources.contract.test.js` and
    `TestPreflightDefaultsMatchSchema` check both against the schema.
  - Layout: the e2e layout spec centers the counts alone. The totals take
    room from the start's share and must fit the one-row header at 1100px,
    also with a device selected. While the selection holds a device, its
    totals are a second line, which adds no width, so the counts stay
    centered. Below a 77rem header, only the diagram's totals show, the
    vCPUs over the memory (`resourcesParts`).
- Snap to grid, placing and drags: see
  [code-and-tests.md](code-and-tests.md#node-positions-and-the-grid). A
  click on a palette entry or an Add command puts the node in the first
  free slot fully in view (`addInView` in `paletteDnd.js`, `freeSpot` with
  `within`). It goes past the view only when no slot is free.
- Icon bytes draw only through the `<img>` of `BuilderIcon` with a
  `data:image/png;base64,` URL, never as markup.
- Auto-group by name pattern runs the pattern in a same-origin worker
  (`grouping.js`, `groupingWorker.js`) with a time limit. It does not use
  community detection: a network that most devices join (management)
  would pull unrelated networks into one group.
- Accepted accessibility deviation (`BuilderCanvas.vue`): the nodes are
  not each a Tab stop, and the canvas has no composite role. In screen
  reader browse mode (NVDA, JAWS), the arrow keys and Page Down move the
  virtual cursor, not focus, so Tab leaves the canvas. The canvas
  description tells the user to turn on the screen reader's focus mode or
  use Go to node. Enter on a node in browse mode selects and focuses it.

## Keys and commands

- Commands are in `commands.js`, and default keys in `keymap.js`. A letter
  alone is refused as a shortcut (`keyRefusal`), except for a command
  whose keys work only on the canvas (`takesLetters`), such as `N`. A key
  press runs a command's `keyChoice`, so `N` adds the plain Device while
  the palette asks for a template.
- A command has these fields:
  - `id` (`area.action`, the key that customized keys are stored under),
    `title` (a title that ends in `…` asks for more in a dialog first),
    `group` (a heading, ordered by `GROUPS`), `aliases` (lowercase single
    words, matched as title words without highlight) and `keywords`.
  - `keys` (default key specs from `keymap.js`, a list or `{mac, other}`),
    `scope` (`SCOPES`, one or a list), `page` (the keys also work on the
    rest of the page, outside text fields and app dialogs), `views`
    (`['editor']` by default, `['landing']`, or both), `local` (the
    focused control handles its keys: listed only, never dispatched) and
    `fixed` (the keys cannot be customized).
  - `palette: false` (hidden from the palette), `offered(ctx)` (false
    hides it from the palette for now), `when(ctx)` (true, or the reason
    it cannot run) and `run(ctx, choice, picked)`.
  - `choices(ctx, picked)` with `steps` (the options of each step),
    `keyChoice(ctx)` (the choice its keys run with, without a question),
    `direct(ctx)` (see below), `prefix` (the palette query for its
    targets), `nameFirst` (name matches before field matches),
    `label(ctx)` and `detail(ctx)`.

  A choice is `{id, title, detail?, keywords?, icon?, disabled?, value?}`.
  `disabled` is the reason it cannot be chosen, and `value` is what `run`
  needs. The context (`createCommandContext`) is `{store, view}`. A run
  can add `source` (`'key'` or `'palette'`) and `additive` (Shift+Enter on
  Go to node). A key press adds `event`, `focus` (`focusScope`) and `item`
  (the focused node, connection or row).
- Key matching (`keymap.js`): letters match `KeyboardEvent.key`, so they
  follow the layout. Digits, and any key pressed with Alt, match
  `KeyboardEvent.code`, so `Shift+1` is one key on QWERTY and AZERTY.
  `detectPlatform` reads `navigator.platform` first, because Playwright
  device profiles report Windows in the user agent on any host. `ariaKey`
  writes a Shift character as its US physical keys (`?` is `Shift+/`), as
  WAI-ARIA 1.2 requires. `keyRefusal` also refuses Ctrl+Alt off macOS,
  because AltGr reports as Ctrl+Alt.
- The palette's prefixes are `SEARCHES` in `commandSearch.js`: `@`, `#`,
  and `t:`, `c:`, `i:` for `goto.template`, `goto.collection` and
  `goto.icon` (choices from `librarySearch.js`). A plain search adds up to
  `NAMED_LIMIT` of each library, matched by words only (no letters-in-order
  band). The icons come from the view's `icons` getter (VIEW_API). A
  collection runs `openTemplateLibrary(show)`, which calls `showList(show)`
  on `BuilderTemplates.vue`.
- `direct(ctx)` gives the choice that a command runs with, from its keys
  and the palette alike, when the state makes one. Without one, the
  command asks (`steps`, `choices`). Open Node Editor (`node.edit`) runs on
  the selected node, else asks for a device or a switch.
- Commands with no default keys, which users can give keys: Add a
  connection… (`dialog.connect`), Move to a group… (`dialog.regroup`),
  Assign addresses (`structure.assignAddresses`, asks for a network with a
  subnet), Set layer of selection (`structure.setLayer`: the store's
  `setNodesLayer` over model `setNodeLayers`, one undo step, with No layer
  and the placeholder layers), Select nodes on layer (`selection.layer`,
  while `someNodeOnLayer`), Select devices on network
  (`selection.network`, `interfacesOnNetwork` in `addressing.js`), Select
  devices using image (`selection.image`, each distinct
  `hardware.drives[].image`, sorted), Show or hide node icons
  (`view.nodeIcons`), Show or hide node descriptions
  (`view.nodeDescriptions`), Snap to grid (`view.snapToGrid`), and Node
  icon size: Off, Small, Medium and Large (`view.nodeIconSize.<value>`).
  Edit layers… is `settings.layers`. Layer choices carry `layer`, which the
  palette draws with `LayerName.vue`.
- The view (VIEW_API) adds `showNodeIcons`, `showNodeDescriptions`,
  `nodeIconSize`, `snapToGrid`, `showConnections`, `openLayers`,
  `toggleNodeIcons`, `toggleNodeDescriptions`, `setNodeIconSize`,
  `toggleConnections` and `toggleSnapToGrid`. `draftsTab` and
  `showDraftsTab` take `layers`.
- Show or hide connections (`view.connections`, default `Alt+Shift+C`,
  scope `editor`) toggles the setting `showConnections`:
  - `BuilderCanvas.vue` sets `is-connections-hidden`. `builder.css` hides
    `.vue-flow__edge` and `.builder-edge__label` only under
    `.vue-flow__pane > .vue-flow__transformationpane`, so the export copy
    (in a holder beside the pane, `exportCopy`) keeps them.
  - Hidden edges take no tab stop (`tabStop`), no pointer selection
    (`onEdgesChange` drops selecting changes), and no Page Down or Up
    (`stepConnections` announces). They leave the selection as they hide
    (the `connectionsShown` watch moves focus off an edge).
    `selection.all` calls `store.selectAll({edges: false})`.
  - The header's links count (`BuilderCounts.vue`) is struck through, and
    a press only announces `connectionsHiddenText()` (`commands.js`), as
    Page Down does. Settings has a **Show connections** switch
    (`settings-connections`).
  - `store.goToIssue` sends Go to on a hidden edge to its source node with
    no field (`hiddenEdge`, which the `Builder.vue` announcement names).
- Go to zooms in: `showNode(id, {zoomIn})` and `revealNode(ids, {zoomIn})`
  call `zoomInOn` (`BuilderCanvas.vue`). Below `READABLE_ZOOM` (0.6),
  `readableZoom` (`adapters/vueflow.js`) gives 100%, or the opening zoom of
  the settings when larger, at most `MAX_ZOOM`. `centeredViewport` centers
  the nodes on a `freeSpot`. `goto.node`, `goto.network` and Go to in the
  lists of checks (`store.focusRequest` watch in `Builder.vue`, for nodes)
  pass it. Adding a node (`N`) does not.
- Matching (`matchItem` in `fuzzy.js`): each query word must occur in the
  text, ignoring case. A word at the start of the text ranks first, then
  at the start of a word, then anywhere. When a word is missing, the query
  letters must occur in order, ranked by how close together they are.
  Aliases count as title words. Keywords and fields match only by words.
  A field match ranks below a title word match and above a title
  letter-order match. As a last resort, the words can
  spread over the title, aliases and keywords, and with `spread: true`
  over the fields too.
- `@` (Go to node) ranks a match by the field that matched
  (`NODE_FIELD_RANKS` in `commands.js`: hostname, IP address, MAC address,
  VLAN and network, annotation, label, description, note, image), through
  the field `rank` of `matchItem` and `fieldOrder` in `fuzzy.js`. A node
  whose name matches letter by letter comes before a field match. The
  command has `nameFirst`, so `matchChoices` (`commandSearch.js`) matches
  each name once (`matchName`, title and aliases), then the fields with
  `name: false`.
  A field's `prefix` ("eth0 IP address", "label") goes before the matched
  value on the second line (`detailParts` in `commandSearch.js`). `t:`
  also finds a template by the layer its fields match in the open diagram.
  Owner is no longer a field of `t:` or `c:`.
- Gotcha: `fuzzy.js` caches the keyword text of each item in a WeakMap
  keyed by the item. So a search item must not change its title, aliases
  or keywords after its first match.
- Header breakpoints are container queries in rem (`builder/builder.css`,
  `Builder.vue`). Below a 97rem header some buttons show only their icons,
  below 77rem more do, and below 66rem the header wraps.

## Lists of checks and Go to

`BuilderIssueList.vue` lists the issues of the Publish dialog, the Diagram
checks dialog and the Preflight report, grouped by severity (`bySeverity`,
`severityHeading` in `issues.js`).

The checks compare drive images with the server's disk images. Users add
images elsewhere, so the editor reads the server's images when it opens,
and again when the page shows again after a while (`BuilderChecks.vue`).

- `toIssue(entry, defaultSeverity, {publishing})` puts each issue into the
  issue shape of [api.md](api.md#issues-and-error-bodies). With
  `publishing`, a `blocksPublish` warning is an error.
  `responseIssues(data)` reads `errors`, then `warnings`, then `issues`,
  and lists a repeated issue once.
- `issueTarget(doc, issue)` resolves `nodeId`, `edgeId`, `networkId`, else
  the element that `path` starts at. `issueField` gives the JSON Forms data
  path. With no element, there is no Go to.
- `store.goToIssue(issue)` selects the target and sets
  `store.focusRequest = {kind, id, field, token}`. `BuilderInspector.vue`
  (`goToField`) focuses the field. It takes a request once
  (`store.takeFocusRequest(token)`), in a `post` watcher and at mount.
- A label or an annotation is a row of `InspectorMapRenderer.vue`, whose
  `data-path` is its index. So the row's value field also has
  `data-entry-path` (`spec.labels.ntp-server`), which `focusField` takes
  when no `data-path` matches. An issue's `field` names the key, and Go to
  announces the entry by it ("ntp-server (Labels) in device web-01").
- A field can name a whole list: `device.interfaces.none` has
  `spec.network.interfaces` and `device.drives.none` has
  `spec.hardware.drives`. `focusField` then takes the list's
  `.array-list-add` (Add interface, Add drive). In `validate.js`, a quoted
  dotted name must be a code or a document path
  (`validate.contract.test.js`), so a field path is a template literal.
- Fix: `issueFix(doc, issue)` (`builder/issueFixes.js`) gives
  `{name, label, apply}` for each code of its `FIXES` map, or null:
  - `node.hostname.duplicate` uses `uniqueHostname` (`model.js`).
  - `interface.ip.shared` uses `freeAddressFor` (`addressing.js`) and
    follows the gateway rules of `assignAddresses`. It sets the
    interface's `gateway` or the network's `gateway` (`setGateway`, named
    in the fix).
  - `interface.mac.shared` deletes `mac`.
  - Null: an included device, a network without a subnet or a free
    address, a router interface that has the gateway.
- Only `BuilderChecks.vue` sets an issue's `fix`, and not while
  `store.readOnly`. `BuilderIssueList.vue` shows Fix before Go to and
  emits `fix(issue, position)`. `store.fixIssue` commits `apply` as one
  step named `label`. The dialog says the label in its own status region
  (`checks-fixed`). It moves focus to the issue now at that position, or to
  `checks-summary`.
- Canvas marks: `nodeIssueSummaries` (by node id, with a network's issues
  on its first switch) and `edgeIssueSummaries` (by `edgeId`) in
  `issues.js` give `{level, text}`. `BuilderCanvas.vue` passes them to
  `toFlowNodes` and `toFlowEdges` (`adapters/vueflow.js`) as
  `options.issues`. These set `data.issue` and put `nodeIssueId(id)` or
  `edgeIssueId(id)` in `aria-describedby` before the canvas hint.
  `NodeIssueMark.vue` draws a node's mark. `NetworkEdge.vue` draws a
  connection's mark in its label, or alone (`builder-edge__issue--alone`)
  at the middle of a line without a label. Both go to the label layer
  (teleported) with the hidden text. `builder.css` hides
  `.builder-edge__issue` with hidden connections, and
  `EXPORT_EXCLUDED_SELECTOR` (`exporters.js`) keeps the marks and texts out
  of downloads.
- Test ids: lists `publish-checks`, `publish-refusal`,
  `publish-result-issues`, `checks-issues`. Items `issue`,
  `issue-message`, `issue-element`, `issue-code`, `issue-go-to`,
  `issue-fix`, `issue-fix-hint`. Canvas marks `node-issue`, `edge-issue`.

## Item actions

Every button that acts on one item (a card, a row, a dialog's own item)
uses `BuilderActionButton.vue` with an `action` from
`builder/itemActions.js` (`ITEM_ACTIONS`). Never style these by hand.

| Action | Label | Icon (`iconPaths.js`) | Tone and class |
|---|---|---|---|
| `edit` | Edit | `pencil` | blue, `builder-button--primary` |
| `delete` | Delete | `trash` | red, `builder-button--danger` |
| `publish` | Publish | `publish` (arrow up) | default |
| `duplicate` | Duplicate | `copy` | default |
| `open` | Open | `folder-open` | blue, `builder-button--primary` |
| `view` | View | `eye` | default |
| `download` | Download | `download` (arrow down) | default |

- View is only for a read-only item. An item that the user may change gets
  Edit. Say Download, never Export, and View, never Preview. Exception:
  each Icons tab card has View, because the preview never edits. An
  uploaded icon's Edit is Rename in the Custom icons dialog.
- A published card's Exp is not an item action. It goes to the experiment
  that the publication made, not to the item. So it stays a
  `BuilderBusyButton.vue` with the `experiment` icon and the label Exp.
- The slot replaces the label ("Delete selected", "Create topology").
  `busy`, `busyText`, `disabled` (aria-disabled) and `locked` pass to
  `BuilderBusyButton.vue`. `busyText` shows in place of the label, in the
  same width (`.builder-button__swap`). On a card the grid sets the width,
  so a busy label does not move the icon or change the button size. Test
  ids and `aria-*` attributes fall through to the `<button>`. It exposes
  `focus()`. `tone` replaces the action's tone: the Publish dialog's Open
  draft is `default`, so it does not outrank Create topology.
- A disabled red button has the plain border (`builder.css`).
- Each card list puts its buttons in `.builder-card__actions`: two columns
  of one width, filled left to right.
  - An odd last button keeps one column's width, so all buttons on a card
    have the same width. The exception is a label that cannot fit half the
    card (see below).
  - The icon and label start at the left padding, as in a menu, and the
    spare width is after the label. A label never wraps. A font too wide
    for the column cuts the label with an ellipsis, never past the button.
  - A card is at least 18rem wide (`.builder-cards`), so half of it holds
    Duplicating. A card narrower than 15.75rem inside (the `builder-card`
    container) has one column.
  - Do not add a flex row or a per-card layout. A label that cannot fit
    half the card gets a shorter label, or its button spans both columns.
- `test/builder/item-actions.contract.test.js` holds the table above.
  Change it, this table and the user docs table
  ([Item buttons](https://phenix.sceptre.dev/latest/builder/drafts/#item-buttons))
  together.

## Drafts page and selectable lists

- `BuilderDrafts.vue` holds the tabs. Bulk actions are a client loop over
  the per-item routes (`runBulk` in `bulk.js`): at most 4 requests at once
  (topology deletes and downloads 1), and one retry of a 412 with the
  `ETag` it carries. A session end, or a server out of reach, stops the run
  (`endsBulk` in `api.js`). The rest are "Not attempted."
- The drafts lists are read again when the landing replaces the editor,
  and when the tab or window comes to the front. The read waits
  `RELIST_DELAY_MS` (`Builder.vue`), because focus and `visibilitychange`
  often arrive together. A read asked for during another read runs once
  after it ends. No read starts while a dialog is open, or while the page
  alert shows something other than a failed listing, because a listing
  clears the alert. While the editor is open, only the template library,
  the layer sets and the icon library are read again.
- The tabs are Drafts, Published Diagrams, Node Templates, Layers and Icons
  (tab ids `drafts`, `published`, `templates`, `layers`, `icons`). Drafts
  lists `store.drafts.all`, newest first (`newestFirst` in
  `draftMessages.js`), then the damaged drafts. Each draft card shows
  "Created by" (`createdBy`) and `Updated <time> by <user>`
  (`lastModifiedBy`). It has Duplicate (not on a damaged draft), Delete,
  and Publish when `store.canPublish`. Its card and bulk buttons are item
  actions. A busy card button shows `busyText`
  (Opening, Duplicating, Deleting, Loading, no ellipsis) in place of its
  label, and its `aria-label` says the same. Delete confirmations pass
  `action="delete"` to `BuilderConfirm.vue`, whose button is otherwise
  plain `builder-button--danger`.
- The filter of Drafts and Published Diagrams (`BuilderCardFilter.vue`,
  test ids `drafts-filter-<tab>`, `drafts-filter-<tab>-count`,
  `drafts-filter-none-<tab>`) is `filterCards` in `builder/cardFilter.js`.
  It runs `matchItem` of `fuzzy.js` on the card's name (title) and its
  `createdBy` and `lastModifiedBy` (fields), with `spread: true`, and keeps
  the list order. The command palette does not pass `spread`.
- `BuilderDrafts.vue` gives `useListSelection` the filtered list. So a
  hidden card leaves the selection, and Select all and the batch actions
  act on the cards shown. The tab count reads "N of M" while a filter is
  set. sessionStorage `phenix.builder.filter.<tab>` keeps the text per tab
  (`readCardFilter`, `keepCardFilter`, both in try/catch). Logout deletes
  it. `focusDraft` clears the filter of a card that the filter hides.
- `BuilderCardFilter.vue` holds the field, its count line `<id>-count`,
  Escape, and the `announce` event 500 ms after typing stops, which each
  tab says in its `filter` slot. The Node Templates and Layers tabs use it
  with `filterItems` (`cardFilter.js`), which takes one `{title, fields}`
  search item per card: `templateSearch` (the `t:` fields, through
  `templateFields`) and `layerSetSearch` in `librarySearch.js`. These
  filters do not change the tab count. The templates filter (test ids
  `templates-filter`, `templates-filter-count`, `templates-filter-none`,
  kept as `phenix.builder.filter.templates`) filters the list that Show
  chose and stays when Show changes.
- Every selectable list (the card tabs, the Node Templates library, the
  Icons tab, the Layers tab, the diagram copies of the Custom icons
  dialog) uses `useListSelection` (`listSelection.js`): selection by key, a
  range anchor, and a roving tabindex on the `<li>`. `listKeyAction` and
  `onListKeydown` map the keys. The Drafts, Published Diagrams, Node
  Templates and Layers tabs wire a card's checkbox, Select all and the card
  keys through `useCardSelection`. It says "N of M selected." through an
  `announce` callback. `matchesKey` finds Mod+A on a layout without Latin
  letters. User docs:
  [Selecting with the keyboard](https://phenix.sceptre.dev/latest/builder/drafts/#selecting-with-the-keyboard).

## Duplicate draft

- The card's Duplicate (`draft-duplicate-<id>`, `duplicating` prop) and
  the command `draft.duplicate` (both views, `direct` in the editor, a
  `Drafts` choice on the landing) call the view's `duplicateDraft(item)`.
  With no item, it settles leaving the open draft (`mayLeave`) and passes
  `store.doc`. With a listed draft, it flushes that draft's background
  saves, then the store reads it (`getDraft`).
- `store.duplicateDraft({id, document})` names the copy with `copyName`
  and builds it with `duplicateDocument` (`builder/duplicate.js`). Both run
  in `store.duplicateCopy`, which Save newer history as a new draft shares
  ([saving.md](saving.md#snapshots)). The copy has a new `metadata.id`, no
  stamp keys, and a `source` reduced to `kind: manual` plus
  `includeTopologies` and `unresolvedIncludes`. The store then calls
  `createDraft` with `sourceToken: ''`, `replaceOpen: true` and no
  `forkOf`. `replaceOpen` closes the open draft (`newDocument`) only after
  the server made the copy, so a refusal leaves the editor, its history
  and its selection as they were.
- Gotcha: never use `forkOf` here. The server's fork keeps the original's
  source token and records its publication as `forked`. So
  `draftCanUpdate` would let the copy update the original's topology.

## Session and browser storage

- A 401 on any Builder request logs out at once, as other pages do.
  `watchSession` in `api.js` calls the app store's `logout()` while `auth`
  is true, so several refused requests log out once. The router logs out
  on a navigation with an expired token (`tokenExpiry`, `tokenExpired` in
  `src/js/src/utils/token.js`).
- What the browser keeps: IndexedDB `phenix-builder` (unsaved changes and
  local drafts), sessionStorage `phenix.builder.tab` (the tab's id) and
  `phenix.builder.filter.<tab>` (the drafts page's filters), and
  localStorage `phenix.builder.*`.
  - Logout, and a sign-in as another user (`phenix.builder.user` names
    whose data the browser holds), delete the IndexedDB data, recent
    commands, `phenix.builder.preflight`, `phenix.builder.groupPattern` and
    `phenix.builder.palette`. Logout deletes the queued changes without a
    warning.
  - They keep the preferences `theme`, `panes`, `minimap`, `shortcuts` and
    `settings` (`BUILDER_PREFERENCE_KEYS` in `session.js`). `settings`
    holds, among others, `showNodeIcons`, `showNodeDescriptions`,
    `showConnections`, `nodeIconSize`, `assignAddressesOnConnect` and
    `snapToGrid` (`SETTINGS` in `settings.js`).
  - The default layer set of new diagrams is on the server, not in the
    browser.
- The live region (`BuilderLiveRegion.vue`) holds each message 750 ms and
  joins messages that arrive meanwhile. A modal dialog makes the page
  inert. So a dialog that must announce has its own `role="status"`
  region, and the live region holds store announcements until the dialog
  closes.
  - A dialog whose region repeats what store actions did (Diagram checks
    Fix, the Node Editor, the layers editor) builds it with
    `useDialogStatus(store)` (`dialogs/message.js`). Each message it shows
    goes to `store.noteDialogSaid`.
  - When the held announcement plays, the live region says only the part
    that no dialog said at or after its `announcementSeq` (`unsaidPart` in
    `announce.js`, the announcer's `rest`). A dialog text that is the same
    sentence, or starts with it (a save with warnings), covers all of it.
    An announcement that starts with a sentence the dialog said plays only
    the remainder (an unsaved note). So a dialog says the store's label
    text, not other words for it.
  - Gotcha: happy-dom matches no `dialog:modal`, so tests of the hold map
    it to `dialog[open]`.
- A dialog renders each status and error region, empty, from the start
  (`useMessage` in `dialogs/message.js`). Each message is a child keyed by
  `key`, and every set changes the key. So the region announces the same
  text again.
- Tooltips (`fixedTooltip.js`, `whenPointed`, `lastPointer`): a
  `mouseenter` shows a tooltip only when a `pointermove` on the control
  comes within `MOVE_WINDOW_MS` (100 ms). A `mouseenter` from a scroll or a
  closed dialog under a still pointer has no `pointermove`, so it shows
  nothing. Chromium sends the `pointermove` after `mouseenter`. Firefox
  sends it before, or a frame later when the pointer comes from outside
  the page. `focusQuietly` (`inspector/fieldTooltip.js`) focuses a field
  with no field tooltip for that one focus. The Node Editor uses it for its
  first field as it opens. Hover and a later focus show the tooltip.
