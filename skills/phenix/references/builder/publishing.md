# Publishing and preflight

Part of the [Builder references](../builder.md). User docs:
[Publishing](https://phenix.sceptre.dev/latest/builder/publishing/) and
[Preflight](https://phenix.sceptre.dev/latest/builder/editor/#preflight).

## Updating a topology or experiment

`POST /builder/drafts/{draft}/publish` needs `If-Match`. A draft can update
a stored Topology or Experiment only when its source token ties it to that
exact source, and nothing changed the source since. Else the answer is 409.
The source tokens:

- `Topology/<name>` or `Experiment/<name>`: imported from the stored
  config. Uploaded sources (`uploaded/…`) and copies (`''`) can only
  create.
- `builder-doc/<document id>`: opened from a published document. To make
  the draft, the caller needs `configs` `get` on the target config of the
  document, else 404.
- `builder-file/<topology>/<digest>`: opened from a Builder file
  ([published-documents.md](published-documents.md#builder-files)).
- `forkOf: "<draft id>"` on `POST /builder/drafts` copies the token of that
  draft and records its last publication as `forked`. A missing draft is
  404.

A published topology names its document in `builder-doc`. After the
configure stage, a published experiment records the draft, the document
and its own digest in its `builder-experiment` annotation. Thus any later
spec change counts as a change. An Experiment update runs the configure
stage of the apps, as `PUT /configs` does. A failed configure stage leaves
the experiment unchanged and gives a `partial` result.

The server accepts an unchanged republish of an experiment only while the
stored spec still equals the draft's projection. A new experiment gets
defaults, so the client cannot predict this case, and `draftCanUpdate`
(`publish.js`) never promises it.

## Host pins on an experiment update

`updatedExperimentConfig` keeps the experiment's `spec.schedules` (host
pins, keyed by hostname). It deletes each pin whose hostname is no longer a
node of the merged topology (`bapi.PruneHostPins` in
`api/builder/hostpins.go`), because a stale pin counts toward its host in
round-robin scheduling. Matching is exact and case-sensitive, as the
schedulers and `minimega_script.tmpl` index `Schedules()`. A pin of an
external node stays.

- The result and the dry run `changes` list the deleted pins as
  `removedHostPins` (`hostname`, `host`, and `renamedTo` for a renamed
  device). The warning `publish.experiment.pins-removed` names them.
- A caller without `experiments/schedule` `get` on the experiment gets no
  `removedHostPins`, and a warning that only counts them
  (`hostPinsWarning`).
- `bapi.DeviceRenames` finds `renamedTo` by node ID, which the editor keeps
  on a rename. The ID comes from the published document that the
  `builder-experiment` annotation names, else from
  `DeviceNodeID(hostname)`, the ID that an import gives.

## Finishing a partial publication

A stage that fails after earlier stages wrote their configs answers
`status` `partial` (`writePublishPartial`), 409 or 500. The draft ETag does
not change, because only the `draft` stage (`MarkPublished`) records the
publication. The `warnings` then carry `publish.retry.available`. The one
exception is a `draft` stage refused because the draft changed meanwhile
(`bapi.ErrConflict`).

- The same request with the same `If-Match` finishes the publication. Each
  config stage whose content is already in place is `skipped`
  (`topologyPublicationApplied`, `experimentAlreadyApplied`, the scenario
  annotations), for `create` and `update` alike (`requirePublishAction`).
- `writePublishRetry` answers a request after the draft stage succeeded
  (`publish.retry.complete`).
- In the UI, `readPublishResult` reads the code as `retry` and leaves it
  out of the listed warnings. The Publish dialog summary
  (`describePublishResult`) and the page error give the advice
  (`PUBLISH_RETRY_ADVICE` in `publish.js`). **Back** keeps the choices.

A publication holds `builderPublishLock` from its draft read to
`MarkPublished`. `DELETE /builder/drafts/{draft}` holds it from its
`If-Match` check to the delete. `POST /builder/cleanup` holds it while it
lists the live documents and deletes drafts (`deletePublishedDrafts`).
Thus no delete runs during the publication of its draft. A delete waits, then
answers 412 `draft.stale` with the new ETag, and `removeDraft` in
`store.js` deletes again with it
(`TestBuilderDeleteDraftWaitsForItsPublication`,
`TestBuilderCleanupWaitsForAPublication`).

## The scenario stage

Root `scenarios` (`bdoc.Document.Scenarios`, at most `MaxScenarios`) lists
Scenario config names. It holds no scenario content. When it lists any,
Publish runs a `scenario` stage after the topology and before the
experiment (`preflightScenarios`, `publishScenarioStage`).

- Each scenario must be readable, else 422.
- The stage appends the topology to the `topology` annotation of each
  scenario (`addTopologyAnnotation`) and keeps the rest of the value byte
  for byte. This needs `configs` `update` on the scenario, else 403.
- A failed write leaves the earlier writes in place, and a new publish
  resumes.
- The request's `scenario` names the experiment's scenario. It must be one
  that the document lists.

Only the Scenarios dialog stores a Scenario config, from an uploaded file
or its YAML editor (`store.saveScenarioConfig(config, {replace, exact})`,
through `api.createConfig` or `api.updateConfig`).

- A `PUT` replaces the annotations. Thus a replacement first reads the
  stored config and sends `replacedScenarioConfig(stored, file)`, which
  keeps every `topology` that the stored value names
  (`mergeTopologyAnnotation`).
- With `exact`, the editor's Save sends the config as it is, annotations
  included, because the editor shows the stored annotations.
- An edit must keep the name, because a `PUT` with another name renames
  the config.
- `store.readScenarioConfig(name)` reads a whole config for View and Edit.
- `builder/scenarios.js` holds the dialog logic: list rows (`scenarioRows`,
  and `filterScenarioRows` past `SCENARIO_FILTER_MIN` rows), YAML text
  (`scenarioText`, `readScenarioText`) and a new config
  (`newScenarioConfig`). The
  editor is `dialogs/ScenarioEditorDialog.vue`, over `ScenarioDialog.vue`.

## What only Publish refuses

Publish answers 422 for four problems that drafts keep. The editor shows
them as warnings with `blocksPublish`, and the Publish dialog lists them as
errors.

- An interface without a VLAN on a device that is not external
  (`InterfaceVLANError`, `interface.vlan.missing`).
- The same IP or MAC address on one network (`InterfaceAddressError`,
  `interface.ip.shared`).
  - An interface's network is its bridge and the VLAN that Publish writes.
    `interfaceNetwork` and `connectInterfaces` in `topology.go`, and
    `interfaceNetworks` in `validate.js`, must agree.
  - The check (`interfaceIP` in `topology.go`) skips the address of a
    `proto: manual` interface. The vrouter app (Vyatta and VyOS) assigns
    it, but the phenix schema, the startup scripts and the Inspector treat
    manual as no address.
  - An interface with `qinq` gets the same check, unless its proto is
    `manual` or `dhcp`.
- A hostname that phenix refuses (`NodeHostnameError`).
- A device setting that makes the experiment fail to start
  (`DeviceSetupError`, `setupErrors` in `devicewarnings.go`):
  `device.drives.none` (a device that is not external has no drive, which
  the phenix schema refuses), `device.ospf.missing` and
  `device.ntp-server.invalid`. JS reports them from `drivesFindings`,
  `ospfFindings` and `ntpFindings` in `validate.js`, and the shared warning
  corpus holds them under `blockers`. An export gives the drive error in
  place of the schema text.

Each `message` names the first three, then how many more. A 422 names only
the first kind found, in the order above. Design warnings
([document.md](document.md#design-warnings)) never block. Publish answers
409 when an included topology now defines a hostname that the document
defines (`publish.include.clash`). User docs:
[What blocks publishing](https://phenix.sceptre.dev/latest/builder/publishing/#what-blocks-publishing).

## Dry run

`"dryRun": true` in the publish body (`previewPublish` in
`builder_preview.go`) needs no `If-Match`, takes no publish lock and writes
nothing. It authorizes and runs `preflightPublish` as a publish does. It
answers 200 `{status: "preview", changes, warnings, errors}`.

- A refusal that a publish answers with 400, 409 or 422 goes in `errors`,
  with `changes: null`. 401, 403, 404 and 5xx keep their status.
- A publish checks `If-Match` before it decodes the body, so
  `builderDryRunRequested` first reads the body loosely.
- `changes` is `bapi.DescribePublishChanges(PublishState)`, which reads and
  writes nothing: `topology`, `experiment`, `includes`, `scenarios`,
  `images`, `vlanAliases` and `removedHostPins`. `images` has `onServer`,
  null when the caller has no permission to list the image or the listing
  gives nothing.
- The Publish dialog sends a dry run 300 ms after each change and each save
  (`usePublishPreview` in `dialogs/publishPreview.js`). It drops stale
  answers. It lists only the issues that its own checks do not list
  (`issuesNotInChecks`).
- The preview never locks the submit button, so its error heading is a
  plain count ("1 error", not "blocks publishing").
- The e2e helpers that wait for a publish (`isPublishRequest` in
  `builder-support.js`) ignore dry runs.

## Preflight

`POST /builder/drafts/{draft}/preflight` with
`{"checks": [...], "experiment"?: "<name>"}` runs the named checks
(`capacity`, `network`, `disks`, `apps`) on the current snapshot
(`bapi.RunPreflight` in `api/builder/preflight.go`, route in
`web/builder_preflight.go`). It writes and starts nothing. Each check runs
at most 20 s (`PreflightTimeout`) and answers `passed`, `failed` (an error
issue) or `unavailable`. Access is that of a draft read.

- `bapi.PreflightEnvironment` wraps every source. The web one applies the
  caller's RBAC as the listing routes do, and hides what the caller has no
  permission to list. Tests set the sources with
  `withBuilderPreflightSources`, so no test needs minimega.
- Gotcha: the bridge listing (`mm.GetBridges` in `util/mm/bridges.go`)
  runs `ovs-vsctl --timeout=5 list-br` on each host. Never use the
  minimega `bridge` command for it: that command creates the default
  bridge and rewrites the minimega `bridges` file.
- The UI is `BuilderChecks.vue` and `builder/preflight.js`.
  `phenix.builder.preflight` keeps the last choice of checks.
