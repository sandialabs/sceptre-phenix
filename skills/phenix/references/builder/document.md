# Builder document format

Part of the [Builder references](../builder.md). The document's root keys
and `metadata`, diagram notes, the generated schema and its documentation
rule, and the provenance fields the server stamps.

## Document metadata

A Builder document's root keys are, in this order, `$schema`, `revision`,
`metadata`, then the content: `nodes`, `networks`, `edges`, `viewport`,
`grid`, and the optional `scenarios`, `source`, `layout`, `iconSize`,
`templates` and `icons`. `metadata` (`bdoc.Metadata`) is required and holds only `id`
(required, the document ID), `name`, `description`, the four provenance
fields below, and `notes`. Strict decoding refuses any other key there, and
refuses the metadata keys at the root (there is no shim for the old root
fields `id`, `name`, `description`, `author`, ...). A missing or null
`metadata` is reported as `metadata.id` "document ID is required"; issue
paths name the new place (`metadata.createdBy`, `metadata.notes[3]`). Go
(`validateMetadata` in `validate.go`) and JS (`validate.js`, `decode.js`)
check it alike through the shared corpus.

`metadata.notes` are free text about the diagram as a whole: at most 100
(`MaxDiagramNotes`, `MAX_DIAGRAM_NOTES`), each not blank after trimming, at
most 4096 bytes (`MaxDiagramNoteBytes`) and free of control characters but
newline and tab. They are document content (in the digest) and never
written to a config. The Inspector's Diagram view lists them after Scenarios
under Notes (`InspectorDiagram.vue`, testids `inspector-notes`,
`inspector-note-N`, `inspector-note-add`): a textarea "Note N" and a
"Delete note N" button each, "No notes." when there are none, and Add note,
disabled at 100 with a hint. A note is written when its textarea fires
`change`, through `store.setDiagramNotes(notes)` (model `setDiagramNotes`,
one undo step "Updated diagram notes"); blank rows are dropped. Text over
4096 UTF-8 bytes or with another control character is never written:
`diagramNoteProblem` (`model.js`) names why, the textarea gets
`aria-invalid` and an error under it (`role="alert"`, testid
`inspector-note-error-N`, named by `aria-describedby`), and the diagram
keeps that note as last saved until the text is fixed; model
`setDiagramNotes` returns the document unchanged for a list holding such a
note. "Blank" means only Go's `unicode.IsSpace` white space everywhere
(`isBlank` in `text.js`, `trimSpace` in `validate.js`, and `notePattern` in
`schema.go`, which spells the set out as `\x00-\x20\x7f\x85\p{Z}` since `\s`
differs between Go and ECMAScript): U+0085 is blank, U+FEFF is not. A
read-only draft shows the notes as text.

The generated schema (`builder.Schema()`, committed as
`src/js/src/builder/schema/builder-v1.schema.json`) gives every
Builder-owned property a `title`, a `description` and `examples`
(`documented()` in `schema.go`, values in `schema_examples.go`; the bundled
phenix `$defs` are left alone). `TestSchemaDocumentsEveryProperty` walks it in Go and
`schema-examples.test.js` checks each example against its subschema with
ajv. The Inspector does not show these titles: `schema.js` strips them from
the fields it builds, so labels stay as they were. `openapi.yml` lists the
root (`BuilderDocument`) and metadata (`BuilderDocumentMetadata`)
properties, which `TestBuilderDocumentDocumented` in `web/builder_test.go`
holds to the Go structs.

## Document provenance

A document's `metadata` has four optional string fields after
`description`: `createdBy`, `createdAt`, `updatedBy`, `updatedAt`. Users are
at most 256 bytes with no control characters; times are exactly
`YYYY-MM-DDTHH:MM:SSZ` (UTC, whole seconds); an empty string or null is
none; no pairing or ordering rule. Revision stays 1. They are document
content and part of its digest. `source.updatedAt` is a different field
(the imported config's time).

The server stamps them in `api/builder` when it stores a draft snapshot;
the editor never sets them:

- `POST /builder/drafts` (`CreateDraft`): `createdBy` and `createdAt` are
  the body's when present, else the caller and now; `updatedBy` and
  `updatedAt` are always the caller and now. The draft record keeps the two
  as `documentCreatedBy` and `documentCreatedAt` (a record with the old key
  `documentAuthor` is refused as corrupt). So an Upload keeps the file's
  `createdBy`, and anyone with `configs` `create` can name anyone. A body
  cannot set `updatedBy` or `updatedAt`, except through the unchanged copy
  below.
- Unchanged copy: with `sourceToken` `builder-doc/<id>` or
  `builder-file/<topology>/<digest>` and no `forkOf`, a body whose canonical
  JSON is the opened document is stored unstamped, so the draft's `digest`
  equals the document's and an unchanged publish answers topology
  `skipped`. A client must send the document exactly as `GET` returned it.
  For a `builder-file/` token all four fields are then whatever the file
  says, also in what the draft publishes before its first save: they are
  only as trustworthy as whoever can write the file.
- `POST .../snapshots` (`AppendSnapshot`): `createdBy` and `createdAt` come from
  the draft record (left out when it has none); `updatedBy` and `updatedAt`
  are the caller and now, equal to the snapshot's `createdBy` and its
  `createdAt` cut to seconds. Every save is an edit, also one that changes
  nothing.
- Nothing else writes a document: cursor moves (undo, redo, restore),
  snapshot delete, shares, publish and a file read never stamp. A
  published document holds the fields of the snapshot it was published from.
- A value that is not valid in any of the four fields answers 422 on create
  and save.

Create and save responses have no document, so they carry `stamp:
{createdBy?, createdAt?, updatedBy?, updatedAt?}` (empty fields left out, `{}`
for an unchanged copy of a document that names nobody); the editor copies
it into its document (`withStamp` in `model.js`). No other response has
`stamp`. Copying it replaces `store.doc`, so the Inspector's watch on the
document skips a change of the stamp alone (`sameButStamp`): a reset there
drops text being typed in a field (`builder-inspector.spec.js` checks it).
Any other change (a layout landing from ELK's worker, an edit on the canvas)
reloads the form only while `keepsWorkingCopy` (`adapters/forms.js`) is
false: no unapplied edits, no text typed and not committed (`typing`), no
focus on Apply or Cancel (`held`), no look held, no change JSON Forms has
not sent, no rows a renderer holds back. Otherwise `rebasedWorkingCopy`
moves the working copy onto the element as it is now (`mergeFormData`), so
a field changed elsewhere shows and Apply keeps it. Apply, and a save of
unapplied edits (`settle`, `saveUnapplied`), reload the form once the store
takes their commit, as every field has committed its text by then: a field
shows the value the document took, such as a VLAN typed as `exp` that
names network `EXP` (`builder-publish.spec.js` checks it).

`sourceFile` on `POST /builder/drafts` records the name of the uploaded
file a draft came from (the UI sends it for Upload of a file and Import of
a config file): a base name of at most 255 bytes, no `/` or `\`, not
`.` or `..`, no control characters, else 422. It is returned on every draft
response when set, is never used to open anything, and a `forkOf` draft
records none.
