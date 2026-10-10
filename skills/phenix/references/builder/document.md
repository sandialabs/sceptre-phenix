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
  `source`, `layout`, `iconSize`, `templates` and `icons`. The revision
  stays 1: new fields are optional.
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
- Presentation fields never reach a config or Topology YAML
  (`TestToTopologyOmitsPresentationFields`,
  `TestToTopologyOmitsVisualNodes`): colors, `lineStyle`, `borderStyle`,
  `icon`, `iconKey`, `iconSize`, switch `notes`, `metadata.notes`, groups,
  the drawing node kinds `shape`, `icon` and `line` (also not GEXF),
  `layout`, edge `route`, and `purdueLevel`. A device's notes are its
  spec's `general.notes`, which Publish writes.
- `purdueLevel` on a device, a switch or a template's device is one of
  `PurdueLevels()` (`"5"`, `"4"`, `"3.5"`, `"3"`, `"2"`, `"1"`, `"0"`).
  Another value is `device.purdue-level.unknown`,
  `switch.purdue-level.unknown` or `template.purdue-level.unknown`. The
  "Layered by tier" layout (`layouts/tiers.js`) reads it. User docs:
  [Purdue layers](https://phenix.sceptre.dev/latest/builder/diagrams/#purdue-layers).
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
- Nothing else stamps: not undo, redo, restore, snapshot delete, shares,
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
- Builder packages: [sources.md](sources.md#downloads-and-packages).
- YAML input of all three formats goes through `builder.JSONFromYAML`
  (`types/builder/yaml.go`). It refuses anchors, aliases, merge keys, a
  second document, non-scalar or duplicate keys, other tags, `.inf` and
  `.nan`, and types scalars as js-yaml's `JSON_SCHEMA` does.
