# Deprecations

This page lists each deprecated part of the `phenix` command line, settings
and configs, and the part to use instead. A future release will remove each deprecated part.

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

## Deprecated Config Values

phēnix continues to accept these values in a config. When it reads one, it
writes a warning to the [log](logging.md).

| Deprecated | Use instead | Notes |
|---|---|---|
| `os_type: linux` on a `Router` or `Firewall` node of a [Topology](configuration.md#topology) | `os_type: vyatta`, `vyos` or `minirouter` | The [vrouter app](apps.md#vrouter-app) configures the node as a Vyatta router. |

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

`phenix workflow apply --name` is also not deprecated: it's the same flag as
`-b/--branch-name`. If you give both, use the same value.

## See Also

* [CLI Reference](cli/index.md): the current commands and flags.
* [Settings](settings.md): the current settings keys and their environment variables.
* [Configuration](configuration.md): the fields of each config.
* [Logging](logging.md): the log that gets the warning for a deprecated config value.
