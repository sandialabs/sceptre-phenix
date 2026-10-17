# Builder editor and drafts page

Part of the [Builder references](../builder.md). The code behind the editor
page and the drafts page: how a diagram opens, the Inspector and the Node
Editor, the layers editor, notes, layouts, the canvas, keys, lists of
checks with Go to, bulk actions, and what the browser keeps.

User docs: [The Editor](https://phenix.sceptre.dev/latest/builder/editor/),
[Building a Diagram](https://phenix.sceptre.dev/latest/builder/diagrams/)
and [Drafts](https://phenix.sceptre.dev/latest/builder/drafts/). They hold
the UI text, the keys and the limits. Keep them current when the UI
changes.

## Opening a diagram

- The Configs page links (`builderAction`, `builderLink`,
  `builderTagLabel` in `builder/configs.js`) go to
  `/builder?topology=<name>`. `openLinked` in `Builder.vue` then picks a
  draft with `draftForPublished(drafts, published, token)`, else opens the
  Import dialog with `initial: {kind: 'topology', name}`. The order is in
  [Editing a published topology](https://phenix.sceptre.dev/latest/builder/publishing/#editing-a-published-topology).
- The address names the open draft as `?draft=<id>`, so a reload opens it
  again (`openLinked` in `Builder.vue`). An unsaved draft's ID stays out of
  the link. A link with a `/` is not a draft ID: it
  opens nothing and says "This link does not name a draft. Open one from
  the lists instead."
- Card Publish on the drafts page loads the draft without the editor
  (`store.loadDraft(id, {quiet: true})`) and opens the editor's Publish
  dialog over the page.
- Without `builder` `use` (`store.canUseBuilder`), `Builder.vue` renders
  only the notice "Your role does not include the Builder." (test id
  `builder-no-access`), and loads no drafts.

## Inspector

- The Inspector builds JSON Forms from the schema (`schema.js`,
  `adapters/forms.js`, `inspector/`). A list of text such as
  `general.notes` gets one text area per item (`isMultilineList` in
  `inspector/control.js`).
- Gotcha: a change of `store.doc` reloads the form only while
  `keepsWorkingCopy` (`adapters/forms.js`) is false: no unapplied edits, no
  uncommitted text, no focus on Apply or Cancel. Otherwise
  `rebasedWorkingCopy` moves the working copy onto the element as it is
  now (`mergeFormData`). A change of the stamp alone (`sameButStamp`) never
  resets the form, because a reset drops typed text
  (`builder-inspector.spec.js` checks it).
- The fields of `instantKeys(kind)` (`adapters/forms.js`: colors, icon,
  icon size, border pattern, a switch's Line style, and a line's or a
  connection's Label and Line style) apply at once, without Apply, each change as one `host.commit`
  named by `instantChangeLabel`. `holdLook` in `BuilderInspector.vue` picks
  them out of a form change, skips a field with a working-copy error, and
  `commitLook` merges them into the element's data (`applyFormData`). A
  select in `HELD_LOOK_PATHS` holds a choice stepped to with keys until it
  is made (`heldCommit.js`). Text typed in such a field does not show Apply
  (`typingInstant`), and Escape puts back its committed text
  (`restoreText`). Gotcha: a new instant field goes in `INSTANT_KEYS`, and
  in `HELD_LOOK_PATHS` when it is a select.
- The Icon field (`inspector/InspectorIconControl.vue`, at `iconKey`) edits
  `iconKey` and the custom icon `icon` with one menu button
  (`BuilderMenuButton.vue`): the built-in icons of `ICON_ORDER` that the
  schema offers and `isIconKey` accepts, then Custom icon…, which opens
  `IconDialog`. `withOneIconField` in `adapters/forms.js` drops the UI
  control of `icon`. A choice changes both fields in one task, so JSON
  Forms sends one change. Default shows only where `fieldDefault` names
  the icon the node draws (not on an icon drawing, whose payload needs
  exactly one icon).
- Default box and Default button (`inspector/InspectorField.vue`,
  `inspector/control.js`): an unset field with a default shows the
  Default box (`useFieldDefault`, the `fallback` prop). While a field with
  a default (`useUnsetValue`) holds another value, `useDefaultReset` gives
  the `reset` prop, and Default is a button (test id
  `inspector-field-reset-default`) named "Reset <label> to the default,
  <value>" (booleans read on or off) and described by "<note>.". A reset
  clears an optional field and sets a required one to the default, in the
  working copy (it waits for Apply), and focus goes back to the input. A
  value equal to the default shows neither. `useInspectorControl` returns
  the field `note` once (the `note` prop) and an `ids.reset` id. Combobox,
  Color, Icon and OneOf controls pass the note only, with no Default box.
- The Layer field acts at once, outside the working copy
  (`inspector/InspectorLayer.vue`, `chooseLayer`). Its choices are
  `effectiveLayers(host.doc)` plus the node's own ID when the diagram does
  not define it (`layerChoices`), each named by `layerTitles` in
  `model.js` (the label, "Control (ID 3)" when two layers share it). For a device at a matched layer, the
  first choice names that layer and the field that matched in place of
  None, and Use matched layer removes the chosen layer
  (`INSPECTOR_LAYER.useMatched`, which calls the store's `setNodeLayer`
  with null). The layer's
  icon and color show before the select (`LayerName.vue`, which reads
  them through `layerLook` and `hasLayerLook` in `layerLook.js`), because
  native options cannot show them. The `layer` prop of `BuilderInspector.vue`
  places it: `section` (the canvas default) above the connection points,
  or `look` (the template and Node Editor default) as a field of the form
  after the look fields. `look` passes `layer: true` to `uiSchemaForKind`,
  which adds a `LAYER_UI_TYPE` element that `InspectorLayerRenderer.vue`
  draws from `INSPECTOR_LAYER`. The template editor reads its layers and
  the conflict rule from `templateDocument`. Edit layers… under the field
  (not in the template editor) and in `InspectorDiagram.vue` emit `layers`.
- The layers editor is `dialogs/LayersDialog.vue`, in two modes.
  `diagram` edits `store.doc` through `addLayer`, `relabelLayer`,
  `updateLayer`, `renumberLayer` and `removeLayer` (one undo step each),
  shows the layer set link, and has Update from layer set, Save to layer
  set, Save as new set…, Unlink and Apply. `library` edits a copy of one
  set of the server and saves it (see
  [templates-and-icons.md](templates-and-icons.md#layer-set-library)).
  Both use `layers/LayerRows.vue`, whose parent passes `ops`: `add()`,
  `renumber(from, to)`, `update(id, changes)` and `remove(id)`, each
  answering what the dialog says. Rows keep a key per layer ID across a
  renumber, and focus returns to the field a re-sort took it from, after
  Tab finishes. `openLayers` in `Builder.vue` (VIEW_API) opens it in the
  editor, or shows the drafts page's Layers tab. While the Node Editor is
  open it opens over it (`layersOver`), as Settings does (`settingsOver`),
  so the Node Editor keeps its edits. Edit layers… at the bottom of
  Settings closes Settings first (`layersFromSettings`). Settings has no
  layers of its own.
- Leaving a draft, publishing and downloading first save unapplied
  Inspector changes as a snapshot (`settle`, `saveUnapplied`).
- The Node Editor (`dialogs/NodeEditorDialog.vue`, command `node.edit`)
  mounts a second `BuilderInspector` with `variant="editor"` on
  `nodeEditorHost` (`nodeEditor.js`): the store seen through one fixed node
  id, so its Apply (`settle()`) is one `store.commit`. That variant takes
  the template editor's wide layout (`.builder-inspector--wide` in
  `builder.css`), keeps checks, connection points and Position, hides the
  heading, subject and Apply bar, and gives its ids the `node-editor-`
  prefix (`idPrefix`), because the side Inspector stays on the page.
  Gotcha: `openNodeEditor` in `Builder.vue` settles the side Inspector
  first and refuses to open while it holds edits it cannot apply, so two
  working copies never hold edits at once. The view's `saveUnapplied`
  (leave guard, logout, `beforeunload`) also calls the dialog's exposed
  `saveUnapplied`. The dialog mirrors store announcements into its own
  status region.
- Node Editor field search: Search fields (test id `node-editor-search`)
  sits before Expand all and Collapse all, for every node kind.
  `builder/fieldSearch.js` holds the pure parts (`searchWords`,
  `searchPath`, `searchFields`, `searchResultText`). A query matches a
  part's label or its spec path, case-insensitive, every word. `[0]` and a
  leading `spec.` are normalized, a dotted partial path matches when its
  parts appear in order, and a list index must match a whole segment.
  Values are not searched. `markSearch` sets `data-search` (`match`,
  `holds`, `inside`, `hidden`) on each part, the scoped CSS hides the
  hidden ones, and the table of contents leaves them out. A matching
  section, list or entry shows all of its contents. The count line
  (`node-editor-search-results`) describes the box, and the dialog's
  status region announces it 500 ms after typing stops. A search start
  saves `collapsedState()` (the collapsed paths, `collapsedPaths` in
  `collapse.js`, and the closed details) and expands all. Clearing it, or
  closing the dialog, calls `restoreCollapsed`. Escape in the box clears
  it, and closes the dialog only when the box is empty. A failed Apply
  clears the search and keeps sections expanded.
- Gotcha, sticky headers: in the Node Editor only, the top-level section
  legends (floated, so they become grid items that can stick) and the open
  More settings summary stick to the top of the scroll area. `STICKY` in
  `NodeEditorDialog.vue` mirrors the scoped CSS selector, and
  `scroll-padding-top` plus the `uncover` focusin handler scroll a focused
  field clear of a stuck header. Change both together.
- The side Inspector's header (`HEAD_ACTIONS`) has Add connection
  (`dialog.connect`, emits `connect`, devices and switches only, not on an
  included device) and Move to group (`dialog.regroup`, emits `regroup`),
  each only while its command can run for the node, then Open Node Editor
  (`builder-button--primary`, emits `edit`). All three share one size
  (`.builder-inspector__head-button`). `.builder-inspector__open-editor`
  only adds the forced-colors border. Test ids: `inspector-connect`,
  `inspector-regroup`, `inspector-node-editor`. The tooltips name the
  keys. The toolbar has no Add connection or Move to group.
- Sections and list entries collapse (`inspector/collapse.js`). A group
  with a label (`InspectorGroupRenderer.vue`), a list, a map and each list
  entry has a disclosure button (`InspectorDisclosure.vue`) in its header.
  The collapsed paths are kept per variant (`canvas`, `template`,
  `editor`) for the session. Reset view clears them (`resetCollapsed`).
  `BuilderInspector.vue` provides `INSPECTOR_COLLAPSE` and
  `INSPECTOR_HEADING_LEVEL` (h3, or h4 under the template editor's own h3,
  for the spec's heading group). A collapsed part with a field error marks
  its header. Go to, the error summary, the table of contents and an Apply
  or a save that errors stop expand the part with the field
  (`collapse.reveal`).
- The outline contract: an element with `data-outline` (and
  `data-outline-label`, `data-outline-count`, `data-outline-errors`) is an
  entry of the Node Editor's table of contents. `data-collapse-path` marks
  what collapses, and `data-outline-focus` is the control that takes focus
  on Go. `formOutline.js` holds the pure parts (`itemSummary`,
  `entryTitle`, `outlineTree`, `activeEntry`, and the path updates after
  an insert, a remove or a move). `BuilderInspector.vue` exposes
  `collapseAll`, `expandAll`, `reveal(key)`, `collapsedState` and
  `restoreCollapsed`. The Node Editor's header has Search fields, Expand
  all and Collapse all, and `dialogs/NodeEditorToc.vue` draws the
  table of contents. The entry gone to stays marked when the fields fit
  without a scroll.
- Gotcha: the first button of a list is now its disclosure button. So
  `focusField` tries `.array-list-add` before any other button.
- Field order and titles: `SPEC_FIELD_ORDER` in `schema.js` orders the
  fields of a drive and of an interface. `INTERFACE_FIELDS` puts `address`
  first for a static interface (`address`, `mask`, `gateway`, `proto`,
  `name`, `vlan`, `mac`). Add interface focuses Address (`FIRST_FIELDS` in
  `InspectorArrayRenderer.vue`). New interfaces are `static`. The titles
  "Firewall rulesets" and "miniccc commands" are in `schema.js`, and
  `capitalize` in `text.js` keeps a lowercase tool name.
- Addresses: `INSPECTOR_RANDOM_MAC` gives an interface's MAC address field
  a Generate button (`randomMac` in `addressing.js`, a MAC no interface of
  the diagram or the form uses). A switch's Inspector shows Assign
  addresses under its fields while the host has `assignAddresses`
  (`addressesShown`). The store's `connect` gives a new connection's
  static interface the next free address of the network's subnet while
  the setting `assignAddressesOnConnect` is on (`connectOptions` in
  `store.js`, model `connect(doc, connection, {assign})`). Paste and
  Duplicate follow the setting too. Otherwise, or with no subnet, it
  announces that no address was set.
- The Diagram section (`InspectorDiagram.vue`) has a Grid size field
  (`inputmode="numeric"`, `inspector/gridSizeField.js`: `isGridSize` in
  `model.js` takes a whole number from `MIN_GRID_SIZE` to `MAX_GRID_SIZE`,
  4 to 200, one undo step through `store.setGrid`, refused text stays in
  the field with `GRID_SIZE_ERROR`, "Grid size must be a whole number from
  4 to 200."), the layer list with the layer set link and Update
  from layer set, and `InspectorTemplateLinks.vue`. While the setting
  `nodeIconSize` is not Off, the Icon size fields show
  `iconSizeOverrideNote()` (`settings.js`).
- A button whose request opens a dialog is a `BuilderBusyButton.vue`: a
  spinner replaces its label while the request runs, and it keeps focus,
  so focus returns to it from the dialog. The Scenarios dialog uses it for
  View, Edit, Create new scenario and Store and add. `dialogs/ScenarioEditorDialog.vue` opens over
  `ScenarioDialog.vue` for View, Edit and Create new scenario (see
  [publishing.md](publishing.md#the-scenario-stage)). Enter in Filter
  scenarios filters and does not save the dialog.
- Diagram notes: `store.setDiagramNotes`, `diagramNoteProblem` in
  `model.js`. A note that breaks a rule is never written.
- A multi-line text box (a `multi` control in `InspectorInputControl.vue`,
  a diagram note) takes `v-fit-text` (`inspector/fitText.js`): two rows,
  grown to its text up to `MAX_LINES`, and never shorter than the user
  dragged it. It refits on input, on a Vue update that changes its value
  and when its width changes.
- The diagram's Scenarios list (`inspector/InspectorDiagram.vue`, after
  Notes) gives each scenario an `h4` with `scenarioCounts`, and each app
  `scenarioAppNote` (`builder/scenarioSummary.js`).

## Notes, layouts and the canvas

- Device and switch notes (`nodeNotes` in `model.js`) draw in a card below
  the node (`nodes/NodeNotes.vue`), outside the node's box. The setting
  `showNodeNotes` hides the cards.
- Every layout, Fit, the least zoom and "bring into view" use the
  footprint: the box plus the notes card (`nodeFootprint`,
  `footprintBounds` in `nodeNotes.js`). Fit uses `getTransformForBounds`,
  because Vue Flow's own fit measures only the boxes. For Fit, zoom and
  view, the card counts only while `showNodeNotes` is on. Group sizing
  takes `{showNotes}` too.
- Layouts are registered in `layouts/index.js`: `elk` (ELK layered, the
  default), `tiers` (By layer, from each node's `layer`), `cards`,
  `dagre`, `standard`, `sfdp` (Yifan Hu) and `radial` (Graphviz sfdp and
  twopi through `@hpcc-js/wasm-graphviz`, `graphviz.js`), and `force`
  (d3-force, `force.js`). ELK and Graphviz run in Web Workers that load on
  first use. The Graphviz worker source uses `import`, so it must start as
  a module worker. The point layouts (`force`, `sfdp`, `radial`) end with
  the box overlap pass `separateBoxes` (`separate.js`). They take random
  numbers from a seed (`seedOf`, `seededRandom`), so the same document and
  selection give the same layout. In `tiers`, the selected devices and
  switches are the roots. With no selection, the roots are firewalls, then
  routers, then the switch with the most connections. An external device
  (`spec.external`, node type `hil` or the `external` icon, see `tierKind`)
  is a root only when selected. Layouts leave drawings in place and grow groups
  around them (`growAroundDrawings`). The document's `layout` records the
  last layout that ran. User docs:
  [Layouts](https://phenix.sceptre.dev/latest/builder/diagrams/#layouts).
- Stacking: the canvas turns Vue Flow's `elevateNodesOnSelect` off and
  sets z-index itself (`nodeZIndex` in `adapters/vueflow.js`). A selected
  shape or line is never lifted, so it never covers a device or its
  handles. Resize and line handles are in Vue Flow's edge label layer
  (z-index 2000), and reach the store through `nodes/canvasEditing.js`, so
  a node rendered alone imports no store.
- PNG and SVG downloads (`exporters.js`) are at most `MAX_IMAGE_SIZE`
  (4096) pixels each way (`computeExportViewport`). `exportImage` passes
  `imagePixelRatio`, because html-to-image multiplies the size by the
  screen's `devicePixelRatio`.
- The coordinate rulers (setting `showCoordinates`) are siblings of
  `<VueFlow>` in `BuilderCanvas.vue`, outside the pane: `.has-rulers`
  gives the pane a top and left margin, so Vue Flow's `dimensions` and
  every overlay or fit computation see the smaller pane, and exports
  (the transformation pane) never hold them. Tick math is `rulers.js`
  (1-2-5 steps, small ticks as a CSS background moved by
  `--minor-offset`). They count absolute flow coordinates, which match a
  node's Position only outside a group.
- Node icons: a device, switch or group gets `useNodeIcon(size)`
  (`nodes/nodeIconSize.js`). While the setting `showNodeIcons` is off, it
  draws no icon and takes no icon layout. The document keeps the icon. The
  size is `nodeIconSize(doc, node, override)` in `model.js`, where the
  override is `iconSizeOverride()` from the setting `nodeIconSize` (`off`,
  `small`, `medium` or `large`). The override applies to the viewer's
  canvas and to PNG and SVG downloads only. The document never keeps it.
  The class of a Medium or Large icon sets `--bx-node-icon` (24px or
  32px), the icon's side.
- Node colors: `useNodeColors(payload, outlines)` (`nodes/nodeColors.js`).
  The outline is the node's own outline color, else its layer's color
  while `layerSet.layerOutlines` is on (`layerOutlineColor`), else for a
  switch its network's color (`defaultOutline`). A custom network color
  that fades into the canvas gets a ring (`needsCasing`). The icon of a
  device, switch or group is in `--bx-node-outline`, or `--bx-accent` with
  no outline (`builder.css`). `iconInk(outline, fill)` in `colors.js` sets
  `is-icon-ink-light` and `is-icon-ink-dark`, which draw the icon in the
  text color in a theme where the outline keeps less than 3:1.
- A switch shows only its icon and its name (its network's name). Its
  accessible name gives its network and VLAN alias
  (`nodeAccessibleName` in `diagramText.js`).
- The header's counts (`BuilderCounts.vue`, `diagramCounts`) include
  `layers` only while `someNodeOnLayer(doc)` (a device or a switch at a
  layer, chosen or matched), and then count `effectiveLayers`,
  placeholders included, as the Inspector lists them. Its press selects every
  node on a layer (`kindSelection` with `nodeHasLayer` in `selection.js`).
- Snap to grid, placing and drags: see
  [code-and-tests.md](code-and-tests.md#node-positions-and-the-grid). A
  click on a palette entry or an Add command puts the node in the first
  free slot fully in view (`addInView` in `paletteDnd.js`, `freeSpot` with
  `within`), and walks past the view only when no slot is free.
- Icon bytes are drawn only through `BuilderIcon`'s `<img>` with a
  `data:image/png;base64,` URL, never as markup.
- Auto-group by name pattern runs the pattern in a same-origin worker
  (`grouping.js`, `groupingWorker.js`) with a time limit.

## Keys and commands

- Commands are in `commands.js` and default keys in `keymap.js`. A letter
  alone is refused as a shortcut (`keyRefusal`), except for a command whose
  keys work only on the canvas (`takesLetters`), such as `N`. A key press
  runs a command's `keyChoice`, so `N` adds the plain Device while the
  palette still asks for a template.
- The palette's prefixes are `SEARCHES` in `commandSearch.js`: `@`, `#`,
  and `t:`, `c:`, `i:` for the commands `goto.template`,
  `goto.collection` and `goto.icon` (choices from `librarySearch.js`). A
  plain search adds up to `NAMED_LIMIT` of each library, matched by words
  only (no letters-in-order band). The icons come from the view's `icons`
  getter (VIEW_API). A collection runs `openTemplateLibrary(show)`, which
  calls `showList(show)` on `BuilderTemplates.vue`.
- A command's `direct(ctx)` gives the choice it runs with, without
  asking, from its keys and the palette alike, when the state makes one.
  Without one, it asks (`steps`, `choices`). Open Node Editor (`node.edit`)
  uses it: it runs on the selected node, else it asks for a device or a
  switch.
- Commands without default keys that users can give keys: Add a
  connection… (`dialog.connect`), Move to a group… (`dialog.regroup`), Assign addresses
  (`structure.assignAddresses`, asks for a network with a subnet), Set
  layer of selection (`structure.setLayer`, the store's `setNodesLayer`
  over model `setNodeLayers`, one undo step, with No layer, offering the
  placeholder layers too), Select nodes on layer (`selection.layer`,
  available while `someNodeOnLayer`), Show or hide node icons (`view.nodeIcons`),
  Snap to grid (`view.snapToGrid`), and Node icon size: Off, Small, Medium
  and Large (`view.nodeIconSize.<value>`). Edit layers… is
  `settings.layers`. Layer choices carry `layer`, which the palette draws
  with `LayerName.vue`.
- The view (VIEW_API) adds `showNodeIcons`, `nodeIconSize`, `snapToGrid`,
  `openLayers`, `toggleNodeIcons`, `setNodeIconSize` and
  `toggleSnapToGrid`, and `draftsTab` and `showDraftsTab` take `layers`.
- `@` (Go to node) ranks a match by the field that matched
  (`NODE_FIELD_RANKS` in `commands.js`: hostname, IP address, MAC
  address, VLAN and network, annotation, label, description, note, image),
  through the field `rank` of `matchItem` and `fieldOrder` in `fuzzy.js`.
  A node whose name matches letter by letter comes before a field match:
  the command has `nameFirst`, so `matchChoices` in `commandSearch.js`
  matches each name once (`matchName`, title and aliases) and then
  matches fields with `name: false`. Gotcha: `fuzzy.js` caches each
  item's keyword text in a WeakMap keyed by the item, so a search item
  must not change its title, aliases or keywords after its first match.
  A field's `prefix` ("eth0 IP address", "label") goes before the matched
  value on the second line (`detailParts` in `commandSearch.js`). `t:`
  also finds a template by the layer its fields match in the open
  diagram. Owner is no longer a field of `t:` or `c:`.
- Header breakpoints are container queries in rem (`builder/builder.css`,
  `Builder.vue`). Below a 97rem header some buttons show only their icons,
  below 77rem more do, and below 64rem the header wraps.

## Lists of checks and Go to

`BuilderIssueList.vue` lists the issues of the Publish dialog, the Diagram
checks dialog and the Preflight report, grouped by severity
(`bySeverity`, `severityHeading` in `issues.js`).

- Every issue goes through `toIssue(entry, defaultSeverity, {publishing})`
  into the issue shape of [api.md](api.md#issues-and-error-bodies). With
  `publishing`, a `blocksPublish` warning is an error.
  `responseIssues(data)` reads `errors`, then `warnings`, then `issues`,
  and lists an issue given twice once.
- `issueTarget(doc, issue)` resolves `nodeId`, `edgeId`, `networkId`, else
  the element the `path` starts at, and `issueField` gives the JSON Forms
  data path. No element, no Go to.
- `store.goToIssue(issue)` selects the target and sets
  `store.focusRequest = {kind, id, field, token}`. `BuilderInspector.vue`
  (`goToField`) focuses the field. It takes a request once
  (`store.takeFocusRequest(token)`), in a `post` watcher and as it mounts.
- Testids: lists `publish-checks`, `publish-refusal`,
  `publish-result-issues`, `checks-issues`. Items `issue`,
  `issue-message`, `issue-element`, `issue-code`, `issue-go-to`.

## Drafts page and selectable lists

- `BuilderDrafts.vue` holds the tabs. Bulk actions are a client loop over
  the per-item routes (`runBulk` in `bulk.js`): at most 4 requests at once
  (topology deletes and downloads 1), and one retry of a 412 with the
  `ETag` it carries. A session that ends, or a server out of reach, stops
  the run (`endsBulk` in `api.js`). The rest are "Not attempted."
- The tabs are Drafts, Published Diagrams, Node Templates, Layers and
  Icons (tab ids `drafts`, `published`, `templates`, `layers`, `icons`).
  Drafts lists `store.drafts.all`, newest first (`newestFirst` in
  `draftMessages.js`), then the damaged drafts. Each draft card shows
  "Created by" (`createdBy`) and "Updated <time> by <user>"
  (`lastModifiedBy`), and has Delete, and Publish when `store.canPublish`.
  The Delete and Delete selected buttons of the tabs
  have the plain style (not `builder-button--danger`). These stay
  `builder-button--danger`: the confirmation's Delete (`BuilderConfirm.vue`),
  Delete and Delete selected of the Custom icons dialog, the Inspector's
  remove connection point button, and Discard in the Template and Node
  Editor dialogs.
- Every selectable list (the card tabs, the Node Templates library, both
  lists of the Custom icons dialog) uses `useListSelection` in
  `listSelection.js`: selection by key, a range anchor, and a roving
  tabindex on the `<li>`. `listKeyAction` and `onListKeydown` map the keys.
  `matchesKey` finds Mod+A on a layout without Latin letters. User docs:
  [Selecting with the keyboard](https://phenix.sceptre.dev/latest/builder/drafts/#selecting-with-the-keyboard).

## Session and browser storage

- Sign in again (`BuilderSignIn.vue`, `signin.js`) sends the login without
  the ended session's token, because the JWT middleware refuses an expired
  token before the login handler reads the password. It stores the new
  token where the last sign-in did (sessionStorage, and localStorage with
  Remember me), then sends the refused changes at once. With
  `VITE_AUTH=proxy`, or a session without a JWT, the save state says to use
  Download and sign in again.
- What the browser keeps: IndexedDB `phenix-builder` (unsaved changes and
  local drafts), sessionStorage `phenix.builder.tab` (the tab's id), and
  localStorage `phenix.builder.*`. Logout, and a sign-in as another user
  (`phenix.builder.user` names whose data the browser holds), remove the
  IndexedDB data, recent commands, `phenix.builder.preflight`,
  `phenix.builder.groupPattern` and `phenix.builder.palette`. They keep
  the preferences `theme`, `panes`, `minimap`, `shortcuts` and `settings`
  (`BUILDER_PREFERENCE_KEYS` in `session.js`). `settings` holds, among
  others, `showNodeIcons`, `nodeIconSize`, `assignAddressesOnConnect` and
  `snapToGrid` (`SETTINGS` in `settings.js`). The default layer set of new
  diagrams is on the server, not in the browser.
  Logout first sends the changes still queued.
- The live region (`BuilderLiveRegion.vue`) holds each message 750 ms and
  joins messages that arrive meanwhile. A modal dialog makes the page
  inert, so a dialog that must announce has its own `role="status"`
  region.
