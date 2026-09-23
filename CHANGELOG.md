# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added

- **RBAC**: Protect Builder, Scorch, and Tunneler with the service-level `builder` (`get`, `post`, `put`), `scorch` (`get`, `post`, `delete`), and `tunneler` (`get`) permissions across REST routes, Scorch websocket updates, the standalone Builder and Tunneler download links, and the web UI. Custom roles need these permissions added explicitly.
- **RBAC**: Built-in roles get service access on first startup, once, so administrators can later remove it:
  - Every viewer role (Global Viewer, Experiment Viewer, VM Viewer, and the new Scorch Viewer) can open the Builder and save topology files locally.
  - Roles that can control VMs (Experiment Admin, Experiment User, and VM Admin) can start and cancel Scorch runs and type into Scorch terminals for their experiments. A Scorch `break` terminal is a shell on the phēnix server, so `scorch` `post` should only go to trusted users.
  - Experiment Admin, Experiment User, and VM Admin can download the Tunneler and create port forwards for their VMs; Experiment User gains `vms/forwards` `create` and `delete`.
- **RBAC**: New built-in roles, created once on existing installs:
  - **Scorch Viewer**: view Scorch pipelines, component output, read-only Scorch terminals, and Scorch run files for assigned experiments.
  - **Scorch Admin**: everything Scorch Viewer can do, plus start and cancel runs, write to Scorch terminals, and view VMs and screenshots for assigned experiments.
  - **Builder**: use the Builder and the Configs page for Topology, Scenario, and Experiment configs, create and update experiments, and list disks, topologies, scenarios, applications, hosts, and options. Not scoped to experiments, and cannot read or change User, Role, or Image configs.

### Changed

- **CLI / Web UI**: Display the release version or source branch alongside the commit hash and build timestamp in the version output and footer.

### Fixed

- **RBAC**: Role and user configs saved by phēnix no longer store `resourceNames: null` for unscoped policies, which failed schema validation when an administrator later edited the role.
- **Web UI**: Permission checks no longer throw for policies with no resource names, and Configs page buttons now check the same `Kind/name` as the server.
- **Store**: `etcd` no longer panics when checking a store component that has not been initialized.

### Security

- **Scorch**: Scorch terminals and component output now require read access to the experiment, and writable Scorch terminals require `scorch` `post`. Previously, any authenticated user could stream or write to them. Scorch pipeline and terminal websocket updates now go only to users with Scorch access to the experiment instead of every connected user.
- **Scorch**: Starting or canceling Scorch through `POST` or `DELETE /api/v1/experiments/{name}/trigger?apps=scorch` now requires `scorch` `post` or `delete` in addition to `experiments/trigger`. Starting and canceling runs on the Scorch pipeline routes now needs `scorch` `post` or `delete` and read access to the experiment, instead of `experiments/trigger`.
- **Configs**: `configs create` is now checked against the new config's `Kind/name`, and renaming a config or changing its kind needs `configs create` for the new name. Previously, any role with `configs create` could create User or Role configs and grant itself more access.
- **Builder**: `PUT /api/v1/experiments/builder` now checks `experiments update` for the named experiment, and `experiments create` when it creates the experiment.
- **Builder / Tunneler**: `GET /builder`, `POST /builder/save`, and `GET /downloads/tunneler/{name}` now require authentication and `builder` `get` or `tunneler` `get`.

## [1.0.0]

### Changed

- Removed the unsupported `Printer` and `Server` topology node types.

### Fixed

- **Tunneler**:
  - **Listener State Synchronization**: Centralized local listener tracking behind a synchronized manager to prevent concurrent map and state access.
  - **Operation Reporting**: Return errors for unknown listeners and local port conflicts instead of reporting successful operations.
  - **Argument Handling**: Reject malformed command arguments and Unix socket payloads without panicking.
  - **Listener Moves**: Restore an active listener's original port when moving to a new port fails.
  - **Listener IDs**: Maintain a direct ID index and prevent IDs from being reused during a tunneler session.
  - **Listener Listing**: Return local listeners in deterministic ID order.
  - **Duplicate Listeners**: Treat repeated listener creation events as idempotent without replacing active listeners.
  - **Unix Socket Protocol**: Replace Gob messages with JSON and typed listener action payloads.

### Added

- **Tunneler Web Interface**: Add a local web dashboard for listing, enabling, disabling, and moving listeners, with live WebSocket updates.
- **CLI Command Aliases**: Added `exp del`, `exp trig`, `exp res`, `exp rec`, `image del`, `config del`, and `vm res`.
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
- **Web UI (Vue 3)**: Upgraded the web frontend to Vue 3 (Vuex → Pinia, vue-resource → axios, vue-cli → Vite, Buefy 1.0). Page components now load dynamically for a quicker initial load, and the codebase was reorganized (page components moved to `views/`).
- **Idle Timeout / Auto-Logout**: Added a configurable inactivity timeout that warns and then logs the user out. Managed from the web UI **Settings** page and backed by a new `GET /api/v1/settings/timeout` route.
- **Podman CI**: Added a GitHub Actions job that builds the Podman `Containerfile` (through the Go build stage) so the Podman build path can't break unnoticed.
- **Experiment Lifecycle Triggers**: Added `phenix exp trigger` for manually invoking app lifecycle stages. Deprecated `phenix exp trigger-running`; use `phenix exp trigger running` instead. The command errors out if a given app isn't part of the experiment or if the requested lifecycle stage isn't applicable to it, and shell completion only suggests apps applicable to the given experiment and stage.

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
- **Web UI Build**: Replaced yarn with npm and `vue-cli` with Vite; the UI build toolchain now targets Node 24.
- **Web UI Environment Variables**: Build-time UI environment variables are now prefixed `VITE_` instead of `VUE_APP_` (e.g. `VITE_AUTH` replaces `VUE_APP_AUTH`, and `VITE_BASE_PATH` replaces `VUE_BASE_PATH`). The Docker `PHENIX_WEB_AUTH` / `PHENIX_BASE_PATH` build args are unchanged.

### Removed

- **Legacy Tests**: Removed outdated `testing/` directory and unused `*_test.go` files (replaced by `examples/`).

### Fixed

- **Web UI VM Interfaces**: Preserve the experiment's configured bridge when reconnecting a disconnected VM interface.
- **Web UI**: Removed non-functional packet-capture controls from the IP column header and renamed the IPv4 column to IP.
- **Web UI VNC Tab**: Excluded external/HIL nodes and "Do Not Boot" (DNB) nodes from the VNC tab in the running experiment view.
- **VM Snapshots**: Replaced deprecated minimega `vm migrate` commands with `vm save` and `vm config state` for snapshot, restore, and redeploy workflows.
- **Image Script Updates**: Prevented `phenix image update` from duplicating refreshed scripts in `script_order`.
- **Timestamp Consistency**: Enforced `2006-01-02 15:04:05.000` time format across file logs.
