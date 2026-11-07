# phenix Web API Reference

`phenix ui` serves the web UI and REST API together on the same port —
`0.0.0.0:3000` by default (`ui.listen-endpoint` / `--listen-endpoint`,
`PHENIX_UI_LISTEN_ENDPOINT`). All routes below are relative to the `/api/v1`
base path unless noted.

Prefer the equivalent `phenix` CLI command over calling the web API directly
unless the HTTP interface is specifically needed (e.g. scripting against a
running `phenix ui` server, or building a UI integration).

When phenix runs in its Docker container, the bundled compose file uses
`network_mode: host`, so `localhost:3000` on the host reaches it directly
with no port mapping.

## Authentication

Authentication depends on `phenix ui --jwt-signing-key` (`ui.jwt-signing-key`,
`PHENIX_UI_JWT_SIGNING_KEY`):

- **No signing key (the default, including the bundled Docker compose file):**
  auth is disabled. Every request is served as the built-in `global-admin` role
  and no token is required or checked. The server logs
  `no JWT signing key provided -- disabling auth` at startup.
- **Signing key set:** a JWT is required. Obtain one from `/login` (registered
  for both `GET` with HTTP basic auth and `POST` with a JSON body) and send it
  on every request in the `X-Phenix-Auth-Token` header as `Bearer <jwt>` — NOT
  the standard `Authorization` header, which is left free for proxy
  authentication. The token
  may also be passed as a `?token=<jwt>` query parameter, which is how browser
  links and image tags reach `/screenshot.png` and `/vnc/ws`.
- `--jwt-signing-key proxy-jwt` accepts JWTs minted by an upstream proxy, and a
  `dev|<user>|<role>` key enables development auth, treating every request as
  that user and role; any other `dev|` key answers every request with `500`.

With auth on, a request without a token gets `403`; a malformed, expired or
signed-out token, or a `--proxy-auth-header` user other than the token's,
`401`. A missing permission is `403`, but jobs, consoles and capture tokens of
other users answer `404`. Unknown `/api/v1` paths answer `404` and wrong methods
`405`, before auth; `OPTIONS` answers `204`, also before auth, without
running the route. Under `proxy-jwt` the token is read only from the header, and
a missing or malformed one is `400`. Behind a proxy with `--base-path` the proxy
strips it, and job and config `Location` headers include it.
`src/go/web/contract_test.go` pins these per route.

Every error, auth, RBAC and routing refusals included, is the JSON body
`{"message", "cause", "metadata"?, "details"?, "code"}` (WebShark's own `/webshark/`
routes, the health routes and a failed console, VNC, port forward or SCORCH
websocket handshake aside); match on
`code` (`invalid_request`, `unauthorized`, `forbidden`, `not_found`,
`conflict`, `rate_limited`, `unavailable`, `internal`, `broken_disks`,
`disk_in_use`, `vlan_in_use`), not the message. `broken_disks` is a 400 from a start (`POST
/experiments/{name}/start`, a workflow apply that starts or restarts) refused
because a VM disk image or CD-ROM image is missing, its backing chain is
broken, or qemu cannot open an image of the chain (`missing` is then empty); `details.disks` lists `{vm, drive, image, path, missing, problem}`
per disk, with full paths. `vlan_in_use` is a 400 from a start that failed
because minimega gives a pinned VLAN ID to another alias; `details.vlan` is the
ID. A bulk `vms.action` start or restart checks only
VMs that minimega launches again (state `QUIT`, `ERROR` or unknown), and fails
each such VM's item with `broken VM disk images: ...` instead. An
unexpected failure is `500` with `internal server error`, its cause only in
the server log.

```bash
TOKEN=$(curl -s -u admin:password http://localhost:3000/api/v1/login | jq -r .token)
curl -H "X-Phenix-Auth-Token: Bearer $TOKEN" http://localhost:3000/api/v1/experiments
```

Sign-in and sign-out, user, token, config and experiment changes, workflow
applies and config upserts, settings changes, app
triggers, SCORCH runs, VM power actions, VM PATCHes, optical disc changes,
netflow captures and their downloads, and the capture, capture token, WebShark, experiment file,
minimega console, VNC websocket, port forward websocket, disk, VM snapshot,
commit and mount routes log `AUDIT` events with ECS fields (`event.action`,
`event.outcome`, `user.name`, `source.ip`, ...) for successes, failures and
RBAC refusals, always written to the system log file whatever `log.level` is;
sign-in and sign-out log no `SECURITY` records. A request refused for its
token (missing, malformed, invalid, expired, revoked, ...) is a
`request-auth` failure, at most one a minute for each client address and
reason (`phenix.suppressed` counts the rest), never with the token; a
refused proxy JWT's unverified user is `phenix.auth.claimed_user`, not
`user.name`. Workflow
dry runs carry `phenix.dry_run` and `event.type` `info`, and requests over
the unix socket have no `user.name` or `source.ip` but `phenix.auth.method`
`unix-socket`. See `docs/content/logging.md` ("Audit Events") for the actions
and fields. A disk clone job and a bulk VM action job are audited when they
finish, as the user who started them, and an archive job's files when
`GET /jobs/{id}/artifact` downloads the archive.

## Routes

