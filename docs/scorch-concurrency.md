# Simultaneous Scorch runs

Different configured Scorch runs can execute together in a running experiment.
Each invocation gets an execution ID, an owner, and persisted state. A run keeps
its reservation through cancellation, component cleanup, and artifact archiving.
The same configured run cannot be started twice at once.

## Setup, work, teardown

```yaml
name: scorch
metadata:
  runs:
    - name: setup-teardown
      start: [setup, ready]
      stop: [teardown]
    - name: work
      start: [work]
  components:
    - name: setup
      type: cc
      metadata:
        vms:
          - hostname: worker
            start:
              - type: exec
                args: mkdir -p /tmp/scorch-demo
                wait: true
    - name: ready
      type: break
      metadata: {}
    - name: teardown
      type: cc
      metadata:
        vms:
          - hostname: worker
            stop:
              - type: exec
                args: rm -rf /tmp/scorch-demo
                wait: true
    - name: work
      type: cc
      metadata:
        vms:
          - hostname: worker
            start:
              - type: exec
                args: touch /tmp/scorch-demo/result
                wait: true
```

1. Start `setup-teardown` on the experiment's Scorch run page.
2. At `ready`, hide the terminal. Hiding disconnects the browser; the breakpoint
   and its shell continue to exist. Reopen it with **Open breakpoint terminal**.
3. Start `work`, or other independent configured runs. Wait for their terminal
   outcomes, including cleanup and archiving, before resuming teardown.
4. Reopen the setup terminal and select **Continue run**, or exit its shell.
   `teardown` then runs in the stop stage. CLI-owned breakpoints must be continued
   in the CLI terminal that owns them.

A `pause` remains a timed delay, with its deadline shown in the UI. It is not a
manual breakpoint. Multiple nested loops retain their existing stage ordering.
Breakpoints require a web or interactive CLI controller. A noninteractive
periodic CLI run containing a breakpoint is rejected before setup begins.

## Shared resources and compatible components

Upgrade both `sceptre-phenix` and `sceptre-phenix-apps` together. The component
capability probe (`--scorch-capabilities`) advertises protocol version 1. Older
external components remain usable, but require exclusive namespace access.
External overrides of the entire Scorch app do not support the managed API.

The controller exports `PHENIX_SCORCH_EXECUTION_ID` and
`PHENIX_SCORCH_CONTROL_DIR` to component subprocesses without changing their
positional arguments. The latter defaults to `<PHENIX_DIR>/.scorch-control`.
All managed component processes for a namespace must run on the same controller
host and use this directory, including standalone CLI invocations.

Updated Python components serialize CC filter/prefix changes and each command
submission using a namespace file lock. They release the lock before response
waiting. CC command prefixes belong to an execution/component/loop/count, so a
managed `cc` reset deletes that component's commands and responses rather than
clearing the namespace. Ordinary minimega clients that do not use this lock can
still change shared CC state; do not run namespace resets alongside Scorch.

Runs share VMs and intentionally share experiment state. CC scripts must use
nonconflicting guest files, ports, service names, and commands. The controller
cannot infer the effects of arbitrary scripts. Background guest commands that
outlive their submission need explicit run stop/cleanup commands.

Admission conservatively reserves resource types through the whole run:

| Component types | Overlap policy |
| --- | --- |
| `break`, `pause`, `soh`, `tap`, updated `cc`, updated `vmstats` | Independent runs allowed |
| Updated `tcpdump`, `snort`, `ettercap`, `iperf` | Same type cannot overlap, because cleanup uses broad guest process operations |
| `mm`, other types, older external components | Exclusive experiment namespace |

Taps belong to an execution and are allocated under a host lock. Other runs'
tap subnets participate in allocation. Artifacts remain under each `run-N`
directory; archives include the execution ID. Filebeat retains a private data
directory per configured run.

## Stop and recovery

Experiment stop prevents new run admission, requests cancellation of every run
(including CLI owners), and waits for cleanup before destroying VMs. If draining
times out or an owner is interrupted, stop returns an error and retains the
experiment resources. Configuration changes/deletion are rejected while a run
is active or awaiting recovery.

On owner loss, Scorch marks the invocation `interrupted` and retains its claims.
It never automatically repeats setup or cleanup. A live local owner is not
displaced merely because its heartbeat is delayed. Remote owners lose their
lease after 30 seconds without a heartbeat; recovery must be performed on the
original controller host after verifying that its process exited.

Inspect remaining child/guest processes, CC commands, taps and network state,
files, and service changes. Clean them up manually, then use **Recover after
manual cleanup**. Recovery requires the current execution ID, an explicit
cleanup acknowledgement, and an exited local owner. It releases claims and
retains a failed outcome; it does not execute configured cleanup steps.

## API contract

- `GET /experiments/{name}/scorch/pipelines` returns `pipelines`,
  `runningRuns`, `executions` keyed by configured run ID, and `stopping`.
  The legacy `running` scalar contains the sole active run ID, or `-1`.
- `POST /experiments/{name}/scorch/pipelines/{run}` atomically reserves a run
  before returning 204. Duplicate starts, resource conflicts, and stopping
  experiments return 409.
- `DELETE` on that URL acknowledges cancellation with 204; reservation remains
  until finalization. Supply `?executionID=...` to fence a stale browser request.
- `POST /experiments/{name}/scorch/pipelines/{run}/recover` accepts
  `{"executionID":"...","cleanupComplete":true}` and returns 204 or 409.
- Execution records contain identity, state, revision, timestamps, current
  stage/component/loop/count, wait kind/deadline, controller, and error.
- Terminal create/exit and pipeline websocket events include `executionID`.
  The UI periodically reconciles with persisted snapshots, including after
  reconnects and runs launched through the CLI.

Reading pipelines, output, and terminals requires experiment get permission.
Starting/recovering runs and writing/continuing breakpoint terminals requires
experiment trigger create permission. Cancellation requires trigger delete.
Terminal connection tokens are tied to the authenticated user and PTY.

## Acceptance check on a deployed experiment

Run the example above against a VM with miniccc. While setup is at `ready`,
finish two work runs, refresh/reconnect the browser, and verify setup is still
waiting. Continue setup and verify its guest cleanup ran. Repeat while canceling
one work run, and while stopping the experiment. Confirm all subprocesses exit,
each archive is separate, and no other run loses its commands or tap.

The automated suite covers store contention, controller admission/cancellation,
CC scoping, PTY exit, snapshots, and frontend state. A real minimega/VM experiment
is still required to verify deployment-specific guest behavior.
