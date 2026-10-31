# Import, upload and download

Part of the [Builder references](../builder.md). User docs:
[Import, Upload and Download](https://phenix.sceptre.dev/latest/builder/import-upload-download/)
and [Legacy Builder](https://phenix.sceptre.dev/latest/builder/legacy/) (what converts, and the warnings).

## Generation

- `GET /builder/sources` groups configs by kind: `topologies` and
  `experiments` (`generatable: true`), `scenarios` and `images`. Each
  config needs the `configs` permission and the `list` permission of its
  kind (`topologies`, `experiments`, `scenarios`). `Image` has no kind
  permission. Topology rows have `includeCount` (absent when 0).
- `POST /builder/generate` (`web/builder_sources.go`,
  `types/builder/generate.go`) takes `{"source": "Topology/<name>"}` (or an
  Experiment) or `{"content": "..."}` (a config file), with the import
  options `includes` (`""`/`"keep"` or `"combine"`), `copy` and `name`. It
  checks the document and writes nothing.
- Uploaded `content` needs `configs` `create`, as `POST /configs` does.
  `builderUploadedSource` parses it with `store.DecodeConfigJSON` and
  `store.DecodeConfigYAML`, which keep each `${NAME}` as written. Never use
  `store.NewConfigFromYAML` on client text in a Builder route: it expands
  `${NAME}` from the server's environment and sends it back
  (`TestBuilderUploadsKeepPlaceholders`).
- Uploaded sources are `stored: false`. Their drafts can publish new
  configs but never update a stored one.
- Generation copies the source's annotations, except each `builder-`
  annotation, into `source.annotations` for display only. They do not
  change `source.digest`, and Publish never writes them.
- From an Experiment, generation drops the injections its apps added (an
  absolute `src` below the experiment's base directory).
- From an Experiment, `dropExperimentResidue` (`types/builder/residue.go`)
  drops what phenix stored at create time (`hookCreate` runs
  `ExperimentSpec.Init`, then `structs.MapDefaultCase` writes every field):
  - `bridge_set_in_topo`, and a bridge equal to `defaultBridge` (or
    `phenix`) when that flag is false.
  - Each field phenix reads as unset, by the `v1.Node` field types: zero
    non-pointer scalars, nulls, empty lists and maps. A pointer field keeps
    an explicit 0 or false.
  - Each Init default. `nodeShape` runs Init on a blank `v1.Node` to get
    the defaults, so a new phenix default needs no list.
  - `structShape.required` keeps the fields that the v1 schema requires
    where the dropped value is valid (`os_type`, `interfaces`, interface
    `vlan`, `mask` with an address, serial `udp_port`, `areas`,
    `area_networks`, `rules`, rule `id`). Extend it when the schema gains
    one.
  - Topology imports stay verbatim. `TestFromExperimentDropsInitResidue`
    (`residue_test.go`) runs Init on a topology and checks that import and
    publish give it back, schema valid.
- Init records the VLAN alias `""` for interfaces without a VLAN.
  Generation skips it without a warning.
- Device nodes keep source order (`groupIncludedDevices` in `finish`): the
  topology's own nodes in spec order, then the nodes of each included
  topology in merge order. ToTopology writes document order, and
  `minimega_script.tmpl` starts VMs in that order, so never sort them.
  `TestTopologyRoundTrip` checks the order. A change to it rewrites the
  legacy golden files (`-update-legacy-golden`).

### Included topologies

Generation resolves `includeTopologies` recursively, as phenix merges them,
and marks the included nodes `device.includedFrom`. It reads includes only
from the config store, never from file paths, under the caller's `configs`
`get` and `topologies` `list`. Publish writes `includeTopologies` and
omits the included nodes, so a round trip does not copy them.

An Experiment import marks included devices only when it can tell them
apart (`generator.markUnreadable`). It cannot when it has no topology
loader, or when an included topology and the experiment's source topology
are both unreadable. A publish then writes those devices as the topology's
own, and phenix duplicates them when it merges the include again. The
import warns in both cases.

### Copy and combine

`copy` and `combine` run `Document.Detach(newName)`
(`types/builder/detach.go`): `source.kind` `manual`, no config named, so
the draft publishes a new topology. `combine` first makes each readable
included node the document's own (`Document.CombineIncludes`). Unreadable
includes stay in `source.includeTopologies` with a warning. The server then
adds the diagram note `Copied from <name>`, with the name in the source's
`metadata` (`NoteCopiedFrom`, `CopiedFromPrefix`). It adds no note when the
name is blank, when the notes already hold it or are full, or when the note
breaks a note rule. Neither option works for an Experiment (422). A bad new
name gives `import.name.invalid` or `import.name.source`. The editor's own
combine is client side (`combineIncluded` in `model.js`). User docs:
[Import options](https://phenix.sceptre.dev/latest/builder/import-upload-download/#import-options).

## Legacy conversion

The converter is pure Go in `types/builder/`: `legacy_xml.go` reads and
`legacy.go` converts.

- `DecodeLegacy(bytes)` returns `*LegacyDiagram` or `*LegacyError{Reason,
  Message}`. `FromLegacy(diagram, name)` converts a bare diagram, and
  `FromLegacyTopology(config, options...)` a Topology with `builder-xml`
  (options as for `FromConfig`). Both run `FromConfig` first and lay the
  XML over it.
- Input limits: no DOCTYPE or entities, at most 5 MiB (`MaxLegacyBytes`),
  32 levels and 10000 cells.
- The warnings are a closed list in `legacyNotes.sentences`.
- `POST /builder/legacy` (`web/builder_legacy.go`) converts a file and
  writes nothing. `POST /builder/generate` converts a Topology with
  `builder-xml` and no `builder-doc` too, and `source.digest`
  (`bdoc.ImportDigest`) covers the annotation.
- Only a draft imported from the stored topology (token `Topology/<name>`)
  can update it. Publish then calls `bapi.ReplaceLegacyDiagram`, which
  deletes `builder-xml` and keeps the other annotations. The client mirrors
  the rule (`updateBlocker`, `legacyDiagramUpdate` in `publish.js`).
- Tests: golden files in `types/builder/testdata/legacy/` (rewrite with
  `-update-legacy-golden`) and a fuzz test. From `src/go`, run
  `go test -fuzz=FuzzDecodeLegacy -fuzztime=30s ./types/builder/`.

## Downloads and packages

- Topology YAML: `POST /builder/export/topology` (`web/builder_export.go`)
  answers the Topology config that Publish would write, with `warnings` and
  `publishBlockers`. It names, and does not refuse, what only Publish
  refuses. User docs:
  [Topology YAML](https://phenix.sceptre.dev/latest/builder/import-upload-download/#topology-yaml).
- GEXF is made in the browser (`gexf.js`). The file names gexf.xsd in
  `xsi:schemaLocation`, as Gephi writes it, and the official gexf.rng
  rejects that attribute. To check a file with xmllint, use a grammar that
  includes gexf.rng and allows `xsi:schemaLocation` on `<gexf>` (its
  `gexf-content`).
  - It writes the 1.3 namespaces and schema location, as the 1.3 primer
    and Gephi write them. It writes `meta` (last change day, creator,
    keywords, description) and typed columns (string, integer, long,
    double, boolean, liststring), with a default where it holds for every
    node or edge. It writes `viz` (position with y up, rgba color, one
    size, shapes that Gephi ignores). It writes `kind` on the edges of a
    device with more than one interface on one network, because GEXF
    readers merge those edges without it.
  - It omits `pid` and nested nodes (Gephi 0.11 reports `pid` as a
    deprecated hierarchy, and graphology drops it). It omits anyURI columns
    and `<options>` (Gephi drops or misreads them), and hex colors and
    other list or numeric types (networkx cannot read them). It omits float
    (double holds every number), dynamic mode (a diagram has no time), the
    per-host app metadata of a scenario (it configures the app and can be
    large), and directed edges, weight and thickness.
  - Gephi Lite reads list columns as text. So the repeating lists (VLANs,
    interfaces, labels, apps) are also one `a|b` text value.
- draw.io is made in the browser (`toDrawio` in `drawio.js`), one way: no
  code reads it back, and legacy conversion refuses its `<mxfile>` root.
  GEXF and draw.io write XML through `xml.js`. Rules:
  - Colors come only from `LIGHT_COLORS`, the light block of `builder.css`
    (`drawio.test.js` holds them to it), in either theme.
  - Cell ids are the Builder ids after `0` and `1`. `idMaker` renames an
    empty, repeated, `0` or `1` id. Groups are containers, parents first. A
    group loop puts its groups on the layer.
  - A style value never holds `;`, so an image is `data:<type>,<base64>`
    without `;base64`. Built-in icons are `builtinIconSvg` of `iconPath`
    (`iconPaths.js`) in one color.
  - A device carries only the `deviceData` allow-list (hostname, type,
    os_type, first image, vcpus, memory, interfaces). Never add the spec,
    injections, metadata, labels or annotations.
  - Labels are HTML (`html=1`), escaped as HTML and again as XML.
  - `drawio-file.contract.test.js` checks ids, containers, ends, finite
    geometry, positions, images and labels with fast-check. It checks the
    docs lab against `test/builder/__snapshots__/riverside-water.drawio`.
    After an intended change, run it with `-u` and review the diff.
  User docs:
  [draw.io](https://phenix.sceptre.dev/latest/builder/import-upload-download/#drawio).
- A package (`bdoc.Package`, `types/builder/package.go`, `package.js`) is
  one JSON or YAML file: the document, the Scenario and Topology configs it
  carries, and `requirements`. A carried config has no times and no
  `builder-*` annotations, so no config created from a package claims a
  document. `decodePackage` (`package.js`) makes the same checks with the
  same words as Go. The docs example
  `docs/content/builder/examples/pump-station.package.yaml` must load
  (`TestDocsPackageExampleLoads`).
- `POST /builder/package` (`web/builder_package.go`) builds a package and
  writes nothing. It runs `TrimRequirements`, then `Validate`, so it never
  answers a package that the resolve route refuses.
- `POST /builder/package/resolve` gives each requirement `present`,
  `missing`, `different` or `unknown`. A config, image or app that the
  caller may not see is `missing` or `unknown`, never disclosed. Tests
  inject `withBuilderDisks` and `withBuilderApps`.
- Images: package resolve, the publish dry run and the preflight disks
  check share one lister, `builderAPI.disks` (`web/builder_disks.go`). It
  reuses a listing for 10 s. `disk.GetImages` gives an empty listing,
  without an error, when minimega is out of reach. So each caller treats an
  empty listing as no answer (`unknown`, `unavailable` or a null
  `onServer`). `images(ctx)` runs the listing in its own goroutine, because
  mmcli cannot be stopped. It waits at most `builderDiskListWait` (10 s) or
  until the request context ends, then returns `errBuilderDiskListWait`,
  which each caller treats as no answer. A request never starts a second
  listing while one runs, and a late listing is cached as usual. Tests set
  `api.disks.wait` (`withBuilderDiskWait`) and hold the source
  (`newBuilderHungDisks`).

User docs: [Moving a diagram with a Builder package](https://phenix.sceptre.dev/latest/builder/import-upload-download/#moving-a-diagram-with-a-builder-package).
