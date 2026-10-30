# Settings & Configuration

phēnix supports dynamic configuration management via the `phenix settings` command.
A running `phenix ui` applies changes to the `log.*` settings, `ui.logs.level` and
the users file at once, without restarting. It reads every other setting only at
startup. On each config file change it logs a warning that names every such key
that now differs from the value it started with (never the values themselves):

```text
config change takes effect only when phenix ui restarts keys="store.endpoint, ui.listen-endpoint"
```

## Configuration Files

phēnix looks for a configuration file (e.g. `config.yaml`, `config.json`, `config.toml`) in the following locations:

**When run as root:**

* `/etc/phenix/config.yaml`

**When run as a regular user:**

* `$HOME/.config/phenix/config.yaml`
* `/etc/phenix/config.yaml`

### Example Configuration

```yaml
base-dir:
  minimega: /tmp/minimega
  phenix: /phenix
log:
  level: info
  console: stderr
  system:
    path: /var/log/phenix/phenix.log
store:
  endpoint: bolt:///etc/phenix/store.bdb
ui:
  listen-endpoint: 0.0.0.0:3000
  jwt-signing-key: abcde12345
  logs:
    minimega-path: /var/log/minimega/minimega.log
```

## Configuration vs. Data Store

It is important to distinguish between the **System Configuration** and the **Data Store**:

* **System Configuration (`config.yaml`)**: Configures the phēnix *daemon* itself. It controls *how* the service runs (e.g., log levels, API ports, storage backend connection strings).
* **Data Store (BoltDB)**: Stores the *application state*. It holds *what* phēnix manages (e.g., defined experiments, topologies, user accounts, active VM states).

Deleting `config.yaml` resets daemon behavior to defaults. Deleting the Data Store results in data loss (experiments, users, etc.).

## Store Configuration

The phenix tool uses a bbolt key-value data store, a single file with no external dependencies that is only accessible on one machine. Set its path with the `bolt://` store endpoint (e.g. `bolt:///etc/phenix/store.bdb`).

## Managing Settings

```bash
# View all current runtime settings
phenix settings list

# Change log level to debug
phenix settings set log.level debug

# Revert a setting to default
phenix settings unset log.level

# Reset all settings
phenix settings unset --all
```

## Checking the Configuration

`phenix doctor` checks the configuration without a running `phenix ui`, so it
helps most when the server will not start or a setting seems to have no
effect:

```bash
docker exec phenix phenix doctor          # Docker deployment
phenix doctor -o json --timeout 10s       # JSON, waiting longer for minimega
```

It runs these checks, then lists every setting with its effective value and
its source: `flag`, `env`, `config file` or `default`.

| Check | Warns when | Fails when |
| :--- | :--- | :--- |
| `config file`, `users file` | no `config.yaml` is found | the file does not parse, so phēnix ignores it |
| `config keys` | the config files hold keys that are not phēnix settings, such as a misspelling | never |
| `PHENIX_*` (one row per set variable, such as `PHENIX_BASE_DIR_PHENIX`) | the variable is empty, a flag or the config file overrides it, phēnix reads it as a value other than the one written (such as `010` as `8`), or it names no setting, such as a misspelling | phēnix would refuse or misread the value |
| `PATH`, `EDITOR`, `SUDO_USER`, `SUDO_COMMAND` | `PATH` has relative entries; `SUDO_USER` names no user when root runs phēnix through `sudo` | `EDITOR` is not a program on `PATH` |
| `store.endpoint` | never | the file cannot be written, or its directory does not exist or cannot be written |
| `log.system.path`, `log.console` | never | the file (`phenix.log` in it when `log.system.path` is a directory) cannot be written, or created in the nearest directory above it that exists |
| `base-dir.phenix`, `base-dir.minimega`, `base-dir.injects`, `base-dir.topologies`, `mount-dir` | the directory does not exist, is not readable and searchable by this user, is relative, or is one phēnix writes to and is not writable by this user | the path is not a directory |
| `ui.jwt-signing-key` | the key is empty (no sign-in) or a development key | never |
| `ui.shutdown-grace-period`, `ui.netflow.max-size` | never | `phenix ui` would refuse the value |
| `minimega` | never | minimega does not give its version on `<base-dir.minimega>/minimega` within `--timeout` (3 seconds by default) |

