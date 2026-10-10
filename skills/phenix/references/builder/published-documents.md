# Published documents and Builder files

Part of the [Builder references](../builder.md). What happens to published
documents when a topology is published again, deleted or renamed, the
documents listing and delete route, the `builder-doc` annotation that names
a topology's document, and Builder files named by `builder-doc.path`.

## Published documents

### Cleanup after a publication or a topology delete

After a publication, older published documents of the same topology are
removed once they are more than an hour old; newer ones go at a later publish
or at the startup cleanup.

Deleting a Topology config, however it is deleted (`DELETE /configs`, the
Configs page, `phenix config delete`, `all` included, or the Builder route
below), removes its published documents too: a Topology config hook, which
`api/builder` registers in every phenix
process, removes those stored before the config was last written and those
more than an hour old. A younger document stored since then may belong to a
publish in flight, which stores its document before it writes the config, so
it is kept; a later publish of the topology or the startup cleanup removes it
once it is more than an hour old. A failure is logged and never fails the
delete.

The config's `metadata.updated` is kept to the second, so the documents stored
before that write are: those stored before that second, the document the
config names in `builder-doc` (unless it was stored again in a later second:
the same content is being published again), and those stored no later than
that document. A document stored in that second after the named one is kept.
The same content published again within that second cannot be told from the
config's own publish and is removed (publishing once more stores it again).
`DELETE /configs` of a topology, and a `PUT /configs` that renames one, hold
the publish lock, so no publish of that phenix process is in flight while the
hook runs; this only concerns another process sharing the store.

### Renaming a topology

