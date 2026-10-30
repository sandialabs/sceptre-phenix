# Troubleshooting

This page consolidates common issues and their solutions when working with phēnix.

## Logging Issues

### Logs Not Appearing in UI

If your application logs are not showing up in the phēnix web UI, check the following:

* **Check the stream**: Ensure your app writes logs to `stderr`. `stdout` is reserved for returning modified experiment configuration JSON to phēnix.
* **Check the format**: The phēnix core expects valid, single-line JSON objects. If your app prints raw text (like `Starting app...`), it may be logged as a warning or error by the core but will not be indexed correctly for the UI.
* **Check the environment**: Verify `PHENIX_LOG_FILE=stderr` is set in your app's environment (phēnix does this automatically for apps it launches).
* **Check for skipped lines**: When the server logs faster than it can send to the browser, the Logs page's live view skips lines and shows a `WARN` entry such as `120 log lines skipped while the server was busy`. The lines are still in the log file: reload the Logs page, or call `GET /logs`, to see them. The server also logs a `WebSocket broker dropped slow clients or live log lines` warning at most every 30 seconds.

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
phenix: warning: ignoring config file /etc/phenix/config.yaml: While parsing config: yaml: line 3: did not find expected key
```

The same warning names a `users.yaml` that does not parse, or a config
directory or file that could not be created on first run. Fix the file it
names; the warning appears in `docker logs phenix` for the Docker deployment.
`phenix doctor` reports the same problems, misspelled keys, and where each
setting's value comes from; see
[Checking the Configuration](settings.md#checking-the-configuration).

### Do Not Delete `config.yaml`

If you delete the `config.yaml` file while phēnix is running, the internal file watcher will break, and dynamic configuration changes will no longer apply.

To reset your configuration, use the command `phenix settings unset --all` instead of deleting the file.

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