Each environment variable phēnix reads gets a row only when it is set, and
is checked the way the code that reads it parses it: whole numbers, `true` or
`false`, log levels, modes, durations with a unit (a bare `24` in
`PHENIX_UI_JWT_LIFETIME` is 24 nanoseconds), `host:port` endpoints, header
names and paths. A directory variable follows the rules of the `base-dir.*`
rows above, a file variable those of the `store.endpoint` and `log.*` rows,
and `PHENIX_UI_TLS_KEY` and `PHENIX_UI_TLS_CERT` must name readable files.
`PHENIX_UI_FEATURES` fails when it holds a comma, and `PHENIX_UI_USERS` when
a user's role does, since phēnix splits these lists on spaces when they come
from the environment.

`phenix doctor` exits 1 when a check fails and 0 otherwise. It never prints
the values of `ui.jwt-signing-key` or `ui.users`, only `<redacted>` when they
are set, nor those of `PHENIX_UI_JWT_SIGNING_KEY`, `PHENIX_UI_USERS`,
`SUDO_COMMAND` or a `PHENIX_*` variable that names no setting. Like `phenix healthcheck`, it opens no store, unix socket or log file;
run it with the same flags, environment and user as the command you are
checking, since those decide the values it sees.

## Configuration Precedence

phēnix resolves configuration settings in the following order (highest to lowest):

1. **Command Line Flags**: Arguments passed directly to the binary (e.g., `phenix ui --log.level=debug`).
2. **Config File**: Settings defined in `config.yaml` (managed via `phenix settings set`).
3. **Environment Variables**: Variables like `PHENIX_LOG_LEVEL`.
4. **Defaults**: Internal application defaults.

## Global Flags

The following global flags are supported by all `phenix` subcommands. The
[CLI reference](cli/index.md) lists them with every command's own flags.

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--base-dir.injects` | `string` | `<base-dir.phenix>/injects` | Directory that `phenix workflow apply` stages topology injects into. The `phenix ui` server reports its value to the CLI, and the flag on the `phenix workflow apply` command line overrides it. See [Workflow](workflow.md). |
| `--base-dir.minimega` | `string` | `/tmp/minimega` | Base minimega directory. |
| `--base-dir.phenix` | `string` | `/phenix` | Base phēnix directory. |
| `--base-dir.topologies` | `string` | `<base-dir.phenix>/topologies` | Directory that `phenix workflow apply` looks up topology directory names in. See [Workflow](workflow.md). |
| `--bridge-mode` | `string` | `manual` | Bridge naming mode for experiments (`auto` uses experiment name for bridge; `manual` uses user-specified bridge name, or `phenix` if omitted). A `phenix ui` running in `auto` mode makes the commands that reach it over `--unix-socket` use `auto`. See [Bridge Mode](bridge-mode.md). |
| `--deploy-mode` | `string` | — | Deploy mode for minimega VMs (`all`, `no-headnode`, or `only-headnode`). |
| `-h, --help` | — | — | Display help information for the command. |
| `--hostname-suffixes` | `string` | `-minimega,-phenix` | Hostname suffixes to strip. |
| `--log.console` | `string` | `stderr` | Destination output for console logs in text format (`stderr`, `stdout`, or a file path). |
| `--log.level` | `string` | `info` | Level to log messages at (`debug`, `info`, `warn`, `error`). |
| `--log.system.max-age` | `int` | `90` | Maximum number of days to retain old log files. |
| `--log.system.max-backups` | `int` | `3` | Maximum number of old log files to retain. |
| `--log.system.max-size` | `int` | `100` | Maximum size in megabytes of the system log file before rotation. |
| `--log.system.path` | `string` | `/var/log/phenix/phenix.log` | Path to persistent system log in JSON Lines format, with one JSON object per line. |
| `--mount-dir` | `string` | `<base-dir.phenix>/mounts` | Base directory for VM filesystem mounts. |
| `--store.endpoint` | `string` | `bolt:///etc/phenix/store.bdb` | BoltDB store endpoint (`bolt://<path>`). |
| `--unix-socket` | `string` | `/tmp/phenix.sock` | phēnix unix socket to listen on (`ui` subcommand) or connect to. |
| `--use-gre-mesh` | `bool` | `false` | Use GRE tunnels between mesh nodes for VLAN trunking. See [GRE Mesh](gre-mesh.md). |

## Settings Reference

