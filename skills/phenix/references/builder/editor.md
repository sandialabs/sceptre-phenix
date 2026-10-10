# Builder editor and drafts page

Part of the [Builder references](../builder.md). The code behind the editor
page and the drafts page: how a diagram opens, the Inspector, notes,
layouts, the canvas, keys, lists of checks with Go to, bulk actions, and
what the browser keeps.

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
- The address names the open draft as `?draft=<owner>/<id>`, so a reload
  opens it again.
- Card Publish on the drafts page loads the draft without the editor
  (`store.loadDraft(owner, id, {quiet: true})`) and opens the editor's
  Publish dialog over the page.

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
- Leaving a draft, publishing and downloading first save unapplied
  Inspector changes as a snapshot (`settle`, `saveUnapplied`).
- Diagram notes: `store.setDiagramNotes`, `diagramNoteProblem` in
  `model.js`. A note that breaks a rule is never written.

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
  default), `tiers` (Layered by tier, from `purdueLevel`), `cards`,
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
  IndexedDB data, recent commands, `phenix.builder.preflight` and
  `phenix.builder.groupPattern`. They keep the preferences `theme`,
  `panes`, `minimap`, `shortcuts` and `settings`. Logout first sends the
  changes still queued.
- The live region (`BuilderLiveRegion.vue`) holds each message 750 ms and
  joins messages that arrive meanwhile. A modal dialog makes the page
  inert, so a dialog that must announce has its own `role="status"`
  region.
