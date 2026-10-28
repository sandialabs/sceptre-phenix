# Deprecations

This page lists each deprecated part of the `phenix` command line and the
part to use instead. A future release will remove each deprecated part.

## How phēnix Shows a Deprecation

A deprecated part continues to work until its removal. When you use it,
phēnix writes one warning to standard error, as in this example:

```text
phenix: warning: phenix settings get -f/--format is deprecated and will be removed in a future release; use phenix settings get -o/--output instead.
```

The warning does not change the output or the exit code of the command.
The `--help` output and the [CLI reference](cli/index.md) do not show
deprecated flags and commands.

## Deprecated Command Line Parts

| Deprecated | Use instead | Notes |
|---|---|---|
| `phenix settings get -f/--format` | `phenix settings get -o/--output` | The two flags take the same formats. |
| `phenix settings list -f/--format` | `phenix settings list -o/--output` | The two flags take the same formats. |
| `phenix experiment trigger-running <experiment name> [<app name> ...]` | `phenix experiment trigger <experiment name> running [<app name> ...]` | The `trig` alias is also deprecated. |
| `phenix experiment trigger <lifecycle> <experiment name> [<app name> ...]` | `phenix experiment trigger <experiment name> <lifecycle> [<app name> ...]` | phēnix reads the old order only when no experiment has the lifecycle as its name. |

## Deprecated Settings

phēnix continues to read these settings keys from the config file and
from their environment variables. When a key is set and its replacement
is not set, `phenix ui` uses the key and writes the warning to standard
error.

| Deprecated | Use instead | Notes |
|---|---|---|
| `unix-socket-gid` (config file), `PHENIX_UNIX_SOCKET_GID` (environment) | `ui.unix-socket-gid`, `PHENIX_UI_UNIX_SOCKET_GID` | The `--unix-socket-gid` flag of `phenix ui` sets `ui.unix-socket-gid`. |

## Flags That Are Not Deprecated

These flags are aliases of `-o/--output`. They are not deprecated.

| Flag | Same as |
|---|---|
| `phenix settings get --json`, `phenix settings list --json`, `phenix settings db list --json` | `-o json` |
| `phenix util app-json -p/--pretty` | `-o json` |
| `phenix util role-table -p/--pretty` | `-o markdown` |

Do not use an alias together with an `-o` value that gives a different
format. phēnix refuses that command line.

`phenix config get -p/--pretty` is not an alias. JSON output is always
indented, so the flag has no effect. It stays so that existing scripts work.
