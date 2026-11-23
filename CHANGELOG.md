# [2026.11.23](https://github.com/sandialabs/sceptre-phenix/compare/v2026.11.12...v2026.11.23) - 2026-11-23

### ❌ Breaking

_(cli)_ [**breaking**]: add variables from flags and env files [_(d4af5860)_](https://github.com/sandialabs/sceptre-phenix/commit/d4af5860d7b614c64ef999950184d5035167e308) ([PR 576](https://github.com/sandialabs/sceptre-phenix/pull/576))

> phenix workflow apply, config create, config apply, config diff and experiment
> create fill in the ${NAME} placeholders of their files from -e/--env, then
> from the env files of --env-file, then from the shell without its PHENIX_
> variables. workflow apply also reads the .env file of the topology directory.
> experiment create also fills in a topology file that includeTopologies names
> by a path. The new phenix workflow render prints each config after
> substitution and does not contact the server. $${NAME} gives the literal text
> ${NAME}. The server gets the branch name as a parameter, so requests for
> different branches do not mix their branch names.
> 
> A variable with no value and no default, a value or a default with a line
> break, and a -e variable that no file uses are errors in the CLI. The three
> config commands take files only: they refuse a directory and a Builder file,
> and config apply also takes its files as arguments. The API error text
> "invalid workflow name" is now "invalid branch name", and
> store.NewConfigFromFile and config.CreateFromPath are removed.
> 
> Closes sandialabs/sceptre-phenix#461

> add variables from flags and env files

[**breaking**]: caffeinated phenix [_(15519107)_](https://github.com/sandialabs/sceptre-phenix/commit/155191074c4eff154fc2176a28a50befbe8629a0) ([PR 447](https://github.com/sandialabs/sceptre-phenix/pull/447))

> Make the web UI fast and keep minimega from becoming the bottleneck: pages show cached data at once and refresh in the background, VM listing takes a fixed number of minimega commands, and screenshots are taken only for VMs someone is viewing. Add State of Health, WebShark with live capture streaming, nested and outside disk images, disk backing chain and qcow checks before a start, VM annotation editing, a persistent minimega console, jobs with feedback, audit events, health endpoints, graceful shutdown, JSON errors with stable codes, `phenix doctor` with a redacted `--report`, CLI commands for disks, experiment files, logs, users and runtime settings with `-o` output formats and `--dry-run`, the experiment name in State of Health logs, a generated CLI reference, and CI checks for contracts, performance budgets, vulnerabilities and SBOMs, along with security and bug fixes.
> 
> Also add experiment packet captures, a new VNC console with VM tools, live tiles and a split view in place of the vendored noVNC page, the steps and the step times of an experiment start, stop and delete, a pool of read-only minimega connections, schedule warnings, pinned VLANs and ID ranges on the VLANs page, and confirmation of VM actions in the web UI.

> caffeinated phenix

### 🚀 Features

- _(api)_ accept variables in workflow requests [_(7ed8b1aa)_](https://github.com/sandialabs/sceptre-phenix/commit/7ed8b1aad219bc9d6d332c216fc345ca030cb430) ([PR 576](https://github.com/sandialabs/sceptre-phenix/pull/576))

- Adding explicit launchability semantics for topology nodes [_(714c30fc)_](https://github.com/sandialabs/sceptre-phenix/commit/714c30fc8997093b7561f9620e62c17b6620df21) ([PR 589](https://github.com/sandialabs/sceptre-phenix/pull/589))

- _(startup)_ support phenix/startup-set-hostname annotation [_(64dad8d9)_](https://github.com/sandialabs/sceptre-phenix/commit/64dad8d90dc70795dbf7f7abd1b80888cfd25e9b) ([PR 581](https://github.com/sandialabs/sceptre-phenix/pull/581))

### 🐛 Bug Fixes

- _(soh)_ tolerate PowerShell progress output in Windows listener checks [_(0f8f8cac)_](https://github.com/sandialabs/sceptre-phenix/commit/0f8f8cacfa8e049bd562e431a667c5966822378a) ([PR 595](https://github.com/sandialabs/sceptre-phenix/pull/595))

- _(plog)_ append to a new log file so other phenix processes' lines survive [_(8e3bfbe8)_](https://github.com/sandialabs/sceptre-phenix/commit/8e3bfbe860dba3bfd95e545aca50591cc41343ca) ([PR 600](https://github.com/sandialabs/sceptre-phenix/pull/600))

- _(cli)_ remove the response body from the debug log [_(e9e61dae)_](https://github.com/sandialabs/sceptre-phenix/commit/e9e61daefedf5f1566ce9889a559eccb3d95d5c5) ([PR 597](https://github.com/sandialabs/sceptre-phenix/pull/597))

- _(inject)_ match Windows inject paths regardless of case [_(e9202a64)_](https://github.com/sandialabs/sceptre-phenix/commit/e9202a64a4d3600738fb68d1d920406ca4c58387) ([PR 570](https://github.com/sandialabs/sceptre-phenix/pull/570))

- _(topology)_ require external nodes to be explicitly true [_(8e093eb5)_](https://github.com/sandialabs/sceptre-phenix/commit/8e093eb5ab68e5f4d0ff83e25fd8709beef8cb59) ([PR 590](https://github.com/sandialabs/sceptre-phenix/pull/590))

- _(image)_ restore nested template assets into new directories [_(4847da55)_](https://github.com/sandialabs/sceptre-phenix/commit/4847da55706d2a5823ddb8d01713d70d3d0aeccc) ([PR 569](https://github.com/sandialabs/sceptre-phenix/pull/569))

- _(startup)_ stop adding the auto-logon user to Administrators [_(1367f8bd)_](https://github.com/sandialabs/sceptre-phenix/commit/1367f8bd1a95d343aaaf6e40009326419b66a2ba) ([PR 568](https://github.com/sandialabs/sceptre-phenix/pull/568))

- _(startup)_ run the Windows startup task once per boot [_(b3839fa5)_](https://github.com/sandialabs/sceptre-phenix/commit/b3839fa519aceaab10c0fedc21440c336e662792) ([PR 567](https://github.com/sandialabs/sceptre-phenix/pull/567))

- _(soh)_ send Windows listener checks as an encoded command [_(4cc10986)_](https://github.com/sandialabs/sceptre-phenix/commit/4cc1098633489b44fce756532822a6688436d7ea) ([PR 584](https://github.com/sandialabs/sceptre-phenix/pull/584))

- _(vm)_ apply deletions when redeploying with replicated injects [_(a42fb98d)_](https://github.com/sandialabs/sceptre-phenix/commit/a42fb98d08a5afb386f407b72d6e255f419bc23d) ([PR 585](https://github.com/sandialabs/sceptre-phenix/pull/585))

### 📚 Documentation

- document caffeinated phenix and refactor docs [_(86fd7323)_](https://github.com/sandialabs/sceptre-phenix/commit/86fd73236988c7a1f40d672eedb5db2cd42904ad) ([PR 553](https://github.com/sandialabs/sceptre-phenix/pull/553))

### 🧹 Chore

- _(ci)_ add releasaurus to automate releases and changelog [_(d19dfdb9)_](https://github.com/sandialabs/sceptre-phenix/commit/d19dfdb9bacd82aeac221666e08da05dd8b2dc55) ([PR 601](https://github.com/sandialabs/sceptre-phenix/pull/601))

- _(deps)_ bump the npm group deps which are safe [_(f7528604)_](https://github.com/sandialabs/sceptre-phenix/commit/f7528604f8c573d387bfa65100b508c9abaabc58) ([PR 586](https://github.com/sandialabs/sceptre-phenix/pull/586))

- _(deps)_ bump the pip group in /docs with 2 updates [_(58a6ac31)_](https://github.com/sandialabs/sceptre-phenix/commit/58a6ac31147e5216f8f4bd20d952d1f75f8f14db) ([PR 573](https://github.com/sandialabs/sceptre-phenix/pull/573))

## Before automated releases

Release notes above this section are generated from the Conventional Commit
subjects on `main` when each release is made. The notes below were written by
hand before releases were automated.

## [2026.11.12] - 2026-11-12

### Fixed

- **Startup App DNS**: On Linux VMs running `systemd-resolved`, the startup app sets the topology's DNS servers in `/etc/systemd/resolved.conf.d/phenix.conf` instead of appending them to `/etc/resolv.conf`, which `systemd-resolved` rewrites when interfaces change.

## [2026.11.04] - 2026-11-04

### Added

- **Web UI Help**: Added a Help link to the documentation in the footer.
- **Image Releases**: `phenix image create` recognizes the Debian releases `trixie` and `forky`.
- **SCORCH**: Support the `cleanup` stage in the built-in `tap` component for reliable tap teardown on run completion or cancellation.
- **Workflow CLI**:
  - Added `phenix workflow apply <DIR|NAME>`, which deploys a topology directory (`phenix-configs/`, `phenix-injects/` and `phenix.yml`) in one command. `-n, --dry-run` validates everything and changes nothing, and `-f, --force` allows restarting a running experiment. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
  - Added `--follow-inject-symlinks` to `phenix workflow apply` to follow symbolic links in `phenix-injects/` and copy targets as regular files and directories (`cp -aL` behavior).
- **Settings**: Added `base-dir.injects` and `base-dir.topologies`, the directories `phenix workflow apply` stages injects in and looks up topology directories in. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
- **Workflow API**: The workflow endpoints accept `?dryRun=true`, which validates the request and changes nothing, and return a JSON result. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
- **Validation Errors**: Config validation errors from the CLI, the API and the web UI name the list item, its hostname or name, and the line. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
- **CLI**: `phenix vlan alias <experiment> <alias>` now prints the VLAN ID of that alias, or an error if the experiment has no such alias or the alias has no VLAN ID yet, instead of rejecting the arguments. Its shell completion also completes experiment and alias names.
- **CLI**: Added `phenix minimega` as an alias for `phenix mm`, and `-a` and `-n` as shorthands for `--attach` and `--namespace`.
- **CLI**: Added tab completion for `phenix mm`, which will now suggest minimega commands from the running minimega and `--namespace` from the names of running experiments.

### Changed

- **Web UI Accessibility**: The web UI now meets more WCAG 2.2 AAA criteria, with 7:1 text contrast, headings, item names in page titles, keyboard access to all controls, and less motion when the user asks for it. The browser tests now also fail on axe-core AAA violations and scan pages that show data.
- **Image Defaults (breaking)**: `phenix image create` defaults `--release` to `noble`. Debian releases now default to the Debian components `main contrib non-free non-free-firmware` instead of the Ubuntu ones.
- **Image miniccc Service**: `miniccc` systemd service created by `phenix image {build|inject-miniexe}` now sets `User=root` and `HOME=/root`, so commands run through `cc exec` have an environment.
- **Workflow API**: A workflow apply is validated before a running experiment is stopped, so an invalid one returns 400 or 409 instead of 500 and leaves the experiment running. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
- **Workflow configs**: `POST /api/v1/workflow/configs/{branch}` refuses a config whose kind or name uses a placeholder other than `${BRANCH_NAME}`. ([#436](https://github.com/sandialabs/sceptre-phenix/pull/436))
- **Workflow Schema (breaking)**: Workflow configs are validated against the new `Workflow` schema, so unknown or misspelled keys, values of the wrong type and a missing `spec` return 400 instead of being ignored. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
- **CLI**: `phenix mm` now stops parsing its own flags at the first minimega argument, so flags meant for minimega (for example the `-a` in `phenix mm cc exec ls -a`) are passed through. `phenix mm` and global flags must now come *before* the minimega command.
- **CLI**: Upgraded cobra from v1.0.0 to v1.10.2 and pflag from v1.0.3 to v1.0.9. `phenix completion bash` now generates cobra's V2 Bash script, and `phenix completion powershell` includes descriptions.

### Removed

- **Topology v0 (breaking)**: Topology configs with apiVersion `phenix.sandia.gov/v0` are no longer supported.
- **Scenario v1 (breaking)**: Scenario configs with apiVersion `phenix.sandia.gov/v1` are no longer supported.
- **Config CLI and API (breaking)**: Removed the `--show-upgraded` (`-u`) flag of `phenix config get` and the `noupgrade` query parameter of `GET /api/v1/configs/{kind}/{name}`.

### Fixed

- **Web UI Accessibility**: Fixed WCAG A and AA failures that only showed with data: controls that worked only with a mouse, controls with no name, low-contrast colored buttons and tags, dialogs without a dialog role or focus return, and a keyboard trap in the terminal.
- **Web UI Idle Timeout**: A warning time longer than the idle timeout no longer delays the logout past the timeout, and Settings rejects a warning time that is not shorter than the timeout.
- **Web UI**: The multi-VM toolbar of a running experiment no longer shows two Restart buttons.
- **CLI**: `phenix mm` exited 0 even when minimega reported an error or the connection to minimega was lost. It now logs the error, prints it to stderr, and exits non-zero.
- **CLI**: `phenix mm` sent empty arguments and arguments containing `#`, quotes, or backslashes to minimega unquoted, so minimega dropped or misread them. Combining `--namespace` with a whole command passed as one string, such as `phenix mm -n my-exp "vm info"`, also failed.
- **CLI**: Zsh and PowerShell shell completion only completed command and flag names, never values such as experiment, VM, or config names, because the scripts generated by cobra v1.0.0 were static. They now complete the same values as Bash and Fish.
- **Web UI**: The Configs page shows validation errors for uploaded configs again instead of failing silently. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
- **Workflow API**: `POST /api/v1/workflow/configs/{branch}` returns 400 for a config with a missing or unknown kind, or with `/` in its name, instead of dropping the connection. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
- **CLI**: Every `phenix` command waits at most one minute for the server's options over `--unix-socket` instead of hanging on a server that never answers. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
- **Startup App**: Switch C2 startup script execution (`phenix/startup-via-cc`) from `cc exec-once` to `cc exec` so commands are delivered when the `miniccc` client connects and re-run on VM reboots.

### Security

- **API Authentication**: The auth middleware let any request whose path ended in `/login` or `/signup` through without a token, which included routes such as `/api/v1/experiments/{name}` and `/api/v1/configs/{kind}/{name}` when the last path variable was `login`. Only `/api/v1/login`, `/api/v1/signup`, and the file server `/login` are now public. Signing in with an unknown username now returns the same 401 as a wrong password, after the same bcrypt work, instead of a 404 that echoed the username. With proxy authentication the 404 still carries the proxied username, which the web UI uses to start sign-up.
- **Workflow configs**: `POST /api/v1/workflow/configs/{branch}` no longer exposes server environment variables in its errors to callers who may not write that config, and checks the role's config names when creating one. ([#436](https://github.com/sandialabs/sceptre-phenix/pull/436))

## [2026.10.02] - 2026-10-02

### Added

- **Topology Builder**:
  - **Topology Endpoints**: Added `POST /api/v1/builder/topologies` and `PUT /api/v1/builder/topologies/{name}` to create and save a topology and its diagram without touching an experiment; both refuse the write while an experiment built from that topology is running.
  - **Default Disk Images**: Added the `$DEFAULT_VM_IMAGE` and `$DEFAULT_ROUTER_IMAGE` experiment variables, which set the disk image new VM and router nodes are created with.
  - **API Documentation**: Documented the Builder routes in `openapi.yml`, which previously carried none of them.
- **Documentation Sources**: Consolidate the phēnix MkDocs site into this repository so documentation changes can ship with the code they describe.
- **Tunneler Web Interface**: Add a local web dashboard for listing, enabling, disabling, and moving listeners, with live WebSocket updates.
- **Experiment Lifecycle Triggers**: Added `phenix exp trigger` for manually invoking app lifecycle stages. Deprecated `phenix exp trigger-running`; use `phenix exp trigger running` instead. The command errors out if a given app isn't part of the experiment or if the requested lifecycle stage isn't applicable to it, and shell completion only suggests apps applicable to the given experiment and stage.

### Changed

- **Web UI Accessibility**: Declared the page language, added accessible names to icon-only buttons, links, and form controls, labelled the config selection checkboxes, made the log viewer keyboard-scrollable, added a visible keyboard focus indicator, a skip link, per-route page titles, and pagination control names, fixed low-contrast placeholder, danger, and code colours, made the Settings form submit on Enter, and added an axe-core WCAG 2.2 AA scan of every route to the browser smoke tests.
- **CLI / Web UI**: Display the release version or source branch alongside the commit hash and build timestamp in the version output and footer.
- **Topology Builder**:
  - **mxGraph**: Updated the vendored mxGraph from 4.1.0 to 4.2.2, the final release before the project was archived. Upstream changed the modifier that deletes a cell together with its connected edges from Shift to Ctrl.
  - **Editor**: The sidebar node palettes now open expanded, the export dialog offers only the XML and SVG formats the server can produce, and the Help button opens the phēnix documentation instead of the defunct `minimega.org`.
- **Topology validation**: Reject the node hostnames `all`, all-digit names, and `phenix` on Windows nodes, and warn about hostnames that may cause problems.
- **Topology schema**: Require node hostnames to be at least 2 characters long.
- Removed the unsupported `Printer` and `Server` topology node types.

### Fixed

- **Topology Builder**:
  - **Annotation Loss**: Saving a topology replaced the config's entire metadata annotation map, discarding every annotation other than `builder-xml`.
  - **Crash on Invalid Input**: A schema-invalid topology crashed the handlers with a nil-pointer dereference instead of returning a validation error.
  - **VLAN Aliases**: Updating an experiment replaced its whole alias map, discarding the IDs phēnix had allocated automatically; aliases are now rebuilt from the saved topology.
  - **Missing Node Icons**: Diagrams lost the stencil directory on every `image=` style, so nodes rendered as missing images. All three import paths, including Edit Diagram, now restore it.
  - **Node Types**: Importing JSON overwrote each node's phēnix `type` with `kvm` or `container`; icon selection now reads `general.vm_type` and `type` keeps a schema-valid role.
  - **Downloads**: `POST /builder/save` served percent-encoded text as `text/plain` with a malformed attachment filename, and accepted empty or unsupported requests.
  - **Authorization**: Using a scenario now requires update permission on it, creating a topology authorizes the named topology rather than the `configs` resource as a whole, and requests missing a name, topology, or diagram are rejected before the data store is touched.
  - **Scenario Membership**: Adding a topology to a scenario now matches names exactly rather than by substring, and no longer creates duplicate or empty entries.
  - **Clipped Dialogs and Panels**: Dialog buttons rendered outside their dialogs, dialog content rendered underneath pinned button rows, and the format panel cut off its buttons, option rows and tab title. The About dialog also drew a redundant corner close image next to its own Close button.
- **Web UI**: The log viewer no longer leaves blank gaps between entries when several log messages share the same millisecond timestamp.
- **Web UI RBAC**: Match resource names with the same namespace-aware semantics as the server, so the UI no longer shows controls the server would reject.
  - A bare pattern such as `vm1` or `*` no longer matches namespaced VM names such as `exp1/vm1`; use `exp1/*` or `*/vm1`.
  - Globstars, braces, and extglobs in patterns no longer match names that the server denies.
  - Config permissions are checked against `<Kind>/<name>`, as the server does.
  - VM snapshot controls check `vms/snapshots` with the server's verbs: `create` to take a snapshot and `update` to restore one. Roles such as Experiment User now see the snapshot button.
- **vrouter**: Set VyOS and Vyatta router hostnames exactly as written in the topology instead of lowercasing them and replacing `.` and `_` with `-`, so the guest hostname matches the minimega VM name. Firewall nodes already behaved this way.
- **Tunneler**:
  - **Listener State Synchronization**: Centralized local listener tracking behind a synchronized manager to prevent concurrent map and state access.
  - **Operation Reporting**: Return errors for unknown listeners and local port conflicts instead of reporting successful operations.
  - **Argument Handling**: Reject malformed command arguments and Unix socket payloads without panicking.
  - **Listener Moves**: Restore an active listener's original port when moving to a new port fails.
  - **Listener IDs**: Maintain a direct ID index and prevent IDs from being reused during a tunneler session.
  - **Listener Listing**: Return local listeners in deterministic ID order.
  - **Duplicate Listeners**: Treat repeated listener creation events as idempotent without replacing active listeners.
  - **Unix Socket Protocol**: Replace Gob messages with JSON and typed listener action payloads.
- **Web UI VM Interfaces**: Preserve the experiment's configured bridge when reconnecting a disconnected VM interface.
- **Web UI**: Removed non-functional packet-capture controls from the IP column header and renamed the IPv4 column to IP.
- **Web UI VNC Tab**: Excluded external/HIL nodes and "Do Not Boot" (DNB) nodes from the VNC tab in the running experiment view.
- **VM Snapshots**: Replaced deprecated minimega `vm migrate` commands with `vm save` and `vm config state` for snapshot, restore, and redeploy workflows.
- **Image Script Updates**: Prevented `phenix image update` from duplicating refreshed scripts in `script_order`.

## [2026.08.26] - 2026-08-26

### Added

- **CLI Command Aliases**: Added `exp del`, `exp trig`, `exp res`, `exp rec`, `image del`, `config del`, and `vm res`.

## [2026.08.25] - 2026-08-25

### Added

- **Web UI (Vue 3)**: Upgraded the web frontend to Vue 3 (Vuex → Pinia, vue-resource → axios, vue-cli → Vite, Buefy 1.0). Page components now load dynamically for a quicker initial load, and the codebase was reorganized (page components moved to `views/`).
- **Idle Timeout / Auto-Logout**: Added a configurable inactivity timeout that warns and then logs the user out. Managed from the web UI **Settings** page and backed by a new `GET /api/v1/settings/timeout` route.
- **Podman CI**: Added a GitHub Actions job that builds the Podman `Containerfile` (through the Go build stage) so the Podman build path can't break unnoticed.

### Changed

- **Web UI Build**: Replaced yarn with npm and `vue-cli` with Vite; the UI build toolchain now targets Node 24.
- **Web UI Environment Variables**: Build-time UI environment variables are now prefixed `VITE_` instead of `VUE_APP_` (e.g. `VITE_AUTH` replaces `VUE_APP_AUTH`, and `VITE_BASE_PATH` replaces `VUE_BASE_PATH`). The Docker `PHENIX_WEB_AUTH` / `PHENIX_BASE_PATH` build args are unchanged.

## [2026.04.17] - 2026-04-17 (1.0.0)

### Added

- **Centralized Logging Architecture**: Implemented a unified logging system where phēnix core aggregates logs from internal services and external apps.
- **Dynamic Configuration**: Integrated `viper` with `fsnotify` to allow hot-swapping of configuration settings (e.g., log levels) without restarting services.
- **Log Rotation**: Configurable log rotation settings (`max-size`, `max-backups`, `max-age`) for the persistent system log.
- **Example Applications**:
  - **Documentation**: Consolidated all example documentation into a single `examples/README.md`. Added sections on the "App Contract", developer usage, and common pitfalls.
  - **CI Integration**: Added a new GitHub Actions workflow (`.github/workflows/examples.yml`) and Makefile targets (`make examples`) to automatically build and test the examples.
  - **Python Example**:
    - Includes panic simulation logic to demonstrate structured error logging.
    - Added comprehensive unit tests (`test_app.py`) covering configuration, modification, and crash recovery.
  - **Go Example**:
    - Uses core `phenix/types` and `phenix/store` packages for robust configuration parsing.
    - Includes panic recovery middleware to log stack traces as structured JSON.
    - Supports dynamic log levels via `PHENIX_LOG_LEVEL`.
    - Added unit tests (`main_test.go`) using the subprocess pattern to verify CLI behavior, panic recovery, and log output.
- **Documentation**:
  - Comprehensive `README.md` updates including architecture diagrams, configuration tables, and developer guides.
  - Added dependency installation instructions (Go, Python, Node, Protoc) for local development.
  - Added developer guidelines for Python app error handling (raise vs sys.exit).
- **Build Tools**: Added `make docker` target for easier container builds.
- **Build System**: Standardized Makefiles with consistent targets (`help`, `all`, `test`, `lint`, `format`, `clean`) and improved help output.
- **Code Quality**: Integrated `golangci-lint` with a comprehensive ruleset (`.golangci.yml`) and fixed numerous static analysis issues. (Note: Some linters are currently disabled to facilitate incremental adoption).
- **Shell Completion**: Added `phenix completion` command for Bash, Zsh, Fish, and PowerShell.
- **Docker Wrapper**: Added `make install-wrapper` to support shell completion when running via Docker.

### Changed

- **Log Output**: Default log output format changed to structured JSON on `stderr` for applications.
- **Configuration Management**: Moved from static flags/env vars to a watched `config.yaml` file managed via `phenix settings` commands.
- **CLI Commands**: Separated runtime configuration (`phenix settings`) from persistent database management (`phenix settings db`). Replaced `reset` command with `unset --all`.
- **CLI UX**: Added helpful error message when `phenix settings unset` is called without arguments.
- **Configuration Precedence**: Enforced `Flag > File > Env > Default` precedence for runtime settings. This ensures `phenix settings set` commands correctly override Docker environment variables.
- **Hot-Swapping**: Enabled runtime configuration updates for `log.console` and `ui.logs.level` without requiring a service restart.
- **Dependencies**: Updated Go modules to version 1.24 to leverage loop variable safety fixes and `slog` support.
- **Refactor**: Removed legacy/redundant `ui.log-level`, `ui.log-verbose`, and `ui.logs.phenix-path` configuration settings.
- **Refactor**: Renamed `log.output` to `log.console` and `log.file.*` to `log.system.*` to clarify their purpose (Human vs Machine).
- **Refactor**: Removed deprecated `ui.unix-socket-endpoint` and `ui.minimega-path` flags.
- **Refactor**: Removed noisy debug logs from HTTP handlers to improve log clarity. Use the `--log-requests` flag with the `ui` command to see HTTP traffic logs instead.
- **Refactor**: Updated `vrouter` app to use structured logging instead of `fmt.Printf`.
- **Refactor**: Replaced `go-bindata` with Go 1.16+ `embed` package for asset embedding, removing the build dependency on `go-bindata`.
- **Performance**: Removed excessive debug logging from hot paths in log file cache management to reduce I/O overhead during high-frequency UI polling.

### Removed

- **Legacy Tests**: Removed outdated `testing/` directory and unused `*_test.go` files (replaced by `examples/`).

### Fixed

- **Timestamp Consistency**: Enforced `2006-01-02 15:04:05.000` time format across file logs.
