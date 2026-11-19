# Settings & Configuration

phēnix supports dynamic configuration management via the `phenix settings` command.
A running `phenix ui` applies changes to the `log.*` settings, `ui.logs.level`,
`experiment.disk-check`, `minimega.read-connections` and the users file at
once, without restarting. It reads every other setting only at startup, and
logs a warning when one of those changes:

```text
config change takes effect only when phenix ui restarts keys="store.endpoint, ui.listen-endpoint"
```

## Configuration Files

phēnix looks for a configuration file (e.g. `config.yaml`, `config.json`, `config.toml`) in the following locations, in order, and reads the first it finds:

**When run as root:**

* `./config.yaml` (the working directory)
* `/etc/phenix/config.yaml`

**When run as a regular user:**

* `./config.yaml` (the working directory)
* `$HOME/.config/phenix/config.yaml`
* `/etc/phenix/config.yaml`

It looks for `users.yaml` in the same locations. `sudo phenix` searches as
the user that runs `sudo`, so it also searches that user's
`$HOME/.config/phenix`.

phēnix creates the default log directory, `$HOME/phenix_logs`
(`/var/log/phenix` as root), and, if it finds no config file, an empty
`config.yaml` in `$HOME/.config/phenix/` (`/etc/phenix/` as root).

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
phenix no longer supports the `etcd://` store endpoint.

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

`phenix settings set` gives the value the type of the flag of the setting. For
example, `log.system.max-size` takes an integer, `ui.jwt-lifetime` takes a
duration such as `24h`, and `ui.users` takes a comma-separated list. A string
setting, such as `ui.jwt-signing-key`, keeps the text as it is, so `0123` stays
`0123`. The command refuses a value that does not have the type of the setting.

`phenix settings set` and `phenix settings unset` change only the key. The
comments, the key order and the other keys in the file stay. A new key goes at
the end of its section. An empty section, such as `{}`, changes to block style
when you set a key in it. When you unset a key that is not in the file, the
file does not change.

Both commands write to the config file that phēnix read, and refuse to change
one that isn't YAML.

## Checking the Configuration

`phenix doctor` checks the configuration without a running `phenix ui`. It
shows where each setting comes from. See [Doctor](doctor.md).

## Configuration Precedence

phēnix resolves each setting in the following order (highest to lowest):

1. **Command Line Flags**: Arguments passed directly to the binary (e.g., `phenix ui --log.level=debug`).
2. **Environment Variables**: Variables like `PHENIX_LOG_LEVEL`. An empty variable is ignored.
3. **Config File**: Settings in `config.yaml` (managed via `phenix settings set`), with
   `users.yaml` merged over it.
4. **Defaults**: Internal application defaults.

A `PHENIX_*` variable therefore hides a value that `phenix settings set` writes
to the config file. `phenix doctor` warns about each one in its `env-overrides`
check; unset the variable to use the file's value.

## Global Flags