`src/go/web/public/docs/openapi.yml`, served rendered at `/docs/`, is the route
reference: every route `src/go/web/server.go` serves, websockets and routes
outside `/api/v1` included, with parameters, bodies, permissions and status
codes. `TestRouterMatchesOpenAPI` keeps the two in step, so read the spec
rather than guessing a path. The server also serves the raw spec at
`/docs/openapi.yml`, with `info.version` set to its build, and every `/api/v1`
response past authentication names that build in `X-Phenix-Version` (an
authentication refusal, and a `404` or `405` for an unserved path or method,
lack it). Points that are easy to miss:

- Outside `/api/v1`, at the server root: `/healthz` and `/readyz` (no token;
  see `docs/content/health.md`), `/version`, `/features`,
  `/downloads/tunneler` (with the `tunneler-download` feature) and the
  `/webshark/` routes, including `GET /webshark/streams/{token}`, whose `pcs_`
  stream token is its only credential.
- Feature-gated routes: the VM mount routes need the `vm-mount` feature, the
  `/console` routes `phenix ui --minimega-console`, and WebShark's view routes
  an installed WebShark.
- Boolean and enum query parameters are strict: a boolean takes `true` or
  `false` (also `1`, `t`, `0`, `f`), an enum only its listed values, and any
  other value, an empty value or a bare `?name` is `400` `invalid_request`
  naming the values it takes. An absent parameter takes its default. This
  covers, for example, `force`, `keepInjectionSnapshots`, `show_dnb`,
  `active`, `kind` and `sortCol`.
- Unmatched `/api/v1/*` requests return a JSON `404`; only non-API routes fall
  through to the SPA index. A route behind a disabled feature flag is therefore
  a real `404`, not `200 text/html`.
- VM power changes are `POST`: `.../vms/{name}/start`, `/stop`, `/restart`,
  `/shutdown`, `/reset` and `/redeploy`, or `POST /experiments/{exp}/vms/actions`
  for several VMs at once. `PATCH /experiments/{exp}/vms` changes several VMs'
  settings, carrying on past a failure, and lists each VM it skipped or failed
  in the response's `errors` (`403` if the role may patch none, `500` if all
  fail). Sign out with `POST /logout`.
- `PATCH .../vms/{name}` answers `409` for an `interface` change while the
  experiment is stopped (edit the topology). The bulk `PATCH
  /experiments/{exp}/vms` marks such a VM `failed`, and applies
  `inject_partition`, `tags` and `annotations` as the single-VM route does.
  `POST .../vms/{name}/redeploy` ignores a `name` in its body.
- phenix saves each VM's `cpus`, `ram`, `disk`, `inject_partition`,
  `snapshot`, `dnb` and `host` when it creates an experiment. It keeps them in
  the experiment annotation `phenix/vm-defaults`, which no create or update
  can set. A VM's `defaults` show them, or `null` when there are none, as for
  experiments created before phenix saved them.
  `POST /experiments/{exp}/vms/resetSettings` with `{"vms": [...]}` (empty or
  absent for every VM the role may patch) restores them in a stopped
  experiment. It needs `vms` `patch` on each VM, answers `409` while the
  experiment runs or another action holds it, and answers like `PATCH
  /experiments/{exp}/vms`. It locks the experiment and saves all the VMs in
  one change. A VM with
  no saved settings is `failed` with `no saved settings for this VM`, and the
  status stays `200`.
- Link quality (minimega `qos`) of a running experiment's VM:
  `GET .../vms/{name}/qos` answers `{"interfaces": [{"interface", "loss",
  "delay", "rate"}]}`, one entry per interface. `PUT
  .../vms/{name}/interfaces/{iface}/qos` takes `{"loss": "0.5", "delay":
  "100ms"}` or `{"rate": "10mbit"}`. A rate cannot go with a loss or a delay,
  and a request with no setting is `400`. A loss is a decimal from 0 up to
  100, a delay is a decimal in `us`, `ms` or `s`, and a rate is 1 to
  999999999 `kbit`, `mbit` or `gbit`. phenix clears the interface, then
  adds each setting. If minimega refuses a setting, phenix puts back the
  previous settings and answers `400` with minimega's message. `DELETE` on the same path clears it. They need `vms`
  `patch` on the VM and answer `409` for a stopped experiment.
- `GET /schedulers` lists the scheduling algorithms as `{"schedulers":
  [{"name", "builtin"}]}`: the built-in `isolate-experiment`, `round-robin`
  and `subnet-compute`, then each `phenix-scheduler-<name>` on the server's
  PATH. It needs `experiments/schedule` `create`.
  `POST /experiments/{name}/schedule` also takes the built-in names with `_`
  for `-`, such as `round_robin`.
- `POST /experiments/{name}/stop` that cannot kill the VMs leaves the
  experiment running and answers 502 with `metadata.retry` `force`;
  `?force=true` marks it stopped anyway.
- `POST /experiments/{name}/start` answers 409 while another phenix process,
  such as `phenix experiment start`, starts the experiment. The stored status
  key `starting` (`since`, `pid`, `host`) records that start, and the
  experiment list reports it as `starting` with `percent` 0.
