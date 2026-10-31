# Published documents and Builder files

Part of the [Builder references](../builder.md). User docs:
[The builder-doc annotation](https://phenix.sceptre.dev/latest/builder/administration/#the-builder-doc-annotation),
[Builder documents in files](https://phenix.sceptre.dev/latest/builder/administration/#builder-documents-in-files)
and
[Publishing to a topology that names a Builder file](https://phenix.sceptre.dev/latest/builder/publishing/#publishing-to-a-topology-that-names-a-builder-file).

## The builder-doc reference

`metadata.annotations["builder-doc"]` of a Topology is a map with the text
sub-keys `digest`, `id` and `path`. It is the one annotation that is not a
string.

- Codec: every JSON and YAML form of a config shows it nested. In memory
  (`store.Annotations` is still `map[string]string`) and in the store, it
  is one compact JSON string (`store.StructuredAnnotation`,
  `Config.StoredJSON`).
- `digest` is `sha256:` plus 64 lowercase hex of the canonical JSON of the
  document (`builder.Encode`: two-space indent, struct field order, no
  final newline). It equals the `sha256sum` of a file only when the file
  is that encoding byte for byte.
- `id` names a record in `builder.published`, valid only when its `target`
  is this topology: `id = PublishedDocumentID(topology, digest)`.
- Go: `bapi.DocumentReference{Digest, ID, Path}`, `DecodeReference`
  (strict), `EncodeReference` (canonical). Every reader goes through
  `Names(doc)`.
- Resolution (`topologyDocument`): the stored document that the reference
  names wins. Else, with a `path`, the file wins, and its digest must
  equal `digest` when set. A store error does not fall through to the
  file.
- Publish writes `digest` and `id`, and keeps `path`. Nothing writes
  Builder files.

The Topology config hook (`api/builder/config_hook.go`) runs in every
phenix process, also with `--skip-validation`. On create and update, it:

1. Refuses a reference that does not decode (400 over REST).
2. Drops an `id` that does not match the digest for this topology name (a
   rename or a copy). It also drops `digest` when the reference has no
   `path`.
3. Deletes an annotation with nothing left, and encodes the reference
   again canonically.

The hook never checks a path against the file system: the process that
reads the file checks it. `phenix config edit` merges annotation maps, so
it cannot delete `builder-doc`. A `PUT /configs` without it deletes it.

## Builder files

The one server file that the Builder reads is the Builder document in
`builder-doc.path`. `bapi.ReadDocumentFile(root, excluded, path)` reads it
only when the diagram opens (`GET /builder/topologies/{topology}/document`),
when a draft comes from it, and when such a draft updates the topology.
Nothing caches it. The listing, the hooks and the `phenix` commands never
read it.

- It uses `os.OpenRoot` below `--base-dir.phenix`, not below the VM mount
  directory. It refuses a link with an absolute target, also one that
  points below the root.
- File errors are a closed set (`bapi.DocumentFileError`). Each is a fixed
  sentence that names the path and nothing that the file holds. A path
  through a regular file (`ENOTDIR`) answers as missing (404), so answers
  do not show which names are files. The log line is
  `builder document file not usable`.
- Authorization gap: the route checks only `configs` `get` on the
  topology. A user who can write the `path` of a topology and read that
  topology can read any Builder file below the base directory. Stored
  documents have no such gap.
- `topologyDiffers: true` means that the stored spec is not the projection
  of the document (`bapi.TopologyHoldsDocument`, node order counts).
- A draft made from a file has
  `sourceToken: "builder-file/<topology>/<digest>"`. The server reads the
  file again and answers 409 when it changed or when a stored document now
  wins.

## Published documents

- `GET /builder/documents` lists each config kind once per request and
  matches documents to references (`DocumentReference.Names`). It lists
  only current documents (`source: "store"`), and a `source: "file"` row for a
  topology with a reference `path` and no current stored document. It
  reads no file.
- After a publication, the server deletes the older documents of the
  topology that are more than one hour old. A publish stores its document
  before it writes the config, so a younger document can belong to a
  publish in progress.
- A delete of a Topology config, by any path, runs the Topology hook. The
  hook deletes the documents stored before the last config write
  (`metadata.updated`, to the second) and those older than one hour. It
  logs a failure and never fails the delete. A later publish, or the next
  cleanup (at start, then every hour), deletes the rest when they are
  more than one hour old.
- The write time has a resolution of one second. Thus
  `Service.DeleteConfigDocuments` keeps a document stored in that second
  after the document that the config named. It deletes the same content
  published again in that second, because it cannot tell that publication
  apart. The REST API keeps such a document because it holds the publish
  lock. Without an `Updated` time, every document in the grace period
  stays.
- A topology rename (`PUT /configs` that changes `metadata.name` or
  `kind`) runs the `rename` stage of the hooks with the old config, which
  deletes the documents of the old name. `DELETE /configs` and a renaming
  `PUT` hold the publish lock.
- `DELETE /builder/documents/{document}` deletes the topology through the
  same call and broadcast as `DELETE /configs`, then all its documents.
  `LeaveTopologyDocuments` tells the hook to leave them to the route, so
  the route can report a failure. Such a failure still answers 204, with
  `Warning: 199`. A published experiment answers 422.
- `ListPublishedDocuments` skips, with `plog.Warn`, a published record
  whose metadata does not decode or is not valid. Thus the listing and the
  delete helpers skip it, and the cleanup keeps it. The same `DELETE` route
  deletes it and its content alone (`GetDamagedDocument`,
  `DeleteDamagedDocument`, no config change). `If-Match` is the record
  revision tag. A target that the record still names needs `get` and
  `delete` on it. A record with no target needs them on `*/*`
  (`builderEveryConfig`).

## phenix config create and Builder files

`cmd/config.go` finds a Builder document, a template file, a package and a
layer set file by `$schema` and no `kind`, with one parse
(`bdoc.FileKindOf`). It skips each in a directory and refuses each on the
command line (`builderFileKind`, `skipBuilderFile`, `builderFileRefusal`).
The user docs quote the messages
([Builder documents and phenix config create](https://phenix.sceptre.dev/latest/builder/import-upload-download/#builder-documents-and-phenix-config-create)).
