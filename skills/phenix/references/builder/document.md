# Builder document format

Part of the [Builder references](../builder.md): the `builder/v1`
document.

User docs: [Builder JSON and YAML](https://phenix.sceptre.dev/latest/builder/import-upload-download/#builder-json-and-yaml)
and [Who made and last saved a diagram](https://phenix.sceptre.dev/latest/builder/import-upload-download/#who-made-and-last-saved-a-diagram).
[Building a Diagram](https://phenix.sceptre.dev/latest/builder/diagrams/)
has the fields a user edits, and the limits.

## Keys and validation

- Root keys, in order: `$schema`, `revision`, `metadata`, `nodes`,
  `networks`, `edges`, `viewport`, `grid`, then the optional `scenarios`,
  `source`, `layout`, `iconSize`, `templates`, `layers`, `layerSet` and
  `icons`. `revision` stays 1, because new fields are optional.
- `grid` has `enabled` and `size`. `size` is a whole number from
  `MinGridSize` to `MaxGridSize` (4 to 200, `MIN_GRID_SIZE` and
  `MAX_GRID_SIZE` in `model.js`), else `document.grid.invalid`. Decoding
  refuses `grid.snap` as an unknown key. Snap to grid is a setting of each
  user (`snapToGrid` in `settings.js`,
  [code-and-tests.md](code-and-tests.md#node-positions-and-the-grid)).
- A node's icon size is its own `iconSize`, else the root `iconSize`
  (`nodeIconSize(doc, node, override)` in `model.js`). The user setting
  Node icon size (`iconSizeOverride()` in `settings.js`) overrides both,
  on the canvas and in PNG, SVG and draw.io downloads only. The document
  never keeps it.
- `hideDescription` on a device, a group or a template's device is a
  boolean. True hides the description on the canvas and in drawings
  (`drawnNodeComment` in `model.js`). The editor writes it only while true
  (`setDescriptionHidden`). Another value is
  `device.hide-description.not-boolean`,
  `group.hide-description.not-boolean` or
  `template.hide-description.not-boolean` (`decodeRule` in
  `decodeissues.go`, `validateHideDescription` in `validate.js`).
- A document must have `metadata` (`bdoc.Metadata`). It holds only `id`
  (required), `name`, `description`, the four provenance fields and
  `notes`. Strict decoding refuses other keys, and refuses these keys at
  the root (no shim for the old root `id`, `name`, `author`).
- Every document identifier is an RFC 4122 UUID. Generation (`FromConfig`)
  derives version 5 UUIDs in `NamespaceUUID` from hostnames, interface
  names and VLAN names, so two imports of one config give identical
  documents. The editor makes random version 4 UUIDs.
- Go (`types/builder/validate.go`) and JS (`validate.js`, `decode.js`)
  check the same rules with the same codes. The shared corpus
  `types/builder/testdata/validation-corpus.json` holds them to each other.
  A new rule goes in both, with a case in the corpus. The exceptions:
  - Two errors are the editor's own, stricter than the server: an
    interface connection point with no interface of that name in the node
    spec, and a spec hostname that is not the device hostname. The server
    publishes such a device with the device hostname and without that
    connection, which the editor would not show.
  - Only the server checks that the PNG of a custom icon decodes.
- "Blank" means white space as Go's `unicode.IsSpace` reads it (`isBlank`
  in `text.js`, `trimSpace` in `validate.js`, `notePattern` in
  `schema.go`). The pattern spells the set out as `\x00-\x20\x7f\x85\p{Z}`,
  because `\s` differs between Go and ECMAScript. U+0085 is blank, and
  U+FEFF is not.
- A network name, a hostname (device or template) and an included topology
  name must not hold white space as `\s` reads it in the ECMAScript schema
  pattern `^\S+$` (`hasNameSpace` in `spec.go`, `WHITESPACE` in
  `validate.js`). That set is `unicode.IsSpace` without U+0085 and with
  U+FEFF. Go's `regexp` reads `\s` as `[\t\n\f\r ]` only, so Go does not
  use it. `testdata/name-spaces.json` lists the code points
  (`TestNameSpaceCorpus`, `name-spaces.contract.test.js`). Import leaves
  an interface whose VLAN holds such white space unconnected, with a
  warning, and never renames the VLAN.
- Case-insensitive comparisons match Go exactly: `foldKey` (TrimSpace,
  then `strings.ToLower`) for hostnames, IDs and interface names, and
  `strings.EqualFold` where Go uses it. JS uses `goCase.js` (`foldKey`,
  `goToLower`, `goEqualFold`, `goTrimSpace`, `trimASCIISpace`), never
  `toLowerCase`. Go maps each character alone ("İ" to "i", a final "Σ" to
  "σ"), and its Unicode version can differ from the browser's.
  - `goCase.js` reads Go's tables from `schema/go-case.json`, which
    `TestFrontendCaseTableMatchesUnicode` (`case_test.go`) writes.
    `testdata/fold-corpus.json` (`TestFoldCorpus`) holds texts and Go's
    results, and `go-case.contract.test.js` checks the JS against it.
  - Both files are Unicode 15.0.0, from the Go 1.24 that builds phenix.
    With another Go version, the two tests skip and
    `make generate-builder-schema` leaves the files alone. Regenerate them
    with `GOTOOLCHAIN=go1.25.5` (or any Go 1.24 or 1.25).
- Presentation and Builder-only fields never reach a config or Topology
  YAML (`TestToTopologyOmitsPresentationFields`,
  `TestToTopologyOmitsVisualNodes`, `TestToTopologyOmitsNetworkAddressing`):
  colors, `lineStyle`, `borderStyle`, `icon`, `iconKey`, `iconSize`,
  `hideDescription`, `metadata.notes`, groups, the drawing node kinds
  `shape`, `icon` and `line` (not GEXF either), `layout`, edge `route`,
  `layers` (with their icons, colors and matchers), `layerSet`, `layer`, a
  template's `source`, and a network's `subnet` and `gateway`.

### Layers

- Root `layers` is the diagram's own copy of its layers (`Document.Layers`,
  `Layer`): at most `MaxLayers` (32). Each `id` is unique and from 0 to
  `MaxLayerID` (99). Each `label` is not blank, at most
  `MaxLayerLabelBytes` (64), with no control characters. Codes:
  `layer.list.too-many`, `layer.id.out-of-range`, `layer.id.duplicate`,
  `layer.label.required`, `layer.label.too-long`, `layer.label.control`.
  Validation ignores the order. With no layers, the editor offers
  `PlaceholderLayers` (7): IDs 0 to 6, labeled "Layer 0" to "Layer 6".
  `layerListIssues` in `types/builder/layerset.go` checks the list of a
  document and of a layer set.
- A layer can also have `iconKey` and `icon` (as a device has), `color`
  (`#rrggbb`) and `matchers`, an object of field paths to regular
  expressions. Codes: `layer.icon-key.unknown`, `layer.icon.invalid`,
  `layer.color.invalid`, `layer.matchers.not-object`,
  `layer.matchers.too-many` (over `MaxLayerMatchers`, 16),
  `layer.matcher-path.too-long` (`MaxMatcherPathBytes`, 128),
  `layer.matcher-path.invalid` (not dotted keys of letters, digits, `_` or
  `-`), `layer.matcher-pattern.not-text`, `layer.matcher-pattern.required`,
  `layer.matcher-pattern.too-long` (`MaxMatcherPatternBytes`, 256) and
  `layer.matcher-pattern.invalid`.
- Go checks a pattern with RE2. The editor applies matchers with `RegExp`,
  so `layerMatchers.js` (`matcherPatternRule`) also refuses what RE2 and
  `RegExp` read differently. Its header comment lists the cases. Go and JS
  both refuse:
  - A nested repeat: a group with a repeat or a `|` in it that a repeat
    repeats more than once, such as `(a+)+` (`nestedRepeat` in
    `layerset.go`, rule `nested`). `RegExp` can take exponential time on
    it.
  - More than `MaxMatcherRepeats` (3) `*`, `+` and `{n,}` repeats in a
    pattern, in groups too (`unboundedRepeats`, rule `repeats`). Each extra
    repeat multiplies the time by the text length.

  The editor never applies such a pattern from a saved diagram. The server
  stays the authority. User docs:
  [Layers](https://phenix.sceptre.dev/latest/builder/diagrams/#layers).
- Matchers apply to devices only. A path holds dotted keys into the device
  spec, and a key of digits indexes a list. Text, a number or a boolean
  matches as its text. A list at the end of the path matches when one
  entry matches. An object, null or a missing field never matches
  (`valuesAt`, `matchDevice`, `matchedLayer`). When matchers of more than
  one layer match, `layerSet.matchConflict` (`lowest`, the default, or
  `highest`) decides. A node's own `layer` always wins over a match.
- `layerSet` (`DocumentLayerSet`) is what a document keeps of the layer set
  that its layers came from: `name` (can be empty), `matchConflict`,
  `layerOutlines` (node outlines in the layer color, when the node has no
  outline color of its own) and `source` (`{id, version}`, the linked set
  of the server library, see
  [templates-and-icons.md](templates-and-icons.md#layer-set-library)).
  Codes: `layerset.not-object`, `layerset.name.too-long`,
  `layerset.name.control`, `layerset.match-conflict.unknown`,
  `layerset.layer-outlines.not-boolean`, `layerset.source-id.invalid`
  (`IsLayerSetID`: 1 to 128 of letters, digits, `.`, `_`, `:` or `-`) and
  `layerset.source-version.invalid` (less than 1).
- `layer` on a device, a switch or a template's device is an integer from 0
  to `MaxLayerID` (Go `*int`, because 0 is a layer). It need not name a
  defined layer: the editor then labels it "Layer N". Another value,
  integer or not, is `device.layer.out-of-range`,
  `switch.layer.out-of-range` or `template.layer.out-of-range`
  (`decodeRule` in `decodeissues.go`). Import never sets it.
- The JS layer helpers are in `model.js`:
  - The list: `effectiveLayers` (the list, or the placeholders),
    `layerOf`, `layerTitles`, `layerLabel` ("Layer N" for an undefined
    ID) and `layerName` (for messages). The UI names a layer by its label
    only. The layers editor rows (`LayerRows.vue`) also show and edit the
    ID.
  - `someNodeOnLayer(doc)`: whether a device or a switch is at a layer,
    chosen or matched. The header layers count (`diagramCounts` in
    `diagramText.js`, placeholders included) and **Select nodes on layer**
    show only while it is true.
  - A node's layer: `nodeLayer` (chosen), `matchedNodeLayer` (`{id,
    path}`), `effectiveNodeLayer` (chosen, else matched) and
    `nodeLayerState` (all three, for the Inspector). All code that places,
    counts or colors nodes by layer uses `effectiveNodeLayer`: the By layer
    layout (`layouts/tiers.js`, a higher ID nearer the top), `layerCounts`,
    `nodeHasLayer` (`selection.js`) and `layerOutlineColor`.
  - Edits: `setNodeLayer`, `setNodeLayers`, the pure list edits
    `withLayerAdded`, `withLayerRelabeled`, `withLayerUpdated`,
    `withLayerRenumbered` and `withLayerRemoved`, and their document forms
    `addLayer`, `relabelLayer`, `updateLayer`, `renumberLayer` (moves the
    nodes and templates on the layer) and `removeLayer` (nodes keep the
    ID, which then shows as "Layer N").
  - The layer set: `layerSetOptions`, `setLayerSetOptions`,
    `withLayerSetCopy` (nodes keep their chosen layers),
    `layerSetContentOf`, `sameLayerSetContent` and `layerSetLink`
    (`unlinked`, `missing`, `update`, `changed` or `linked`).

  Each list edit writes through `normalizedLayers`: highest `id` first,
  and empty (`layers` left out) when it equals the placeholders. No edit
  changes an included device. Each store action of the same name is one
  undo step. A new blank diagram copies the server's default layer set
  (`withDefaultLayerSet` in `layerSetStore.js`, called by `newDocument`).
  The browser keeps no layers of its own.

### Networks and addresses

- A network with no switch is valid. `network.switch.missing` is a JS
  editing warning only (`collectWarnings` in `validate.js`). Go defines
  the code and never reports it. `removeElements` in `model.js`, the one
  path that deletes nodes, also deletes each network whose last switch it
  deleted when `networkInUse` is false. In use means an edge `networkId`,
  or a device interface VLAN that names it (`networkByName`). Template
  VLANs do not count. Thus Undo restores both. Loading, Upload and Import
  never delete a network.
- A network can have `subnet` (IPv4 CIDR without host bits, such as
  `10.1.0.0/24`) and `gateway` (an IPv4 address, a host address of the
  subnet when the subnet is valid). Codes: `network.subnet.invalid`,
  `network.gateway.invalid` and `network.gateway.outside-subnet`. Go has
  `ParseSubnet` and `IsSubnetHost` (`validate.go`). `addressing.js` reads
  them the same way (`parseSubnet`, `isSubnetHost`). A /31 or /32 has no
  network or broadcast address to leave out.
- Other `addressing.js` helpers: `hostRange`, `nextFreeHost`,
  `subnetsOverlap`, `suggestSubnet`, `suggestedSubnet` (the first free
  `10.a.b.0/24`, for the new network of a new switch), `randomMac`,
  `documentMacs`, `needsAddress` (a `static` or `ospf` interface, not
  serial, with no address), `interfacesByNetwork`, `interfacesOnNetwork`,
  `hostAddress` (none for `dhcp` or `manual`), `isRoutingType` (`Router`
  or `Firewall`, any case), `overlappingSubnets` and
  `assignAddresses(doc, networkId, only)`. A connected interface is on its
  edge's network, else on the network that its VLAN names.
- `assignAddresses` never changes an existing address, or a device from
  an included topology. Router interfaces go first. The first takes the
  network gateway when it is a free host address (no interface has it in
  its address field). When the network has no gateway, it sets the gateway
  to its new address in the same document. Router interfaces never get a
  `gateway`, and other interfaces get the network's. `assigned` entries
  carry `holdsGateway` and `setGateway`, which the store announces
  (`announceGateway`). `freeAddressFor` (Fix) and `withNetworkGateway` (a
  host joined before its router through a new switch, `connectDevices` in
  `model.js`) apply the same rules.

### Design warnings

Design warnings flag settings that phenix accepts but that do not work as
the diagram shows. `codes.go` describes each code. `DesignWarnings` in
`types/builder/warnings.go` and `collectWarnings` in `validate.js` report
the same codes, paths and messages. The shared corpus
`types/builder/testdata/warning-corpus.json` (`TestWarningCorpus`,
`validate.contract.test.js`) holds them to each other. Its `codes` are the
warnings. Its `blockers` are the device settings that only Publish refuses
([publishing.md](publishing.md#what-only-publish-refuses)), which the Go
test reads from `PublishBlockers`. `projectTopology` adds the warnings to
those of Publish, the dry run and the Topology YAML download.

- `device.rulesets.ignored` at `device.spec.type` (field `spec.type`),
  also for an interface `ruleset_in` or `ruleset_out`. The vrouter app
  configures only `Router` and `Firewall` (`app/vrouter.go`).
- `interface.ruleset.unknown` at the interface `ruleset_in` or
  `ruleset_out`. The name match is exact. The vrouter app skips an unknown
  name, so the interface gets no firewall.
- `interface.ip.outside-subnet` at the interface `address`.
- `network.gateway.unowned` at `networks[i].gateway`. External and
  included devices count as holders of the gateway.
- `network.subnet.overlap` at `networks[i].subnet`: one warning for each
  network whose subnet holds or is in another, and it names one of them.
  CIDR blocks only nest, so a sort and a stack find them in n log n.
- Device settings that the phenix apps do not apply as drawn
  (`types/builder/devicewarnings.go`, and `deviceSetupFindings` and
  `ntpFindings` in `validate.js`): `device.router-os.unsupported`,
  `device.interfaces.unconfigured`, `device.serial.unconfigured`,
  `device.injections.unapplied`, the minirouter OSPF warnings
  `interface.ospf.outside-area`, `device.ospf-network.unused` and
  `device.ospf.unused`, the `ntp-server` label warnings
  `device.ntp-server.ignored` and `device.ntp-server.no-mgmt`, and the
  annotation warnings `device.enable-ssh.invalid`,
  `device.autotunnel.invalid` and `device.default-apps.invalid`.
  - Each one mirrors the app code that it names (`app/vrouter.go`,
    `startup.go`, `serial.go`, `ntp.go`, `app.go`,
    `tmpl/templates/minimega_script.tmpl` and `vyatta.tmpl`). It uses the
    same lowercase or `EqualFold` comparisons, and `net.ParseIP` and
    `net.ParseCIDR` for addresses (`parsedIPv4`, `isIP` and `ipv4Network`
    in JS).
  - A device whose `phenix/default-apps` is `false` still gets
    `device.injections.unapplied`, `device.autotunnel.invalid` and the
    `ntp-server` issues, because phenix reads those on every device. Only
    a Vyatta or VyOS router gets `device.enable-ssh.invalid`. An
    autotunnel `dport` can be `VNC`, in any case, with an empty host.
  - The path and field of a label or annotation issue end in its key
    (`spec.labels.ntp-server`). The Inspector shows it under the map
    (`mapField`).

Included devices get no design warnings. External devices get no ruleset
or device setting warnings, but the `ntp-server` checks read them, as the
phenix ntp app does. Each ruleset, address and device setting warning has a
`field`, so Go to opens it. `deviceFieldWarnings` shows the same warnings
on the Inspector's working copy.

### Templates

- A template can have `source` (`TemplateSource`): `kind` (`library`,
  `builtin` or `preloaded`), `id`, `version` (1 or more) and an optional
  `digest` of the copy as the editor last copied it. A library template
  never has one. Codes: `template.source.kind-unknown`,
  `template.source.id-invalid`, `template.source.version-invalid` and
  `template.source.digest-invalid`. The server checks only the form. The
  editor compares the version and the digest
  ([templates-and-icons.md](templates-and-icons.md#template-library)). A
  merge treats `source` as one value, labeled "library link"
  (`ATOMIC_TEMPLATE_KEYS` in `merge.js`).
- `TemplateDevice` must hold every `Device` field except `hostname`,
  `interfaces` and `includedFrom`. A reflection test checks it, so a new
  device field is a template field too.

## Generated schema

The repository keeps `builder.Schema()` in
`src/js/src/builder/schema/builder-v1.schema.json`
([Rules](../builder.md#rules) says when to regenerate it).

Every Builder-owned property has a `title`, a `description` and `examples`
(`documented()` in `schema.go`, values in `schema_examples.go`).
`TestSchemaDocumentsEveryProperty` checks it in Go, and
`schema-examples.contract.test.js` checks each example with ajv. The
template file and package schemas follow the same rule. `openapi.yml`
lists the root (`BuilderDocument`) and `metadata`
(`BuilderDocumentMetadata`) properties, and `TestBuilderDocumentDocumented`
holds them to the Go structs. The Inspector strips the schema titles
(`schema.js`), so its labels do not change.

## Document provenance

`metadata.createdBy`, `createdAt`, `updatedBy` and `updatedAt` are document
content and part of the digest. `api/builder` stamps them when it stores a
snapshot. The editor never sets them.

- `POST /builder/drafts` (`CreateDraft`): `createdBy` and `createdAt` come
  from the body when present, else the caller and now. `updatedBy` and
  `updatedAt` are always the caller and now. Thus an Upload keeps the
  file's `createdBy`.
- Unchanged copy: with `sourceToken` `builder-doc/<id>` or
  `builder-file/<topology>/<digest>` and no `forkOf`, the server stores a
  body whose canonical JSON is the opened document, with no stamp. The
  draft digest then equals the document digest, and an unchanged publish
  answers topology `skipped`. The client must send the document exactly as
  `GET` returned it.
- `POST …/snapshots` (`AppendSnapshot`) keeps the `createdBy` and
  `createdAt` of the draft record and stamps `updatedBy` and `updatedAt`.
  Every save is an edit, also one that changes nothing.
- Nothing else stamps: not undo, redo, restore, snapshot delete, publish
  or a file read.
- Create and save responses carry no document, so they carry `stamp`
  (empty fields left out). The editor copies it into its document
  (`withStamp` in `model.js`). Watchers that must not react to a stamp
  alone use `sameButStamp`.

`sourceFile` on `POST /builder/drafts` records the base name of the
uploaded file: at most 255 bytes, no `/` or `\`, not `.` or `..`, and no
control characters, else 422. Nothing opens it. A `forkOf` draft records
none.

## Other file formats

- Template files: [templates-and-icons.md](templates-and-icons.md#template-files).
- Layer set files: [templates-and-icons.md](templates-and-icons.md#layer-set-files).
- Builder packages: [sources.md](sources.md#downloads-and-packages).
- YAML input of all four formats goes through `builder.JSONFromYAML`
  (`types/builder/yaml.go`). It refuses anchors, aliases, merge keys, a
  second document, non-scalar or duplicate keys, other tags, `.inf` and
  `.nan`. It types scalars as the js-yaml `JSON_SCHEMA` does.
