# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added

- **WebShark**: A WebShark tab (`/packets`) shows an experiment's running captures, live, and its saved capture files in the browser. Capture files on the Files tab and capturing interfaces in VM details open in it. The Docker, Podman and Debian builds ship it in `/opt/phenix/webshark`, and the tab shows only when the server finds it.
- **Capture streaming**: `GET /experiments/{exp}/vms/{name}/captures/{iface}/stream` (HTTP or websocket, `?from=now` to skip the backlog) and `phenix vm capture stream` stream a running capture as pcap from any cluster node. The WebShark tab's Copy Wireshark command button copies a `curl` command for it.
- **API**: `POST /experiments/{exp}/webshark` readies a capture for WebShark, which the server serves under `/webshark/`, also behind a proxy that adds a base path or signs users in with a cookie.
- **State of Health**: A State of Health tab summarizes every experiment's SOH (verdict, VM health, checks, reachability and a 12-run trend) from the new `GET /soh`. The soh app records its last run and a 20-run history in its app status.
- **State of Health graph**: A layout picker (Force, Radial tree, Layered, VLAN groups, ELK layered; each loaded only when picked and remembered), a Fit button, a VM count, and a Download menu for PNG, SVG and GEXF (Gephi). Adds `@dagrejs/dagre` (MIT), `webcola` (MIT) and `elkjs` (EPL-2.0).
- **Experiment files**: The Files tab can delete a file, or select several to download as a zip or delete. Backed by `DELETE /experiments/{name}/files/{file}`, `POST .../files/delete` and `POST .../files/download` (at most 500 files and 2 GiB), and a new `experiments/files` `delete` permission that Experiment Admin roles gain on startup.
- **Disks**: The Disks tab and the experiment page's Disk and CD-ROM menus list the images in folders of the minimega files directory, down to 16 levels and named by their path within it, and the images a topology names anywhere else phēnix can read, by full path. A warning icon on the Disks tab, beside a Disk menu whose disk is outside, and in a running VM's details explains that minimega does not copy such images to other cluster nodes. The listing leaves out hidden folders and the folders experiments and minimega keep their own files in, and the disk list follows folders as they are created. The Disk and CD-ROM menus group outside images apart and disable names minimega cannot use, the Disk menus show a VM's disk that the list does not hold as `(not listed)` with a warning, and disk details gain a Location row.
- **VM table**: An Actions column on the running experiment's VM table: Shut down, Restart, Port forward, Snapshot, Backing image, and Capture all interfaces, which becomes Stop all packet captures while any are running.
- **Experiment annotations**: `phenix experiment create --node-annotation` and `--annotation`, and the create experiment card's Advanced Options, add annotations to every VM or to the experiment. A VM's own value wins, and `topology` and `scenario` are rejected.
- **VM annotations**: A running experiment's VM details show the VM's annotations and let roles that may update the VM edit them. `GET /experiments/{exp}/vms/{name}` returns them and `PATCH` replaces them, whether or not the experiment is running.
- **Console**: The minimega console survives page changes, reloads and other tabs, replaying up to 256 KiB of output, and ends on CTRL+D, logout, or 15 seconds after its last tab closes (`GET` and `DELETE /console/{pid}`).
- **SCORCH**: Clear a finished run's status (`POST .../scorch/pipelines/{run}/clear`) or run only its cleanup stage (`POST .../cleanup`), and Start all or Stop all runs, from the pipeline page.
- **Documentation**: New pages for the web UI, Disks, Hosts and WebShark, rewritten VM, netflow, SCORCH, tunneler, config and RBAC pages with new screenshots, and the new routes in `openapi.yml`.
- **CI**: A `perf` job enforces bundle size (size-limit) and Lighthouse budgets.
- **Topology Builder**:
  - **Topology Endpoints**: Added `POST /api/v1/builder/topologies` and `PUT /api/v1/builder/topologies/{name}` to create and save a topology and its diagram without touching an experiment; both refuse the write while an experiment built from that topology is running.
  - **Default Disk Images**: Added the `$DEFAULT_VM_IMAGE` and `$DEFAULT_ROUTER_IMAGE` experiment variables, which set the disk image new VM and router nodes are created with.
  - **API Documentation**: Documented the Builder routes in `openapi.yml`, which previously carried none of them.