Renaming a topology (an update that changes `metadata.name`: `PUT /configs`,
the Configs page, `phenix config edit`) stores it under the new name and
deletes it under the old one. The config hooks of its kind then run a `rename`
stage with the config as it was, where the Topology hook removes the old
name's documents as for a delete. The renamed topology's `builder-doc` loses
its `id`, and its `digest` too unless it has a `path` (see
[The builder-doc reference](#the-builder-doc-reference)), which a copy
stored under a new name gets too. Without a `path` it is a topology like any
other until it is published again. The hooks never read or remove a Builder
file.

### Listing published documents

`GET /builder/documents` lists only current documents: it lists each kind
of config once per request and matches the documents against their references
(`DocumentReference.Names`), rather than reading one config per document.
Stored rows carry `source: "store"`. A topology the caller may list whose
reference has a `path` and no current stored document gets a file row,
exactly `{source: "file", target, kind: "Topology", config, path}`: no `id`,
`digest`, `size`, `createdAt` or `createdBy`. The listing reads no file, so a
file row is listed even when the file is missing or invalid.

### Deleting a published topology

`DELETE /builder/documents/{document}` deletes a published topology: the
Topology config the document is current for, through the same config call and
broadcast as `DELETE /configs`, then every published document of that
topology, however recent: holding the publish lock, no publish of it is in
flight in this process. The route removes the documents itself, to report a
failure: `LeaveTopologyDocuments` has the config hook leave them to it. It
needs `configs` `delete` for the topology. A document the caller may not get,
or one that is no longer current (the topology was published again, points at
another document, or was deleted), gets 404, as `GET` answers. A published
experiment gets 422 `Only published topologies can be deleted here. Delete
experiments from the Experiments page.` If the documents cannot be removed
once the config is gone, the answer is still 204, with a `Warning: 199`
header; such documents are never listed again, and the startup cleanup removes
them once they are more than an hour old. Drafts and experiments made from the
topology are not changed. On
the drafts page, each Published Diagrams card of a published topology has
Delete, for a role with `configs` `delete`; a File card has none. It asks
first ("Delete topology <name>?"), is
aria-disabled and says Deleting… while it runs, then removes the card,
announces `Deleted topology <name>.` and moves focus as for a deleted draft
card. A refused delete keeps the card and shows the error; a 404 reads the list
again. A draft that published the topology, was imported from it, or was
opened from its published diagram creates it again when it publishes: a source
config deleted since then passes the source freshness check.

## The builder-doc reference

`metadata.annotations["builder-doc"]` of a Topology config is a map with the
text sub-keys `digest`, `id` and `path`: each optional, at least one, no
other key. It is the one annotation that is not a string. Every JSON and
YAML encoding of a config shows it nested (REST, `phenix config get`/`edit`,
the Configs page). In memory (`store.Annotations` is still
`map[string]string`) and in BoltDB/etcd it is one compact JSON string; the
codec is on `store.Annotations` (`store.StructuredAnnotation`,
`Config.StoredJSON`). A string that is not a JSON object of these sub-keys
is refused (`POST`/`PUT /configs` answer 400).

```yaml
metadata:
  annotations:
    builder-doc:
      digest: sha256:1c92d3b0c95588fbb20926c1e58bb0452903c4e2cb0ffbf12fcdc19f72717732
      id: fd063e784604f43e5c39cc9959501cf117b4dc2be895e92f9bb98e23f8465c4b
      path: /phenix/topologies/pump-station/pump-station.builder.json
```

- `digest`: `sha256:` plus 64 lowercase hex, of the document's canonical JSON
  (`builder.Encode`: `json.MarshalIndent` with two spaces, struct field
  order, no final newline). It is not `sha256sum` of a file unless the file
  is byte for byte that encoding. Without `id` it also finds the stored
  document: `id = PublishedDocumentID(topology name, digest)`.
- `id`: a record in `builder.published`. It counts only when that record's
  `target` is this topology.
- `path`: a Builder file on the phenix server (see
  [Builder files](#builder-files)).

Go: `bapi.DocumentReference{Digest, ID, Path}`, `DecodeReference` (strict),
`EncodeReference` (canonical: sorted, compact), `StoredID(topology)`,
`Names(doc)` (`StoredID(doc.Target) == doc.ID` and digest equal when set),
`Publishes(topology, digest)`. Every reader goes through `Names`.

Resolution (`topologyDocument` in `web/builder_documents.go`): the stored
document the reference names for this topology wins; else, with a `path`,
the file, whose digest must equal `digest` when the reference has one (a
pin); else no document (`?topology=<name>` then says "No published Builder
document exists for topology <name>."). A store error or corrupt record
is an error and does not fall through to the file.

Publish writes `digest` and `id` and keeps an existing `path`. Nothing
writes a Builder file. You write `path` by hand.

The Topology config hook (`api/builder/config_hook.go`, registered in every
phenix process, `--skip-validation` included)
on create and update:

- refuses a reference that does not decode (error matching
  `types.ErrValidationFailed` and `builder.ErrInvalid`; 400 over REST, and
  `phenix config create` logs `calling config hook: config validation
  failed: topology <name>: builder: invalid request: builder-doc...`).
  Messages: `builder-doc: digest is not a sha256 digest`,
  `builder-doc.path: must be an absolute path`, `... must be a clean path,
  with no ".", "..", "//" or trailing "/"`, `... must end in .json, .yaml or
  .yml` (case-sensitive); a path is at most 1024 bytes with no control
  characters. The config schema refuses a non-map (`value must be an
  object`), an unknown sub-key (`property "x" is unsupported`) and `{}`;
- when `id` and `digest` are both set and `id` is not the ID that digest
  gives for this topology name (a rename, or a copy under a new name), drops
  `id`, and `digest` too unless `path` is set (it stays as the file's pin);
  with nothing left the annotation is removed;
- re-encodes a valid reference canonically;
- never checks a path against the file system: confinement is checked by
  the process that reads the file, at read time.

An `id` without a `digest` cannot be checked there; readers find that it
names nothing. `phenix config edit` merges annotation maps, so it can change
a sub-key but not remove `builder-doc`; `PUT /configs` without the
annotation removes it. `${NAME}` in `path` is expanded when the config is
parsed, as in any config field.

The Configs page tags any topology with a `builder-doc` key `builder` and
sends its edit button to `/builder?topology=<name>`, whatever the
reference names.

## Builder files

The one exception to "Builder never reads server files": a Builder
document file named by `builder-doc.path`, read by
`bapi.ReadDocumentFile(root, excluded, path)` only when a diagram is
opened (`GET /builder/topologies/{topology}/document`), when a draft is
created from it, and when such a draft updates the topology. The listing,
the hooks and the `phenix` commands do not read it. Nothing is cached.

Rules (all checked at read time, by `phenix ui`):

- below `--base-dir.phenix` (default `/phenix`) and not below the VM mount
  directory (`common.MountDir()`, default `<base>/mounts`), also after
  resolving symbolic links; opened through `os.OpenRoot`, so only a link
  with a relative target that stays below the root is followed: one that
  leaves the root is refused, and so is every link with an absolute target,
  also one that points below the root (`cannot be read`);
- a regular file (opened `O_NONBLOCK`; a FIFO or directory is refused), at
  most 5 MiB;
- one valid Builder document, JSON or YAML decided by content, not by the
  extension (which the path rule still requires). YAML goes through
  `builder.JSONFromYAML` (`types/builder/yaml.go`), which refuses anchors,
  aliases, merge keys, a second document, non-scalar or duplicate keys,
  other tags, `.inf` and `.nan`, and types scalars as js-yaml's
  `JSON_SCHEMA` does. No `${NAME}` expansion in the file;
- a `digest` beside `path` must equal the file document's digest.

Authorization is `configs` `get` on the topology in the URL, and nothing
else. The name scope of a role therefore does not protect a Builder file:
anyone with `configs` `create` or `update` on any one topology name, plus
`get` on it, can point that topology's `path` at any Builder file below the
base directory and read it. Stored documents have no such gap (their ID is
bound to the target). A forbidden or missing topology, no or an invalid
annotation, and a reference that names nothing all answer the same 404
(`builder document of topology <name> not found`).
File errors are a closed set (`bapi.DocumentFileError`), each a fixed
sentence naming the path and never anything the file holds; the cause is
not wrapped, and the log line is `builder document file not usable` with
`topology`, `path`, `reason`. A path that goes on through a regular file
(`ENOTDIR`) is missing, as a path nothing has is, so the answers do not say
which other names below the base directory are files:

| Status | `message` |
|---|---|
| 422 | `Builder file <path> is outside <root>, the directory phenix reads Builder files from.` |
| 404 | `Builder file <path> does not exist on this phenix server.` |
| 422 | `Builder file <path> cannot be read by phenix.` |
| 422 | `Builder file <path> is not a regular file.` |
| 413 | `Builder file <path> is larger than 5 MiB.` |
| 422 | `Builder file <path> is not a valid Builder document. Upload it in the Builder to see why.` |
| 422 | `Builder file <path> does not match the digest topology <name> records for it.` |

`topologyDiffers: true` on the read route (files only, omitted when false)
means the stored spec is not the document's projection for that topology
name (`bapi.TopologyHoldsDocument`; node order counts, so use the Topology
YAML download as the config's `spec`).

Edit as a draft of a file diagram: `POST /builder/drafts` with
`sourceToken: "builder-file/<topology>/<digest>"` (the digest the read route
returned) and the document exactly as returned. The server reads the file
again with the same authorization: the file errors above; 409 `The Builder
file of topology <name> changed since it was opened. Open its diagram
again.`; 409 `Topology <name> is no longer read from its Builder file. Open
its diagram again.` (a stored document now wins); 404 for a malformed token.
The UI reuses a user's draft with exactly that token, so a changed file
gives a new draft.

Publishing such a draft as an update of that topology is allowed while (1)
the draft's token names this topology and the file's digest still equals
the token's, or the draft's snapshot digest equals the file's, and (2) the
topology's spec still equals the file document's projection. Otherwise 409:
`topology <name> or its Builder file changed after this draft was opened
from the file`, or `topology <name> is not what its Builder file publishes,
so this draft cannot update it`. The token decides before the import rule
does: a file document that was imported from the topology (`source`) gives
its draft no other way to update it. The source freshness check still
applies to such a document, under any target name (`builder source
Topology/<name> changed after this draft was imported`). The client cannot
tell beforehand, so the dialog offers Update and shows the refusal. A
publish writes `digest` and
`id`, keeps `path`, never writes the file, and adds to `warnings`:
`Topology <name> names the Builder file <path>, which Publish does not
change. Download the diagram and replace the file to keep it in step.` From
then on the stored document wins and the listing row is `source: "store"`.
On a server without that record, the file is used only when it matches the
`digest`.

In the UI a file row has the local id `file/<topology>`, a text tag File,
"Read from <path>", and no Delete; the read-only banner says "You are
viewing the diagram of topology <t>, read from <path> on the phenix
server.", plus a sentence when `topologyDiffers`.

## phenix config create and Builder files

`phenix config create` recognizes a Builder document
(`bdoc.IsDocumentText`). Found in a directory, it is skipped with the log
line `skipped Builder document; upload it in the Builder to publish it`.
Named on the command line, it is refused with `<file> is a Builder document,
not a configuration: upload it in the Builder, or send it to the Builder
REST API (/api/v1/builder/drafts), and publish it to create its topology`. A
template file (`bdoc.IsTemplateFileText`) and a package
(`bdoc.IsPackageText`) are recognized by their `$schema` and no `kind`, as a
document is. They are skipped the same way, with the debug line `skipped
Builder file, which is not a configuration` (`kind`, `path`). Named on the
command line, they are refused with `<file> is a Builder template file, not
a configuration: use Import templates in the Builder, or the Builder REST
API (POST /api/v1/builder/templates/{owner}/items), to add its Node
Templates` or `<file> is a Builder package, not a configuration: upload it
in the Builder to open its diagram` (`builderFileKind`, `skipBuilderFile`,
`builderFileRefusal` in `cmd/config.go`).
