# Builder publishing

Part of the [Builder references](../builder.md). The scenarios a document
lists and the Scenarios dialog, the scenario stage of Publish, when a draft
may update a topology or experiment, what only Publish refuses, and the
publish preview (dry run).

## Scenarios and publishing

### Scenarios of a document

A document names the Scenario configs it is used with in root `scenarios`
(`bdoc.Document.Scenarios`): at most 20 (`MaxScenarios`, `MAX_SCENARIOS`),
each a config name (`^[A-Za-z0-9_@.-]+$`) of at most 256 bytes
(`MaxScenarioNameBytes`, the bound on a publication's targets), none twice
ignoring case, left out when empty. It holds no scenario content: there is
no `scenario` object (strict decoding refuses that key, with no shim), no
uploaded kind and no digest. Go (`validateScenarios`) and JS
(`validate.js`, `scenarioNameProblem`; `decode.js` refuses a non-list or a
non-text entry, and validation an empty one) check it alike through the
shared corpus.

Generating from an Experiment (stored or a file) lists its `scenario`
annotation when that names a Scenario config the caller may list on this
server (`configs` `list` and `scenarios` `list`;
`bdoc.WithScenarioResolver`, `storedScenarioResolver` in
`web/builder_sources.go`); otherwise it lists none and warns `the
experiment's scenario "<name>" is not a stored Scenario config and was not
attached` (`the experiment's scenario is not ...` when it names none but
holds content). The experiment's embedded copy is never attached. For a
file it also warns `scenario "<name>" is this server's Scenario config of
that name, not the copy the experiment file holds`. A store error answers
500.

### The Scenario dialog

The Scenario dialog (`dialogs/ScenarioDialog.vue`, title "Scenarios",
testids `scenario-…`) edits the list: Remove, Add a stored scenario (the
sources not listed), and Upload a scenario file (`phenix.sandia.gov/v2`
only; name from `metadata.name`, else the file name through `configName`,
editable). Store and add creates the config with `api.createConfig` (`POST
/configs`, JSON `{apiVersion, kind: Scenario, metadata: {name,
annotations?}, spec}`), or, for a name the sources list, after the
confirmation "Replace scenario <name>?" (it says the spec is replaced and
the annotations kept), replaces it with `api.updateConfig` (`PUT
/configs/Scenario/<name>`). A PUT replaces the annotations too, so
`store.saveScenarioConfig` first reads the stored config
(`api.getConfig`, `GET /configs/Scenario/<name>`) and sends
`replacedScenarioConfig(stored, file)` (`builder/publish.js`): the stored
annotations with the file's over them, and the `topology` annotations
merged by `mergeTopologyAnnotation` (the stored value byte for byte, then
each file name it lacks, after a comma), so no topology is taken off a
scenario. The server's refusal, of the read or the write, shows in the
dialog's alert (`serverReason`). `store.saveScenarioConfig` then reads the
sources again. A stored name the list already has in another letter case
replaces that entry with the stored config's spelling (the status says
"the list names it <name> in place of <old>"); one listed exactly is left
("The list already names it."). Save writes the list with
`store.setScenarios` (one undo step "Updated scenarios"); Cancel keeps a
stored scenario on the server but not in the list.

### The scenario stage of Publish

Publish (`preflightScenarios`, `publishScenarioStage` in
`web/builder_publish.go`), in either mode, runs a `scenario` stage after the
topology and before the experiment whenever the draft's document lists
scenarios: each must exist and be readable (`configs` `get`, `scenarios`
`list`), else 422 `scenario <name> does not exist` (a hidden one alike);
one whose `topology` annotation does not name the topology exactly
(comma-separated, trimmed; `hasTopologyAnnotation`) gets it added
(`addTopologyAnnotation`: appended after a comma, the rest of the value
kept byte for byte, or the whole value when it was empty), which needs `configs` `update` on it (else 403
`adding topology <t> to scenario <s> not allowed`); others are not written.
The one stage reports `updated` or `skipped`, a message such as `added
topology t to scenarios a, b; scenario c already names it`, and `config`
only when one scenario is listed. A failed write leaves the earlier ones
written (a warning names them); publishing again resumes. The request's
`scenario` is `{name}` only, allowed only in `topology-experiment` mode
(400 otherwise, and for a name outside the config rule or any other key),
and must be one of the document's `scenarios` (422 `scenario <name> is not
one of the scenarios this draft lists`); the experiment is created with it
(`CreateWithScenario`, after the stage annotated it) or updated with it
(`MakeCustomScenarioFromConfig`, `MergeScenariosForTopology`, the
`scenario` annotation); none leaves the experiment without one. The
publication records it as `scenarioTarget`. The Publish dialog's
Experiment scenario select (`publish-scenario`) lists the document's
scenarios and No scenario, the first by default, and its hint
(`scenarioStageHint`) says the topology is added to each listed scenario.
`phenix builder publish` changes no scenario and notes `The document's
scenario is not changed: only the topology is published.` (`The
document's N scenarios are not changed: …`).

