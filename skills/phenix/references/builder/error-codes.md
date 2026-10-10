# Error codes and issues

Part of the [Builder references](../builder.md). The code registry, decoding
refusals as issues, the issue shape in Go and JS, and the `code` and
`issues` of Builder routes' error bodies.

## The registry

Every Builder problem has a stable code, dotted lowercase words whose first
names the subject (`node.hostname.duplicate`, `publish.topology.exists`,
`request.not-found`). The one registry is `types/builder/codes.go`
(`bdoc.Codes()`, `LookupCode`, constants `Code…`, each with a default
severity and a description); `make generate-builder-schema` writes it to
`src/js/src/builder/schema/codes.json` and the table of
`docs/content/builder/error-codes.md` (Go tests fail when either is stale).
It writes and checks the docs table only where the repository's `docs/`
directory exists (`BUILDER_SCHEMA_TESTS` in `src/go/Makefile`): the Docker
and Podman builds run `make bin/phenix`, and so `make generate`, on a copy
of `src/go` and `src/js` without it.
A code never changes meaning once released: a new rule gets a new code, in
Go and in `validate.js` and `decode.js`, which report the same codes for the
same rules (the shared corpus pins `code` for every case with an `error`,
and `validate.test.js` checks every quoted text of two or more dotted words
in `validate.js` and `decode.js` is a code of `codes.json` or a path of the
document schema, and that no code is put together in a template string).

## Decoding refusals

Decoding refusals are issues on both sides: `bdoc.Decode` (through
`decodeIssues` in `types/builder/decodeissues.go`, which walks the JSON
against the Go types by reflection, matching keys as `encoding/json` does)
returns a `*ValidationError` when every key and value the strict decoder
refuses is an unknown key (`document.field.unknown`, at the key's own path
such as `icons.<name>.type`) or a value of the wrong type at a path the
editor checks (`decodeRule`: `metadata.not-object`, `metadata.user.not-text`,
`metadata.time.not-text`, the notes lists, `document.list.missing`,
`drawing.arrow.not-boolean`, `document.layout.not-text`,
`template.list.not-list`, `icon.list.not-object`, `include.list.not-list`,
`include.name.required`, `source.annotations.not-object`,
`source.annotation.not-text`); any other refusal keeps the decoder's error.
`decode.js` throws a `DocumentError` whose `issues` hold the same issue
(`refusal` there) for each of these it refuses before validation.

## Issues

An issue (`bdoc.Issue`, OpenAPI `BuilderIssue`) is `{code, severity,
message, path?, nodeId?, edgeId?, networkId?, field?}`: `field` is the JSON
Forms path of a device field (`spec.network.interfaces.0.vlan`, `hostname`).
`Document.Validate` issues carry codes and the IDs `LocateIssues` reads from
the path; the publish blockers (`InterfaceVLANError`,
`InterfaceAddressError`, `NodeHostnameError`) list located issues through
`Issues()`, and `bdoc.ErrorIssues(err)` finds the issues of any error.
`Topology.Warnings`, `TopologyExport.Warnings` and
`bapi.TopologyPublication.Warnings` are issues. JS issues keep `level` and
add `code` and `severity` (the same value), and so do the issues
`templateFileIssues` (`templateFile.js`) finds in a template file, with the
codes `TemplateFile.Issues` gives them.

## Error bodies of Builder routes

Every Builder route's error body has `code` (`builderHandler` and
`builderCodedError` in `web/builder.go`: an explicit `WithCode`, else
`package.invalid`, `document.invalid`, `template.file.invalid`,
`publish.blocked` for any other error with issues, or the status's
`request.*`/`server.*` code) and `issues` for a package or a document that
does not validate or what only publishing refuses; other routes leave both
out (`TestNonBuilderErrorsCarryNoCode`). The package routes' refusals and
`POST /builder/package` warnings have `package.*` codes (see
[Builder packages](downloads-and-packages.md#builder-packages)). The draft
answers a client tells apart have `draft.*` codes: `draft.stale`
(`builderCheckIfMatch`, 412), `draft.shares.stale` (412 of `PUT .../shares`),
`draft.conflict` (`builderWebError` for a `bapi.ConflictError` of kind
`bapi.KindDraft`, 409) and `draft.snapshot.current` (409); other routes keep
`request.stale` and `request.conflict`. A method a Builder route does not
take is answered by `builderMethodNotAllowed` with 405,
`request.method-not-allowed` and `Allow` (the methods the router matches for
the path); any other path gets the bare 405. The new topology name of a copy
or combine is refused with `import.name.invalid` or `import.name.source`,
which `store.generate` reads (`serverCode`, `NEW_NAME_CODES`) to put the
error on the `newName` field. Each `failed` entry of a template share or
publish answer has `code` (`template.item.not-found`,
`template.shares.too-many`) beside `reason`. A `publish.scenario.*` refusal
names the scenario in `metadata.scenario`. The publish response's `warnings` and
`errors`, and the Topology YAML download's `warnings` and `publishBlockers`
(one per check, the code of its first problem, located when it has one),
are issues; `readIssues` in `api.js` reads them, and `readPublishResult` and
`exportTopology` keep `warnings`, `errors` and `publishBlockers` as the
messages beside `warningIssues`, `errorIssues` and `publishBlockerIssues`.
`publishRefusal(serverRefusal(error), intent, context)` in `publish.js`
picks the form field by the code (`REFUSAL_RULES`). `phenix builder
publish` prints each refusal line and warning with `[code]` after it, and a
file that is not a valid document as `<file> is not a valid Builder
document [document.invalid]:` with each issue (`path: message [code]`) on a
line of its own (`builderInvalidDocument` in `cmd/builder.go`).
Generation and legacy conversion warnings (`source.warnings`, the generate
and legacy responses) are plain text.