- **Documentation Sources**: Consolidate the phēnix MkDocs site into this repository so documentation changes can ship with the code they describe.

### Changed

- **Loading**: A header status with a refresh button replaces full-page spinners. After login the Experiments, Configs, Disks, Hosts, Users, Logs, Scorch, State of Health and Settings tabs preload in the background, pages show their cached data at once, and a load you leave keeps running for up to 2 minutes. Empty tables say why they are empty.
- **minimega load**: Listing VMs takes 3 minimega commands instead of about 3 per VM, and identical concurrent listings and screenshots share one run. Experiment starts use their own connection, file and host listings are cached for 5 seconds, and waiting miniccc commands share one poller per namespace. Hosts refresh every 30 seconds and VM tiles every 60, only while the page is visible.
- **VM screenshots**: Taken every 15 seconds instead of 5, only of running VMs while the tab is visible, and no longer at all once you leave the experiment page (they kept minimega busy for the rest of the session). They reach the page as `experiment/vm/screenshot` updates after the VM rows.
- **Server**: BoltDB store reads no longer wait on each other, and writes keep the free page list in the file and use one transaction (existing stores convert on first write). Requests read the experiment once, websocket messages are encoded once, and log lines go only to Logs page clients whose role may read logs.
- **Web UI speed**: JavaScript loaded on every page is about a quarter smaller, bundled images shrank from 1.6 MB to about 110 kB, page code prefetches when idle, and static files and API responses are gzip-compressed, with hashed assets cached as immutable. The Builder loads its scripts as one bundle (182 requests to 26), configs open in well under a second, and the Logs, Scorch and experiment pages do less work per update.
- **Disks**: With `qemu-img` installed, phenix inspects disk images itself, in parallel, and reuses results until the files change; directory changes and the refresh button trigger a rescan, and open Disks pages update live. Listings name the experiments using each disk that the role may list, matched by full path. The table gains Size on Disk, sortable Virtual Size, an Actions column and an upload dialog, and every disabled disk action says why. `GET /disks` lists each image once by its full path, adds `relativePath`, `outsideFilesDir` and `readOnly`, gives `backingImages` as full paths, sorts by name and then full path, and answers 404 for an unknown `expName`, which now needs `get` on that experiment. Images outside the minimega files directory, or at paths the listing leaves out, are read-only: every disk action, Download included, refuses them with a 400, and the actions minimega runs, and new image names, refuse a name minimega cannot take as one argument. Clone, Rename and Delete act on the file directly, and Clone and Rename never replace a file (409). Snapshot, Clone and Rename refuse a new name that does not name a file. The Redeploy dialog offers the images in the files directory and, of those elsewhere, only the ones this experiment names, rather than every experiment's; `PATCH` of one or more VMs and Redeploy refuse a disk minimega cannot use with a 400 and change no VM; and Redeploy answers a missing or malformed body with a 400 before touching the VM.
- **VM details**: Redesigned with a state-colored header holding the description, the screenshot, resource tiles, one row per interface with capture controls, buttons that capture all interfaces, stop all captures or open the WebShark tab, the disk's backing chain, labeled power buttons, disabled actions that say why, and links to the docs, Disks, State of Health and SCORCH.
- **VNC page**: Banner text from a VM's `vncBanner` annotation shows as text, so HTML in it no longer renders; a string's newlines and the map form's `banner` list start new lines. Banner colors must be CSS color names or hex colors, other colors and unknown map keys are ignored with a logged warning, and the page sends a Content-Security-Policy that runs only its own scripts.
- **Experiment page**: Opens at once from the cache, shows netflow as a searchable table, sorts by Uptime and Delay, and, for a running experiment, returns to the experiment list with a message when the experiment is stopped or deleted elsewhere.
- **State of Health page**: The graph fits the window with its legend beside it, the "All" filter is the default and persists, Run SOH and Refresh Network show progress, and Go to Experiment and SCORCH buttons sit above the graph.
- **SCORCH**: The Scorch table has separate experiment and SCORCH start/stop buttons and links to each experiment's pipelines. The pipeline page colors the experiment by state, gives each run a start/stop button, and opens node output at once.
- **Tables and search**: Every search box has the same size, position, magnifier and clear button, which shows only when there is something to clear. Sort arrows sit beside headings, and paginate toggles sit above tables, start off, and are remembered per table in the browser.
- **Pages**: Users gains search and First Name sort, Hosts sorts by RAM and Uptime and labels Load spans, Configs sorts by Name and Last Updated and has an "All kinds" filter, Experiments sorts by Topology and Scenario and deletes spin only their row, the create experiment card is wider with a Docs button and field help, Settings has Reset Form, and the footer links to the docs.
- **Error popups**: Wider, with each step of a nested server error on its own line and the root cause last.
- **Permissions**: The UI uses the server's permission names, redirects away from pages a role cannot use, and checks permissions faster.
- **API**: Experiment listings report start `percent` and can skip minimega (`?vms=false`), SCORCH pipelines include `app_running` and the experiment's status, VM lists sort by `delayed`, and the experiment start broadcast no longer lists VMs.
- **Hosts**: Disk usage is measured at most once a minute and shown as a percentage.
- **Tunneler**: The page lists the builds the server has (`GET /downloads/tunneler`).
- **Browser console**: Production builds log only warnings and errors, and lint rejects new `console.log` calls.
- **CI**: The Frontend workflow cancels superseded runs, and closing a pull request cancels its CI.
- **Web UI Accessibility**: Declared the page language, added accessible names to icon-only buttons, links, form controls, selection checkboxes and modal close buttons, made the log viewer keyboard-scrollable, added a visible keyboard focus indicator, a skip link, per-route page titles, and pagination control names, fixed low-contrast placeholder, danger, status tag, code and State of Health tab colors, made the Settings form submit on Enter, and added an axe-core WCAG 2.2 AA scan of every route to the browser smoke tests.
- **CLI / Web UI**: Display the release version or source branch alongside the commit hash and build timestamp in the version output and footer.
- **Topology Builder**:
  - **mxGraph**: Updated the vendored mxGraph from 4.1.0 to 4.2.2, the final release before the project was archived. Upstream changed the modifier that deletes a cell together with its connected edges from Shift to Ctrl.
  - **Editor**: The sidebar node palettes now open expanded, the export dialog offers only the XML and SVG formats the server can produce, and the Help button opens the phēnix documentation instead of the defunct `minimega.org`.
