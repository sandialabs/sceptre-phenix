# Builder document format

Part of the [Builder references](../builder.md). The `builder/v1` document:
its keys, how Go and JS validate it, the generated schema, and the
provenance fields the server stamps.

User docs: [Builder JSON and YAML](https://phenix.sceptre.dev/latest/builder/import-upload-download/#builder-json-and-yaml)
and [Who made and last saved a diagram](https://phenix.sceptre.dev/latest/builder/import-upload-download/#who-made-and-last-saved-a-diagram).
[Building a Diagram](https://phenix.sceptre.dev/latest/builder/diagrams/)
describes the fields a user edits, and the limits.

## Keys and validation

- Root keys, in this order: `$schema`, `revision`, `metadata`, `nodes`,
  `networks`, `edges`, `viewport`, `grid`, then the optional `scenarios`,
  `source`, `layout`, `iconSize`, `templates`, `layers`, `layerSet` and
  `icons`. The revision stays 1: new fields are optional.
- `grid` has `enabled` and `size`. The size is a whole number from
  `MinGridSize` to `MaxGridSize` (4 to 200, `MIN_GRID_SIZE` and
  `MAX_GRID_SIZE` in `model.js`), else `document.grid.invalid`. The format
  has no `grid.snap`: Snap to grid is a setting of each user (`snapToGrid`
  in `settings.js`, see
  [code-and-tests.md](code-and-tests.md#node-positions-and-the-grid)). A
  `grid.snap` key is refused like any other unknown grid key.
- A node's icon size is its own `iconSize`, else the root `iconSize`
  (`nodeIconSize(doc, node, override)` in `model.js`). The user's Node icon
  size setting (`iconSizeOverride()` in `settings.js`) comes before both,
  on the canvas and in PNG and SVG downloads only. The document never keeps
  it.
- `metadata` (`bdoc.Metadata`) is required. It holds only `id` (required),
  `name`, `description`, the four provenance fields and `notes`. Strict
  decoding refuses any other key, and refuses these keys at the root (no
  shim for the old root `id`, `name`, `author`).
- Go (`types/builder/validate.go`) and JS (`validate.js`, `decode.js`)
  check the same rules with the same codes. The shared corpus
  `types/builder/testdata/validation-corpus.json` holds them to each other.
  A new rule goes in both, with a case in the corpus.
- "Blank" means Go's `unicode.IsSpace` white space everywhere (`isBlank` in
  `text.js`, `trimSpace` in `validate.js`, `notePattern` in `schema.go`).
  The pattern spells the set out as `\x00-\x20\x7f\x85\p{Z}`, because `\s`
  differs between Go and ECMAScript. U+0085 is blank and U+FEFF is not.
- Presentation and Builder-only fields never reach a config or Topology
  YAML (`TestToTopologyOmitsPresentationFields`,
  `TestToTopologyOmitsVisualNodes`, `TestToTopologyOmitsNetworkAddressing`):
  colors, `lineStyle`, `borderStyle`, `icon`, `iconKey`, `iconSize`, switch
  `notes`, `metadata.notes`, groups, the drawing node kinds `shape`, `icon`
  and `line` (also not GEXF), `layout`, edge `route`, `layers` (with their
  icons, colors and matchers), `layerSet`, `layer`, a template's `source`,
  and a network's `subnet` and `gateway`. A device's notes are its spec's
  `general.notes`, which Publish writes.
- `layers` at the root is the diagram's own copy of its layers
  (`Document.Layers`, `Layer`): at most `MaxLayers` (32), each `id` from 0
  to `MaxLayerID` (99) and unique, each `label` not blank, at most
  `MaxLayerLabelBytes` (64) and without control characters. Codes:
  `layer.list.too-many`, `layer.id.out-of-range`, `layer.id.duplicate`,
  `layer.label.required`, `layer.label.too-long`, `layer.label.control`.
  The order is not checked. The editor writes it highest `id` first. With
  no layers, the editor offers `PlaceholderLayers` (7) placeholders, IDs 0
  to 6 labeled "Layer 0" to "Layer 6". `layerListIssues` in
  `types/builder/layerset.go` checks the list, for a document and for a
  layer set.
- A layer can also have `iconKey` and `icon` (as a device's), `color`
  (`#rrggbb`) and `matchers`, an object of field paths to regular
  expressions. Codes: `layer.icon-key.unknown`, `layer.icon.invalid`,
  `layer.color.invalid`, `layer.matchers.not-object`,
  `layer.matchers.too-many` (more than `MaxLayerMatchers`, 16),
  `layer.matcher-path.too-long` (`MaxMatcherPathBytes`, 128),
  `layer.matcher-path.invalid` (not dotted keys of letters, digits, `_` or
  `-`), `layer.matcher-pattern.not-text`, `layer.matcher-pattern.required`,
  `layer.matcher-pattern.too-long` (`MaxMatcherPatternBytes`, 256) and
  `layer.matcher-pattern.invalid`. Go checks a pattern with RE2.
  `layerMatchers.js` (`matcherPatternRule`) also refuses what RE2 and
  JavaScript's `RegExp` do not read the same way, because the editor
  applies the matchers with `RegExp`. Its header comment lists the cases.
  The server stays the authority. User docs:
  [Layers](https://phenix.sceptre.dev/latest/builder/diagrams/#layers).
- Matchers apply to devices only. A path is dotted keys into the device
  spec, and a key of digits indexes a list. Text, a number or a boolean
  matches as its text. A list at the end of the path matches when one
  entry matches. An object, null or a missing field never matches
  (`valuesAt`, `matchDevice`, `matchedLayer`). When the matchers of more
  than one layer match, `layerSet.matchConflict` (`lowest`, the default,
  or `highest`) decides. A node's own `layer` always wins over a match.
- `layerSet` (`DocumentLayerSet`) is what a document keeps of the layer set
  its layers came from: `name`, `matchConflict`, `layerOutlines` (node
  outlines in the color of their layer, when the node has no outline color
  of its own) and `source` (`{id, version}`, the linked set of the server's
  library, see
  [templates-and-icons.md](templates-and-icons.md#layer-set-library)).
  `name` may be empty. Codes: `layerset.not-object`,
  `layerset.name.too-long`, `layerset.name.control`,
  `layerset.match-conflict.unknown`, `layerset.layer-outlines.not-boolean`,
  `layerset.source-id.invalid` (`IsLayerSetID`: 1 to 128 of letters,
  digits, `.`, `_`, `:` or `-`) and `layerset.source-version.invalid`
  (less than 1).
- `layer` on a device, a switch or a template's device is an integer from
  0 to `MaxLayerID` (Go `*int`, since 0 is a layer). It need not name a
  defined layer: the editor then labels it "Layer N". Another value is
  `device.layer.out-of-range`, `switch.layer.out-of-range` or
  `template.layer.out-of-range`, also for a value that is not an integer
  (`decodeRule` in `decodeissues.go`). Import never sets it.
- The JS helpers are in `model.js`:
  - The layers: `effectiveLayers` (the list, or the placeholders),
    `layerOf`, `layerTitles` (a Map of ID to name: the label, with
    " (ID n)" added when two layers share it), `layerLabel` (that name, or
    "Layer N" for an ID that is not defined) and `layerName` (for
    messages: "layer Control", or the label alone when it has the word
    "layer", such as "Layer 3"). The UI names a layer by its label only.
    The layers editor rows (`LayerRows.vue`) still show and edit the ID.
  - `someNodeOnLayer(doc)`: whether a device or a switch is at a layer,
    chosen or matched. The header's layers count (`diagramCounts` in
    `diagramText.js`, which then counts `effectiveLayers`, placeholders
    included) and **Select nodes on layer** show only while it is true.
  - A node's layer: `nodeLayer` (the layer chosen, the node's own `layer`),
    `matchedNodeLayer` (`{id, path}` of the matcher that matched),
    `effectiveNodeLayer` (chosen, else matched) and `nodeLayerState` (all
    three, for the Inspector). Everything that places, counts or colors
    nodes by layer uses `effectiveNodeLayer`: the By layer layout
    (`layouts/tiers.js`, a higher ID nearer the top), `layerCounts`,
    `nodeHasLayer` (`selection.js`) and `layerOutlineColor`.
  - Edits: `setNodeLayer`, `setNodeLayers` (one pass over many nodes,
    `{doc, changed}`), the pure list edits `withLayerAdded`,
    `withLayerRelabeled`, `withLayerUpdated` (`iconKey`, `icon`, `color`,
    `matchers`), `withLayerRenumbered`, `withLayerRemoved`, and their
    document forms `addLayer`, `relabelLayer`, `updateLayer`,
    `renumberLayer` (moves the nodes and templates on the layer) and
    `removeLayer` (they keep the ID, which then shows as "Layer N").
  - The layer set: `layerSetOptions`, `setLayerSetOptions`,
    `withLayerSetCopy` (a set's layers and options replace the diagram's,
    linked to its ID and version, and nodes keep their chosen layers),
    `layerSetContentOf`, `sameLayerSetContent` and `layerSetLink`
    (`unlinked`, `missing`, `update`, `changed` or `linked`).

  Each list edit writes through `normalizedLayers`: highest `id` first,
  and empty (so `layers` is left out) when it equals the placeholders. An
  included device is never changed. The store's actions of the same names
  are one undo step each. A new blank diagram copies the server's default
  layer set (`withDefaultLayerSet` in `layerSetStore.js`, called by
  `newDocument`). The browser keeps no layers of its own.
- A network with no switch is valid. `network.switch.missing` is a JS
  editing warning only (`collectWarnings` in `validate.js`); Go defines the
  code and never reports it. `removeElements` in `model.js`, the one path
  that removes nodes, also drops each network whose last switch it removed
  when `networkInUse` is false (no edge `networkId`, no device interface
  VLAN naming it by `networkByName`). Template VLANs do not count. So Undo
  restores both. Loading, Upload and Import never drop a network.
- A network can have `subnet` (IPv4 CIDR without host bits, such as
  `10.1.0.0/24`) and `gateway` (an IPv4 address, a host address of the
  subnet when the subnet is valid). Codes: `network.subnet.invalid`,
  `network.gateway.invalid` and `network.gateway.outside-subnet`. Go has
  `ParseSubnet` and `IsSubnetHost` (`validate.go`). JS reads them the same
  way in `addressing.js` (`parseSubnet`, `isSubnetHost`, where a /31 or
  /32 has no network or broadcast address to leave out). The other
  `addressing.js` helpers: `hostRange`, `nextFreeHost`, `subnetsOverlap`,
  `suggestSubnet` and `suggestedSubnet` (the first free `10.a.b.0/24`,
  which a switch that makes a new network gets), `randomMac` and
  `documentMacs`, `needsAddress` (a `static` or `ospf` interface that is
  not serial and has no address), `interfacesOnNetwork` and
  `assignAddresses(doc, networkId, only)`. `assignAddresses` never changes
  an address that is set or a device from an included topology.
- A template can have `source` (`TemplateSource`): `kind` (`library`,
  `builtin` or `preloaded`), `id`, `version` (1 or more) and an optional
  `digest` of the copy as the editor last copied it. A library template
  never has one. Codes: `template.source.kind-unknown`,
  `template.source.id-invalid`, `template.source.version-invalid` and
  `template.source.digest-invalid`. The server checks only the form. The
  editor compares the version and the digest (see
  [templates-and-icons.md](templates-and-icons.md#template-library)). A
  merge treats `source` as one value, labeled "library link"
  (`ATOMIC_TEMPLATE_KEYS` in `merge.js`).
- `TemplateDevice` must hold every `Device` field except `hostname`,
  `interfaces` and `includedFrom`. A reflection test checks it, so a new
  device field is a template field too.

## Generated schema

`builder.Schema()` is committed as
`src/js/src/builder/schema/builder-v1.schema.json`. Run `make generate` in
`src/go` after a change to `types/builder/` or to the config schemas. CI
fails when the bundle is stale.

Every Builder-owned property has a `title`, a `description` and `examples`
(`documented()` in `schema.go`, values in `schema_examples.go`).
`TestSchemaDocumentsEveryProperty` checks it in Go, and
`schema-examples.test.js` checks each example with ajv. The template file
and package schemas follow the same rule. `openapi.yml` lists the root
(`BuilderDocument`) and `metadata` (`BuilderDocumentMetadata`) properties,
and `TestBuilderDocumentDocumented` holds them to the Go structs. The
Inspector strips the schema titles (`schema.js`), so its labels do not
change.

## Document provenance

`metadata.createdBy`, `createdAt`, `updatedBy` and `updatedAt` are document
content and part of the digest. The server stamps them in `api/builder`
when it stores a snapshot. The editor never sets them.

- `POST /builder/drafts` (`CreateDraft`): `createdBy` and `createdAt` are
  the body's when present, else the caller and now. `updatedBy` and
  `updatedAt` are always the caller and now. So an Upload keeps the file's
  `createdBy`.
- Unchanged copy: with `sourceToken` `builder-doc/<id>` or
  `builder-file/<topology>/<digest>` and no `forkOf`, the server stores a
  body whose canonical JSON is the opened document without a stamp. The
  draft's digest then equals the document's, and an unchanged publish
  answers topology `skipped`. The client must send the document exactly as
  `GET` returned it.
- `POST …/snapshots` (`AppendSnapshot`) keeps the draft record's
  `createdBy` and `createdAt` and stamps `updatedBy` and `updatedAt`. Every
  save is an edit, also one that changes nothing.
- Nothing else stamps: not undo, redo, restore, snapshot delete,
  publish or a file read.
- Create and save responses carry no document, so they carry `stamp`
  (empty fields left out). The editor copies it into its document
  (`withStamp` in `model.js`). Watchers that must not react to a stamp
  alone use `sameButStamp`.

`sourceFile` on `POST /builder/drafts` records the base name of the
uploaded file (at most 255 bytes, no `/` or `\`, not `.` or `..`, no
control characters, else 422). Nothing opens it. A `forkOf` draft records
none.

## Other file formats

- Template files: [templates-and-icons.md](templates-and-icons.md#template-files).
- Layer set files: [templates-and-icons.md](templates-and-icons.md#layer-set-files).
- Builder packages: [sources.md](sources.md#downloads-and-packages).
- YAML input of all four formats goes through `builder.JSONFromYAML`
  (`types/builder/yaml.go`). It refuses anchors, aliases, merge keys, a
  second document, non-scalar or duplicate keys, other tags, `.inf` and
  `.nan`, and types scalars as js-yaml's `JSON_SCHEMA` does.
