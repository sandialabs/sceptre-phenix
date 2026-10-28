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

You can execute non-interactive minimega commands directly through `phenix mm`.

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

If minimega errors or the minimega daemon is unreachable, `phenix mm` will exit with a non-zero status and record the error in the phēnix log.

### Namespace Scoping

When working with experiment-specific VMs or VLANs, use the `--namespace` (`-n`) flag to target a specific minimega namespace. Each running experiment has a minimega namespace with the same name as the experiment.

```bash
# Execute minimega commands within the 'my-experiment' namespace
phenix mm --namespace my-experiment vm info

# Check the network connections of VMs in the 'my-experiment' namespace
phenix mm -n my-experiment .columns name,vlan,bridge,tap,ip vm info
```

With [shell completion](index.md#accessing-the-cli-shell-completion) enabled, `--namespace` completes the names of running experiments, and `phenix mm` completes minimega commands using suggestions from the running minimega.

!!! warning
    Specifying a namespace that does not exist will cause minimega to create it.

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

### From the Web-UI

When the phēnix server is started with `phenix ui --minimega-console` (or
`PHENIX_UI_MINIMEGA_CONSOLE=true`), the `Console` tab in the banner near the top
of the UI opens a minimega console, running `phenix mm --attach`, in the
browser. The tab is shown to roles with `post` on `miniconsole`; without
`--minimega-console`, it says that console access is not configured.

The console belongs to the user who started it; no other user can see, use, or
end it. It keeps running while you use other pages, reload the page, or open
phēnix in another tab of the same browser: coming back to the `Console` tab
shows the same console and its earlier output instead of starting a new one.
The console ends when:

* you press `Ctrl+D` or type `disconnect` in it
* you log out
* 15 seconds pass after the last tab that has it open closes

Once it has ended, `Start a new console` starts another one. If the connection
to it is lost, including when the server drops a tab that falls too far behind
the console's output, `Reconnect` attaches to it again, or starts a new one if
it has ended in the meantime.

![minimega console in the bennu namespace showing the output of vm info with each VM's host, name, state and IP addresses.](images/console_tab.png){: width=800 .center}

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
phenix mm -n my-experiment vm info

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
