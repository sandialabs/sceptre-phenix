# Troubleshooting

This page consolidates common issues and their solutions when working with phēnix.

## Logging Issues

### Logs Not Appearing in UI

If your application logs are not showing up in the phēnix web UI, check the following:

* **Check the stream**: Ensure your app writes logs to `stderr`. `stdout` is reserved for returning modified experiment configuration JSON to phēnix.
* **Check the format**: The phēnix core expects valid, single-line JSON objects. If your app prints raw text (like `Starting app...`), it may be logged as a warning or error by the core but will not be indexed correctly for the UI.
* **Check the environment**: Verify `PHENIX_LOG_FILE=stderr` is set in your app's environment (phēnix does this automatically for apps it launches).

### JSON Parsing Errors

If you see "malformed log entry" warnings in the main phēnix system log, it's likely because your application is writing non-JSON text to `stderr`.

* **Debug Prints**: Ensure you are not using `print()` (Python) or `fmt.Println()` (Go) for debugging. These pollute the `stderr` stream. Use the structured logger.
* **Tracebacks**: Ensure exception tracebacks are not being printed as raw text.
  * **Python**: Use `logger.exception("message")` inside an `except` block. This correctly captures the traceback as a JSON field. Standard `print(e)` or `traceback.print_exc()` will break the parser.
  * **Go**: Use structured logging fields for errors (e.g., `slog.Error("message", "err", err)`).

### Inconsistent Timestamps

Ensure timestamps in your JSON logs are in a consistent format that phēnix can parse, such as RFC3339. The core now enforces a `2006-01-02 15:04:05.000` format for its own file logs for easier reading, but is flexible on ingest.

## Configuration Issues

### Do Not Delete `config.yaml`

If you delete the `config.yaml` file while phēnix is running, the internal file watcher will break, and dynamic configuration changes will no longer apply.

To reset your configuration, use the command `phenix settings unset --all` instead of deleting the file.

### Scenario v1 to v2 Upgrade

The phēnix Scenario configuration was upgraded from `v1` to `v2`. While phēnix attempts to handle this automatically, experiments created with an older `v1` scenario may fail with an error like this:

```text
'scenario.apps': source data must be an array or slice, got map
```

There are two ways to fix this:

1. **Delete and Recreate**: Delete the experiment using `phenix config delete experiment/<name>` and recreate it using `phenix experiment create ...`.
2. **Edit the Experiment**: Manually edit the experiment configuration.

If you choose to edit the experiment, you need to convert the `scenario.apps` map into a list.

**Before (v1 format):**

```yaml
scenario:
  apps:
    experiment:
    - name: test-user-app
      metadata: {}
    host:
    - name: protonuke
      hosts:
      - hostname: host-00
        metadata:
          args: -logfile /var/log/protonuke.log
```

**After (v2 format):**

```yaml
scenario:
  apps:
  - name: test-user-app
    metadata: {}
  - name: protonuke
    hosts:
    - hostname: host-00
      metadata:
        args: -logfile /var/log/protonuke.log
```
