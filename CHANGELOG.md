# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added

- **Documentation Sources**: Consolidate the phēnix MkDocs site into this repository so documentation changes can ship with the code they describe.
- **Workflow CLI**: Added `phenix workflow apply <DIR|NAME>`, which deploys a topology directory (`phenix-configs/`, `phenix-injects/` and `phenix.yml`) in one command. `-n, --dry-run` validates everything and changes nothing, and `-f, --force` allows restarting a running experiment. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
- **Settings**: Added `base-dir.injects` and `base-dir.topologies`, the directories `phenix workflow apply` stages injects in and looks up topology directories in. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
- **Workflow API**: The workflow endpoints accept `?dryRun=true`, which validates the request and changes nothing, and return a JSON result. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
- **Validation Errors**: Config validation errors from the CLI, the API and the web UI name the list item, its hostname or name, and the line. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
- **Node Notes**: Topology nodes take `general.notes`. A new experiment copies them to each VM's notes.
- **Builder**: New topology editor at `/builder`. See the [Builder documentation](https://phenix.sceptre.dev/latest/builder/).
  - **Permissions**: The new RBAC permission `builder` `use` gives a role the Builder, with every draft, template, layer set and icon on the server, and the new built-in Builder role holds it.
  - **Documents**: A Builder document keeps its ID, name, description, up to 100 notes and who made and last saved it (`createdBy`, `createdAt`, `updatedBy`, `updatedAt`) in a required `metadata` object. The Inspector lists and edits the notes. The document's JSON Schema gives every field a title, a description and examples.
  - **Node notes**: Devices and switches show their notes in a card below them on the canvas, in their info tooltips and in PNG and SVG downloads. Layouts leave room for the cards. A device's notes are its `general.notes`. A switch keeps up to 100 notes of its own in the diagram. **Show node notes** in the Builder settings hides them.
  - **Icons**: Built-in icons for computers, phones, network devices, ICS devices, power lines, operating systems, security tools, attackers, people, robots and animals. The docs list them and link to their sources and licenses. One custom icon library for the whole server, in which diagrams and templates name their icons. Any Builder user renames an uploaded icon (the old name keeps working) or deletes it. The drafts page's **Icons** tab lists, previews, uploads, downloads and deletes icons, and shows the built-in icons first, which download as SVG files. A downloaded diagram carries copies of up to 50 icons it uses, and uploading it adds those the server lacks.
  - **Icon sizes**: Devices, switches and groups draw their icons Small (16 pixels), Medium (24) or Large (32), for the whole diagram (`iconSize`) or for one node. **Node icon size** and **Show node icons** in the Builder settings change the icons on your own canvas only.
  - **Inspector**: One **Icon** menu sets a built-in or custom icon. Its last item, **Custom icon…**, opens the **Custom icons** dialog. Fields that change only how a node or a connection looks, such as colors, line styles, icons and labels, take effect at once, without **Apply**. A text box for more than one line, such as **Description** or a note, is two lines tall and grows to fit its text. With nothing selected, **Notes** comes before **Scenarios**, and each scenario is a sub-heading that gives the numbers of its apps, hosts and disabled apps.
  - **Scenarios**: A diagram lists up to 20 Scenario configs by name (`scenarios`), which the Scenarios dialog checks in a list, shows and edits as YAML, creates, or stores from an uploaded file. Publish adds the topology to each listed scenario's `topology` annotation, and the Publish dialog picks the experiment's scenario.
  - **Drawings**: Rectangles, circles, icons and lines (with bends and arrowheads) that a diagram shows and Publish ignores. Shapes, icons, notes and groups resize with the mouse.
  - **Template files**: Node Templates export to and import from YAML or JSON template files, one collection per file, with the custom icons they use. `phenix ui` reads the template files in `base-dir.builder-templates` (default `<base-dir.phenix>/builder/templates`) at start as read-only server collections that every user sees and can copy.
  - **Merging**: Merge a draft with another editor's changes on a save conflict.
  - **Packages**: Download and upload a diagram with its configs and icons as one file.
  - **Error codes**: Stable error codes on Builder errors and issues.
  - **Publish dialog**: The Publish dialog shows what publishing changes. It marks an update as a warning, and says why a config name is not allowed.
  - **Preflight**: Checks a draft against the server's hosts, VLANs, bridges, images and apps.
  - **Checks**: The checks list errors first. Each error has a **Go to** button for its node and field.
  - **Layers**: A diagram defines layers with labels, icons, colors and node matchers: the Inspector puts a device or a switch at a layer, and a matcher puts a device at a layer by its fields. The **By layer** layout arranges a diagram by layer, and the documentation shows how to use layers for the Purdue model with the example diagram `lakeside-treatment.builder.json`.
  - **Layer sets**: The server keeps a library of layer sets, with a built-in Purdue Model set and one default set for all users, which the drafts page's **Layers** tab edits, downloads and uploads. A diagram keeps a copy of a set's layers, and the layers editor (**Edit layers…**) updates the copy from the set or saves it to the set.
  - **Layouts**: The Yifan Hu, Force and Radial layouts in the layout menu.
  - **Coordinates**: **Show coordinates** in the Builder settings shows X and Y rulers along the edges of the canvas, in the units of the Inspector's **Position**.
  - **Node Editor**: A large dialog edits one node of the diagram, with a table of contents, a field search by label or YAML path, section headers that stay in view, and sections that collapse as in the Inspector. It opens from the Inspector, a double-click on the node, **Alt+Shift+E** or the command palette.
  - **Default values**: In the Inspector and the Node Editor, the **Default** button beside a changed field resets it to its default value.
  - **Connect and group without dragging**: **Add connection** and **Move to group** in the Inspector's header open dialogs that connect a device to a switch and move a node into or out of a group.
  - **Command palette search**: `@` finds nodes by name, IP address, MAC address, VLAN, annotation, label, description, note or image, and `t:`, `c:` and `i:` find node templates, collections and custom icons. Templates, collections and icons record who created and last changed them (`createdBy`, `updatedBy`).
  - **Commands**: **Set layer of selection**, **Select nodes on layer**, **Assign addresses**, **Snap to grid** and **Node icon size** run from the command palette, and the shortcuts editor can give them keys.
  - **Networks**: When you delete the last switch of a network that nothing else uses, Builder also removes the network. One **Undo** restores the switch and the network. **Remove network** in the command palette removes a network with its switches.
  - **Template sections**: The Templates list in the left column shows the diagram's templates, the library templates in no collection, one collapsible section for each collection, and the built-in templates.
  - **Template library**: One Node Templates library for the whole server, which every user with Builder access can change, beside the read-only built-in templates and server collections. A diagram's copy of a library template shows **Update available** when the library has a newer version, with **Update**, **Update all** and **Unlink**.
  - **Network addresses**: Networks take an optional subnet and gateway, and a connection gives a static interface the next free address of the subnet while **Assign addresses on connect** is on. New interfaces are static, **Assign addresses** fills the empty addresses of a network, and **Generate** sets a random MAC address.
  - **Canvas**: Switches show only their icon and name, with their network's color as their outline, and node icons take the node's outline color or the accent color. The header counts the diagram's layers while a node is at one.
  - **Grid**: **Snap to grid** is a setting of each user, off by default, and the Inspector's Diagram section sets the grid size, a whole number from 4 to 200.
  - **Keyboard use**: **N** on the canvas adds a Device. The drafts lists, the Node Templates library and the Custom icons dialog support keyboard multi-select and bulk actions.

### Changed

- **Web UI Accessibility**: Declared the page language, added accessible names to icon-only buttons, links, and form controls, labelled the config selection checkboxes, made the log viewer keyboard-scrollable, added a visible keyboard focus indicator, a skip link, per-route page titles, and pagination control names, fixed low-contrast placeholder, danger, and code colours, made the Settings form submit on Enter, and added an axe-core WCAG 2.2 AA scan of every route to the browser smoke tests.
- **CLI / Web UI**: Display the release version or source branch alongside the commit hash and build timestamp in the version output and footer.
- **Config Schemas**: Descriptions for node and interface fields in the v1 schema. The schema shows defaults as phenix applies them (`general.snapshot` `true`, `hardware.memory` 512).
- **Builds and CI**: Browser tests run in parallel jobs, and only for changes that can affect them. `PHENIX_BROTLI_QUALITY` (0 to 11, default 9) sets how hard UI builds compress the Builder's files.
- **Topology validation**: Reject the node hostnames `all`, all-digit names, and `phenix` on Windows nodes, and warn about hostnames that may cause problems.
- **Topology schema**: Require node hostnames to be at least 2 characters long.
- **Workflow API**: A workflow apply is validated before a running experiment is stopped, so an invalid one returns 400 or 409 instead of 500 and leaves the experiment running. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
- **Workflow Schema (breaking)**: Workflow configs are validated against the new `Workflow` schema, so unknown or misspelled keys, values of the wrong type and a missing `spec` return 400 instead of being ignored. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))

