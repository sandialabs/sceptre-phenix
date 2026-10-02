# Node Annotations

`annotations` is a free-form `map[string]any` on a Topology node
(`spec.nodes[].annotations`), used to pass out-of-band hints to apps without
adding first-class schema fields. Apps read them via `node.GetAnnotation("key")`
and interpret the value however they define it. The [skill body](../SKILL.md)
covers the Topology and Scenario shapes these annotations attach to.

An experiment can also be given node annotations when it is created, with
`phenix experiment create --node-annotation key=value`, the `node_annotations`
field of `POST /experiments`, or the Advanced Options of the web UI's create
card. They are added to every VM (not external nodes) in the experiment's own
copy of the topology; the Topology config is unchanged, and a VM that already
has the key keeps its value. Creation rejects a value of the wrong type for the
default apps' annotations below (a non-boolean `phenix/default-apps`, a
`phenix/startup-autotunnel` that is not a list of strings, a non-string
`vrouter/*` value), which the apps would otherwise ignore silently.

One VM's annotations in an existing experiment can be read and replaced, even
while it runs, with `GET` and `PATCH /experiments/{exp}/vms/{name}` (see
[`web-api.md`](web-api.md#vm-annotations)) or the Annotations box of the web UI's
VM details card. The same type checks apply. Apps read most annotations when the
experiment starts, so a change to a running experiment mostly takes effect at
its next start.

Annotations read by phenix's own default apps and web server:

## `phenix/default-apps` (all default apps)

`phenix/default-apps: false` skips the `ntp`, `serial`, `startup`, and
`vrouter` default apps for that specific node on every lifecycle stage, while
still letting phenix/minimega manage the VM normally. User apps from the
scenario are unaffected. Omit the annotation (or set it `true`) to keep the
default behavior.

The value must be a real YAML boolean: anything that does not decode to a bool
(a quoted `"false"`, `"no"`, `0`) is silently ignored and default apps stay
enabled. Unlike `phenix/startup-via-cc` below, this annotation does not coerce
strings or numbers.

## `phenix/startup-autotunnel` (`startup` app, `post-start` stage)

A list of strings, each describing a port forward to auto-create for the node
once it boots, e.g. `["8080", "8080:9090", "8080:10.0.0.5:9090"]`. Each entry
is `sport[:dhost]:dport`:

| Form | Forwards to |
|---|---|
| `sport` | `127.0.0.1:sport` |
| `sport:dport` | `127.0.0.1:dport` |
| `sport:dhost:dport` | an arbitrary destination host/port |

Malformed entries are logged and skipped.

## `phenix/startup-via-cc` (`startup` app, `pre-start` stage)

When truthy, the startup app drops its disk injections for that node and
instead delivers and runs the generated startup scripts over minimega's C2
(miniccc) channel. The value may be a bool, a number (`0` is false), or a
string (`false`/`0`, case-insensitively, are false; any other string is true);
an absent annotation means false.

C2 delivery is also used automatically when the node's first drive sets
`inject_partition: 0`, and is skipped entirely for `do_not_boot` nodes.

## `vrouter/vyos-password` (`vrouter` app)

Overrides the default `vyos` login password used when templating the boot
config for a VyOS router node.

## `vrouter/enable-ssh` (`vrouter` app)

An interface name or IP address. When set, SSH access is enabled on the router,
templated to listen on that interface's address (or the literal IP given).

## `vncBanner` (web server, VNC page)

Sets the banners above and below the VM's screen on the web UI's VNC page,
which otherwise read `EXP: <exp> - VM: <vm>`. A string is the text of both
banners, split into lines at its newlines. A map takes `topBanner` and
`bottomBanner`, each with `banner` (a list of lines), `backgroundColor` and
`textColor`, or `disabled: true` for no banners. A banner the map leaves out is
not shown. Colors must be CSS color names or hex colors (`#a91f3d`); others
fall back to white and black, and unknown keys are ignored, each with a logged
warning. The page shows banner text as text, never as HTML.

## Example

```yaml
spec:
  nodes:
    - type: VirtualMachine
      general:
        hostname: web-01
      annotations:
        phenix/startup-via-cc: true
        phenix/startup-autotunnel: ["8080:80"]
      # ...
    - type: Router
      general:
        hostname: rtr-01
      annotations:
        vrouter/enable-ssh: IF0
      # ...
```