- The start takes an optional JSON body with the options of
  `phenix experiment start`: `dryRun`, `treatMinimegaErrorsAsWarnings`,
  `honorRunPeriodically`, `vlanMin` and `vlanMax` (1-4094, min not above
  max). No body, or `{}`, answers `200` with the experiment, as before.
  `dryRun` or `treatMinimegaErrorsAsWarnings` answers `200` with
  `{experiment, dryRun, warnings, mmScript}`. A dry run leaves the experiment
  stopped. `honorRunPeriodically: true` answers `202` with the
  `experiments.periodic` [job](jobs.md). An unknown field, a bad range, or
  `dryRun` with `honorRunPeriodically: true` is `400`. A start that is not a
  dry run runs the scenario's `runPeriodically` apps in that job unless the
  body sets `honorRunPeriodically: false`. The web UI's start dialog sends
  these options.
- The experiment name `all` in `POST /experiments/all/start` and
  `POST /experiments/all/trigger` acts on every experiment the role may start
  or trigger, and answers `202` with an `experiments.start` or
  `experiments.trigger` [job](jobs.md) (`403` when the role may act on none).
- Experiment names `all`, `minimega`, `__phenix__` (any letter case), `.` and
  `..` are reserved: creating one (`POST /experiments`, `POST /configs`) is
  `400`.
- Through the API (`/experiments` and `/configs`), an Experiment's
  `spec.baseDir` must be a folder inside `<base-dir.phenix>/experiments`, else
  `400` `invalid_request`. An update may keep a stored base directory that is
  outside it. An API delete does not remove a base directory outside that
  folder (it logs a warning). The CLI has no such limit.
- `POST /configs/delete` with `{"kind": "<kind>|all"}` deletes every config of
  the kind that the role may delete, as `phenix config delete all [kind]`
  does, and answers `{deleted, failed}`. It leaves the config of a running
  experiment, and `403` when the role may delete none.
- `POST /minimega/commands` with `{"command", "namespace"?}` sends one command
  line to minimega, as `phenix mm` does, and answers `{command, output,
  responses, errors}`; a minimega error is still `200`. minimega expands
  `$NAME` in every argument from its own environment. It needs
  `minimega/commands` `create` (only Global Admin of the built-in roles) and
  is audited as `minimega-command`.
- `GET /runtimeSettings` and `GET /runtimeSettings/{key}` read the settings of
  `phenix ui` with their default and source (secrets `<redacted>`). `PUT` and
  `DELETE /runtimeSettings/{key}` change the config file as
  `phenix settings set|unset` do, and the answer's `restart` says whether the
  change waits for a restart. They need `runtimeSettings` `read`/`update`
  (only Global Admin) and are audited as `runtime-setting-change`.
