# Builder CLI

Part of the [Builder references](../builder.md). `phenix builder publish`,
`phenix config create` and Builder documents, `phenix builder drafts` and
`phenix builder templates` with the REST client they share, and the Builder
routes on the unix socket. The flag summary is in
[`../cli.md`](../cli.md#phenix-builder--builder-documents-drafts-and-node-templates).

## CLI: phenix builder publish

```bash
phenix builder publish </path/to/document> [-n|--name N] [--update] [--dry-run] [--user U] [--record-path]
```

One Builder document file (JSON or YAML by content, at most 5 MiB, no
`${NAME}` expansion) becomes a Topology config with its stored published
document and a `builder-doc` of `digest` and `id`. It runs in the CLI
process against the store (`bapi.Service.PublishTopology` in
`api/builder/publish.go`, command in `cmd/builder.go`), with or without a
running `phenix ui`.

- Name: `--name`, else `bdoc.TopologyName(document name)`, the Publish
  dialog's proposal ("Pump station" gives `Pump-station`).
- It refuses what Publish refuses and lists every blocker, one per line.
- Existing topology: the same document already published there is a no-op
  (`topology already up to date`, exit 0). Otherwise `--update` is required
  (`topology X already exists; use --update to replace it`), and allowed
  only when the topology is still exactly what its stored document
  publishes, or the document's `source` names the topology with a
  `source.digest` equal to the stored topology's. Else: `topology X was
  changed after it was published, and replacing it would discard that
  change`, or `topology X was not published from a Builder document that is
  still stored, and this document was not made from the topology as it is
  now` (`generatedFrom`, which compares `bdoc.ImportDigest`, so a
  `builder-xml` topology is replaced only by a document imported from it as
  it is now, diagram included). An update of a `builder-xml` topology
  deletes the annotation (`ReplaceLegacyDiagram`) and warns. There is no
  force flag.
  An update keeps the topology's other annotations and an existing `path`.
- `--dry-run` runs every check, writes nothing, and prints a report on
  stdout (Document, File, Digest, Document ID, optional Path, Topology with
  "would be created/updated/left as it is", Nodes, Changes, Warnings).
  Changes are `TopologyPublication.Changes.Lines()` (see
  [Publish preview (dry run)](publishing.md#publish-preview-dry-run)): the topology,
  its includes and its disk images, with no scenario, no experiment and no
  `onServer` (the CLI reads no disk list). A refusal prints the error and
  exits 1. It is the way to get a file's digest.
- `--record-path` also writes `path`: the absolute, clean path of the input
  file (must pass the path rule). Outside the base directory or below the
  mount directory the command still succeeds and warns that the server does
  not read the file there. `--update --record-path` replaces a recorded
  path.
- The document is stored as it is in the file (no stamping). The record's
  `createdBy` is `--user`, else the sudo caller, else the OS account.
- Exit 0 for created, updated or up to date; 1 otherwise. Results and
  warnings are log lines on stderr (`topology created`, `topology updated`,
  `topology already up to date`, with `name`, `document`, `digest`).
- Not done: scenarios, experiments, VLAN aliases (warned), includes named by
  file path (warned, not checked), drafts. No broadcast to open pages, and
  no lock shared with a running server: a CLI publish and a UI publish of
  one topology at the same moment are not serialized.
- REST has no single "publish this document" call: create a draft, then
  publish it.

`phenix config create` recognizes a Builder document (`bdoc.IsDocumentText`):
found in a directory it is skipped with the log line `skipped Builder
document; use phenix builder publish`, and named on the command line it is
refused with `<file> is a Builder document, not a configuration: use
"phenix builder publish <file>" to create its topology`. A template file
(`bdoc.IsTemplateFileText`) and a package (`bdoc.IsPackageText`), by their
`$schema` and no `kind` as for a document, are skipped the same way with
the debug line `skipped Builder file, which is not a configuration` (`kind`,
`path`), and refused on the command line with `<file> is a Builder template
file, not a configuration: use "phenix builder templates import <file>" to
add its Node Templates` or `<file> is a Builder package, not a
configuration: upload it in the Builder to open its diagram`
(`builderFileKind`, `skipBuilderFile`, `builderFileRefusal` in
`cmd/config.go`).

## CLI: phenix builder drafts and templates

```bash
phenix builder drafts list|export|validate|preflight ...   # cmd/builder_drafts.go
phenix builder templates list|export|import ...            # cmd/builder_templates.go
```

Both groups call the REST API (`cmd/builder_client.go`, `builderClient`).
`--url` and `--token` are flags of each subcommand, not inherited ones, so
its help lists them (`phenix builder` help hides inherited flags). With
`--url` (`PHENIX_URL`; `http`/`https`, a proxy path kept, no user
info, query or fragment) they send `--token` (`PHENIX_TOKEN`; flag wins) as
`X-Phenix-Auth-Token: Bearer <token>` and act as that user; without it they
dial `common.UnixSocket` (`--unix-socket`) and act as global-admin, sending
no token. A token sent to an `http` URL whose host is not `localhost` or a
loopback address prints one warning on stderr, `warning: the token
travels unencrypted to` and the host, and is still sent
(`cleartextTokenWarning`, from `builderClientFor`). Before dialing, the socket is checked with `Lstat`
(`checkBuilderSocket`, `builderSocketProblem`): it must be a socket (not a
link), owned by the caller's uid or root, without the other-write bit
(`0770` with a group, as `--unix-socket-gid` sets, passes); else exit 2
with the reason, and a missing one gets the "start phenix ui" hint. A flag
given an empty value counts as not given
(`builderSetting`), and a token without a URL is refused with exit 2
(`builderClientFor`: "--token needs --url; ..."). Redirects are not
followed (`CheckRedirect` returns `http.ErrUseLastResponse`): a 3xx is
refused with exit 2 and names its `Location` (`redirectRefusal`), so the
token header never reaches another host and a POST is never replayed as a
GET. Requests time out after 5 minutes, connecting after 30 s; answers
above 256 MiB are refused, and of an error answer at most 64 KiB is read.
The token is never printed. A non-2xx answer is a `*builderAPIError`
(`message`, `cause`, `code`, and `errors` and `issues` as issues, strings
or objects; `reason()` is the message plus the cause when it differs), with
a hint for 401 and for a 403 sent without a token; a body that is not JSON
shows only its first line, at most 1 KiB (`builderErrorText`). Exit status
(`exitError`, `exitCode` in `Execute`): 0 success; 1 findings
(`exitFindings`); 2 refused or no answer, flag and argument errors
included (`exitRefused`, `refused`, `builderFlagError`, `refusedArgs`),
and an unknown subcommand of `drafts` or `templates` (`unknownSubcommand`,
with cobra's suggestions). A report is written before exit 1.

`-o table|json|yaml` (default `table`; JSON indented by two spaces, HTML
not escaped; YAML converted from that JSON by `builderYAML`, keys in the
JSON's order, block style, indent 2). Issues print as `{code?, severity,
message, path?, nodeId?, edgeId?, networkId?, field?}`; a string issue
becomes `{severity, message}` (`builderIssues`).

- `drafts list [--shared] [--owner U]`: `GET /builder/drafts`; own rows,
  plus `shared` with `--shared` or `--owner`, and always over the socket
  (global-admin's own drafts are rarely the ones wanted); sorted by owner
  then ID;
  `{"drafts": [{owner, id, name?, updatedAt?, updatedBy?, access?}]}` from
  `title`, `updated`, `lastModifiedBy`, `access`. Damaged drafts are left
  out.
- `drafts export O/D [--format json|yaml] [--output F]`: the `document` of
  `GET /builder/drafts/{owner}/{draft}`, JSON indented by two spaces in the
  server's key order plus a newline, or YAML; byte-stable. `--package
  [--include scenarios,topologies,icons,images]` posts `{document,
  include}` to `POST /builder/package` and writes the answer's `package`
  instead, by the same format rules (`fetchBuilderPackage`); each of the
  answer's `warnings` is a `warning: [code] message` line on stderr, and an
  answer without a package is exit 2.
  `TestBuilderDraftsExportPackageMatchesRoute` runs it against
  `web/testdata/builder-package-exchange.json`, which
  `TestBuilderPackageAnswersAsRecorded` checks against the real route (API
  and socket routers).
- `drafts validate O/D`: posts the document to `POST
  /builder/export/topology`; `{"draft": {owner, id, name?}, "valid",
  "errors", "warnings"}` from `publishBlockers` and `warnings`. A 422
  answer is invalid: its issues are the errors and warnings by severity,
  and when none is an error, its message and cause (the publish refusal
  puts the reason in `cause`) are the one error. Exit 1 with errors.
- `drafts preflight O/D [--check capacity,network,disks,apps]
  [--experiment E] [--strict]`: posts `{checks, experiment?}` to `POST
  /builder/drafts/{owner}/{draft}/preflight` and prints its answer
  `{checks: [{name, status, summary, issues}], passed, failed,
  unavailable}` (lists never null). Exit 1 when a check failed, and with
  `--strict` when one is unavailable.
- `templates list [--owner U]`: `GET /builder/templates` with the
  `preloaded` collections flattened in; `{"user", "collections": [{id,
  name, description?, owner?, source, templates}], "templates": [{id,
  name, description?, owner?, source, collections}]}`; `source` is `mine`,
  `built-in` (an own template whose ID is a `BuiltinTemplates()` ID),
  `shared`, `server-wide` (listing `server`) or `server` (`preloaded`).
- `templates export [--collection NAME|ID] [--owner U] [--format yaml|json]
  [--output F]`: a template file (collection name `Node templates` without
  `--collection`; two collections of one name is exit 2), names numbered
  apart as `uniqueTemplateNames` does (a warning each), icons the templates
  name embedded from `GET /builder/icons` (name or alias, ignoring case;
  at most 50; each left out is a warning), decoded and validated by
  `DecodeTemplateFile` and `Validate` before it is written.
- `templates import FILE [--name N]`: `ParseTemplateFile` (refused before
  any request), the library limits checked as `importProblem` does, each
  icon posted to `POST /builder/icons` (409 or another refusal is a
  warning), then one `POST /builder/templates/{owner}/items` with
  `collection` named as `uniqueCollectionName` does among the caller's own
  collections.

The unix socket's router (`newSocketRouter` in `web/server.go`) serves the
Builder routes besides the workflow and option routes: `mirrorBuilderRoutes`
(`web/builder.go`) registers every route of the API router whose path
`isBuilderPath` accepts, with the same handlers, so one `builderAPI` (one
startup cleanup, one read of the template files) answers both, under
`middleware.NoAuth` (global-admin). `TestBuilderRoutesAnswerOnUnixSocket`
and `TestBuilderSocketPostKeepsHandlersAndLimits` (a POST answered, 413
past the body limit, 405 for a method the route lacks) check it.