### Updating a topology or experiment

Topology and Experiment updates require a draft tied
to that exact stored source: one imported from it, or one that published it (or
was opened from the published diagram that did), with nothing else having
changed it since. Otherwise the update gets 409, for example `topology <name>
changed after this draft published it` or `experiment <name> changed after this
draft published it`. `POST /builder/drafts` accepts
`forkOf: "<owner>/<draft id>"`, which saving the editor's history as a new
draft sends: the new draft takes that draft's source token and records its last
publication as `forked`, so it can update what that draft published or was
opened from (not what that draft publishes later). The caller must be able to
read that draft (owner, a share, or `builder-drafts` `get`), otherwise 404. A
`sourceToken` of `builder-doc/<document id>` needs `configs` `get` for the config
that document was published to, otherwise 404. A `sourceToken` of
`builder-file/<topology>/<digest>` names the Builder file a topology
references (see [Builder files](published-documents.md#builder-files)). A published
topology names its document in its `builder-doc` annotation (see
[The builder-doc reference](published-documents.md#the-builder-doc-reference)); a published
experiment records the draft and document that published it, and its digest
after the configure stage, in its `builder-experiment` annotation, so any later
change to its spec counts. An Experiment update then runs the apps' configure
stage, as `PUT /configs` does. A failed configure stage leaves the experiment
unchanged and is reported as a `partial` result, and an experiment found
running once its lock is held is too. An Experiment create is refused with 422
before anything is written if its name is `all` (in any case), or longer than
15 characters in auto bridge mode. A name outside the config naming rule is
refused with 400.

### What only Publish refuses

Publish answers 422 when an interface of a device that is not external has no
VLAN, and the error `message` names the devices and interfaces (the first three,
then how many more; by position, such as `#2`, when unnamed or when two share a
name): phenix would store such a topology, but minimega refuses the interface
when the experiment starts. Connect the interface or give it a VLAN. A VLAN
that names no network of the document still publishes as it is, since phenix
allocates VLANs by name and matches them exactly (`exp` is not network `EXP`).
Drafts keep such interfaces; the editor flags them as warnings.

Publish also answers 422 when interfaces on the same network use the same IP
or MAC address, and the error `message` names each address, its network and
the interfaces that use it (the first three addresses, then how many more),
such as `IP address 10.0.0.5 on VLAN "EXP" is used by interface "eth0" of
device "a" and interface "eth0" of device "b"`: phenix would store such a
topology, but the addresses clash once the experiment runs. An interface's
network is its bridge and the VLAN Publish writes for it: the connected
network's name, else its own VLAN, compared exactly after trimming white
space (`interfaceNetwork`, `connectInterfaces` in `types/builder/topology.go`;
`connectionNetworks`, `publishedVLANs`, `interfaceNetworks` in
`validate.js`). A blank bridge and `phenix` are the experiment's default
bridge, and any other bridge is compared as written (`on VLAN "EXP" of
bridge "lab"`). Interfaces on two VLANs, or on VLANs of one name on two
bridges, may share an address, as isolated networks do and as minimega
allows; interfaces without a VLAN are compared with each other only
(`without a VLAN`). IP addresses are compared parsed, without a prefix
length typed after them, and MAC addresses in any case and with any
separators. The IP addresses of interfaces whose `proto` is `dhcp` or
`manual`, blank values and external devices' MAC addresses are not
compared. Included devices are, but an address only they use is left to
their topology. When interfaces also have no VLAN, the 422 names only
those. Drafts keep shared addresses; the editor flags each interface that
uses one as a warning (`... is also used on VLAN "EXP" by ...`; the
Inspector's field warning compares a VLAN the working copy changed as
typed), and the Publish dialog lists them as errors.

Publish also answers 422 for a hostname of a device that is not external
which phenix refuses, and the error `message` gives phenix's reason for each,
which names the hostname (the first three, then how many more): one character
long, which phenix's schema refuses, or `all`, all digits, or `phenix` on a
Windows node, which phenix stores but refuses when it creates an experiment.
When interfaces also have no VLAN or share addresses, the 422 names only
those. Drafts keep such hostnames, as a topology an older phenix stored may
have them; the editor flags each as a warning, and the Publish dialog lists
them as errors. It shows phenix's warnings about other casings of `all`, and
about `phenix` on a node that is not Windows, as plain warnings, and Publish
returns them in `warnings`. Included devices are left to their topology, but
an experiment publish whose included topology has a hostname phenix refuses
in an experiment (any of the above, one character long included) answers 422
before anything is written.

## Publish preview (dry run)

`POST /builder/drafts/{owner}/{draft}/publish` with `"dryRun": true` in the
intent (`builderPublishRequest.DryRun`, `previewPublish` in
`web/builder_preview.go`) needs no If-Match and takes no publish lock. A
publish still answers a missing or malformed If-Match (400) before it decodes
the body; only a request without a valid one has its body read first for
`dryRun` (`builderPublishIntent`; `builderDryRunRequested` decodes loosely
and puts the body back for the strict decode), and is answered with that 400
unless it asks for a dry run. It
authorizes the draft (`builderVerbUpdate`) and the targets and runs
`preflightPublish` on the current snapshot as a publish does, writes nothing
(no published document, config, scenario, experiment or `MarkPublished`),
and answers 200 `{status: "preview", changes, warnings, errors}`. A refusal a
publish answers with 400 (a target name), 409 or 422 is listed in `errors`
(the refusal's issues, `bdoc.ErrorIssues`, else one issue of its code and
words, each severity `error`) with `changes: null`
(`builderPreviewRefusal`); 401, 403, 404, a body strict decoding refuses and
5xx keep their status. `warnings` are the projection's plus those a publish
adds once it writes the topology (`publish.file.unchanged`,
`publish.legacy.replaced`/`removed`).

`changes` is `bapi.DescribePublishChanges(PublishState)` in
`api/builder/changes.go`, which reads and writes nothing: `topology` and
`experiment` (`{name, action}`, action `create`, `update` or `unchanged` when
the config already holds the publication: `plan.topology.applied`,
`plan.experiment.applied`), `includes` (`spec.includeTopologies` of the
stored topology against the projection's: `added`, `removed`, `kept`),
`scenarios` (`annotate`, or `unchanged` when `HasTopologyAnnotation`),
`images` (`hardware.drives[].image` of the stored topology's devices against
the projection's, with the hostnames using each, those of the stored
topology for a removed one, and `onServer`), `vlanAliases` (experiment only,
left out when empty: `spec.vlans.aliases` of the stored experiment against
`projection.VLANAliases`, `{name, from, to, change}`, all `added` on
create). Lists are sorted by name. `onServer` compares the image's file name
(`path.Base`, as `validate.js` does) with the names of every image the
server has (`builderAPI.listDisks`, `disk.GetImages("")`; tests set
`withBuilderDisks`), and is null without `disks` `list`, when the listing
fails, when it lists none (minimega not running), and for an image whose
file name the caller may not list (`hideUnlistedImages`, as `GET /disks`
leaves it out), whether the server has it or not. `PublishChanges.Lines()`
words each change as the dialog does (`publishChangeLines` in
`publish.js`), ending "Nothing outside the Topology changes" when the four
lists are empty. `planTopology` never fails on it: when a stored topology
cannot be decoded for it, `TopologyPublication.Changes` is nil and only a
dry run warns (`publish.changes.unknown`).

The Publish dialog's What publishing changes (testids `publish-preview`,
lists `publish-preview-configs`, `-includes`, `-scenarios`, `-images`,
`-aliases`; `publish-preview-blocked`, `publish-preview-error`,
`publish-preview-issues`) calls `store.previewPublish(intent)` once the
lists it reads on open settle, 300 ms after a change of the form or of
`store.sources`/`store.documents`, after each save (`store.etag`), and after
Back (`usePublishPreview` in `dialogs/publishPreview.js`). The store drops an
answer a later preview made stale (`previewRequest`, and a change of draft)
and returns a failure as `{failed, message}` without setting `store.error`;
the dialog also drops an answer asked for before a later change, once it
closed, or for an intent it could not build (shown as
`publish-preview-blocked`, nothing sent). The section is `aria-busy` from
the change that schedules a preview (through the 300 ms pause) until the
answer, keeps the last answer meanwhile, and says it was read again through
a polite live line (no second `status` role) that counts the warnings. The
answer's `errors`, then `warnings`, are listed below the changes in
`BuilderIssueList` (`publish-preview-issues`, `-error`, `-warning`, with
codes), less those the dialog's checks list already
(`issuesNotInChecks` in `builder/issues.js`: same code and same node,
connection or network, or both about none, whatever the words or severity),
so a check the server also refuses (`interface.vlan.missing`,
`interface.ip.shared`, a refused hostname) is listed once, under Checks;
server-only refusals (`publish.include.clash`, `publish.scenario.missing`)
still show. While the checks have errors, the section says "Publishing is
blocked by the errors listed under Checks." (`publish-preview-checks`). It
is hidden for a read-only draft and never disables Publish. e2e helpers that wait for a
publish use `isPublishRequest`/`isPublishResponse` in `builder-support.js`,
which leave out dry runs (`isPublishPreview`).
