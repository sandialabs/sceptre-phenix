# Preflight checks

Part of the [Builder references](../builder.md). The preflight route, the
four checks and what each reads, and the Preflight section of the Diagram
checks dialog.

`POST /builder/drafts/{owner}/{draft}/preflight` with `{"checks":
["capacity","network","disks","apps"], "experiment"?: "<name>"}` (strict;
at least one check, none twice, an unknown one, an unknown key or an
experiment name outside the config rule is 400 `request.invalid`) puts the
draft's current snapshot through those checks (`bapi.RunPreflight` in
`api/builder/preflight.go`; route `preflightDraft` in
`web/builder_preflight.go`), all at once, each for at most 20 s
(`PreflightTimeout`), and answers 200 `{checks: [{name, status, summary,
issues}], passed, failed, unavailable}` in request order. Access is
`readDraft`'s (`configs` `get` first, then the draft as GET reads it: 404
for a stranger). Nothing is written and nothing is started. Status:
`failed` when an issue is an error, else `unavailable` when the check or a
part of it could not be made (a `preflight.unavailable` warning says why;
an unreadable scenario is a `preflight.app.scenario-unreadable` warning),
else `passed`. Every source is behind `bapi.PreflightEnvironment`; the web
one (`builderPreflightEnvironment`) applies the caller's RBAC as the listing
routes do and reads through `builderPreflightSources` (tests set them with
`withBuilderPreflightSources`, so no test needs minimega):

- `capacity`: devices that are not external (included ones too), vcpus
  and memory (number or text; 1 and 512 MB when unset) against
  `mm.GetClusterHosts(true)` filtered by `hosts` `list` (also per name):
  summed free `CPUs-CPUCommit` (short is the warning
  `preflight.capacity.cpu`) and `MemTotal-MemCommit` (short is the error
  `preflight.capacity.memory`), and each device against each host: it must
  fit on one host with both `vcpus <= CPUs` and `memory <= MemTotal`
  (`deviceFit`; `preflight.capacity.vm-too-large` at field
  `spec.hardware.vcpus` or `.memory` when one exceeds every host, else one
  issue at `spec.hardware.vcpus`). The hosts are read once per request
  (`sync.OnceValues`).
- `network`: distinct VLANs (a connected handle's network, else the spec's
  `vlan`) and network aliases against the named experiment's
  `vlans.min`/`max` (only when both are set, as `minimega_script.tmpl`
  applies them; `configs` `get` and `experiments` `get`, else that part is
  unavailable with "experiment X does not exist, or your role may not read
  it"; `preflight.network.vlan-range`); aliases against the status VLANs of
  running experiments but the named one (`experiments` `list`; the holder
  is named only for a role that may list it;
  `preflight.network.alias-in-use`); bridges the interfaces name and the
  default bridge (the experiment's `defaultBridge`, else `phenix`) against
  `mm.GetBridges(hosts...)` (`util/mm/bridges.go`: `shell ovs-vsctl
  --timeout=5 list-br` on the head node, `mesh send <host> shell ...`
  elsewhere, and nothing else; never minimega's `bridge` command, which
  creates the default bridge and rewrites minimega's `bridges` file when it
  lists; any error fails the listing), a missing one the warning
  `preflight.network.bridge-missing` at the first interface naming it
  (minimega creates a missing bridge when a VM starts on it).
- `disks`: each drive image by file name against `disk.GetImages("")`
  (`disks` `list`, also per name); an empty listing (no minimega) is
  unavailable, as the editor's own check then checks nothing. kvm needs
  VM or ISO, container needs Container, Unknown fits either
  (`preflight.disk.missing`, `preflight.disk.kind`, field
  `spec.hardware.drives.<i>.image`).
- `apps`: no scenarios passes without reading anything; else
  `applications` `list` (default apps always, others per name), then each
  listed scenario (`configs` `get` and `scenarios` `list`, checked before
  the store is read, so a forbidden one is not disclosed): missing is the
  error `preflight.app.scenario-missing`, forbidden or unreadable the
  warning `preflight.app.scenario-unreadable`; each app it does not disable
  (`preflight.app.missing`, path `scenarios[<i>]`).

UI: the Diagram checks dialog's Preflight section (`BuilderChecks.vue`,
`builder/preflight.js`, `store.runPreflight`, `builderApi.preflight`,
`readPreflightReport`): four checkboxes, none ticked until the user ticks
one, then the last choice kept in `phenix.builder.preflight`
(`{"checks": [...]}`; not a listed preference, so logout removes it), an
Experiment field suggesting the source experiments, Run (`aria-disabled`
with none ticked, "Running…" and
`aria-busy` while it runs). `store.runPreflight` saves queued changes first
as Publish does (a view-only draft skips that). The report, kept in the
component outside the dialog, lists each check (testids `preflight`,
`preflight-check-<id>`, `preflight-experiment`, `preflight-run`,
`preflight-error`, `preflight-outcome`, `preflight-result` with
`data-status`/`data-check`, `preflight-summary`, issues
`preflight-issues-<id>`) with a heading "<label>: Passed|Failed|Unavailable"
and its issues through `BuilderIssueList` with Go to; it says
`Preflight: 2 passed, 1 failed, 1 unavailable` (`preflightOutcome`) in the
dialog's own `role="status"` region (`preflight-outcome`; the page behind a
modal dialog is inert), or through the editor's live region when the dialog
was closed before the answer came, and is dropped when `store.doc` changes
other than its stamp (`sameButStamp`).