| Setting Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `bridge-mode` | `PHENIX_BRIDGE_MODE` | `manual` | Bridge naming mode for experiments. `auto` uses the experiment name as the bridge name; `manual` uses the user-specified bridge name, or `phenix` if not specified. See [Bridge Mode](bridge-mode.md). |
| `use-gre-mesh` | `PHENIX_USE_GRE_MESH` | `false` | Enable Layer 2 GRE tunnels for the default bridge of experiments created or updated with this setting. See [GRE Mesh](gre-mesh.md) for usage, server inheritance, and MTU requirements. `phenix ui` reads this setting at startup. **(Restart Required)** |
| `log.level` | `PHENIX_LOG_LEVEL` | `info` | Global log verbosity (`debug`, `info`, `warn`, `error`). |
| `log.console` | `PHENIX_LOG_CONSOLE` | `stderr` | Destination for console logs (`stderr`, `stdout`, or a file path). Uses **Text/Human-Readable** format. Note: Setting this to a file path will prevent console logs from appearing in `docker logs`. |
| `log.system.path` | `PHENIX_LOG_SYSTEM_PATH` | `/var/log/phenix/phenix.log` | Path to the persistent system log file (used by UI). Uses **JSON** format. This is independent of `log.console` and is always active. |
| `log.system.max-size` | `PHENIX_LOG_SYSTEM_MAX_SIZE` | `100` | Max size in MB before rotation. |
| `log.system.max-backups` | `PHENIX_LOG_SYSTEM_MAX_BACKUPS` | `3` | Number of old log files to retain. |
| `log.system.max-age` | `PHENIX_LOG_SYSTEM_MAX_AGE` | `90` | Max age in days to retain old logs. |
| `ui.logs.level` | `PHENIX_UI_LOGS_LEVEL` | `""` | Log level for the web UI stream (defaults to `log.level`). |
| `ui.logs.minimega-path` | `PHENIX_UI_LOGS_MINIMEGA_PATH` | `""` | Path to the minimega log file to display in the UI and offer for download on the Logs page. **(Restart Required)** |
| `ui.features` | `PHENIX_UI_FEATURES` | `""` | Comma-separated list of optional UI features to enable (space-separated in `PHENIX_UI_FEATURES`). Currently supports `vm-mount`, which enables transferring files to and from a running VM. See [Mount a VM](vms.md#mount-a-vm). **(Restart Required)** |
| `ui.file-server-endpoint` | `PHENIX_UI_FILE_SERVER_ENDPOINT` | `0` (disabled) | Address (`port` or `host:port`) for the separate experiment file-upload server. A port-only value binds to `127.0.0.1`. Also works with the `vm-mount` feature - see [Uploading Experiment Files from the phēnix Server](vms.md#uploading-experiment-files-from-the-phenix-server). If it is set and the address cannot be listened on, `phenix ui` exits with an error. **(Restart Required)** |
| `ui.shutdown-grace-period` | `PHENIX_UI_SHUTDOWN_GRACE_PERIOD` | `5` | How many seconds stopping `phenix ui` may take to finish requests and jobs and close connections, as a whole number such as `10`. `0` closes everything without waiting; a negative value, or one that is not a whole number of seconds (such as `10s` or `1.5`), is refused at startup. Also `phenix ui --shutdown-grace-period`. See [Stopping the Server](#stopping-the-server). **(Restart Required)** |
| `ui.netflow.max-size` | `PHENIX_UI_NETFLOW_MAX_SIZE` | `100` | Maximum size in megabytes of each recorded netflow capture, as a whole number. A capture that reaches it stops recording and is marked truncated, while the live view goes on. `0` does not record captures; a negative value, or one that is not a whole number, is refused at startup. Also `phenix ui --netflow.max-size`. See [Recorded Captures](netflow.md#recorded-captures). **(Restart Required)** |
| `mount-dir` | `PHENIX_MOUNT_DIR` | `<base-dir.phenix>/mounts` | Base directory used for VM filesystem mounts created via `phenix vm mount`. See [Mount a VM](vms.md#mount-a-vm). |
| `base-dir.injects` | `PHENIX_BASE_DIR_INJECTS` | `<base-dir.phenix>/injects` | Directory that `phenix workflow apply` stages a topology directory's `phenix-injects/` into, as `<base-dir.injects>/<name>`. The phēnix server's value is used unless `--base-dir.injects` is given; see [Workflow](workflow.md#settings). Use an absolute path. **(Restart Required)** |
| `base-dir.topologies` | `PHENIX_BASE_DIR_TOPOLOGIES` | `<base-dir.phenix>/topologies` | Directory where `phenix workflow apply NAME` looks for `NAME` when `NAME` isn't an existing path. Use an absolute path. See [Workflow](workflow.md). |

## Stopping the Server

`phenix ui` shuts down cleanly on `SIGINT` (CTRL+C) or `SIGTERM` (`docker stop`),
taking at most `ui.shutdown-grace-period` seconds (5 by default):

* It stops accepting connections at once. Requests in progress may finish
  until the grace period ends, and are cut off then.
* It closes every websocket, including consoles, SCORCH terminals, VNC and
  port forwards, with close code `1001` (going away), so the web UI says the
  server is shutting down rather than that the network failed. New websockets
  are refused with `503 Service Unavailable`.
* Running background jobs get the first half of the grace period to finish,
  and are canceled then. Queued jobs are canceled at once, and starting a job
  or a console answers `503 Service Unavailable`.
* It ends minimega consoles and SCORCH terminals, so their processes do not
  outlive the server.
* It logs `shutdown complete` once everything has stopped, or a warning that
  names what was still running when the grace period ended, and exits with
  status 0 either way.

A second `SIGINT` or `SIGTERM` stops the server at once, with exit status 130
or 143. `docker stop` waits 10
seconds before it kills the container, which covers the default. If you raise
`ui.shutdown-grace-period` past that, raise Docker's timeout with it:
`stop_grace_period` in `docker-compose.yml`, or `docker stop --time`.

## The Settings Page

The web UI's `Settings` tab needs `update` on `settings`. Unlike the settings
above, these are kept in the data store and take effect as soon as they are
saved, with no `config.yaml` change or restart.

| Section | Setting | Default |
| :--- | :--- | :--- |
| `Password Settings` | `Require a lowercase letter`, `Require a number`, `Require a symbol` | on |
| `Password Settings` | `Require an uppercase letter` | off |
| `Password Settings` | `Minimum length of password`, 8 to 32 | `8` |
| `Timeout Settings` | `Log out users after period of inactivity` | off |
| `Timeout Settings` | `Time (minutes) to log out users after idle for` | `30` |
| `Timeout Settings` | `Display idle user logout with (minutes) left` | `3` |
| `File Logging Settings` | `Max log file size (MiB)` of the system log | `100` |
| `File Logging Settings` | `Max number of file rotations (0 for infinite)` | `0` |
| `File Logging Settings` | `Max rotated log file age (0 for infinite)`, in days | `90` |

The two idle timeout boxes are greyed out until the timeout is turned on. The
page opens on the settings it last loaded, and the refresh button in the header
reloads them without discarding edits you have not saved. `Reset Form` puts
the loaded settings back, `Save Changes` saves them and says
`Settings updated`, and both stay greyed out until you change something.

## phēnix Apps Environment Variables

Apps (`phenix-apps`) run as subprocesses and read their configuration from the environment.
phēnix sets these itself when it launches an app:

| Environment Variable | Value | Description |
| :--- | :--- | :--- |
| `PHENIX_DIR` | `base-dir.phenix` | Base phēnix data directory. |
| `PHENIX_FILES_DIR` | experiment files directory | Where the app reads and writes experiment files. |
| `PHENIX_LOG_LEVEL` | phēnix's own value, else `DEBUG` | App log verbosity. |
| `PHENIX_LOG_FILE` | `stderr` | App logs stream back to phēnix rather than to a file. |
| `PHENIX_DRYRUN` | `true` / `false` | Whether the run is a dry run. |
| `PHENIX_STORE_ENDPOINT` | `store.endpoint` | Data store endpoint (user apps only). |
| `PHENIX_SCORCH_STARTTIME` | run start time | Scorch components only. |

The rest are inherited from the phēnix process:

| Environment Variable | Default | Description |
| :--- | :--- | :--- |
| `PHENIX_TEMP_DIR` | `/tmp/phenix` | App temporary directory. |
| `MM_FILEPATH` | `/phenix/images` | Base minimega file path. |
| `MM_SOCKET_PATH` | `/tmp/minimega/minimega` | minimega command socket. |

### miniccc (`cc`) Timing

Apps drive VMs over minimega's `cc` command-and-control channel. These bound how long an app
waits, in seconds.

| Environment Variable | Default | Description |
| :--- | :--- | :--- |
| `PHENIX_CC_POLL_RATE` | `2.0` | Interval between `cc` polls. |
| `PHENIX_CC_CLIENT_GRACE` | `300.0` | Wait for a miniccc client to register before failing. |
| `PHENIX_CC_SEND_GRACE` | `300.0` | Wait for a file send or component start to be acknowledged. |
| `PHENIX_CC_CMD_GRACE` | `0.0` | Wait for a command response. `0` waits indefinitely, supervised by client liveness instead. |
| `PHENIX_CC_EXITCODE_GRACE` | `10.0` | Wait for an exit code once the response is counted. |
| `PHENIX_CC_LIVENESS_INTERVAL` | `10.0` | Interval between client-liveness checks during an unbounded wait. |
| `PHENIX_CC_LOG_INTERVAL` | `10.0` | Delay before the first "still waiting" log line. |
| `PHENIX_CC_LOG_MAX_INTERVAL` | `320.0` | Ceiling for that interval, which doubles each time. |
