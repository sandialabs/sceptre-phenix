# Published documents and Builder files

Part of the [Builder references](../builder.md). The `builder-doc`
annotation, Builder files, and what happens to published documents when a
topology is published again, deleted or renamed.

User docs: [The builder-doc annotation](https://phenix.sceptre.dev/latest/builder/administration/#the-builder-doc-annotation),
[Builder documents in files](https://phenix.sceptre.dev/latest/builder/administration/#builder-documents-in-files)
(the read rules, who can read a file, and the file error messages) and
[Publishing to a topology that names a Builder file](https://phenix.sceptre.dev/latest/builder/publishing/#publishing-to-a-topology-that-names-a-builder-file).
Code: `api/builder/published.go`, `config_hook.go`, `file.go`,
`web/builder_documents.go`, `store/types.go`.

## The builder-doc reference

`metadata.annotations["builder-doc"]` of a Topology is a map with the text
sub-keys `digest`, `id` and `path`. It is the one annotation that is not a
string.

- Codec: every JSON and YAML form of a config shows it nested. In memory
  (`store.Annotations` is still `map[string]string`) and in BoltDB and
  etcd, it is one compact JSON string (`store.StructuredAnnotation`,
  `Config.StoredJSON`).
- `digest` is `sha256:` plus 64 lowercase hex of the document's canonical
  JSON (`builder.Encode`: two-space indent, struct field order, no final
  newline). It is not the `sha256sum` of a file unless the file is that
  encoding byte for byte. `id` is a record in `builder.published`, valid
  only when its `target` is this topology:
  `id = PublishedDocumentID(topology, digest)`.
- Go: `bapi.DocumentReference{Digest, ID, Path}`, `DecodeReference`
  (strict), `EncodeReference` (canonical). Every reader goes through
  `Names(doc)`.
- Resolution (`topologyDocument`): the stored document the reference names
  wins. Else, with a `path`, the file, whose digest must equal `digest`
  when set. A store error does not fall through to the file.
- Publish writes `digest` and `id` and keeps `path`. Nothing writes a
  Builder file.

The Topology config hook (`api/builder/config_hook.go`) runs in every phenix
process, also with `--skip-validation`. On create and update it refuses a
reference that does not decode (400 over REST). It drops an `id` that does
not match the digest for this topology name (a rename or a copy), and the
`digest` too unless `path` is set. It removes an annotation with nothing
left, and encodes the reference again canonically. It never checks a path against the file
system: the process that reads the file checks it then. `phenix config edit`
merges annotation maps, so it cannot remove `builder-doc`. `PUT /configs`
without it removes it.

## Builder files

The one server file the Builder reads is the Builder document a topology
names in `builder-doc.path`. `bapi.ReadDocumentFile(root, excluded, path)`
reads it only when the diagram opens
(`GET /builder/topologies/{topology}/document`), when a draft is made from
it, and when such a draft updates the topology. Nothing caches it. The
listing, the hooks and the `phenix` commands never read it.

- `os.OpenRoot` below `--base-dir.phenix`, not below the VM mount
  directory. A link with an absolute target is refused, also one that
  points below the root.
- File errors are a closed set (`bapi.DocumentFileError`). Each is a fixed
  sentence that names the path and nothing the file holds. A path that goes
  through a regular file (`ENOTDIR`) answers as missing (404), so answers do
  not show which names are files. The log line is `builder document file
  not usable`.
- Authorization gap: the route checks only `configs` `get` on the topology.
  Anyone who may write one topology's `path` and read it can read any
  Builder file below the base directory. Stored documents have no such gap.
- `topologyDiffers: true` means the stored spec is not the document's
  projection (`bapi.TopologyHoldsDocument`, node order counts).
- A draft made from a file uses `sourceToken:
  "builder-file/<topology>/<digest>"`. The server reads the file again and
  answers 409 when it changed or when a stored document now wins.

## Published documents

- `GET /builder/documents` lists each config kind once per request and
  matches documents to references (`DocumentReference.Names`). It lists
  current documents only (`source: "store"`), and a `source: "file"` row
  for a topology whose reference has a `path` and no current stored
  document. It reads no file.
- After a publication, the server removes older documents of the topology
  once they are more than an hour old. A publish stores its document before it writes
  the config, so a younger document may belong to a publish in flight.
- Deleting a Topology config, however it is deleted, runs the Topology
  hook, which removes its documents stored before the config was last
  written (`metadata.updated`, to the second) and those older than an hour.
  A failure is logged and never fails the delete. A later publish or the
  startup cleanup removes the rest once they are more than an hour old.
- Renaming a topology (`PUT /configs` that changes `metadata.name`) runs the
  hooks' `rename` stage with the old config, which removes the old name's
  documents. `DELETE /configs` and a renaming `PUT` hold the publish lock.
- `DELETE /builder/documents/{document}` deletes the topology through the
  same call and broadcast as `DELETE /configs`, then all its documents
  (`LeaveTopologyDocuments` tells the hook to leave them to the route, so
  it can report a failure). A failure there still answers 204, with
  `Warning: 199`. A published experiment answers 422.

## phenix config create and Builder files

`cmd/config.go` recognizes a Builder document (`bdoc.IsDocumentText`), a
template file (`bdoc.IsTemplateFileText`) and a package
(`bdoc.IsPackageText`) by `$schema` and no `kind`. It skips each in a
directory and refuses each on the command line (`builderFileKind`,
`skipBuilderFile`, `builderFileRefusal`). The user docs quote the messages
([Builder documents and phenix config create](https://phenix.sceptre.dev/latest/builder/import-upload-download/#builder-documents-and-phenix-config-create)).
