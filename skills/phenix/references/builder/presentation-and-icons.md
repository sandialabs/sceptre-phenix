# Presentation fields and custom icons

Part of the [Builder references](../builder.md). Colors, line and border
styles, icon size, drawings (shapes, icons and lines), the node face and
tooltip, PNG and SVG limits, and the server's custom icon library with its
routes, records and dialog.

## Presentation fields

Optional document fields (schema revision stays 1; none is ever written to
a Topology, Scenario or Experiment config, `TestToTopologyOmitsPresentationFields`):
device and switch `outlineColor`, `fillColor` (`#rrggbb`); switch `notes` (the
rules of `metadata.notes`; a device's notes are its spec's `general.notes`,
which are published); network and edge
`lineStyle` (`solid`, `dashed`, `dotted`, `dash-dot`; empty is Auto); group
`description`, `borderStyle` (`solid`, `dashed`, `dotted`, `double`),
`iconKey`, `icon` (an icon name); device `icon` (an icon name); root `icons`
(`{"<icon name>": {data}}`, at most 50 (`MaxDocumentIcons`,
`MAX_DOCUMENT_ICONS`), each a PNG of 1 to 96 pixels a side and at most 40960
bytes, chunks `IHDR`, `PLTE`, `tRNS`, `IDAT`, `IEND` only;
`bdoc.ValidateIcons`, `customicons.go`) and root `templates` (at most 50;
see [Node templates](templates.md)). An icon name is 1 to 64 bytes of
`^[A-Za-z0-9_@.-]+$`, neither `.` nor `..` (`bdoc.IconNameProblem`,
`iconNameProblem` in `validate.js`); a document may name an icon it does not
carry. Go and JS validate them alike (`testdata/validation-corpus.json`).
The Inspector writes them; the network's own `color` is labelled "Edge
Color".

## Icon size

