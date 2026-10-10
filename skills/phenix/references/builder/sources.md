# Import, upload and download

Part of the [Builder references](../builder.md). How a document is made
from configs, the conversion of legacy diagrams, Builder packages, and the
Topology YAML and GEXF downloads.

User docs: [Import, Upload and Download](https://phenix.sceptre.dev/latest/builder/import-upload-download/)
and [Legacy Builder](https://phenix.sceptre.dev/latest/builder/legacy/).

## Generation

- `GET /builder/sources` groups configs by kind: `topologies` and
  `experiments` (`generatable: true`), `scenarios` and `images`. Each
  config passes the `configs` permission and the kind's own `list`
  permission (`topologies`, `experiments`, `scenarios`). `Image` has no
  kind permission. Topology rows have `includeCount` (left out when 0).
- `POST /builder/generate` (`web/builder_sources.go`, `types/builder/generate.go`)
  takes `{"source": "Topology/<name>"}` (or an Experiment) or
  `{"content": "..."}` (a config file), and the import options `includes`
  (`""`/`"keep"` or `"combine"`), `copy` and `name`. It only checks the
  document. It writes nothing.
- Uploaded `content` needs `configs` `create`. The server parses it as
  `POST /configs` does, with `${NAME}` substitution from the server's
  environment, so anyone who may create configs can read the server's
  environment variables (sandialabs/sceptre-phenix#436).
- Uploaded sources are `stored: false`. Their drafts can publish new
  configs but never update a stored one.
- Generation copies the source's annotations into `source.annotations`,
  except every `builder-` annotation. They are for display only: they do
  not change `source.digest`, and Publish never writes them.
- From an Experiment, generation drops the injections its apps added (an
  absolute `src` below the experiment's base directory).

### Included topologies

Generation resolves `includeTopologies` recursively, as phenix merges them,
and marks the included nodes `device.includedFrom`. It reads includes from
the config store only, never file paths, under the caller's `configs` `get`
and `topologies` `list`. Publish writes `includeTopologies` and leaves the
included nodes out, so a round trip does not copy them.

### Copy and combine

`copy` or `combine` run `Document.Detach(newName)` (`types/builder/detach.go`):
`source.kind` `manual`, no config named, so the draft publishes a new
topology. `combine` first makes every readable included node the
document's own (`Document.CombineIncludes`). Includes it cannot read stay
in `source.includeTopologies` with a warning. The server then adds the
diagram note `Copied from <name>`, with the name in the source's
`metadata` (`NoteCopiedFrom`, `CopiedFromPrefix`). It adds no note when the
name is blank, when the notes already hold it or are full, or when the note
breaks a note rule. Neither option works for an
Experiment (422). The new name is refused with `import.name.invalid` or
`import.name.source`. The editor's own combine is client side
(`combineIncluded` in `model.js`). User docs:
[Import options](https://phenix.sceptre.dev/latest/builder/import-upload-download/#import-options).

## Legacy conversion

The converter is pure Go in `types/builder/`: `legacy_xml.go` reads and
`legacy.go` converts.

- `DecodeLegacy(bytes)` returns `*LegacyDiagram` or `*LegacyError{Reason,
  Message}`. `FromLegacy(diagram, name)` converts a bare diagram, and
  `FromLegacyTopology(config, options...)` a Topology with `builder-xml`
  (same options as `FromConfig`). Both run `FromConfig` first and lay the
  XML over it.
- Input limits: no DOCTYPE or entities, at most 5 MiB (`MaxLegacyBytes`),
  32 levels and 10000 cells.
- The warnings are a closed list in `legacyNotes.sentences`.
- `POST /builder/legacy` (`web/builder_legacy.go`) converts a file and
  writes nothing. `POST /builder/generate` of a Topology with `builder-xml`
  and no `builder-doc` converts it too, and `source.digest`
  (`bdoc.ImportDigest`) covers the annotation.
- Only a draft imported from the stored topology (token `Topology/<name>`)
  can update it. Publish then calls `bapi.ReplaceLegacyDiagram`, which
  deletes `builder-xml` and keeps the other annotations. The client
  mirrors the rule (`updateBlocker`, `legacyDiagramUpdate` in
  `publish.js`).
- Tests: golden files in `types/builder/testdata/legacy/` (rewrite with
  `-update-legacy-golden`) and a fuzz test. From `src/go`, run
  `go test -fuzz=FuzzDecodeLegacy -fuzztime=30s ./types/builder/`.

User docs: [Legacy Builder](https://phenix.sceptre.dev/latest/builder/legacy/)
lists what is converted and the warnings.

## Downloads and packages

- Topology YAML: `POST /builder/export/topology` (`web/builder_export.go`)
  answers the Topology config Publish would write, with `warnings` and
  `publishBlockers`. It refuses nothing that only Publish refuses. It
  names it instead. User docs:
  [Topology YAML](https://phenix.sceptre.dev/latest/builder/import-upload-download/#topology-yaml).
- GEXF is made in the browser (`gexf.js`). The file names gexf.xsd in
  `xsi:schemaLocation`, as Gephi writes it, and the official gexf.rng
  rejects that attribute. To check a file with xmllint, use a grammar that
  includes gexf.rng and allows `xsi:schemaLocation` on `<gexf>` (its
  `gexf-content`).
- A package (`bdoc.Package`, `types/builder/package.go`, `package.js`) is
  one JSON or YAML file: the document, the Scenario and Topology configs it
  carries, and `requirements`. A carried config has no times and no
  `builder-*` annotations, so no config created from a package claims a
  document. `decodePackage` in `package.js` makes the same checks with the
  same words as Go. The docs example
  `docs/content/builder/examples/pump-station.package.yaml` must load
  (`TestDocsPackageExampleLoads`).
- `POST /builder/package` (`web/builder_package.go`) builds a package and
  writes nothing. It runs `TrimRequirements`, then `Validate`, so it never
  answers a package the resolve route refuses.
- `POST /builder/package/resolve` says, for each requirement, `present`,
  `missing`, `different` or `unknown`. A config, image or app the caller
  may not see is `missing` or `unknown`, never disclosed. Tests inject
  `withBuilderDisks` and `withBuilderApps`.
- Images: package resolve, the publish dry run and the preflight disks
  check share one lister, `builderAPI.disks` (`web/builder_disks.go`), which
  reuses a listing for 10 s. `disk.GetImages` gives an empty listing,
  without an error, when minimega cannot be reached. So each caller treats
  an empty listing as no answer (`unknown`, `unavailable` or a null
  `onServer`).

User docs: [Moving a diagram with a Builder package](https://phenix.sceptre.dev/latest/builder/import-upload-download/#moving-a-diagram-with-a-builder-package).