- `GET /settings` and `POST /settings` (the web UI's Settings page) read and
  change the log file limits in `logging_settings` as the runtime settings
  `log.system.max-size`, `log.system.max-backups` and `log.system.max-age`:
  a change applies at once and is written to the config file. A limit that a
  flag or a `PHENIX_` variable sets, or no YAML config file, is `409`, and
  nothing changes.
- `GET /permissions` lists every resource phenix checks and its verbs, as
  `phenix util role-table` does (`roles` `list`).
- `POST .../vms/{name}/start` of a VM with `general.do_not_boot` that minimega
  did not launch answers 409; redeploy the VM to launch it.
- Paths use camelCase (`captureSubnet`, `memorySnapshot`, `captureTokens`).
- VLANs (resource `vlans`, resource names are experiment names):
  `GET /vlans` (`vlans` `list`) merges every experiment's pins and range with
  minimega's live aliases and blacklist into `{"vlans": [row], "experiments":
  [summary], "issues": [issue], "blacklist": [id], "minimega": {"ok", "read",
  "error", "blacklistRemoval"}}`. It filters with `experiment`, `vlan`,
  `state` (`pinned`, `allocated`, `blacklisted`) and `issues=true`, and
  `format=csv` answers `vlans.csv`, with an apostrophe before a text cell
  that a spreadsheet would read as a formula. A role sees only the rows of the
  experiments its names match. Only the name `*` sees the blacklist rows, the
  `blacklist` field and the unowned namespaces. An issue names a hidden
  experiment "another experiment". The minimega side is reused for 5 s while
  the same experiments run. `minimega.ok` false means phēnix side only.
  Also: `GET /vlans/{id}` (`vlans` `get`), `GET /vlans/free?count&min&max&outside_ranges=true`
  (`{"start", "end"}`, `404` when none fits), `POST /vlans/check` with
  `{"experiment", "aliases", "min", "max"}` (`{"issues"}`, saves nothing),
  `GET /vlans/blacklist` (`{"vlans", "removable"}`, `vlans` `list` on `*`),
  `PUT|DELETE /vlans/blacklist/{id}` (`vlans/blacklist` `create|delete` on
  `*`; `DELETE` answers `404` when not blacklisted, `409` when minimega
  refuses and `501` when minimega has no `clear vlans blacklist <vlan>`), and
  `DELETE /vlans/allocations/{namespace}` (`vlans/allocations` `delete` on
  `*`; `409` for an experiment's namespace or one whose VMs use its IDs).
  An experiment's VLANs: `GET /experiments/{name}/vlans` (`vlans` `get`),
  and with `experiments` `patch`: `PUT|DELETE .../vlans/aliases/{alias}`
  (body `{"id": 105}`), `PUT|DELETE .../vlans/range` (body `{"min", "max"}`)
  and `POST .../vlans/pin-live`. Pins and ranges answer `409` for a running
  experiment, and pin-live for a stopped one. A pin that another owner holds
  or minimega blacklists, or a range that overlaps another, answers `409`
  with `issues` unless `?force=true`. A pin while minimega does not answer
  also answers `409` unless `?force=true`. Each change publishes the websocket
  event `vlans` `update`.
- `POST .../vms/{name}/captures` names the interface by index in `interface`
  or by topology name in `interfaceName` (exactly one), and `DELETE` on the
  same path stops one capture with `?iface=<index>` or `?ifaceName=<name>`
  (at most one), or all without them. Pausing a VM leaves its captures
  running. `captureSubnet`, `captureVlan` and their stops take `vms` or
  `filter` (not both). A filter has the VM search syntax of the web UI, such
  as `running and web`, and a filter that matches no VM is `400`.
  `stopCaptureSubnet` with an empty or absent `subnet` stops every capture of
  the VMs.
- `POST .../vms/{name}/screenshots` saves a full-size PNG of the running VM in
  the experiment files, at `screenshots/<vm>-<YYYYMMDD-HHMMSS>.png` (UTC), and
  answers `201` with `{"path"}` relative to the files directory. It never
  replaces a file: a name that is taken gets `-1`, `-2` and so on. It needs
  `vms/screenshot` `create`, answers `409` when the experiment or the VM is not
  running, and is audited as `vm-screenshot`. The CLI has no equivalent.
- `DELETE .../vms/{name}/snapshots/{snapshot}` deletes a snapshot from every
  node (`vms/snapshots` `delete`). The experiment does not have to run, and a
  snapshot whose disk a running VM uses is `409`.
- `POST /experiments/{name}/captureVlan` and `.../stopCaptureVlan` take
  `{"vlan": "<alias>", "vms": [...]}`. They act on each interface on the VLAN,
  only of the VMs whose `vms/captures` `create` (start) or `delete` (stop) the
  role has. They answer `403` when the role has it for none, and `400` without
  a VLAN. Start returns the captures that started; stop returns the VMs.
- `POST /experiments/{name}/trigger?apps=...&stage=<stage>` runs one lifecycle
  stage: `configure`, `pre-start`, `post-start`, `running` or `cleanup`, or
  the shorthand `config`, `pre`, `post`, `run` or `clean`, in any letter case.
  An absent `stage` is `running`, and an empty one is `400`. With `stage`, it answers `400` for an unknown stage,
  an app the experiment lacks, or a default app's running stage, and `409` for
  the running stage of a stopped experiment. `DELETE` on the same path cancels
  the triggers of the apps `apps` names. Without `apps`, it cancels every app
  a trigger runs and the experiment's `experiments.periodic` job.
- `GET /experiments/{name}/scorch/pipelines` answers `200` with no pipelines
  for an experiment without a scorch app, and its `executing` lists the runs
  that this server executes and can cancel (not a run the CLI executes in
  its own process).
- An experiment that Builder published has `builder` (`draft`, `document`) from
  its `builder-experiment` annotation; other experiments send `null`.
- `GET /experiments/{name}/apps/input` answers the JSON a user app gets on
  stdin, as `phenix util app-json` prints it (with `hosts` and `vms`). It needs
  `experiments` `get`, `configs` `get` on `Experiment/<exp>` and `vms` `list`
  on `<exp>/*` (else `403` before any read) and on each `<exp>/<vm>`; `404`
  for an unknown experiment, `502` when minimega does not report every host
  and VM.
- `GET /schemas/{version}` returns the whole schema file (`v0` Workflow, `v1`
  Topology/Experiment/Image/User/Role, `v2` Scenario).
  `GET /schemas/{kind}/{version}` returns one kind, only at the version it is
  stored at; any other kind or version answers `404`.
- `GET /soh` (each summary) and `GET /experiments/{name}/soh` report
  `soh_disabled`: the scenario sets `disabled: true` on the soh app. Such an
  app does not run at post-start, so `soh_initialized` stays false until
  `POST /experiments/{name}/trigger?apps=soh` runs it.
- Bulk VM actions, `POST /disks/clone/jobs` and `POST .../files/archive` start
  [background jobs](jobs.md) and answer `202` with a `Location`.
- Workflow routes are covered in [`workflow.md`](workflow.md#rest-endpoints).
- `GET /disks` lists the files in the minimega files directory with a known
  extension (`.qcow2`, `.qc2`, `.qcow`, `_rootfs.tgz`, `.hdd`, `.iso`), every
  image a topology names, and their backing images. `POST /disks/snapshot`
  adds `.qcow2` to `new` unless it ends in `.qc2` or `.qcow2`. `/clone`,
  `/clone/jobs` and `/rename` keep the format, so `new` follows the extension
  of `disk`: `bar` becomes `bar.iso` for `foo.iso`, and `bar.qcow2` answers
  `400`. A `.qcow` image also takes `.qcow2` and `.qc2` and adds `.qcow`. For
  a `disk` with no known extension, `new` is named as for a snapshot.
- `GET /disks` marks an image qemu cannot open (a missing backing file, a
  loop, or more than 64 images) with `brokenChain`, `chainProblem` and
  `missingBacking` (full path; empty for a loop); `backingImages` then holds
  only the readable part. phenix reads the head node's files; no CLI command
  lists these fields.
- Each `GET /disks` image also has `format` (as qemu-img names it, such as
  `qcow2`, `qcow` or `raw`), `formatVersion` (the qcow2 or qcow version, else
  0) and `formatIssue`, or `null`. `formatIssue` holds `level` (`error` when
  qemu refuses the image, `warning` for an old format, or one whose chain
  phenix does not check), `problem`, `fix` and `command` (the qemu-img fix, or
  empty). They come from the image header, cached with the chain. An image
  that qemu-img cannot open for its version or feature bits is listed from
  its header alone.
- `DELETE /disks?disk=` refuses (`409`, code `disk_in_use`, `metadata.retry`
  `force`) an image that backs other images or that an experiment uses,
  directly or through a backing chain. `details.images` (full paths) and
  `details.experiments` (`{name, running}`) list those the role may see, and
  `details.hidden` counts the others. Send it again with `force=true` to
  delete the image anyway.
- `POST /disks/rename` needs `disks` `update` on `disk` and `create` on
  `new`. `POST /disks/rebase` needs `update` on `disk` and `get` on
  `backing`. `/commit` (the image and its backing image), `/resize` (the
  image) and a rebase with `unsafe=false` (both backing chains) answer `400`
  when an image they read or write is outside the minimega files directory.
  They also refuse a vmdk image or a qcow2 image with an external data file
  there, because such an image can name other files.
- `GET /disks/details?disk=` reads one image that `GET /disks` shows the
  role (`disks` `list` permission for its name). It returns the file sizes,
  the qcow header (`compat`, `clusterSize`, `refcountBits`, `encryption`,
  feature flags), `partitionTable` (`mbr`, `gpt`, `none` or `unknown`),
  `partitions` with their file systems, `overlays` and `vms`. A qcow or qcow2
  disk is read through its backing chain with `qemu-img dd -U`, at most
  16 MiB in 30 seconds; phenix never mounts it. `fileUnavailable` and
  `partitionsUnavailable` say why a part is missing. Partition numbers are
  those that `inject_partition` uses. No CLI command shows these fields.
- `GET /experiments`, `/vms`, `/disks`, `/jobs` and the experiment VM and
  file lists page with `pageNum` and `perPage` (both, from 1; `400`
  otherwise) and report the unpaged `total`; without them they return
  everything.
- `GET /experiments/{name}` lists only VMs carrying any repeated
  `label=key=value` (`400` without an `=` or a key) and running on any repeated
  `host=<host>` (`400` when empty); it and `/experiments/{name}/vms`
  sort by label count with `sortCol=labels`, by VLAN IDs (as numbers) or tap
  names in interface order with `sortCol=network` or `taps` (VMs with none
  last, ties by name), and by `cpus`, `ram` or `inject_partition` as numbers.
  `show_dnb` is a boolean. The VM list's `labels` and `hosts` hold every label
  and cluster host to filter by, its `running` counts the running VMs, and its
  `delayed` names the VMs that still wait for a delayed start, all
  before filtering and paging. The websocket `experiment/vms` `list` request
  takes the same filters as `labels` and `hosts`.
- Slow `/ws` clients are closed with code 1013 and should reconnect; the live
  log stream may skip lines under load.

When a config fails its schema, the config routes (`POST /configs`,
`PUT /configs/{kind}/{name}`) and the workflow routes answer 400 with the
explained lines in `metadata.validation`, one per line, and the validator's
text in `metadata.validation-raw`; see [`workflow.md`](workflow.md).

`POST /users` and `POST /signup` answer `409` `user <name> already exists` for
a name another user has, and `500` `error creating user` for other failures.
`POST /users` answers `400` `role not found` for an unknown role before it
creates anything.
Both answer `400` `invalid username ...` for a username that does not start
with a letter or digit, or that has a character other than letters, digits,
`.`, `_`, `@`, `+` and `-`, and create no user.

Roles: `GET /roles` and `GET /roles/{name}` (`roles` `list`, `get`) read them;
`{name}` is the config name or the display name. Only Global Admin holds
`roles` `create`, `patch` and `delete`, and the requester must also hold every
permission the policies grant (an unnamed policy counts as `*`), else `403`
listing them, also when `POST /users` or `PATCH /users/{username}` assigns a
role (beyond what the user already holds). A custom role cannot grant `roles`
`create`/`patch`/`delete` (`400`). Bodies refuse unknown fields (`400`), are at
most 1 MiB (`413`), 256 policies and 64 items per list.

- `POST /roles` `{"name", "metadataName"?, "policies": [...]}` answers `201`
  with the role and `Location: .../roles/<metadataName>`; `metadataName`
  defaults to a slug of the name (`Lab Admin` -> `lab-admin`). `409` when either
  name is a name of another role or a built-in role (case-insensitive).
- `PATCH /roles/{name}` `{"policies": [...]}` (may be `[]`; `name`, if sent,
  must be unchanged) answers `200` `{"role", "users", "count"}`: it also
  rewrites the copy of the role each user holds, keeping the user's resource
  names and own `users` policy, and closes their websockets (1012). `users`
  lists only users the requester may list. A `500` with `details.users` names
  users not updated; send the request again.
- `DELETE /roles/{name}` answers `204`, or `409` for a built-in role, one that
  `ui.users` or a `dev|<user>|<role>` key names, or one that users have
  (`details.users`, filtered as above).
- Built-in roles (from `api/config/default/`) cannot be changed: `409`.

Config revisions: `GET /configs/{kind}/{name}` sends `ETag: "r<revision>"`
and `X-Phenix-Revision` (none for a config with no stored revision; the ETag stays strong when gzipped) and answers `If-None-Match` with
`304`. `PUT` and `DELETE` take an optional `If-Match` (`"r<n>"` list or `*`):
`412` with `metadata.revision` when the config moved on, `400` when malformed.
An unknown config is `404` (a `PUT` or `DELETE` with `If-Match`: `412`;
`GET` ignores `If-Match`).
`POST` and `PUT` return the new `ETag`. `GET /configs` returns `[]`, never
`null`.

Config RBAC names are `Kind/name` (`Topology/demo`). `POST /configs` and the
create branch of `POST /workflow/configs/{branch}` check `configs` `create`
against the new config's `Kind/name`, and a `PUT` that renames a config or
changes its kind also needs `update` and `create` for the new one; otherwise
`403`.
A `User` config comes back from `GET /configs/{kind}/{name}` and
`POST /configs/download` without `password` and `tokens` (the ETag is still the
stored revision), and `PUT` or the workflow upsert keeps the stored ones,
ignoring any in the body.

Ordering: every `/api/v1` route's response past auth (not a `401`/`403` auth
refusal or an `OPTIONS` answer) has `X-Phenix-Seq` (taken as the request
started) and every `/ws` message a `seq` (taken as it was published; a VM list
reply's before its minimega read), from one strictly increasing sequence per
`phenix ui` process. Data tagged S reflects every change whose message has a
seq of S or less; keep the highest seq applied per item, ignore lower, and
reset on websocket reconnect. Runtime reads add `X-Phenix-Observed-At` (when
minimega was read; compare with `Date`) and `X-Phenix-Stale: true` (earlier
data while minimega is busy). A minimega failure is never an empty success:
VM lists set `runtime_unknown` (`vms_unknown` per experiment) and list
unreported VMs as configured, `/soh` has `vms.known`/`vms_stale`, file listings
answer `502`, and `GET /experiments` answers `500` when the store is
unreadable (and is `X-Phenix-Stale` when it leaves out unreadable experiments). See `docs/content/api.md#freshness`.

## Downloads

Single-file downloads stream from disk and resume:
`GET /experiments/{name}/files/{filename}?path=`, `GET /disks/download?disk=`,
the VM mount's `GET .../files/download?path=`, `GET /downloads/tunneler/{name}`,
a saved capture's `GET /webshark/captures/{name}`, a recorded netflow
capture's `GET /experiments/{name}/netflow/captures/{id}?format=text` and the
log files' `GET /logs/files/{phenix|minimega}` (`logs` `get`; `GET /logs/files`
says which can be downloaded and why not; audited as `log-download`). Each also answers `HEAD`,
and sends `Accept-Ranges: bytes`, `Content-Length`, a
strong `ETag` (from the file's inode, size and nanosecond modification time,
never its contents) and `Last-Modified`, with `Cache-Control: private,
no-cache`. `Range` gives a `206` (or `416` with `Content-Range: bytes */<size>`),
and `If-Range` with the `ETag` resumes only while the file is unchanged,
answering a changed file with the whole new one (`200`). `If-None-Match` gives
`304`. Downloads are attachments and are never gzip-compressed; a compressed
API response drops `Accept-Ranges` and `Repr-Digest` and weakens its `ETag` to
`W/"..."` (except a config's revision ETag).

With `Accept: text/plain` the same route is the Files tab's viewer: inline,
`text/plain; charset=utf-8`, compressible, and `413` for a file over 10 MiB.
The `file-download` audit event fires once per download (a `GET` answered
`200`, or `206` from byte 0), with the `Range` asked for as
`http.request.range`.

`POST /experiments/{name}/files/download` and `POST /configs/download` build a
zip as they send it, and `GET /experiments/{name}/netflow/captures/{id}` with
`format=csv` or `format=jsonl` converts the capture as it sends it:
`Accept-Ranges: none`, `Cache-Control: no-store`, and no resume. List the
recorded netflow captures with `GET /experiments/{name}/netflow/captures`; a
capture over `ui.netflow.max-size` (MiB, `phenix ui` restart) is `truncated`. Download very large files one at a time.

```bash
curl -C - -o out.pcap -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  "http://localhost:3000/api/v1/experiments/exp/files/out.pcap?path=scorch/run-0/out.pcap"
```

Each time a SCORCH run finishes it archives `scorch/run-<run>/` to
`scorch-run-<run>_<start, 2006-01-02T15-04-05Z0700>.tgz` at the top of the
experiment's files, one archive per execution; the SCORCH runs page's
`Download` menu lists them from `GET /experiments/{name}/files` and downloads
them with this route.

`curl -C -` sends only `Range`, not `If-Range`, so it splices a file that
changed between attempts, such as a disk image a running VM writes to.
Verify with a checksum: `GET /experiments/{name}/files/{filename}/checksum?path=`
and `GET /disks/checksum?disk=` (same permission as the download) answer `202`
with `Retry-After` while the SHA-256 is computed in the background (two files at
a time server-wide, kept in memory while the file is unchanged), then `200`
with `{path, size, modified, etag, sha256}`; `409` if the file changed while
hashed. Once known, downloads of the file also send it as `Repr-Digest:
sha-256=:<base64>:` (RFC 9530), valid for ranges too.

A running capture in WebShark (`/webshark/captures/{name}` while live) sends no
`ETag` or `Last-Modified` and `Cache-Control: no-store`: a range is still the
bytes captured so far, but a resume with `If-Range` gets the whole capture
again. `GET .../captures/{iface}/stream` is a live stream with no ranges.

## Experiment captures

An experiment capture records a running experiment's traffic on all its hosts
into one file in the experiment's files directory on the head node. It needs
minimega 3.4.0 or later on every node. The RBAC resource is
`experiments/captures`, and the resource name is the experiment. Of the
built-in roles, only `global-admin` has `create` and `delete`. The routes are
in `openapi.yml`:

| Method and path | Verb | Answer |
|---|---|---|
| `GET /experiments/{exp}/captures` | `list` | VM interface `captures`, plus `experimentCaptures`, `support`, `budget`, `budgetError` and `experimentCapturesError` |
| `POST /experiments/{exp}/captures` | `create` | `201` `{capture, warnings}` |
| `GET /experiments/{exp}/captures/{id}` | `get` | one capture with `streams`, `404` when minimega does not list it |
| `DELETE /experiments/{exp}/captures/{id}` | `delete` | `200` with the last counters, and the file stays |
| `GET /experiments/{exp}/captures/{id}/stream[?from=now]` | `get`, and `experiments/files` `get` | websocket binary messages or a chunked body |
| `POST /experiments/{exp}/webshark` with `{"capture": "<id>"}` | as for the stream | WebShark on the merged file, title `exp1 capture 3 (live)` |

- The start body is `{"vlans", "filter", "snaplen", "name", "format"}`, all
  optional. Empty `vlans` is every VLAN (aliases, matched without case),
  `snaplen` 0 is 1600 (else 14 to 65535), `format` is `pcapng` (default) or
  `pcap`. The filter allows only letters, digits, spaces and
  `. : / [ ] ( ) & | ! < > = + - * ,`, at most 1024 bytes. The name becomes `[A-Za-z0-9._-]` with the format's extension.
  The default name is `<exp>[-<vlan>]-<YYYYmmdd-HHMMSS>.pcapng`, with `-2`,
  `-3` ... when a file or running capture has it. phenix adds `maxsize` from
  `ui.captures.max-size` (MiB, default 10240, 0 no limit, restart).
- Start status codes: `400` invalid input, `409` experiment not running, the
  mirror budget refuses (`compute1 / mega_bridge_exp1 has 32 of 32 Open
  vSwitch mirrors`, or 32 captures already run), or a running capture or a
  file has the given name, `501` no minimega support. A `409` or `400` sends
  nothing to minimega.
- Mirror budget: Open vSwitch applies 32 mirrors per bridge and ignores a 33rd.
  `budget` rows are `{host, bridge, total, experiment, limit, level}` for each
  host and bridge the experiment's VMs use. `level` is `full` (total >= 32),
  `near` (>= 28), `half` (experiment > 16) or `ok`. `warnings` name the levels
  that the new capture passes. A mirror count that fails starts the capture
  with `cannot count Open vSwitch mirrors: <err>`.
- `support` is `{available, reason, version}`, for example reason
  `requires minimega 3.4.0 or later; the head node runs 3.3.0`. `phenix ui`
  reads the head node's version once at startup, asking every 10 s until
  minimega answers, and never again: restart it after a minimega upgrade.
  `ui.features` `minimega-captures` forces support on.
- A capture has `id`, `state` (`starting`, `running`, `stalled`, `stopping`,
  `stopped`, `failed`, `orphaned`, `full`), `running`, `path`, `file`
  (relative to the experiment files, for their download and delete), `format`,
  `vlans`, `filter`, `start`, `size`, `error`, `counters` and `streams` (one per
  host and bridge, with `skewWarning` when the skew is over 500 ms).
  `counters` has `ports`, `packets`, `bytes`, `kdrops`, `pdrops`, `qdrops`,
  `lost`, `late`, `lagMs` and `skewMs`. 64-bit numbers are decimal strings.
- Start and stop publish an `experiment/capture` resource on `/ws`, named
  `<exp>/<id>` with action `start` or `stop`. An experiment stop sends one
  `stop` named `<exp>/*`. The audit log records them as
  `capture-start` and `capture-stop` with `phenix.capture.id`.
- A pcapng stream with `from=now` starts with the section header and the
  interface blocks. Its `frame.interface_name` is `<host>/<bridge>`.
- `POST .../vms/{name}/captures` and `POST .../captureVlan` take an optional
  `format` (`pcapng` default, `pcap`), and the file extension follows it.
  minimega before 3.4.0 writes only pcap.

## minimega Console

The console routes need `phenix ui --minimega-console`. `POST /console` starts
`phenix mm --attach` on a pty, with `TERM=xterm-256color` so that minimega's
command history and completion work, and answers `{"pid": N, "namespace": ""}`.
`POST /console?namespace=<experiment>` adds `--namespace=<experiment>`. The
experiment must exist, and the role must have `experiments` `get` on it;
otherwise the answer is `400`, because minimega makes a namespace that does
not exist. The console belongs to the user who started it, and every other
console route answers 404 for another user's pid. `GET /console/{pid}/ws` sends up to 256 KiB of recent output, then
live output, as binary frames, and what the client sends is the console's
input. Several websockets can attach at once; closing one leaves the console
running, so the UI keeps it across page changes, reloads and tabs. A console
ends when its process exits (the server then closes its websockets with code
1000), on `DELETE /console/{pid}` (the UI sends it on logout), or once no
websocket has been attached for 15 seconds. `GET /console/{pid}` checks that it
is still running, and answers its pid and namespace. Starting, resizing and
ending need `miniconsole` `post`; checking and attaching need `miniconsole`
`get`.

## VNC Console

`GET /experiments/{exp}/vms/{name}/vnc` (`vms/vnc` `get`) answers the VNC
session of a VM as JSON, with no-cache headers; the web UI's `/vnc` console
page, live tiles and details window read it, then connect noVNC to its
`websocket`:

```json
{"experiment": "demo", "vm": "web",
 "websocket": "experiments/demo/vms/web/vnc/ws",
 "banners": {"top": {"lines": ["EXP: demo - VM: web"], "backgroundColor": "white", "textColor": "black"},
             "bottom": {"lines": ["EXP: demo - VM: web"], "backgroundColor": "white", "textColor": "black"}},
 "display": {"vga": "std", "remoteResize": false},
 "clipboard": {"mode": "pasteAsKeys"}}
```

- `websocket` is relative to the API root (`<base path>api/v1/`); add
  `?token=<jwt>` when auth is on. Banners come from the `vncBanner` annotation
  (`annotations.md`); `lines` is `[]` for a banner that is off, and the lines
  are plain text.
- `display.remoteResize` is true only for `vga` `virtio`. `clipboard.mode` is
  `bidirectional` when minimega's `bidirectional-copy-paste` is on for the VM,
  else `pasteAsKeys`.
- `?probe=true` also dials the VM's VNC port (3 s timeout); a failed dial
  answers `502` with code `vnc_unreachable` and `details.host` and
  `details.port`. The UI probes after a websocket failure, because a websocket
  handshake cannot carry the HTTP error.
- `404` for a VM not in the topology, `409` (`VM is not running`) when minimega
  has no KVM VM of that name, `500` (`minimega is too old: vm info has
  no vga column, update minimega`) for a minimega without the `vga` column of
  `vm info`, which phēnix needs. A refusal is audited as `vnc-attach`.
- The old HTML VNC page, `/novnc/` and `/xterm.js/` are gone; noVNC is an npm
  dependency of the UI build.

## Creating an experiment

`POST /experiments` takes JSON and answers `204 No Content`; websocket clients
then get an `experiment` `create` event carrying the new experiment.

```json
{
  "name": "exp1",
  "topology": "example",
  "scenario": "example",
  "disabled_apps": ["soh"],
  "vlan_min": 100,
  "vlan_max": 200,
  "deploy_mode": "no-headnode",
  "default_bridge": "exp1",
  "workflow_branch": "main",
  "annotations": {"phenix.workflow/tags": "run=nightly"},
  "node_annotations": {
    "phenix/default-apps": false,
    "phenix/startup-autotunnel": ["8080:80"]
  }
}
```

Only `name` and `topology` are required. `annotations` become the experiment's
metadata annotations: `topology` and `scenario` are set by phenix and rejected,
and `phenix.workflow/branch` comes from `workflow_branch`, so a request may not
give both. `node_annotations` are added to every VM in the experiment's own
copy of the topology (the Topology config is unchanged), keep their JSON types,
and never replace a VM's own value for the same key. A value of the wrong type
for an annotation the default apps read (see [`annotations.md`](annotations.md))
is rejected with `400` naming the key. Giving `phenix/startup-autotunnel` in
`node_annotations` needs `vms/forwards` `create` on every VM the experiment
deploys (external nodes excluded), or the request gets `403`, because the
startup app creates those forwards.

## VM OS type

Every VM object the REST API and websocket VM updates send carries `os_type`,
its topology node's `hardware.os_type` (`linux`, `windows`, `vyos`, ...), left
out when the node gives none.

## VM annotations

`GET /experiments/{exp}/vms/{name}` includes the VM's own `annotations` from the
experiment's copy of the topology; VM lists and websocket VM updates carry
`null` instead. `PATCH /experiments/{exp}/vms/{name}` with
`{"annotations": {...}}` replaces the whole set (`{}` removes them all), also
while the experiment is running, where only `interface` (the VLAN it connects
to), `tags` and `annotations` may change. Values keep their JSON types, and the
same type checks as creation reject a bad value with `400` naming the key. The
response carries the saved annotations only when the role may also `get` the
VM. Setting `phenix/startup-autotunnel` to a value the VM does not already have
needs `vms/forwards` `create` on that VM, or the request gets `403`.

```json
{"annotations": {"phenix/startup-autotunnel": ["8080:80"], "vncBanner": "lab"}}
```