The following global flags are supported by all `phenix` subcommands. The
[CLI reference](cli/index.md) lists them with every command's own flags.

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--base-dir.builder-templates` | `string` | `<base-dir.phenix>/builder/templates` | Directory of Builder node template files that `phenix ui` reads at start. See [Template files on the server](builder/details/administration.md#template-files-on-the-server). |
| `--base-dir.injects` | `string` | `<base-dir.phenix>/injects` | Directory that `phenix workflow apply` stages topology injects into. The `phenix ui` server reports its value to the CLI, and the flag on the `phenix workflow apply` command line overrides it. See [Workflow](workflow.md). |
| `--base-dir.minimega` | `string` | `/tmp/minimega` | Base minimega directory. |
| `--base-dir.phenix` | `string` | `/phenix` | Base phēnix directory. |
| `--base-dir.topologies` | `string` | `<base-dir.phenix>/topologies` | Directory that `phenix workflow apply` looks up topology directory names in. See [Workflow](workflow.md). |
| `--bridge-mode` | `string` | `manual` | Bridge naming mode for experiments (`auto` uses experiment name for bridge; `manual` uses user-specified bridge name, or `phenix` if omitted). A `phenix ui` running in `auto` mode makes the commands that reach it over `--unix-socket` use `auto`. See [Bridge Mode](bridge-mode.md). |
| `--color` | `string` | `auto` | When to color the output (`auto`, `on`, or `off`). `auto` colors only a terminal, and not when `NO_COLOR` is set or `TERM` is `dumb`. See [Colors](#colors). |
| `--deploy-mode` | `string` | — | Deploy mode for minimega VMs (`all`, `no-headnode`, or `only-headnode`). |
| `--experiment.disk-check` | `string` | `error` | What a start does when a VM disk image or CD-ROM image is missing, or its backing chain is broken (`error`, `warn`, or `off`). See [Disk Check Before a Start](experiments.md#disk-check-before-a-start). |
| `-h, --help` | — | — | Display help information for the command. |
| `--hostname-suffixes` | `string` | `-minimega,-phenix` | Hostname suffixes to strip. |
| `--log.console` | `string` | `stderr` | Destination output for console logs in text format (`stderr`, `stdout`, or a file path). |
| `--log.level` | `string` | `info` | Level to log messages at (`debug`, `info`, `warn`, `error`). |
| `--log.system.max-age` | `int` | `90` | Maximum number of days to retain old log files. |
| `--log.system.max-backups` | `int` | `3` | Maximum number of old log files to retain. |
| `--log.system.max-size` | `int` | `100` | Maximum size in megabytes of the system log file before rotation. |
| `--log.system.path` | `string` | `/var/log/phenix/phenix.log` as root, `$HOME/phenix_logs/phenix.log` otherwise | Path to persistent system log in JSON Lines format, with one JSON object per line. |
| `--minimega.read-connections` | `int` | `16` | Maximum number of read-only commands that phēnix sends to minimega at the same time, from 3 to 64. phēnix keeps 2 of them for reads of the head node. |
| `--mount-dir` | `string` | `<base-dir.phenix>/mounts` | Base directory for VM filesystem mounts. |
| `--store.endpoint` | `string` | `bolt:///etc/phenix/store.bdb` as root, `bolt://$HOME/.phenix.bdb` otherwise | BoltDB store endpoint (`bolt://<path>`). |
| `--unix-socket` | `string` | `/tmp/phenix.sock` | phēnix unix socket to listen on (`ui` subcommand) or connect to. |
| `--use-gre-mesh` | `bool` | `false` | Use GRE tunnels between mesh nodes for VLAN trunking. See [GRE Mesh](gre-mesh.md). |

## Colors

phēnix colors the statuses in tables, the rows of `phenix doctor`, warnings
and errors. Set `--color`, the `color` setting or `PHENIX_COLOR` to `auto`
(the default: only on a terminal, and not when `NO_COLOR` is set or `TERM` is
`dumb`), `on` (even into a file or a pipe) or `off`. JSON and YAML output, and
`phenix doctor --report`, never have color.

## Settings Reference