### Removed

- **Legacy Builder**: Removed the mxGraph Builder at `/builder` and its routes (`POST /builder/save`, `/api/v1/builder/topologies`, `/api/v1/experiments/builder`). See [Legacy Builder](https://phenix.sceptre.dev/latest/builder/legacy/).
- **etcd Store (breaking)**: Removed the etcd data store, so `etcd://` store endpoints are no longer accepted; use a `bolt://` endpoint.

### Fixed

- **Web UI**: The log viewer no longer leaves blank gaps between entries when several log messages share the same millisecond timestamp.
- **Web UI RBAC**: Match resource names with the same namespace-aware semantics as the server, so the UI no longer shows controls the server would reject.
  - A bare pattern such as `vm1` or `*` no longer matches namespaced VM names such as `exp1/vm1`; use `exp1/*` or `*/vm1`.
  - Globstars, braces, and extglobs in patterns no longer match names that the server denies.
  - Config permissions are checked against `<Kind>/<name>`, as the server does.
  - VM snapshot controls check `vms/snapshots` with the server's verbs: `create` to take a snapshot and `update` to restore one. Roles such as Experiment User now see the snapshot button.
- **minimega script**: Node labels whose keys or values hold quotes, backslashes, tabs or line breaks reach minimega intact as VM tags.
- **vrouter**: Set VyOS and Vyatta router hostnames exactly as written in the topology instead of lowercasing them and replacing `.` and `_` with `-`, so the guest hostname matches the minimega VM name. Firewall nodes already behaved this way.
- **Web UI**: The Configs page shows validation errors for uploaded configs again instead of failing silently. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
- **Workflow API**: `POST /api/v1/workflow/configs/{branch}` returns 400 for a config with a missing or unknown kind, or with `/` in its name, instead of dropping the connection. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
- **CLI**: Every `phenix` command waits at most one minute for the server's options over `--unix-socket` instead of hanging on a server that never answers. ([#445](https://github.com/sandialabs/sceptre-phenix/pull/445))
- **Config Schemas**: Invalid v1 schema (empty `pattern` on a serial interface's `device`).
- **Schemas API**: Unknown schemas return 404 instead of 500.
- **API docs**: The OpenAPI document is valid again.
- **Configs API**: Creating or replacing a config checks the permission on the kind and name in the request body.
- **Settings**: Password checks on a new server no longer fail when several requests arrive at once. An error reading a setting names the setting instead of saying `decoding image spec`.
- **Web UI**: The header's Logout can be reached with the keyboard, and says when logging out fails. The keyboard focus ring on the header's links, Logout and menu button is no longer cut off at the top of the window. The sign-in page focuses Username after a logout, labels its fields and the Create Account dialog, and fits narrow screens. The Create Account dialog opens empty each time, and reopening it as it closes no longer leaves an invisible dialog over the page. Red field errors in dialogs are legible.
- **Web UI**: For users with a character such as `é` in their username, moving between pages no longer fails, and an expired sign-in logs them out.
- **Config YAML**: Configs written as YAML (`phenix config get -o yaml`, `phenix config edit`, the configs API and downloads) keep strings that start with a line break or a tab.
- **Configs page**: The viewer opens for topologies saved by the legacy Builder instead of showing an error, is labeled with the config's name, and returns focus to it when closed.
- **Users**: Signing in as the same user from parallel requests no longer loses a token. Creating a user or signing up with a name already in use returns 409, and the Users page and the Create Account dialog say the user exists. Creating a user with an unknown role no longer leaves a user without a role. A `ui.users` entry without a role is skipped and logged instead of crashing phenix.

### Security

- **Workflow configs**: `POST /api/v1/workflow/configs/{branch}` no longer exposes server environment variables in its errors.
- **Web UI**: Updated axios and js-yaml.

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