- **Topology validation**: Reject the node hostnames `all`, all-digit names, and `phenix` on Windows nodes, and warn about hostnames that may cause problems.
- **Topology schema**: Require node hostnames to be at least 2 characters long.

### Fixed

- **minimega**: Deleting or redeploying a VM removed every other stopped VM in the experiment, restoring a snapshot failed on multi-node experiments, and concurrent redeploys could launch a VM with another VM's settings.
- **miniccc**: A command meant for one VM could run on another when two were sent at once, answered SOH commands ran again after a miniccc restart, and waiting for a VM's miniccc client ran up to 2 s past its timeout or cancellation.
- **Namespaces**: On minimega 2.9, taps and netflow captures on other nodes outlived their experiment, and listing a stopped experiment's captures, port forwards or files recreated its namespace with a stale host list. The names `minimega` and `__phenix__` are now rejected.
- **Port forwards**: Closing a forward closed other users' forwards to the same VM and port, and forwards through a head-node VM in a multi-node experiment reported an error and left the tunnel open.
- **Captures**: Starting captures on a subnet listed a VM's captures once for each of its interfaces in the subnet, and stopping them tried a failed stop again for each further interface.
- **Security**: `GET /logs` kept streaming after a 403, the websocket topology search and SCORCH output and terminal endpoints skipped permission checks, disk downloads and VM mount paths accepted `../` and prefix-sibling paths, error popups rendered server text as HTML, captures and SOH used the wrong permission checks, any console user could attach to another user's minimega console, disk actions other than download accepted any path on the server, including `../` and paths outside the minimega files directory, cloning a disk did not check the new image's name against the role, committing a disk inspected it before checking permission, `GET /disks` showed the images outside the files directory that any experiment's topology named, even with an `expName` the role could not get, and the dialog confirming a VM's new disk rendered the disk path and VM name as HTML. The VNC page rendered a VM's `vncBanner` annotation as HTML, so anyone who could set a VM's annotations could run script as whoever opened its VNC page, and the annotation's map form could also set where the page loaded noVNC from and the token it connected with. Its log line for each opened VNC page left out who opened it.
- **Server**: A websocket VM list request without sort or paging fields, or a malformed netflow record, crashed phenix. VM lists panicked on a `pageNum` or `perPage` below 1 and counted their total after paging, VM details panicked for a VM without a disk, and download names with a space or semicolon were cut short. Disconnected clients leaked goroutines, the topology endpoint could reply twice, SOH DHCP waits could race or panic, and bad file paging values or missing files returned 500.
- **SCORCH**: A pipeline request for a run the experiment does not have, or a break component's update once its experiment could no longer be read, crashed phenix, and a pipeline request made as the server started could hang. Run errors reached the UI as `{}` and were never shown, output streams and terminal checks raced, the Scorch table broke on start or stop and on searches containing `(`, closed terminals left their buttons behind, and the runs page errored on early updates.
- **Experiment page**: VM events from another experiment with the same VM name changed rows, VM state missed redeploys, resets, shutdowns and delayed starts, and failed commits, snapshots and redeploys left buttons busy. Bulk Snapshot, Backing image and Memory snapshot sent requests for VMs that were not running, bulk Reset disk named the VMs it skipped in a toast and a second dialog, bulk actions sent requests for VMs busy with another action, bulk actions skipped selected VMs the table no longer showed without saying so, or said they lacked snapshots, the selection stayed after a toolbar action, a file whose name contained a running capture's file name could not be opened, and one with `#`, `?` or `%` in its name could not be opened or downloaded. Snapshot names had wrong dates, the file viewer showed an empty box or "[object Object]", the CD-ROM picker showed an empty list, capture buttons looked stuck, stopping netflow twice showed an error, and roles without file listing lost the page's controls. Searching reloaded the whole experiment, VM updates stopped after a websocket reconnect, viewing a file waited seconds on minimega, and the stopped experiment page paged VMs wrongly and booted the wrong VM.
- **Experiments list**: It missed experiments created elsewhere, broke on experiments started from the CLI, flipped a stopped experiment back to started, hung on "Loading experiments" during a large start, and counted a range of queued VMs as one.
- **State of Health**: The page never showed a run in progress because it listened for a message the server no longer sends, and it failed to draw after "no nodes match", showed the wrong node for a not-booted VM, and drew links with an invalid width.
- **Users**: Role sorting failed, closing the edit dialog changed the row, a rejected edit closed the dialog, a failed delete left a spinner, roles disappeared after updates from other sessions, and resource names were split into characters after a user was created.
- **Other pages**: Hosts could not sort by VM count, disk details said "N/A", sizes sorted wrongly, and a backing image showed the in-use state of the image built on it, configs with builder data failed to open, invalid config uploads and failed saves were silent, config search treated `(` as a pattern, leaving an unchanged config asked about discarding edits, leaving the Logs page early threw an error, error notifications lost their icon, permission checks stayed cached from the previous user's role, the create experiment card loaded scenarios late and clipped tooltips, and pages leaked SCORCH terminals, output sockets and graph simulations after you left them.
- **Disks**: An image with the same file name as another listed image, such as a topology's `/data/ubuntu.qc2` beside `ubuntu.qc2` in the minimega files directory, was left out of disk listings. Redeploy opened with no disk selected, and redeploying without picking one moved the VM onto the image with its file name at the top of the files directory and, unless the injections were replicated, dropped the files phēnix had injected into its disk. The stopped experiment's Disk menu was blank for a disk the list did not hold, and clicking a backing image the list did not hold broke the disk details. Disk actions from the Disks page broke on names containing `+`, `&`, `%` or `#`, and Clone, Rename and Delete ran minimega `shell` commands that split names at spaces, acting on other files. A running snapshot VM's disk was the backing name stored in its snapshot, which is relative to the snapshot's folder when minimega runs without `-abssnapshot`. phēnix asked minimega for its files directory once, while starting and before applying its own settings, so when minimega did not answer then, phēnix used `/phenix/images` until restarted. A disk upload with no file, or one phēnix could not save, panicked after answering with the error.
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
- **Containers**: The Docker and Podman builds gave the `INSTALL_CERTS` certificates to yarn, but the UI builds with npm, so building the UI behind a TLS-inspecting proxy failed to install its packages.

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