| Setting Key | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `bridge-mode` | `PHENIX_BRIDGE_MODE` | `manual` | Bridge naming mode for experiments. `auto` uses the experiment name as the bridge name; `manual` uses the user-specified bridge name, or `phenix` if not specified. See [Bridge Mode](bridge-mode.md). |
| `experiment.disk-check` | `PHENIX_EXPERIMENT_DISK_CHECK` | `error` | What a start does when a VM disk image or CD-ROM image is missing, or its backing chain is broken. `error` refuses the start, `warn` starts the experiment and shows each problem as a warning, and `off` does not check the images. It applies to every start: the CLI, the web UI, the REST API, `phenix workflow apply`, and the bulk start and restart of VMs. phēnix refuses another value. `phenix ui` applies a change at once. See [Disk Check Before a Start](experiments.md#disk-check-before-a-start). |
| `use-gre-mesh` | `PHENIX_USE_GRE_MESH` | `false` | Enable Layer 2 GRE tunnels for the default bridge of experiments created or updated with this setting. See [GRE Mesh](gre-mesh.md) for usage, server inheritance, and MTU requirements. `phenix ui` reads this setting at startup. **(Restart Required)** |
| `log.level` | `PHENIX_LOG_LEVEL` | `info` | Global log verbosity (`debug`, `info`, `warn`, `error`). |
| `color` | `PHENIX_COLOR` | `auto` | When phēnix colors its output: statuses in tables, `phenix doctor` rows, warnings and errors. `auto` colors only a terminal, and not when `NO_COLOR` is set or `TERM` is `dumb`. `on` and `off` ignore both variables. JSON and YAML output never have color. phēnix refuses another value. See [Colors](#colors). |
| `log.console` | `PHENIX_LOG_CONSOLE` | `stderr` | Destination for console logs (`stderr`, `stdout`, or a file path). Uses **Text/Human-Readable** format. Note: Setting this to a file path will prevent console logs from appearing in `docker logs`. |
| `log.system.path` | `PHENIX_LOG_SYSTEM_PATH` | `/var/log/phenix/phenix.log` as root, `$HOME/phenix_logs/phenix.log` otherwise | Path to the persistent system log file (used by UI). Uses **JSON** format. This is independent of `log.console` and is always active. |
| `log.system.max-size` | `PHENIX_LOG_SYSTEM_MAX_SIZE` | `100` | Max size in MB before rotation. |
| `log.system.max-backups` | `PHENIX_LOG_SYSTEM_MAX_BACKUPS` | `3` | Number of old log files to retain. |
| `log.system.max-age` | `PHENIX_LOG_SYSTEM_MAX_AGE` | `90` | Max age in days to retain old logs. |
| `ui.logs.level` | `PHENIX_UI_LOGS_LEVEL` | `""` | Log level for the web UI stream (defaults to `log.level`). |
| `minimega.read-connections` | `PHENIX_MINIMEGA_READ_CONNECTIONS` | `16` | Maximum number of read-only commands, such as `vm info`, that phēnix sends to minimega at the same time. Each command uses a connection of its own, which phēnix opens when it needs one and closes after 5 minutes without use. The value must be from 3 to 64. phēnix uses the nearest value in that range for a value outside it, and logs a warning. phēnix keeps 2 connections for reads of the head node only, and lets screenshots use at most 4 fewer than the maximum. It applies to `phenix ui` and to every CLI command. `phenix ui` applies a change in the config file at once, unless a flag or an environment variable sets the value. The Settings page of the web UI also changes it. |
| `ui.logs.minimega-path` | `PHENIX_UI_LOGS_MINIMEGA_PATH` | `""` | Path to the minimega log file to display in the UI and offer for download on the Logs page. **(Restart Required)** |
| `ui.features` | `PHENIX_UI_FEATURES` | `""` | Optional UI features to enable. Separate them with commas in the `--features` flag and with spaces in `PHENIX_UI_FEATURES`. Use a YAML list in the configuration file. The features are:<ul><li>`vm-mount`: enables transferring files to and from a running VM (see [Mount a VM](vms.md#mount-a-vm)). Set it yourself.</li><li>`webshark`: `phenix ui` turns it on when it finds a [WebShark build](webshark.md#installing-webshark), and refuses to start with `webshark` set but no WebShark build.</li><li>`tunneler-download`: `phenix ui` turns it on when the `downloads/tunneler` directory exists (see [Downloading the Tunneler](tunneler.md#downloading-the-tunneler)).</li><li>`file-server`: `phenix ui` turns it on when `ui.file-server-endpoint` is set.</li><li>`minimega-captures`: `phenix ui` turns it on when minimega supports experiment captures (see [Requirements](packet-captures.md#requirements)).</li></ul>**(Restart Required)** |
| `ui.file-server-endpoint` | `PHENIX_UI_FILE_SERVER_ENDPOINT` | `0` (disabled) | Address (`port` or `host:port`) for the separate experiment file-upload server. A port-only value binds to `127.0.0.1`. Also works with the `vm-mount` feature - see [Uploading Experiment Files from the phēnix Server](vms.md#uploading-experiment-files-from-the-phenix-server). If it is set and the address cannot be listened on, `phenix ui` exits with an error. **(Restart Required)** |
| `ui.shutdown-grace-period` | `PHENIX_UI_SHUTDOWN_GRACE_PERIOD` | `5` | How many seconds stopping `phenix ui` may take to finish requests and jobs and close connections, as a whole number such as `10`. `0` closes everything without waiting; a negative value, or one that is not a whole number of seconds (such as `10s` or `1.5`), is refused at startup. Also `phenix ui --shutdown-grace-period`. See [Stopping the Server](#stopping-the-server). **(Restart Required)** |
| `ui.netflow.max-size` | `PHENIX_UI_NETFLOW_MAX_SIZE` | `100` | Maximum size in megabytes of each recorded netflow capture, as a whole number. A capture that reaches it stops recording and is marked truncated, while the live view goes on. `0` does not record captures; a negative value, or one that is not a whole number, is refused at startup. Also `phenix ui --netflow.max-size`. See [Recorded Captures](netflow.md#recorded-captures). **(Restart Required)** |
| `ui.captures.max-size` | `PHENIX_UI_CAPTURES_MAX_SIZE` | `10240` | Maximum size in megabytes of each packet capture file that minimega writes, as a whole number. `0` sets no limit; a negative value, or one that is not a whole number, is refused at startup. Also `phenix ui --captures.max-size`. See [Capture Options](packet-captures.md#capture-options). **(Restart Required)** |
| `ui.unix-socket-gid` | `PHENIX_UI_UNIX_SOCKET_GID` | `-1` | ID of a group that may write to the unix socket of `phenix ui`. `-1` does not change the group. Also `phenix ui --unix-socket-gid`. The deprecated key `unix-socket-gid` (`PHENIX_UNIX_SOCKET_GID`) still works when this key is not set, and `phenix ui` then writes a warning to standard error. See [Deprecations](deprecations.md#deprecated-settings). **(Restart Required)** |
| `mount-dir` | `PHENIX_MOUNT_DIR` | `<base-dir.phenix>/mounts` | Base directory used for VM filesystem mounts created via `phenix vm mount`. See [Mount a VM](vms.md#mount-a-vm). |
| `base-dir.injects` | `PHENIX_BASE_DIR_INJECTS` | `<base-dir.phenix>/injects` | Directory that `phenix workflow apply` stages a topology directory's `phenix-injects/` into, as `<base-dir.injects>/<name>`. The phēnix server's value is used unless `--base-dir.injects` is given; see [Workflow](workflow.md#settings). Use an absolute path. **(Restart Required)** |
| `base-dir.topologies` | `PHENIX_BASE_DIR_TOPOLOGIES` | `<base-dir.phenix>/topologies` | Directory where `phenix workflow apply NAME` looks for `NAME` when `NAME` isn't an existing path. Use an absolute path. See [Workflow](workflow.md). |
| `base-dir.builder-templates` | `PHENIX_BASE_DIR_BUILDER_TEMPLATES` | `<base-dir.phenix>/builder/templates` | Directory of Builder node template files (`*.yaml`, `*.yml`, `*.json`) that `phenix ui` reads at start as read-only server collections. Use an absolute path. See [Template files on the server](builder/details/administration.md#template-files-on-the-server). **(Restart Required)** |

## Stopping the Server

`phenix ui` shuts down cleanly on `SIGINT` (CTRL+C) or `SIGTERM`
(`docker stop`), within `ui.shutdown-grace-period` seconds (5 by default).
Requests in progress get the whole grace period to finish, running
[background jobs](jobs.md) get the first half, and queued jobs are canceled.
Every websocket closes (consoles, SCORCH terminals, VNC and port forwards). A
second signal stops the server at once. `docker stop` kills the container
after 10 seconds, so if you raise the grace period past that, raise Docker's
timeout too (`stop_grace_period` in `docker-compose.yml`, or
`docker stop --time`).

## The Settings Page

The `Settings` page is in the `Admin` menu.

![Settings page with its Password, Timeout, File Logging and minimega settings and the Reset Form and Save Changes buttons.](images/settings_page.png){: width=800 .center}

The password and timeout settings live in the data store and take effect as
soon as you save. The minimum password length is 8 to 32. From the command
line, use `phenix settings db edit`:

```shell
phenix settings db edit Password MinLength 12
```

The file logging and `minimega` fields apply at once and save to the config
file like `phenix settings set`. The save fails if a flag or `PHENIX_` variable of
`phenix ui` sets a log limit you changed, or if `phenix ui` has no YAML config
file.

### Web UI Session Timeout

Turn on `Log out users after period of inactivity` to log out idle users. The
idle time must be at least `0.5` minutes, and the warning has to come before
the idle time is up.

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

## See Also

* [phenix settings](cli/settings.md): view and change stored settings.
* [phenix ui](cli/ui.md): the flags of the server.
* [Logging](logging.md): what the log settings do.
* [Troubleshooting](troubleshooting.md#configuration-issues): when a setting has no effect.
