# Publishing and preflight

Part of the [Builder references](../builder.md). The publish route: when a
draft may update a config, the scenario stage, what only Publish refuses,
the dry run, and the preflight checks.

User docs: [Publishing](https://phenix.sceptre.dev/latest/builder/publishing/)
(the rules, refusals and messages a user sees) and
[Preflight](https://phenix.sceptre.dev/latest/builder/editor/#preflight).
Code: `web/builder_publish.go`, `web/builder_preview.go`,
`api/builder/changes.go`, `types/builder/topology.go`, `builder/publish.js`.

## Updating a topology or experiment

`POST /builder/drafts/{owner}/{draft}/publish` needs `If-Match`. A draft may
update a stored Topology or Experiment only when it is tied to that exact
source and nothing else changed the source since. Otherwise 409. The tie
is the draft's source token:

- `Topology/<name>` or `Experiment/<name>`: imported from the stored
  config. Uploaded sources (`uploaded/…`) and copies (`''`) can only create.
- `builder-doc/<document id>`: opened from a published document. Creating
  the draft needs `configs` `get` on the config the document was published
  to, else 404.
- `builder-file/<topology>/<digest>`: opened from a Builder file (see
  [published-documents.md](published-documents.md#builder-files)).
- `forkOf: "<owner>/<draft id>"` on `POST /builder/drafts` copies that
  draft's token and records its last publication as `forked`. The caller
  must be able to read that draft, else 404.

A published topology names its document in `builder-doc`. A published
experiment records the draft, the document and its own digest after the
configure stage in its `builder-experiment` annotation, so any later change
to its spec counts as a change. An Experiment update runs the apps'
configure stage, as `PUT /configs` does. A failed configure stage leaves
the experiment unchanged and is a `partial` result.

## The scenario stage

A document lists Scenario config names in root `scenarios`
(`bdoc.Document.Scenarios`, at most `MaxScenarios`). It holds no scenario
content. When it lists any, Publish runs a `scenario` stage after the
topology and before the experiment (`preflightScenarios`,
`publishScenarioStage`). Each scenario must be readable, else 422. Its
`topology` annotation gets the topology appended
(`addTopologyAnnotation`, the rest of the value kept byte for byte), which
needs `configs` `update` on it, else 403. A failed write leaves the earlier
ones written, and publishing again resumes. The request's `scenario` names
the experiment's scenario and must be one the document lists.

Only the Scenarios dialog stores a Scenario config (`api.createConfig` or
`api.updateConfig`). A `PUT` replaces the annotations, so
`store.saveScenarioConfig` first reads the stored config and sends
`replacedScenarioConfig(stored, file)`, which keeps every `topology` the
stored value names (`mergeTopologyAnnotation`).

## What only Publish refuses

Publish answers 422 for three problems that drafts keep. The editor shows
them as warnings with `blocksPublish`, and the Publish dialog lists them as
errors.

- An interface without a VLAN on a device that is not external
  (`InterfaceVLANError`, `interface.vlan.missing`).
- The same IP or MAC address on one network (`InterfaceAddressError`,
  `interface.ip.shared`). An interface's network is its bridge and the VLAN
  Publish writes (`interfaceNetwork`, `connectInterfaces` in `topology.go`,
  and `interfaceNetworks` in `validate.js`, which must agree).
- A hostname phenix refuses (`NodeHostnameError`).

Each `message` names the first three, then how many more. A 422 names only
the first kind found, in that order. Publish also answers 409 when an
included topology now defines a hostname the document defines
(`publish.include.clash`). User docs:
[What blocks publishing](https://phenix.sceptre.dev/latest/builder/publishing/#what-blocks-publishing).

## Dry run

`"dryRun": true` in the publish body (`previewPublish` in
`builder_preview.go`) needs no `If-Match`, takes no publish lock and writes
nothing. It authorizes and runs `preflightPublish` as a publish does, and
answers 200 `{status: "preview", changes, warnings, errors}`. A refusal a
publish answers with 400, 409 or 422 is listed in `errors` with `changes:
null`. 401, 403, 404 and 5xx keep their status. A publish still checks
`If-Match` before it decodes the body, so `builderDryRunRequested` reads
the body loosely first.

`changes` is `bapi.DescribePublishChanges(PublishState)`, which reads and
writes nothing: `topology`, `experiment`, `includes`, `scenarios`,
`images` (with `onServer`, null when the caller may not list the image or
the listing gives nothing) and `vlanAliases`. The Publish dialog sends a
dry run 300 ms after each change and after each save
(`usePublishPreview` in `dialogs/publishPreview.js`). It drops stale
answers, and lists only the issues its own checks do not already list
(`issuesNotInChecks`). e2e helpers that wait for a publish
(`isPublishRequest` in `builder-support.js`) leave dry runs out.

## Preflight

`POST /builder/drafts/{owner}/{draft}/preflight` with `{"checks": [...],
"experiment"?: "<name>"}` runs the named checks (`capacity`, `network`,
`disks`, `apps`) on the current snapshot (`bapi.RunPreflight` in
`api/builder/preflight.go`, route in `web/builder_preflight.go`). It writes
and starts nothing. Each check runs at most 20 s (`PreflightTimeout`). Each
answers `passed`, `failed` (an error issue) or `unavailable`. Access is
that of a draft read.

- Every source is behind `bapi.PreflightEnvironment`. The web one applies
  the caller's RBAC as the listing routes do, and hides what the caller may
  not list. Tests set the sources with `withBuilderPreflightSources`, so no
  test needs minimega.
- Gotcha: the bridge listing (`mm.GetBridges` in `util/mm/bridges.go`) runs
  `ovs-vsctl --timeout=5 list-br` on each host. Never use minimega's
  `bridge` command for it: that command creates the default bridge and
  rewrites minimega's `bridges` file.
- The UI is `BuilderChecks.vue` and `builder/preflight.js`. The last choice
  of checks is kept in `phenix.builder.preflight`.
