# Builder sources and legacy import

Part of the [Builder references](../builder.md). What a document can be
generated from, Import and its options (included topologies, combine,
copy), uploaded configs, and the conversion of legacy Builder diagrams.

## Sources and generation

`GET /builder/sources` groups configs by kind: `topologies` and `experiments`
(what a document can be generated from, reported as `generatable: true`),
`scenarios` (what the Scenario dialog offers; no `digest`) and `images` (node property editing;
empty for the built-in Builder role, which has no `configs` on `Image/*`).
Each config is filtered through the `configs` permission *and* the kind specific
`list` permission that already gates the kind elsewhere (`topologies`,
`experiments`, `scenarios`); `Image` configs have no kind specific vocabulary,
so `configs` is their only gate. Generating from a non-generatable kind is
`422`. VLANs are derived from the document and are not a config kind.
Generating from an Experiment drops, without a warning, the injections its apps
added when it started: those whose `src` is an absolute path under the
experiment's base directory (its `baseDir`, or `<phenix base>/experiments/<name>`).
The topology's own injections are kept.

Generation copies the source config's `metadata.annotations` into the
document's `source.annotations`. It leaves out every `builder-` annotation
(`builder-xml`, `builder-doc`, `builder-experiment`) and keeps the others, such
as an experiment's `topology` and `scenario`. They are shown only: they do not
change `source.digest`, and publishing never writes them. A document holds at
most 100 annotations and 256 KiB of keys and values in all. Keys must not be
blank, must be at most 512 bytes long and must not contain control characters.
Generation keeps the annotations that fit, in key order, and warns about the
rest. `POST /builder/generate` also sets `source.importedAt` (RFC 3339, UTC).

### Import options: includes, combine and copy

