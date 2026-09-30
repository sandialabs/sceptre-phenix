# minimega Integration

phēnix uses [minimega](https://sandia-minimega.github.io/minimega/) as its underlying network and virtual machine emulation backend. The `phenix mm` command provides direct access to send commands to minimega or attach to minimega's interactive console from the phēnix host. For full details on minimega's command set and capabilities, consult the [minimega Documentation](https://sandia-minimega.github.io/minimega/) and the [minimega API Reference](https://sandia-minimega.github.io/minimega/reference/minimega/).

## Command Line Usage

```text
Send commands, or attach, to minimega

  Sends a command to minimega and prints the response, or attaches to the
  interactive minimega console. Exits non-zero if minimega reports an error.

  Flags must come before the minimega command. Everything after the first
  minimega argument, including arguments that start with "-", is passed to
  minimega unchanged, and arguments are quoted for minimega as needed. A
  single argument is sent as-is, so a whole command written in minimega's own
  syntax can be passed as one quoted string.

Usage:
  phenix mm <minimega args>... [flags]

Aliases:
  mm, minimega

Examples:

  phenix mm vm info
  phenix mm -n <experiment name> vm info
  phenix mm -n <experiment name> cc exec ls -a
  phenix mm -n <experiment name> "vm info"
  phenix minimega -a -n <experiment name>

Flags:
  -a, --attach             Attach to minimega console instead of sending commands
  -h, --help               help for mm
  -n, --namespace string   Default minimega namespace to use
```

!!! note
    For a list of global flags supported across all `phenix` subcommands, see [Global Flags](settings.md#global-flags).

## Sending Commands to minimega

You can execute non-interactive minimega commands directly through `phenix mm`, or its alias `phenix minimega`.

```bash
# View minimega cluster host status
phenix mm host

# Display minimega VM information across all namespaces
phenix mm vm info

# List active VLANs in minimega
phenix mm vlans
```

`phenix mm` flags, and any [global flags](settings.md#global-flags), must come before the minimega command. Everything after the first minimega argument is passed to minimega unchanged, including arguments that start with `-`, so `phenix mm cc exec ls -a` runs `ls -a` rather than treating `-a` as the `--attach` flag.

phēnix quotes each argument for minimega as needed, so empty arguments and arguments containing spaces, quotes, or `#` arrive intact. A single argument is sent as-is instead, which lets you pass a whole command written in minimega's own syntax as one quoted string, such as `phenix mm "vm info"`.

`phenix mm` exits with a non-zero status, and records the error in the phēnix log, when minimega reports an error or the connection to minimega is lost, so scripts can check whether a command succeeded.

### Namespace Scoping

When working with experiment-specific VMs or VLANs, use the `--namespace` (`-n`) flag to target a specific minimega namespace. Each running experiment has a minimega namespace with the same name as the experiment.

```bash
# Execute minimega commands within the 'my-experiment' namespace
phenix mm --namespace my-experiment vm info

# Check the network connections of VMs in the 'my-experiment' namespace
phenix mm -n my-experiment .columns name,vlan,bridge,tap,ip vm info
```

With [shell completion](index.md#accessing-the-cli-shell-completion) enabled, `--namespace` completes the names of running experiments, and `phenix mm` completes minimega commands using suggestions from the running minimega. Naming a namespace that does not exist makes minimega create it.

## Attaching to the minimega Console

The `--attach` (`-a`) flag opens an interactive minimega console session, allowing real-time interaction with the minimega shell.

```bash
# Attach to the global minimega console
phenix mm --attach

# Attach to the minimega console scoped to a specific experiment namespace
phenix mm -a -n my-experiment
```

To exit the attached console session, type `disconnect` or press `Ctrl+D`.

!!! warning
    Typing `quit` in the console stops the minimega daemon itself, not just the console session. minimega asks you to enter `quit` a second time to confirm.

## minimega Command and Control (`miniccc`)

phēnix and its applications rely on minimega's command and control infrastructure ([miniccc](https://sandia-minimega.github.io/minimega/reference/minimega/)) running inside experiment VMs to execute commands, transfer files, and monitor VM health.

* **VM Execution**: phēnix apps run scripts and commands on VMs via `cc exec`.
* **File Operations**: Files are sent to VMs using `cc send`.
* **State of Health**: Health metrics are collected via C2 probes (e.g., `ip addr`, `systemctl`, `pgrep`).

## Daemon Configuration and Socket Access

By default, phēnix connects to minimega via a UNIX domain socket.

| Setting | Default | Description |
| :--- | :--- | :--- |
| `--base-dir.minimega` | `/tmp/minimega` | Base directory for minimega runtime state. |
| `MM_SOCKET_PATH` | `/tmp/minimega/minimega` | Path to the minimega command socket. |
| `MM_FILEPATH` | `/phenix/images` | Base directory for minimega disk images. |
| `--deploy-mode` | — | Deploy mode for minimega VMs (`all`, `no-headnode`, `only-headnode`). |

## Common Workflows

### 1. Debugging Experiment VMs

When troubleshooting virtual machine or network deployment issues, use `phenix mm` to inspect minimega's internal state:

```bash
# Check detailed VM status and tap interface mappings
phenix mm --namespace my-experiment vm info

# Inspect recent minimega log entries (the log ring must first be enabled,
# for example with `phenix mm log ring 1000`)
phenix mm log ring
```

### 2. Monitoring Cluster Hosts

In multi-node minimega deployments, use `phenix mm` to verify cluster connectivity and host status:

```bash
# Verify health of all minimega headnodes and compute nodes
phenix mm host
```
