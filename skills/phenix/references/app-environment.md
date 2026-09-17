# Environment Variables (phenix apps)

Two different things set environment variables an app subprocess sees;
identify which one applies before debugging a missing variable:

1. **Variables phenix sets itself.** When phenix launches a user app, SCORCH
   component, or user scheduler, it appends a short fixed list to the
   subprocess environment (`src/go/app/user.go`, `src/go/api/scorch/user.go`,
   `src/go/scheduler/user.go`). This is the only list phenix controls.
2. **Variables the app library reads from the inherited environment.** The
   subprocess also inherits the phenix process's own environment
   (`src/go/util/shell/exec.go` starts from `os.Environ()`). The Python
   `phenix_apps` library (in `sceptre-phenix-apps`,
   `src/python/phenix_apps/common/settings.py`) reads several extra variables
   from that inherited environment. phenix does not set, read, or document
   defaults for them; they take effect only if the operator exports them on the
   phenix process (systemd unit, container env, shell).

## Variables phenix sets

| Environment Variable | Value | Set for | Description |
|---|---|---|---|
| `PHENIX_DIR` | `base-dir.phenix` | apps, SCORCH, schedulers | Base phēnix data directory |
| `PHENIX_FILES_DIR` | experiment files directory | apps, SCORCH | Where the app reads/writes experiment files |
| `PHENIX_LOG_LEVEL` | phenix's own `PHENIX_LOG_LEVEL` if exported, else `DEBUG` | apps, SCORCH (schedulers: always `DEBUG`) | App log verbosity |
| `PHENIX_LOG_FILE` | `stderr` | apps, SCORCH, schedulers | App logs stream back to phēnix rather than to a file |
| `PHENIX_DRYRUN` | `true`/`false` | apps, SCORCH | Whether the run is a dry run |
| `PHENIX_STORE_ENDPOINT` | `store.endpoint` | apps only | Data store endpoint |
| `PHENIX_SCORCH_STARTTIME` | run start time | SCORCH only | Start time of the SCORCH run |

A user app that needs anything else must read it from the experiment spec on
stdin or from its own configuration; phenix will not pass it.

## Variables read by the `phenix_apps` Python library

These are not set by phenix. They are `os.getenv` lookups in the
`phenix_apps` library with the defaults shown, so a Python app sees the default
unless the phenix process itself was started with the variable exported. Go
apps and other non-Python apps are unaffected unless they choose to read them.

| Environment Variable | Library default | Description |
|---|---|---|
| `PHENIX_TEMP_DIR` | `/tmp/phenix` | App temporary directory |
| `MM_FILEPATH` | `/phenix/images` | Base minimega file path |
| `MM_SOCKET_PATH` | `/tmp/minimega/minimega` | minimega command socket |

The library drives VMs over minimega's `cc` (command-and-control/miniccc)
channel; these bound how long it waits (seconds):

| Environment Variable | Library default | Description |
|---|---|---|
| `PHENIX_CC_POLL_RATE` | `2.0` | Interval between `cc` polls |
| `PHENIX_CC_CLIENT_GRACE` | `300.0` | Wait for a miniccc client to register before failing |
| `PHENIX_CC_SEND_GRACE` | `300.0` | Wait for a file send or component start to be acknowledged |
| `PHENIX_CC_CMD_GRACE` | `0.0` | Wait for a command response (`0` waits indefinitely, supervised by client liveness) |
| `PHENIX_CC_EXITCODE_GRACE` | `10.0` | Wait for an exit code once the response is counted |
| `PHENIX_CC_LIVENESS_INTERVAL` | `10.0` | Interval between client-liveness checks during an unbounded wait |
| `PHENIX_CC_LOG_INTERVAL` | `10.0` | Delay before the first "still waiting" log line |
| `PHENIX_CC_LOG_MAX_INTERVAL` | `320.0` | Ceiling for that interval, which doubles each time |

If a Python app is not picking one of these up, check the environment of the
running `phenix` process (`cat /proc/$(pidof phenix)/environ | tr '\0' '\n'`),
not the app's scenario config.

See [phēnix Apps Environment Variables](https://phenix.sceptre.dev/latest/settings/#phenix-apps-environment-variables)
for the authoritative reference.