`POST /builder/generate` accepts either `{"source":"Topology/name"}` (or an
Experiment source) or `{"content":"..."}` containing an uploaded JSON/YAML
Topology or Experiment, plus the import options `includes` (`""`/`"keep"`,
or `"combine"`), `copy` (bool) and `name` (the new topology name; only with
`copy` or `combine`, else 400). `combine` (`bdoc.WithCombinedIncludes()`,
then `Document.CombineIncludes` in `types/builder/detach.go`) makes every
readable included node the document's own; includes that could not be read
(missing, forbidden, a file path, past the 100th) stay in
`source.includeTopologies` with the warning `Included topology X was not
combined and stays in includeTopologies: publishing keeps the reference.`
`copy` or `combine` then run `Document.Detach(newName)`: `source.kind`
`manual`, no config named, document name = the new name (default
`<name>-copy` / `<name>-combined`; 1 to 512 bytes, config name pattern; not
the stored source's own name, else 422; the server does not look for a free
name, the dialog proposes one with `uniqueName`). Neither is allowed for an
Experiment (422 `only a topology can be combined or copied on import`). The
draft of a copy or combine gets source token `''` (stored source) and so
publishes a new topology and cannot update the source. `source.unresolvedIncludes`
(keep mode) lists included topologies whose nodes are not in the document;
informational, Publish ignores it. A config file needs `metadata.name`
(422 otherwise). `GET /builder/sources` topology rows gain `includeCount`
(left out when 0; only for a topology the caller may `get`), which the
Import dialog uses to offer "Included topologies" (`dialogs/importOptions.js`,
`uploadedConfigInfo` for a file). A stored topology that has `builder-xml`
and no `builder-doc` is converted from its legacy diagram by
`(*builderAPI).generate` (see [Legacy import](#legacy-import)). The editor
command "Combine included nodes into a new draft" (and the Inspector button
"Combine into a new draft" under an included device's lock note) does the
same as combine on the open draft, client side (`combineIncluded` in
`model.js` and `store.js`), into a new draft with a name no topology and no
draft of the user's has. Uploaded sources are reported as `stored: false` and
receive uploaded provenance, so they can create publication targets but cannot
authorize a Topology or Experiment update. Uploaded `content` needs `configs`
`create` (a stored `source` needs only read permissions): uploads are parsed
like `POST /configs`, including `${NAME}` / `${NAME:default}` substitution from
the server's environment, so anyone allowed to create configs can read the
server's environment variables (sandialabs/sceptre-phenix#436 describes this).

### Included topologies

Generation resolves a Topology's `includeTopologies` recursively, the way
phenix merges them, and adds the included nodes as devices marked
`device.includedFrom: <defining topology>`; for an Experiment, whose topology
phenix already merged, the included nodes are recognized and marked instead
(a hostname the experiment's own topology defines is never marked). When an
include cannot be read now, a node that neither the experiment's own topology
nor a readable include defines is marked as coming from it (from the first,
when several cannot be read).
Includes are read from the config store only (never file paths), under the
caller's `configs` `get` and `topologies` `list` permissions. (The one server
file Builder reads is a Builder file named by `builder-doc.path`; see
[Builder files](published-documents.md#builder-files).) A missing,
forbidden, cyclic, or repeated include, or a hostname that collides with
another topology's, is reported as a warning. Included devices are read only in
the editor (they can be moved, not changed, deleted, or reconnected), and
publishing omits them and writes `includeTopologies` instead, so a round trip
does not duplicate them. The exception is an Experiment whose own topology
and one of whose includes both cannot be read: that include's nodes cannot be
told from the topology's own, so they are imported as its own (with a warning)
and a topology publish copies them in, where phenix finds them twice. Publish
answers 409 when an included topology now defines a hostname the published
topology also defines, and an experiment update, which merges the includes
itself, answers 403 or 422 for an include the caller may not read or that is
not a stored topology.

## Legacy import

The converter is pure Go in `src/go/types/builder/` (`legacy_xml.go` reads,
`legacy.go` converts): `DecodeLegacy(bytes)` → `*LegacyDiagram` or a
`*LegacyError{Reason, Message}` (reasons `not-diagram`, `malformed`,
`doctype`, `root`, `too-large`, `too-deep`, `too-many`); `FromLegacy(diagram,
name)` for a bare diagram; `FromLegacyTopology(config, options...)` for a
Topology with `builder-xml` (same options as `FromConfig`, so combine works);
`HasLegacyDiagram(config)`, `LegacyXMLAnnotation`. Both run `FromConfig`
first and lay the XML over it.

- Input: plain mxGraph XML (root `mxGraphModel` or `root`, UTF-8, no DOCTYPE
  or entity declaration, at most 5 MiB (`MaxLegacyBytes`), 32 levels, 10000
  cells), or a Topology config (YAML or JSON) whose `builder-xml` holds such
  XML. No base64, no compressed or `<mxfile>` draw.io form, no pasted text.
- Bare diagram: node settings come from each cell's `schemaVars`; `$NAME`
  placeholders are resolved from the diagram's `experimentVars` and the four
  legacy defaults (`DEFAULT_MEMORY` 2048, `DEFAULT_VCPU` 1,
  `DEFAULT_VM_IMAGE` `ubuntu.qc2`, `DEFAULT_ROUTER_IMAGE` `vyos.qc2`), never
  from the environment; values stay strings. `source.kind` is `manual`.
- Topology: its spec is the truth; the XML gives positions, VLAN ids (as
  VLAN aliases), notes (text cells) and groups (containers), matched by
  hostname ignoring case. Diagram-only nodes are left out, topology-only
  nodes placed below.
- Both: positions ×2 and snapped to 16; a switch is added for a VLAN drawn
  without one; a switch is named after its network; icon variants map to
  `router`, `firewall`, `desktop`, `server`, `external`; styles, layers,
  edge labels and the rest are dropped. The warnings are a closed list,
  built in `legacyNotes.sentences` (plus the unreadable-diagram warning in
  `FromLegacyTopology`); names are sorted, 8 shown, then "N more".
- `POST /builder/legacy` (`web/builder_legacy.go`): `{content, name?}`
  (strict; `name` names a bare diagram, default `legacy-diagram`), needs
  `configs` `get` and `create` (a Topology file is parsed as `POST /configs`
  parses one, `${NAME}` from the environment included). Answers `{document,
  warnings, source?}` (no `source` for a bare diagram), 200, no ETag;
  400/403/413/422 with sentences that repeat nothing of the content but a
  config's name and kind or the XML root name. Nothing is written.
- `POST /builder/generate` of a stored (or uploaded) Topology with
  `builder-xml` and no `builder-doc` converts through `(*builderAPI).generate`
  (`web/builder_sources.go`); `source.builder` is then `builder-xml`, and
  `source.digest` (`bdoc.ImportDigest`) covers the annotation, as does a
  sources row's `digest`.
- Draft tokens: Import of the stored topology `Topology/<name>`; Upload of a
  Topology file `uploaded/Topology/<name>`; Upload of a bare diagram
  `uploaded/legacy-xml` (`LEGACY_TOKEN`). Only the first can update the
  topology.
- Publish of that draft to the topology (web, and `phenix builder publish
  --update`) calls `bapi.ReplaceLegacyDiagram`: deletes `builder-xml`, keeps
  the other annotations, and warns `The legacy Builder diagram of topology
  <name> was replaced by this diagram.` (or `… could not be read and was
  removed.` when `DecodeLegacy` refuses it). While a topology has
  `builder-xml`, `topologyUpdateMatchesSource` accepts only a draft whose
  document source is that topology (not one imported from an experiment);
  the client mirrors it (`updateBlocker`, `legacyDiagramUpdate` and
  `targetHint` in `publish.js`). A changed topology or diagram is the usual
  409 `changed after this draft was imported`.
- UI: Upload's fourth source "Legacy Builder diagram or Topology"
  (`LEGACY_SOURCE` in `dialogs/message.js`), a file field only
  (`upload-legacy-file`), submit "Convert"; `store.convertLegacy({content,
  name})` checks the document and the dialog loads it on Continue, so Cancel
  keeps the open draft. XML given to File or Paste text gets the hint
  `legacyDiagramHint`. The Import list labels a legacy topology "NAME (legacy
  Builder diagram)" with the hint `import-legacy-hint`. Warnings show in the
  shared `dialogs/ImportWarnings.vue`. The palette finds Upload and Import
  by `legacy`.
- No CLI, no bulk conversion, nothing at server start.
