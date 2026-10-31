# Troubleshooting

This page consolidates common issues and their solutions when working with phēnix.

When reporting a bug, run `docker exec phenix phenix doctor --report` (or
`phenix doctor --report`) and paste its output into the issue. It gives the
phēnix and minimega versions, the OS, user and container, and the result of
each configuration check, with secrets redacted; see
[Checking the Configuration](settings.md#checking-the-configuration).

## Logging Issues

### Logs Not Appearing in UI

If your application logs are not showing up in the phēnix web UI, check the following:

* **Check the stream**: Ensure your app writes logs to `stderr`. `stdout` is reserved for returning modified experiment configuration JSON to phēnix.
* **Check the format**: The phēnix core expects valid, single-line JSON objects. If your app prints raw text (like `Starting app...`), it may be logged as a warning or error by the core but will not be indexed correctly for the UI.
* **Check the environment**: Verify `PHENIX_LOG_FILE=stderr` is set in your app's environment (phēnix does this automatically for apps it launches).
* **Check for skipped lines**: When the server logs faster than it can send to the browser, the Logs page's live view skips lines and shows a `WARN` entry such as `120 log lines skipped while the server was busy`. The lines are still in the log file: reload the Logs page, or call `GET /logs`, to see them. The server also logs a `WebSocket broker dropped slow clients or live log lines` warning at most every 30 seconds.

### No minimega Logs on the Logs Page

The Logs page shows minimega's log only when `ui.logs.minimega-path` names
the file minimega logs to, such as `/var/log/phenix/minimega.log` in the
Docker Compose setup. When the file is not there, `phenix ui` waits for it
without a warning. `phenix doctor` warns in its `ui.logs.minimega-path` check
when the file is missing, or when the setting is empty while minimega logs to
a file.

### JSON Parsing Errors

If you see "malformed log entry" warnings in the main phēnix system log, it's likely because your application is writing non-JSON text to `stderr`.

* **Debug Prints**: Ensure you are not using `print()` (Python) or `fmt.Println()` (Go) for debugging. These pollute the `stderr` stream. Use the structured logger.
* **Tracebacks**: Ensure exception tracebacks are not being printed as raw text.
    * **Python**: Use `logger.exception("message")` inside an `except` block. This correctly captures the traceback as a JSON field. Standard `print(e)` or `traceback.print_exc()` will break the parser.
    * **Go**: Use structured logging fields for errors (e.g., `slog.Error("message", "err", err)`).

### Inconsistent Timestamps

Ensure timestamps in your JSON logs are in a consistent format that phēnix can parse, such as RFC3339. The core now enforces a `2006-01-02 15:04:05.000` format for its own file logs for easier reading, but is flexible on ingest.

## Configuration Issues

### Settings in `config.yaml` Have No Effect

When phēnix cannot read its config file, it runs on environment variables
and defaults and prints a warning on stderr at startup, such as:

```text
phenix: warning: ignoring config file /etc/phenix/config.yaml: it does not parse at line 3
```

The warning gives only the line of the error, since the parser's message can
quote a password or key. The same warning names a `users.yaml` that does not
parse:

```text
phenix: warning: ignoring config file /etc/phenix/users.yaml: it does not parse at line 1
```

It also names a config directory or file that could not be created on first
run. Fix the file it names; the warning appears in `docker logs phenix` for
the Docker deployment.
`phenix doctor` reports the same problems, misspelled keys, and where each
setting's value comes from, then a summary naming each failed check with a
hint; see
[Checking the Configuration](settings.md#checking-the-configuration).

### Every Command Fails on a Mode Setting

Every phēnix command except `phenix doctor` reads `bridge-mode`,
`deploy-mode`, `experiment.disk-check` and `color` before it runs, and stops on
a value it does not know:

```text
setting user-specified bridge mode: setting bridge mode: unknown bridge mode provided: sideways
```

Fix the value in the flag, `PHENIX_BRIDGE_MODE`, `PHENIX_DEPLOY_MODE`,
`PHENIX_EXPERIMENT_DISK_CHECK` or `PHENIX_COLOR`, or the config file. `phenix doctor` fails its
`bridge-mode`, `deploy-mode`, `experiment.disk-check` or `color` check for such a value, and warns about a `log.level` or `log.system.*` value that
phēnix ignores or reads as `0`.

### Output Shows Escape Codes Such as `^[[32m`

phēnix colors its output when `--color` is `on`, or when it is `auto` and
the output is a terminal. A program that reads the output, or a terminal
without colors, then shows the codes as text. To remove the colors, do one
of these steps:

* Run the command with `--color off`.
* Set `NO_COLOR=1` in the environment of the command. `auto` then colors no
  output.
* Set `color: off` in the config file, or `PHENIX_COLOR=off`.

JSON and YAML output never have color. See [Colors](settings.md#colors).

### `phenix ui` Fails to Start, or Ignores TLS or Users

`phenix ui` exits at startup when it cannot listen on `ui.listen-endpoint` or
`ui.file-server-endpoint`, create its unix socket, or give the socket the
group in `unix-socket-gid`. It starts, but not as configured, when only one
of `ui.tls-key` and `ui.tls-cert` is set (it serves plain HTTP), when a
`ui.users` entry is not `name:password:role[:resource...]` (it skips the
user), or when `ui.jwt-lifetime` has no unit (`24` is 24 nanoseconds). Run
`phenix doctor --skip minimega` with the settings and user of `phenix ui`: its
`auth`, `tls` and `socket` checks report each of these.

### A Command Ignores `bridge-mode` or `deploy-mode`

Before it runs, every phēnix command asks `phenix ui` on `unix-socket` for its
modes. When `phenix ui` uses `bridge-mode: auto`, the command uses `auto` too,
and when `deploy-mode` is not set here, the command uses the mode of
`phenix ui`. `phenix doctor` warns in its `server` check when either mode
differs from the one set here, and names the mode the command uses. It also
warns when the version of `phenix ui` is not the version of the command.

### Do Not Delete `config.yaml`

If you delete the `config.yaml` file while phēnix is running, the internal file watcher will break, and dynamic configuration changes will no longer apply.

To reset your configuration, use the command `phenix settings unset --all` instead of deleting the file.

### `phenix settings set` Has No Effect

A flag or a `PHENIX_*` environment variable overrides the config file, so a
value that `phenix settings set` writes is hidden while one is set, as in a
Docker deployment that sets `PHENIX_LOG_LEVEL`. `phenix doctor` shows each
setting's source and warns in its `env-overrides` check about each variable
that overrides a different config file value. Drop the flag or unset the
variable to use the file's value. Its `settings-file` check names the file
`phenix settings set` writes, and its `config-paths` check names each config
file that phēnix ignores because another one comes first in the search path.

### A Command Sees Different Experiments Than `phenix ui`

A user other than root uses the store `$HOME/.phenix.bdb` by default, and root
uses `/etc/phenix/store.bdb`. So a command run without root reads a different
store than a `phenix ui` that runs as root. `sudo phenix` also uses the store
of the user that runs `sudo`; run the command from a root shell (`sudo -i`),
or set `store.endpoint`, to use the store of `phenix ui`. `phenix doctor`
warns in its `store-root` check when the command uses a per-user store while
`/etc/phenix/store.bdb` exists, and in its `store-owner` check when the store
file belongs to another user. Its `store-file` check fails when the store file
is not a whole BoltDB file.

### Upgrading the BoltDB Store

phēnix keeps BoltDB's free page list inside the store file. The first time it
writes to a store left by an earlier release, it walks the file once to build
that list and saves it; every open after that reads the saved list instead. This
happens on its own, so there is no need to delete or recreate `store.bdb` after
an upgrade, and an earlier release can still open a store this one has written.

### Reserved Experiment Names

An experiment cannot be named `minimega`, `__phenix__` or `all`, in any mix of
upper and lower case. Creating one fails immediately:

```text
cannot use 'minimega' for experiment name: reserved minimega namespace
```

## minimega Issues

### `timeout running command`

phēnix sends most minimega commands over one shared connection, one at a time.
A command that waits more than 10 minutes for that connection, or holds it for
more than 30 minutes, fails with `timeout running command`. A web request that
changes VMs gives minimega 5 minutes, or 10 when it changes several VMs, and
past that fails with an error that includes `context deadline exceeded`.
Long-running work, such as an experiment's start script and VM launches, uses
connections of its own and is not limited this way. This error usually means
minimega itself is stuck or overloaded: check `minimega -e version` and the
minimega log on the headnode.

### A Start Fails on a Broken Backing Chain

phēnix refuses to start an experiment when a VM disk image or CD-ROM image is
missing, or when a file in the backing chain of a disk is missing:

```text
Error: unable to start the foo experiment: qemu cannot open 1 disk on 1 VM: 1 error occurred:
    * VM host-01: disk kali.qc2: backing file /phenix/images/ro_kali.qc2 is missing
```

The experiment stays stopped. A restart refuses before it stops the
experiment, which continues to run. To repair the problem, do one of these
steps:

1. Restore the file that the error names, on the head node.
2. Point the image at a copy of the missing backing file. Use `Rebase` on the
   `Disks` page with `Change reference only` selected, or minimega's
   `disk set-backing`. A rebase without this option must read the missing
   file, so it fails.
3. Change the topology to use a different image.

If the file is on a compute node only, set `experiment.disk-check` to `warn`.
See [Disk Check Before a Start](experiments.md#disk-check-before-a-start).

To find such images before a start, run `phenix doctor`. Its `disk-chains`
check warns about each image in minimega's files directory with a broken
backing chain.

The start also refuses an image that qemu cannot open. Examples are an image
with the old AES encryption and an unknown qcow2 version. A qcow version 1
image gives a warning. To fix these images, see
[Image Format Versions](disks.md#image-format-versions). The `disk-formats`
check of `phenix doctor` lists them.

### phēnix Cannot Reach minimega or Its Images

`phenix doctor` fails its `minimega` check when minimega does not give its
version on `<base-dir.minimega>/minimega`, and names the likely cause: no
socket (minimega is not running, or `base-dir.minimega` names another
directory), a stale socket file, or a user that may not connect. Its
`minimega-env` check then reads the flags minimega runs with. It fails when
the `-filepath` directory does not exist for phēnix, which then cannot find
the images minimega launches. In a container, mount that directory at the
same path for both, as the Docker Compose setup does with `/phenix`.