Icon size: root `iconSize` and `iconSize` on the device, switch and group
payloads (and so on a template's device), each `small`, `medium` or `large`
(`IconSizes()` in `validate.go`, `ICON_SIZES` in `model.js`; empty or null
is none). A node draws its own, else the root's, else small
(`nodeIconSize`, `documentIconSize`), at 16, 24 or 32 pixels
(`ICON_SIZE_PIXELS`, `iconPixels`); drawings (`icon` nodes) are sized by
their box instead. `setIconSize` leaves the root without the key for small.
The Inspector's Diagram view has an Icon size select
(`InspectorDiagram.vue`, testid `inspector-icon-size-select`, one undo step
"Changed the diagram's icon size to Large", `store.setIconSize`); the
device, switch and group forms have Icon size with "Diagram default (…)"
(`UNSET_KEYWORD`, `fieldDefault` names the diagram's size), which removes
the node's. A device's applies at once as a look key (`LOOK_KEYS`), a
switch's and a group's with Apply; copy and paste keep a node's own
(`clipboard.js`). Node boxes keep `DEFAULT_SIZES`: a Small
icon sits before the name as before; Medium and Large add
`builder-node--icon-<size>` (`nodes/nodeIconSize.js`) and `builder.css`
places the icon absolutely in Vue Flow's wrapper, left of the node's lines
(centered on a device or switch, top left of a group); every node of these
kinds carries `data-icon-size`.

## Drawings

Drawings: node kinds `shape` (`shape: {shape: rectangle|circle, label?,
fillColor?, outlineColor?, borderStyle?}`), `icon` (`icon: {iconKey?, icon?,
label?}`, exactly one of a built-in key and a custom icon name, which
resolves, embeds and uploads as a device's does; one nothing resolves draws
the built-in `external`) and `line`
(`line: {points, label?, color?, lineStyle?, startArrow?, endArrow?}`, 2 to
64 points (`MinLinePoints`, `MaxLinePoints`) relative to the node's
position, which the editor keeps at the top left of the points' box with
the box as `size`: `placedLine` in `model.js`). Colors are `#rrggbb`, styles
the border and line styles above; the payload's `label` is the node's
label. Like notes and groups they never reach a config
(`TestToTopologyOmitsVisualNodes`), GEXF or Topology YAML, take no
connections, may have a group `parentId`, and Go and JS check them through
the shared corpus. The schema's payload definitions are `shape`, `iconNode`
(the node key is `icon`; `$defs.icon` is a custom icon) and `line`. The
palette (`PALETTE` in `catalog.js`, entries `rectangle`, `circle`, `icon`,
`line`; testids `palette-<id>`) and the commands `add.rectangle`,
`add.circle`, `add.icon`, `add.line` add them. Canvas: `nodes/ShapeNode.vue`,
`IconNode.vue`, `LineNode.vue` (flow types `builderShape`, `builderIcon`,
`builderLine`; shapes and lines on the groups' layer, under devices). The
canvas turns Vue Flow's `elevateNodesOnSelect` off and stacks nodes itself
(`nodeZIndex` in `adapters/vueflow.js`, applied by `toFlowNodes` and
`withSelection`): a selected node, and every node in a selected group, is
lifted by 1000, but a shape or a line never is, so a selected one never
covers a device, a switch or their connection handles. A selected line has
a handle per point (`line-point-N`, pointer only and `aria-hidden`;
dragging commits one `store.setLinePoints`, double-click on the line
`store.insertLinePoint`, and on a clicked handle the keys of
`linePointKey` in `model.js`: arrows `moveLinePoint`, Delete and Backspace
`removeLinePoint`, which keeps two points, Escape back to the line); the
Inspector's Points list (absolute canvas coordinates) is the keyboard path:
X and Y, Add, Remove, and "Insert point after point N"
(`insertedListItem` in `adapters/forms.js`, offered through
`INSPECTOR_INSERT_ITEM` to `InspectorArrayRenderer.vue`: a bend halfway to
the next point, in the working copy that Apply commits as one step).
Layouts leave drawings where they are, moving one in a group with its
group, and then grow each group right and down to hold its drawings with
the layouts' `GROUP_PADDING`, innermost first (`runLayout` and
`growAroundDrawings` in `layouts/index.js`). Notes, groups, shapes and
icons resize with `@vue-flow/node-resizer` (`nodes/NodeResize.vue`,
committed by `store.resizeNodeBox`, which keeps a group around its
members) and with Alt+Shift and an arrow key (`keyResizedSize`,
`minimumSize`, `RESIZABLE_KINDS` in `model.js`). The resizer's handles
(`.builder-resizer[data-node-id]`) and the line's handles are in Vue
Flow's edge label layer (z-index 2000, over every node), outside the
node's wrapper (a button, which holds nothing focusable); `NodeResize.vue`
and `LineNode.vue` reach the store through what `BuilderCanvas.vue`
provides (`nodes/canvasEditing.js`), so a node rendered alone imports no
store. PNG and SVG downloads keep a line's arrowheads: `exportCopy` in
`exporters.js` removes the copy's IDs but renames each one an SVG
`marker-start`, `marker-mid` or `marker-end` points at (`<id>-image-<n>`),
with the references. A PNG or SVG is at most `MAX_IMAGE_SIZE` (4096)
pixels wide and high however large the diagram: `computeExportViewport`
scales it down to fit whole, below the canvas's least zoom (0.2) when it
must. html-to-image multiplies a PNG's size by its `pixelRatio`, the
screen's `devicePixelRatio` when none is given, so `exportImage` passes
`imagePixelRatio`: the screen's ratio while the PNG stays within
`MAX_IMAGE_SIZE` each way, less otherwise.

## Node face and tooltip

A device node shows `spec.type` as stored ("External" when `spec.external`,
"Device" without a type). Devices and switches have an info tooltip on
hover (400 ms) and on keyboard focus (`BuilderNodeTooltip.vue`,
`nodeInfo.js`, `useFixedTooltip`); it is not in PNG or SVG downloads.

## Custom icons

Custom icons: the server keeps one icon library for every user
(`api/builder/icons.go`, `web/builder_icons.go`). Each icon has a unique
name its uploader chose (the icon name rule; unique ignoring case; the first
upload of a name wins, a later one with other bytes is 409 naming the
uploader), an `id` (SHA-256 of the bytes), `owner`, size and times. Nodes
and templates name icons; a draft carries no image data, and `settleIcons(doc,
library)` in `store.commit` only drops copies: those nothing names and those
the library holds under that name with the same bytes. A name resolves to
the document's copy, else to the library's icon (`resolveIcon`, `iconSrc`
in `icons.js`; the library is `iconLibrary` in `iconLibrary.js`, read once a
session and after each change); a name nothing resolves draws the node's
built-in icon. A download embeds a copy of every icon the document names
(`embedIcons`, `downloadDocument` in `exporters.js`; at most 50), and so
does the logout warning's and the version chooser's Download
(`downloadedDiagram` in `session.js`, which loads `icons.js` and
`iconLibrary.js` only for a diagram that names a custom icon). An upload
with copies (`ingestIcons`) adds each name the library lacks (as the
uploader's), drops copies the library holds with the same bytes, and keeps
copies whose name the library holds with other bytes or that it could not
add, each with a warning; Edit as a draft of a topology's Builder file
does the same before it creates the draft (`openPublishedDocument` in
`store.js`; the warnings are announced and stay in the canvas notice). The browser converts PNG, JPEG, GIF, WebP and SVG
(up to 5 MiB) to a PNG of at most 96 pixels before upload. Icon bytes are
drawn only through `BuilderIcon`'s `<img>` with a `data:image/png;base64,`
URL, never as markup.

### Icon routes and records

Routes (`{icon}` is a name or an alias, ignoring case): `GET/POST
/builder/icons` (`configs` `list`/`create`; the listing has every icon,
`canRename`/`canDelete` per icon, and the caller's `usedIcons`/`usedBytes`
of `maxIcons`/`maxBytes`), `GET /builder/icons/{icon}` (`configs` `get`),
`PUT /builder/icons/{icon}` `{name}` renames (`configs` `update`; the
uploader, or `builder-icons` `update`; the old name stays an alias; a case
change keeps no alias), `DELETE /builder/icons/{icon}` deletes the icon and
its aliases (`configs` `delete`; the uploader, or `builder-icons` `delete`).
Another user's icon without the permission is 403; an unknown or invalid
name is 404 `icon not found`. An upload is a PNG of at most 96 pixels and
65536 bytes (body 131072); a strict PNG of at most 40960 bytes is kept as it
is, any other is re-encoded to its pixels; 201 for a new icon, 200 for the
same name with the same bytes. Limits: 64 icons and 1 MiB per uploader
(`MaxLibraryIcons`, `MaxLibraryIconBytes`), 2000 icons in all (`MaxIcons`).
Records in `builder.icons`: `name/<lowercase name>` holds an icon
(`{kind: "icon", name, id, owner, width, height, bytes, created, updated,
aliases, data}`) or an alias (`{kind: "alias", name, target}`). A rename
points every alias the icon lists at the new name (`retargetAliases`), so an
alias is one hop from its icon; resolution follows at most 16 hops, which
only a failed retarget can need. Creates are create-if-absent and renames
compare-and-swap, so two uploads of one name never both win. A delete that
loses a race to a change of the icon is 409. At start, `CleanupLegacyIcons`
deletes records of the earlier per-user layout (`<OwnerScope>/<hex>`).
Every `/builder/` response has `X-Content-Type-Options: nosniff`; the icon
routes also `Content-Security-Policy: default-src 'none'; frame-ancestors
'none'` (`builderResponseHeaders`). No CSP is set on the application page.
Files: `icons.js`, `iconLibrary.js`, `dialogs/IconDialog.vue`,
`inspector/InspectorIconControl.vue`, `nodes/nodeColors.js`.

### Custom icons dialog

The Custom icons dialog selects several icons (`listSelection.js`): a
diagram copy the server lacks (once the library has been read) and a
server icon with `canDelete` have a checkbox (`icon-select`), each list a
`BuilderBulkBar` (`icons-diagram`, `icons-library`). Add selected to server (`bulk-add-icons-diagram`)
uploads each selected copy with `iconLibrary.upload(icon, {refresh:
false})`; Delete selected (`bulk-delete-icons-library`) asks once ("Delete
2 icons?", `iconsDeleteMessage` in `bulk.js`; one icon alone gets the
row's question) and deletes with `iconLibrary.remove(name, {refresh:
false})`; both through `runBulk` (stopped by `library.ends`), then one
`load()`. The status says "Added 2 icons to the server. …" (counting only
answers with `created`; a copy the server already had is named, "The
server already has plc." or "… plc as pump.", as the row's Add to server
says) or "Deleted 2 icons from the server."; failures are a
`BuilderBulkSummary` under the row, which takes focus. The library list's
selection follows the filter (hidden icons leave it).
