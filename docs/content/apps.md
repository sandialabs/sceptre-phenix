# Apps

`phenix` apps provide a means of modifying an experiment topology, cluster
networking, hardware-in-the-loop devices, etc. in a layered, scripted, and
codified manner. `phenix` itself includes four (4) default apps that get applied
to every experiment by default. In addition to the default apps, it is possible
to apply _user apps_ to an experiment using a
[scenario](configuration.md#scenario) configuration. Non-default apps can also be disabled at startup,
either through the [`phenix experiment create`](cli/experiment.md#phenix-experiment-create) command or in the [scenario file](schema.md#scenario-schema). This allows apps to execute
their `running` stage only when [manually triggered](experiments.md#triggering-app-lifecycle-stages) by the user.

## Default Apps

| App                     | Description                                                                                  |
|-------------------------|----------------------------------------------------------------------------------------------|
| [ntp](#ntp-app)         | provides/configures NTP service for experiment                                               |
| [serial](#serial-app)   | configures serial interfaces in VM images                                                    |
| [startup](#startup-app) | configures guest operating systems at startup using disk injection or C2                     |
| [vrouter](#vrouter-app) | customizes Vyatta/VyOS and minirouter routers, including setting interfaces, ACL rules, etc. |

### Disable Default Apps for a VM

To keep a VM managed by `phenix` and minimega without applying the built-in
default apps, set the `phenix/default-apps` node annotation to `false` in the
[topology](configuration.md#topology):

```yaml title="disable default apps for one VM"
spec:
  nodes:
    - annotations:
        phenix/default-apps: false
      general:
        hostname: custom-vm
      # remaining node configuration omitted
```

This skips the `ntp`, `serial`, `startup`, and `vrouter` apps for that node
during every lifecycle stage. User apps configured in a scenario are
unaffected. Omit the annotation, or set it to `true`, to retain the default
behavior.

To set it on every VM of an experiment without editing the topology, add the
annotation when you create the experiment (see
[Adding Annotations](experiments.md#adding-annotations)).

### startup App

The `startup` app generates scripts that configure supported guest operating
systems when an experiment starts. These scripts configure settings such as the
hostname, time zone, and network interfaces. [Scenario](configuration.md#scenario) metadata for the app can
also add settings such as Windows domain membership.

By default, phēnix injects the generated scripts into the VM's first drive. For
images where disk injection is unavailable or intentionally disabled, phēnix
can instead send and execute the scripts through
[minimega command and control](minimega.md#minimega-command-and-control-miniccc)
(C2).

#### Scenario Configuration

The `startup` app accepts per-host metadata in a scenario for domain membership
and Windows automatic logon. The hostname, network interfaces, and
routes come from the [topology](configuration.md#topology). The time zone of
Linux, RHEL, and CentOS guests is always set to `Etc/UTC`. DNS servers also
come from the topology unless Windows `domain_controller.ip` overrides the
first network adapter's DNS server.

The supported metadata is:

| Metadata | Guest OS | Fields | Result |
|----------|----------|--------|--------|
| `domain_controller` | Linux, RHEL, or CentOS | `domain`, `username`, `password` | Joins the guest to the domain with `realm join`. |
| `domain_controller` | Windows | `domain`, `ip`, `username`, `password` | Uses `ip` as the first network adapter's DNS server, then joins the domain. |
| `auto_logon` | Windows | `username`, `password`, and optional `local` | Configures automatic logon. |

A host with no `domain_controller` metadata does not join a domain. If
`domain_controller` is set and one of its fields is missing, pre-start fails
with an error that names the host and the field.

##### Linux Domain Join

The Linux guest must provide the `realm` command and any packages required to
join its domain.

```yaml title="Linux domain join scenario"
apiVersion: phenix.sandia.gov/v2
kind: Scenario
metadata:
  name: linux-domain-join
  annotations:
    topology: example
spec:
  apps:
    - name: startup
      hosts:
        - hostname: linux-client
          metadata:
            domain_controller:
              domain: example.test
              username: administrator@example.test
              password: change-me
```

##### Windows Domain Join and Automatic Logon

When `domain_controller` and `auto_logon` are used together, omit `local` or set
it to `false` to use a domain account for automatic logon.

```yaml title="Windows domain join scenario"
apiVersion: phenix.sandia.gov/v2
kind: Scenario
metadata:
  name: windows-domain-join
  annotations:
    topology: example
spec:
  apps:
    - name: startup
      hosts:
        - hostname: windows-client
          metadata:
            domain_controller:
              domain: example.test
              ip: 192.0.2.10
              username: administrator
              password: change-me
            auto_logon:
              username: student
              password: change-me
              local: false
```

##### Windows Local Automatic Logon

`auto_logon` can also be used without `domain_controller` to configure an
existing local account for automatic logon. The app does not create the
account.

```yaml title="Windows local automatic logon scenario"
apiVersion: phenix.sandia.gov/v2
kind: Scenario
metadata:
  name: windows-local-logon
  annotations:
    topology: example
spec:
  apps:
    - name: startup
      hosts:
        - hostname: windows-client
          metadata:
            auto_logon:
              username: student
              password: change-me
```

!!! warning
    Scenario metadata stores these passwords as plain text. Protect scenario
    files and replace the example credentials before use.

#### C2 Startup Script Delivery

There are two ways to enable C2 delivery for a VM:

* Set the node annotation `phenix/startup-via-cc` to `true`. This explicitly
  uses C2 and removes only the disk injections generated by the `startup` app.
  Other injections configured on the node are unaffected.
* Set [`inject_partition: 0`](configuration.md#optional-values) on the first drive. This disables disk injection for
  the drive and automatically uses C2 for the generated startup scripts. Other
  files configured for disk injection are still skipped.

The annotation is useful when the disk supports injection but you want startup
configuration to run through C2 instead:

```yaml title="explicit C2 startup delivery"
spec:
  nodes:
    - annotations:
        phenix/startup-via-cc: true
      type: VirtualMachine
      general:
        hostname: host-00
      hardware:
        os_type: linux
        drives:
          - image: ubuntu.qc2
```

Like `phenix/default-apps`, you can also add this annotation to every VM of an
experiment when you create it (see
[Adding Annotations](experiments.md#adding-annotations)).

For a disk layout that cannot be injected into, such as an LVM-based image, set
the injection partition on the first drive to `0`:

```yaml title="automatic C2 startup delivery"
spec:
  nodes:
    - type: VirtualMachine
      general:
        hostname: host-00
      hardware:
        os_type: linux
        drives:
          - image: ubuntu-lvm.qc2
            inject_partition: 0
```

| Node configuration | Startup script delivery |
|--------------------|-------------------------|
| Annotation omitted or `false`, first drive partition omitted or nonzero | Disk injection |
| First drive `inject_partition: 0` | C2, selected automatically |
| `phenix/startup-via-cc: true` | C2, selected explicitly |
| Only `general.snapshot: false` | None; phēnix injects files only into a snapshot, and this setting alone does not enable C2 |

!!! warning
    C2 delivery requires the [`miniccc` agent](troubleshooting.md#a-vm-has-no-miniccc-client) to be installed and configured to
    start in the guest. Linux, RHEL, and CentOS guests must provide `bash`, and
    Windows guests must provide PowerShell.

The `startup` app does not generate scripts for router nodes or nodes with
[`phenix/default-apps: false`](#disable-default-apps-for-a-vm).

#### Skipping Hostname Changes

Set the node annotation `phenix/startup-set-hostname: false` on a Topology node
to keep the guest's existing hostname while still applying timezone, network
interface, and domain configuration.

#### Windows Node Annotations

The Windows startup script also reads these node annotations:

* `phenix/domain-controller`: set to `true` on a Windows domain controller.
  The script then waits until the Active Directory domain services of the VM
  are ready before it continues.
* `phenix/windows-rearm`: set to `true` to stop the Windows licensing prompt
  (`slui.exe`) each time the script runs. On the first run, the script also
  disables the `WLMS` service and rearms the Windows license with
  `slmgr.vbs /rearm`.

#### Port Forwards

Set the node annotation `phenix/startup-autotunnel` to a list of
[port forwards](tunneler.md#creating-a-port-forward-for-a-vm) for the `startup`
app to create in the `post-start` stage. Each entry is a string (put it in
quotes) in one of these forms:

* `sport`: forward local port `sport` to port `sport` of the VM.
* `sport:dport`: forward local port `sport` to port `dport` of the VM.
* `sport:dhost:dport`: forward local port `sport` to port `dport` of `dhost`,
  as the VM reaches it.

A `dport` of `VNC` forwards to the VNC console of the VM. Other forwards need a
[`miniccc` agent](troubleshooting.md#a-vm-has-no-miniccc-client) that is active
in the VM when the `post-start` stage runs. If the agent is not active, phēnix
does not create the forward. phēnix creates these forwards as the user `bot`,
so you must [activate](tunneler.md#activate-id) them in your tunneler. phēnix
reads this annotation even when `phenix/default-apps` is `false`.

```yaml title="port forwards for one VM"
spec:
  nodes:
    - annotations:
        phenix/startup-autotunnel:
          - "8080:80"
          - "5901:VNC"
      general:
        hostname: web-01
      # remaining node configuration omitted
```

### ntp App

The `ntp` app configures experiment VMs to use a NTP server in the experiment.

There are two ways to configure the NTP app:

* By specifying a VM node in the [Topology](configuration.md#topology) with the label `ntp-server`, with the value for the label being the interface name to use as a destination IP for NTP clients. This method will result in all VMs with an `os_type` of `linux`, `rhel`, `centos`, or `windows`, and all nodes of type `Router`, in the Topology being configured for NTP.
* By setting `defaultSource` in the `ntp` app metadata in [Scenario](configuration.md#scenario). This method requires all VMs to be configured as NTP clients be listed in the `hosts` attribute.

If any node has the `ntp-server` label, phēnix uses the label method and ignores
the `ntp` app metadata.

For a Vyatta or VyOS router, the [`vrouter` app](#vrouter-app) also writes the
NTP server into the router configuration. With the label method, it uses the
address of the labeled node's interface on the `MGMT` VLAN (in any letter case),
not the interface that the label names. If the labeled node has no interface on
that VLAN, the router uses the app metadata. With app metadata, a router listed in
`hosts` must set `client: ntp`; another value causes an error.

#### NTP Scenario Options

These are only needed if not using the VM label method as explained above.

* `defaultSource`: NTP server to use if no label is specified in topology
    * `hostname`: hostname of NTP server in the topology to use as the source
    * `interface`: interface name from which IP will be extracted to configure clients. Note that this name must be the name of the interface in the phenix Topology.
    * `address`: IP address to use as the NTP server. This takes precedence over other configs, and can be used to configure an external NTP server (e.g. hardware clock).
* `hosts`: list of hosts to configure NTP options for, including clients and server. Each host must set `client` or `server`. If a host sets both, phēnix uses `client`.
    * `client`: what NTP service is used by the client, and thus what configuration file will be changed. Available options are `ntp`, `chrony`, `systemd`, and `windows`.
        * `ntp`: `/etc/ntp.conf`, for the ntp daemon (`sudo apt install ntp`)
        * `chrony`: `/etc/chrony/chrony.conf`, for `chronyd` (`sudo apt install chrony`). Recommended for VMs that may not be able to reach the NTP server immediately at startup — the config is tuned to retry aggressively and step the clock quickly once the server becomes reachable.
        * `systemd`: `/etc/systemd/timesyncd.conf`, for `systemd-timesyncd`
        * `windows`: `/phenix/startup/25-ntp.ps1`, which will configure Windows NTP using `w32tm`
    * `server`: what NTP service is used by the server. Available options are `ntpd` and `chronyd`. If unset, the NTP server won't be configured.
        * `ntpd`: `/etc/ntp.conf`, for the ntp daemon
        * `chronyd`: `/etc/chrony/chrony.conf`, for `chronyd`. Default path is for Debian/Ubuntu — other distros may use `/etc/chrony.conf`.
    * `source`: Override source options for this client VM. Available options are the same as `defaultSource`. For a server, `source` sets its upstream NTP server. Without it, the server uses its own clock; a server does not use `defaultSource`.

#### NTP Label Example

Example of the `ntp` app using a VM label in a Topology.
Note that with this option, the NTP VM must be configured manually via an inject or other method.

```yaml title="ntp example scenario using VM label"
spec:
  apps:
    - name: ntp
```

```yaml title="ntp example topology using VM label"
spec:
  nodes:
    - general:
        description: Network Time server
        hostname: ntp
      labels:
        ntp-server: eth0  # this tells ntp app to use eth0 on this VM
      hardware:
        drives:
          - image: bennu.qc2
        os_type: linux
      network:
        interfaces:
          - address: 172.17.0.11
            gateway: 172.17.0.1
            mask: 24
            name: eth0
            proto: static
            type: ethernet
            vlan: example
          - address: 172.16.1.14
            mask: 16
            name: mgmt
            proto: static
            type: ethernet
            vlan: MGMT
```

#### NTP App Metadata Example

Example of the `ntp` app using app metadata to configure the server and clients.

```yaml title="ntp example scenario with app metadata"
spec:
  apps:
    - name: ntp
      metadata:
        defaultSource:
          hostname: ntp-server
          interface: eth0
      hosts:
        - hostname: ntp-server
          metadata:
            server: ntpd         # serve time using ntpd
        - hostname: chrony-host
          metadata:
            client: chrony
        - hostname: ntp-host
          metadata:
            client: ntp
        - hostname: systemd-host
          metadata:
            client: systemd
            source:
              hostname: other-ntp-server # use a different NTP server for this client
              interface: eth1            # address extracted from topology host|iface pair
        - hostname: windows-host
          metadata:
            client: windows
            source:
              address: 172.16.1.14  # use a specific IP instead of in-experiment host|iface
```

```yaml title="ntp example topology with app metadata"
spec:
  nodes:
    - general:
        description: Network Time server
        hostname: ntp-server
      hardware:
        drives:
          - image: bennu.qc2
        os_type: linux
      network:
        interfaces:
          - address: 172.17.0.11
            gateway: 172.17.0.1
            mask: 24
            name: eth0
            proto: static
            type: ethernet
            vlan: example
          - address: 172.16.1.14
            mask: 16
            name: mgmt
            proto: static
            type: ethernet
            vlan: MGMT
```

### serial App

The `serial` app configures the serial interfaces of Linux VMs. It needs no
scenario configuration: it reads the network interfaces of type `serial` from
the [topology](configuration.md#topology) (see the
[Interface Schema](schema.md#interface-schema) for their settings).

For each VM with an `os_type` of `linux` and at least one serial interface, the
app generates a script that starts a `socat` process for each serial interface.
The process creates a pseudo-terminal at the `device` path of the interface, at
its `baud_rate`, and carries the serial traffic over UDP multicast on its
`udp_port`. The script also adds a route for multicast traffic through the
address of the interface.

The app adds the script to the VM as a disk injection at
`/etc/phenix/serial-startup.bash`, along with the `serial-startup.service`
systemd unit that runs the script each time the VM boots. The injections are
added in the `configure` stage, and the files are generated in the `pre-start`
stage. The guest must provide systemd, `socat`, `ping`, and `route`.

The `serial` app does not configure RHEL or CentOS guests, external nodes, or
nodes with [`phenix/default-apps: false`](#disable-default-apps-for-a-vm).
Unlike the `startup` app, it never uses C2, so a VM whose first drive has
`inject_partition: 0` does not get these files.

```yaml title="serial interface in a topology"
spec:
  nodes:
    - type: VirtualMachine
      general:
        hostname: rtu-01
      hardware:
        os_type: linux
        drives:
          - image: ubuntu.qc2
      network:
        interfaces:
          - name: S0
            vlan: SERIAL
            address: 10.0.0.1
            mask: 24
            proto: static
            type: serial
            udp_port: 8989
            baud_rate: 9600
            device: /dev/ttyS0
```

With this interface, `/dev/ttyS0` in the VM is a 9600 baud serial port. Its
traffic reaches the other VMs that have a serial interface in the same subnet
(`10.0.0.0/24`) with UDP port 8989.

### vrouter App

The `vrouter` app supports the use of minimega's `minirouter` to include interface configuration, DHCP and DNS configuration, firewall rules, etc.

The app configures each topology node of type `Router` or `Firewall` with an
`os_type` of `vyatta`, `vyos`, or `minirouter`. For Vyatta and VyOS, it
generates a configuration in the `pre-start` stage and injects it into the
router's disk. For minirouter, it sends minimega `router` commands in the
`post-start` stage. The `os_type` `linux` is deprecated and gets the Vyatta
configuration. The app skips external nodes and nodes with
[`phenix/default-apps: false`](#disable-default-apps-for-a-vm).

#### vrouter Example

The following is an example of how the `vrouter` app can be configured via a
[`Scenario`](configuration.md#scenario) configuration, showing all the possible options.

!!! warning
    Some configuration sections only apply to Vyatta/VyOS and do nothing for
    a node marked as `minirouter`. See notes below or the
    [source for more details](https://github.com/sandialabs/sceptre-phenix/blob/main/src/go/app/vrouter.go).

``` yaml title="vrouter example"
spec:
  apps:
    - name: vrouter
      hosts:
        - hostname: rtr
          metadata:
            ipsec:
              - local: 10.0.10.2
                remote: 10.0.40.2
                peer: rtr2
                tunnels:
                  - local: 192.168.10.0/24
                    remote: 192.168.100.0/24
            acl:
              ingress:
                eth0: in-rules
              rulesets:
                - name: in-rules
                  default: drop
                  rules:
                    - id: 10
                      action: accept
                      description: Allow Incoming HTTP
                      source:
                        address: 192.168.0.0/24
                      destination:
                        address: 10.0.0.0/24
                        port: 80
                      protocol: tcp
                    - id: 20
                      action: accept
                      description: Allow Established
                      stateful: true
                      protocol: all
            dhcp:
              - listenAddress: 10.0.0.254
                ranges:
                  - lowAddress: 10.0.0.10
                    highAddress: 10.0.0.20
                defaultRoute: 10.0.0.254
                dnsServers:
                  - 10.0.0.254
                staticAssignments:
                  00:00:00:00:00:AA: 10.0.0.50
              - listenAddress: 192.168.0.254
                ranges:
                  - lowAddress: 192.168.0.10
                    highAddress: 192.168.0.20
                defaultRoute: 192.168.0.254
                dnsServers:
                  - 192.168.0.254
                staticAssignments:
                  "00:00:00:00:00:BB": 192.168.0.50
            dns:
              "1.2.3.4": foo.com
            snmp:
              listenAddress: 10.0.0.254
              systemName: edge-router-01
              location: "Lab A, Rack 2, U 24"
              contact: "network-admins@example.com"
              communities:
                - name: readonly-community
                  authorization: ro
                  trapTargets:
                    - 10.0.50.5
                - name: readwrite-community
                  authorization: rw
                  clients:
                    - 10.0.1.11
                  trapTargets:
                    - 10.0.50.5
            snat:
              - interface: eth0
                srcAddr: 192.168.0.0/24
                translation: masquerade
            dnat:
              - interface: eth1
                destAddr: 10.0.0.1
                dstPort: 80
                protocol: tcp
                translation: 192.168.0.250:8080
            emulators:
              - egress:
                  - eth0
                  - eth1
                name: comcast
                bandwidth: 400kbit
                delay: 500
                corruption: 5
                loss: 10
                reordering: 5
```

#### vrouter Scenario Options

* `ipsec`: if present, point-to-point IPSec tunnels are nailed up between the
  list of given IP addresses and traffic between the given networks is tunneled.
  Each site needs `local` and `remote` (and `peer` on a VyOS router), and each
  of its tunnels needs `local` and `remote`.

    * `local`: Local IP address for IPSec connection with this peer. It must
      be the address of one of the router's interfaces in the topology.
      It must have a route to the `remote` address.

    * `remote`: Remote IP address or hostname for IPSec connection. IPv4 or
      IPv6 address is used when a peer has a public static IP address.
      Hostname is a DNS name which could be used when a peer has a public
      IP address and DNS name, but an IP address could be changed from time
      to time. A Vyatta router also uses it as the name of the peer
      connection.

    * `peer`: Name of peer connection. The peer name must be an alphanumeric
      and can have hyphen or underscore as special characters. A VyOS router
      uses it as the name of the site-to-site peer. A Vyatta router does not
      use it.

    * `secret`: pre-shared key for the connection. If it is not set, phēnix
      generates a random key of 32 letters and digits. A site on another
      router with the reverse `local` and `remote` addresses gets the same
      generated key. Thus set the same `secret` on both ends of a connection,
      or set it on neither.

    * `tunnels`: list of local and remote networks to tunnel through this
      point-to-point connection.

        * `local`: local network to tunnel to the given remote network.

        * `remote`: remote network to tunnel to the given local network.

* `acl`: if present, access control lists (ACLs / firewall rules) are created on
  the router per the defined rulesets.

    * `ingress`: for each interface-to-ruleset mapping, apply the given ruleset
      to the given interface for inbound traffic. Note that the interface name
      used (in this example, `eth0`) refers to the name given to a network
      interface in the router's [topology](configuration.md#topology) configuration.

    * `egress`: for each interface-to-ruleset mapping, apply the given ruleset
      to the given interface for outbound traffic.

    * `rulesets`: list of rulesets to create on the router.

        * `name`: name of the ruleset; used in the interface-to-ruleset mapping
          in the `ingress/egress` sections.

        * `description`: description of the ruleset in the Vyatta/VyOS router
          configuration.

        * `default`: default action to apply to traffic that doesn't match any
          rules.

        * `rules`: list of rules to apply to traffic.
            * `id`: integer to identify the order of the rules in the set.

            * `description`: description of the rule in the Vyatta/VyOS router
              configuration.

            * `action`: action to apply to traffic matching rule.

            * `source`: map describing what source to limit matching traffic to.
              If not provided, all sources are matched.

                * `address`: source address to limit matching traffic to. If not
                  provided, all source addresses are matched.

                * `port`: source port to limit matching traffic to. If not
                  provided, all source ports are matched.

            * `destination`: map describing what destination to limit matching
              traffic to. If not provided, all destinations are matched.

                * `address`: destination address to limit matching traffic to.
                  If not provided, all destination addresses are matched.

                * `port`: destination port to limit matching traffic to. If not
                  provided, all destination ports are matched.

            * `protocol`: IP protocol to limit matching traffic to. Must be
              provided; to allow all protocols, use the `all` keyword.

            * `stateful`: if true, enable established and related traffic for
              this ruleset.

* `dhcp`: if present, DHCP is configured on the router per the provided list.

    * `listenAddress`: IP address on a local interface (e.g. this router) to
      bind this DHCP configuration to.

    * `ranges`: list of IP address low/high ranges to use for DHCP assignments.
      The IP addresses must be within the IP network of the `listenAddress`.

    * `defaultRoute`: default gateway to be included in DHCP leases.

    * `dnsServers`: list of DNS servers to be included in DHCP leases.

    * `staticAssignments`: map of MAC-to-IP assignments to use for static DHCP
      addresses.

* `dns`: if present, map of IP-to-domain DNS entries to create on the router.

* `snmp`: if present, SNMP is configured on the router.

    * `listenAddress`: IP address on a local interface to bind SNMP to. Defaults to listening on all interfaces.

    * `systemName`: a string describing the system.

    * `location`: a string for the system's physical location.

    * `contact`: a string for the system's contact information.

    * `communities`: a list of SNMP communities to configure.

        * `name`: the community string/name. This is required.

        * `authorization`: the authorization level for the community. Can be `ro` (read-only) or `rw` (read-write). Defaults to `ro` if not specified.

        * `clients`: a list of client IP addresses that are allowed to use this community.

        * `trapTargets`: a list of IP addresses to send SNMP traps to.

* `snat`: if present, a list of source NAT rules to create on the router.
  phēnix numbers the rules from 100, in the order of the list.

    * `interface`: name of the topology interface that the translated traffic
      goes out of. This is required. phēnix stops with an error if the router
      has no interface with this name.

    * `srcAddr`: source address or network to match.

    * `srcPort`: source port to match, as an integer.

    * `destAddr`: destination address or network to match.

    * `dstPort`: destination port to match, as an integer.

    * `protocol`: protocol to match, for example `tcp`.

    * `translation`: `masquerade`, an address, or an address and a port in
      the form `address:port`. If it is not set, phēnix uses `masquerade`.

* `dnat`: if present, a list of destination NAT rules to create on the router.
  phēnix numbers the rules from 100, in the order of the list. A rule takes the
  same keys as an `snat` rule, with these differences:

    * `interface`: name of the topology interface that the traffic comes in on.
      This is required.

    * `translation`: address, or address and port in the form `address:port`,
      to send the matched traffic to. This is required.

* `emulators`: if present, a list of network emulator traffic policies to apply
  to one or more interfaces on egress. For each emulator in the list, only the
  `name` key is required, though the traffic policy will not get applied if
  there's not at least one `egress` interface defined. The `delay`,
  `corruption`, `loss`, and `reordering` values must be integers. A text value
  such as `500ms` causes an error.

    * `name`: unique name of traffic policy for this router.

    * `egress`: list of interface names to apply traffic policy to on egress.
      The names listed should be the same names used for network interfaces in
      the topology. A name that is not an interface of the router is an
      error.

    * `ingress`: list of interface names to apply traffic policy to on
      ingress. Only Vyatta routers use it. VyOS routers ignore it.

    * `bandwidth`: maximum allowed bandwidth for interface and direction this
      traffic policy is applied to, for example `400kbit`.

    * `delay`: fixed amount of time to add to all packets for interface and
      direction this traffic policy is applied to. On VyOS, the value is in
      milliseconds, from 0 to 65535.

    * `corruption`: percentage of corrupted packets for interface and direction
      this traffic policy is applied to, from 0 to 100.

    * `loss`: percentage of lost packets for interface and direction this
      traffic policy is applied to, from 0 to 100.

    * `reordering`: percentage of reordered packets for interface and direction
      this traffic policy is applied to, from 0 to 100.

!!! important
    The `ingress` and `egress` setting for ACLs are from the perspective of the
    network segment the specified interface is connected to, not from the
    perspective of the interface itself. For example, if `eth0` on a router is
    connected to VLAN `EXP_01`, then specifying `eth0: in-rules` for the
    `ingress` setting means the rules specified in the `in-rules` ruleset will
    be applied to packets "ingressing into" VLAN `EXP_01`. Under the hood, the
    rules are actually applied to packets "egressing out of" interface `eth0`.

!!! note
    Currently, the `ipsec`, `emulators`, `snmp`, and `snat`/`dnat` metadata sections only
    apply to Vyatta/VyOS routers.

!!! note
    If the host metadata has no `snat` rules, phēnix uses the
    [`nat` settings](configuration.md#network-address-translation-nat) of the
    router in the topology. For each `ethernet` interface named in `in` that
    has `proto: static`, phēnix masquerades the network of that interface out
    of the `out` interface. Scenario `snat` rules replace the topology `nat` settings, and
    `dnat` rules do not change them.

!!! note
    Currently, the `stateful` setting for ACL rules only applies to Vyatta/VyOS
    routers.

!!! note
    Currently, the `dhcp` and `dns` metadata sections only apply to minirouter
    routers.

!!! note
    Currently, the only emulator policy used is
    [network-emulator](https://docs.vyos.io/en/latest/configuration/trafficpolicy/index.html#network-emulator)
    and on VyOS routers it only applies to outbound traffic (egress).

#### Router Node Annotations

Set the node annotation `vrouter/enable-ssh` on a Vyatta or VyOS router to
enable its SSH service. The value is an IP address, or the name of one of the
router's topology interfaces. SSH listens on that address, or on the address of
that interface. If the value is neither, phēnix does not enable SSH.

Set the node annotation `vrouter/vyos-password` on a VyOS router to set the
password of its `vyos` user. Without the annotation, the router keeps the
password of its image. The value must be a string with no single quote (`'`)
and no control character.

```yaml title="enable SSH on a router"
spec:
  nodes:
    - annotations:
        vrouter/enable-ssh: eth2
      type: Router
      general:
        hostname: rtr
      hardware:
        os_type: vyos
      # remaining node configuration omitted
```

## Additional Core Apps

The apps listed below are provided by the core `phenix` application, but are not
considered default apps since they do not get applied to every experiment by
default. They're more like a [user app](#user-apps), but implemented in the core
application instead of as a stand-alone executable.

|                  |                                                                                                                                   |
|------------------|-----------------------------------------------------------------------------------------------------------------------------------|
| scorch           | [Scorch](scorch.md) &mdash; **SC**enario **ORCH**estration &mdash; is an automated scenario orchestration framework within phenix |
| soh              | provide [state of health](state-of-health.md) monitoring for an experiment                                                        |
| [tap](#tap-app)  | manage host taps (typically used for external network access) for an experiment                                                   |

### tap App

The `tap` app manages the creation and removal of host taps needed by
experiments to access external network resources. This includes creating the tap
in a network namespace (`netns`) to avoid interface address collisions with
other experiments, connecting the netns with the system network to enable
external network access, and modifying iptables to allow external network access
from the tapped experiment [VLAN](experiments.md#vlan-aliases).

!!! note
    Host taps can also be used to access VM network resources directly from the
    host the VM is running on. This is an advanced topic that will be documented
    soon.

In order for a tap to have access to experiment VMs in the tapped VLAN, it must
have an IP address on the same subnet as the rest of the VMs in the VLAN.
Attempting to tap multiple experiments could fail if the VLANs being tapped are
using the same subnet, so the tap is put into a netns to provide isolation and
avoid address collisions.

With the tap in a netns, however, it no longer has a path to external networks
via the system's default netns. To remedy this, a `veth pair` is used to connect
the tap's netns with the default netns. A very small (`/30`) IP subnet is used
for the veth pair, and phenix manages the selection and tracking of the subnets
used for each pair to avoid collisions.

With the veth pair in place, packets from the experiment VLAN can now be routed
externally with the help of IP masquerading in both the tap's netns and the
default netns.

!!! warning
    This has not been fully tested against all the possible iptables firewall
    configurations. If you experience problems with external access, it may be
    due to a more restrictive iptables configuration than we've tested with.

By default, the host taps are created during the experiment's **post-start**
stage. The optional `stage` metadata option can be used to instead create the
taps during the **pre-start** stage, which is useful when other apps or VMs need
the taps to already exist when the experiment starts. Valid values are
`pre-start` and `post-start`; when unset, it defaults to `post-start` to
preserve backwards compatibility.

The taps are created on the experiment host running the fewest VMs, the first
by name among hosts with equally few, so every run places them the same way.
Each tap must name a different VLAN. When `externalAccess.enabled` is `true`,
`ip` must be an address in CIDR form, such as `172.20.5.254/24`.

The following is an example of how the `tap` app can be configured via a
[`Scenario`](configuration.md#scenario) configuration, showing all the possible options.

!!! note
    The `externalAccess.firewall` portion of the tap configuration has not been
    implemented yet.

```yaml title="tap app example"
spec:
  apps:
    - name: tap
      metadata:
        # the experiment lifecycle stage to create the taps in -- valid values
        # are 'pre-start' and 'post-start' (will default to 'post-start' if not
        # provided)
        stage: post-start
        taps:
            # the bridge to add the tap to (will default to the experiment's default
            # bridge, 'phenix' unless changed, if not provided)
          - bridge: phenix
            # the experiment VLAN to tap
            vlan: MGMT
            # IP address to use for host tap -- VMs on the tapped VLAN would use this
            # address as their gateway if they need external access (and it's enabled
            # below)
            ip: 172.20.5.254/24
            # IP subnet to use for veth pair between host and tap's network
            # namespace when external access is enabled (will default to a /30
            # subnet that is not already in use by any other running experiment if
            # not provided; if this subnet is in use, the next unused subnet of
            # the same size is used)
            subnet: 10.213.47.8/30
            externalAccess:
              # defaults to false
              enabled: true
              # this section is planned, but not implemented yet
              firewall:
                # default action to take if none of the rules below match a packet
                default: drop
                rules:
                    # action to take if a packet matches this rule
                  - action: accept
                    description: Only allow web access
                    source:
                      # can also use `addresses` to specify a list of addresses
                      address: 172.20.5.0/29
                    destination:
                      address: 10.0.0.0/24
                      # can also use `port` to specify a single port
                      ports: [80, 443]
                    # can also use `protocols` to specify a list of protocols
                    protocol: tcp
```

## User Apps

`phenix` _user apps_ are stand-alone executables that `phenix` shells out to at
different stages of the experiment lifecycle (
`configure`, `pre-start`, `post-start`,
`running`, and `cleanup`). When `phenix` encounters an app in an experiment
scenario that isn't a default app, it
[checks to see if an executable exists](troubleshooting.md#an-app-or-scorch-component-is-not-found) in
its current `PATH` in the form of `phenix-app-<app-name>`. If the executable
exists, `phenix` shells out to it, providing the current lifecycle stage as an
argument and providing the experiment `metadata`, `spec`, and `status` as a JSON
string over `STDIN`.

!!! note
    There will always be three (3) top-level keys available in the JSON passed to a
    user app over `STDIN`: `metadata`, `spec`, and `status`. For the `configure`
    and
    `pre-start` stages, the `status` value will be null or otherwise ignored.
    The
    `spec` value will be [experiment schema](schema.md#experiment-schema).
    phēnix also adds `hosts` (the schedulable cluster hosts) and `vms` (the
    experiment VMs that minimega reports), and leaves each out when it is
    empty.

!!! tip
    You can run [`phenix util app-json <exp-name>`](cli/util.md#phenix-util-app-json) to see an example of what the
    JSON that's passed to a user app looks like.

!!! note
    The runtime `hosts` that a user app gets have no `diskusage` field.
    phēnix does not measure the disk usage of the hosts for user apps. A
    [user scheduler](experiments.md#schedulers) gets the disk usage of the
    schedulable hosts.

!!! tip
    You can also get this JSON from the [REST API](api.md) with
    `GET /api/v1/experiments/{name}/apps/input`.

The user app can modify the experiment at will, then return the updated
JSON over `STDOUT` and exit with a `0` status. If the user app encounters an
error, it should print error messages to `STDERR` and exit with a non-zero
status. Logs written to `STDERR` must be single-line JSON objects
containing `level` and `msg` fields to be fully integrated into the
phēnix [centralized logging system](logging.md#the-app-contract). A `type` field with a phēnix log type, such
as `SCORCH`, sets the log type of the record. phēnix does not accept the
`AUDIT` or `SECURITY` type from an app. A field with the same name as a phēnix
field, such as `time` or `app`, gets the `proc_` prefix.

!!! note
    `phenix` will only process updates to the `spec` value for the `configure`,
    `pre-start`, and `cleanup` stages, and will only process updates to the `status` value
    for the `post-start`, `running`, and `cleanup` stages. More specifically, it
    will only process updates to `status.apps.<app-name>`, which can be anything
    the app wants it to be (e.g. a simple string, an array, or a
    map/dictionary).

!!! note
    It is possible for the execution of app stages to be
    [canceled](cli/experiment.md#phenix-experiment-trigger) by the caller.
    In the case of user apps, `phenix` will send a `SIGTERM` to the user app
    process and wait a maximum of 10 seconds for the process to exit gracefully
    before killing the process with a `SIGKILL`.

!!! note
    phēnix won't start an app's `running` stage (a [Scorch](scorch.md) run, for example)
    while that app is still running a stage. If phēnix
    [stops partway through](troubleshooting.md#apps-or-a-scorch-run-stay-marked-as-running),
    [`phenix ui`](cli/ui.md#phenix-ui) clears this when it starts again, so you don't need to restart
    the experiment to run the app.

### Available User Apps

The
[sandialabs/sceptre-phenix-apps](https://github.com/sandialabs/sceptre-phenix-apps)
repo is home to some user apps that have already been created by the community,
including the following:

| App          | Link                                                                                                         | Description                                                                                                                                                             |
| ------------ | ------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| caldera      | [caldera source](https://github.com/sandialabs/sceptre-phenix-apps/tree/main/src/python/phenix_apps/apps/caldera)      | Deployment and orchestration of [MITRE Caldera](https://caldera.mitre.org/) in an experiment                                                                            |
| helics       | [helics source](https://github.com/sandialabs/sceptre-phenix-apps/tree/main/src/python/phenix_apps/apps/helics)       | Aids in the generation of [HELICS](https://helics.org/) broker start scripts and injects them into the appropriate nodes in an experiment                               |
| ignition     | [ignition source](https://github.com/sandialabs/sceptre-phenix-apps/tree/main/src/python/phenix_apps/apps/ignition)     | Configures an Inductive Automation Ignition Gateway 8.3 SCADA master at experiment start                                                                                |
| [mgmt_tap](#mgmt_tap-app) | [mgmt_tap source](https://github.com/sandialabs/sceptre-phenix-apps/tree/main/src/python/phenix_apps/apps/mgmt_tap) | Creates a tap on the management network of an experiment, allowing a user to copy files to and from VMs connected to that network                                       |
| mirror       | [mirror source](https://github.com/sandialabs/sceptre-phenix-apps/tree/main/src/go/cmd/phenix-app-mirror)                | The mirror app configures cluster-wide packet mirroring for specific VLANs to a specific interface on a predefined node using GRE tunnels. It is written in Golang.     |
| ot-sim       | [otsim source](https://github.com/sandialabs/sceptre-phenix-apps/tree/main/src/python/phenix_apps/apps/otsim)        | Aids in the generation of config files for [OT-sim](https://ot-sim.patsec.dev/)                                                                                         |
| protonuke    | [protonuke source](https://github.com/sandialabs/sceptre-phenix-apps/tree/main/src/python/phenix_apps/apps/protonuke)             | Configures the protonuke traffic generator                                                                                                                              |
| scale        | [scale source](https://github.com/sandialabs/sceptre-phenix-apps/tree/main/src/python/phenix_apps/apps/scale)        | Specialized phenix application designed for high-volume simulations                                                                                                     |
| sceptre      | [sceptre source](https://github.com/sandialabs/sceptre-phenix-apps/tree/main/src/python/phenix_apps/apps/sceptre)      | Core functionality for [SCEPTRE](https://sandialabs.github.io/sceptre-docs/), including generating configs for field devices (bennu), SCADA systems, and other systems. |
| wind-turbine | [wind_turbine source](https://github.com/sandialabs/sceptre-phenix-apps/tree/main/src/python/phenix_apps/apps/wind_turbine) | Deprecated in favor of the `wind_turbine` plugin of the `scale` app. Aids in the generation of wind-turbine-specific [OT-sim](https://ot-sim.patsec.dev/) config files  |
| wireguard    | [wireguard source](https://github.com/sandialabs/sceptre-phenix-apps/tree/main/src/python/phenix_apps/apps/wireguard)    | Configures Wireguard on VMs                                                                                                                                             |

In addition, this repo also contains some generic library/utility code for
making custom user app development easier. See the
[README](https://github.com/sandialabs/sceptre-phenix-apps/blob/main/README.md)
for additional details.

#### mgmt_tap App

The `mgmt_tap` application creates tap interfaces on the management network of an experiment, enabling file transfer and remote access to virtual machines within the experiment. Unlike the [`tap` app](#tap-app), it is a user app, so its `phenix-app-mgmt_tap` executable must be installed. The `mgmt_tap`:

* Creates taps on the configured VLAN (defaults to `MGMT`) on all compute hosts in the experiment
* Automatically assigns sequential IPs from a configurable subnet
* Removes taps during cleanup phase

The following is an example of how the default `mgmt_tap` app can be configured via a `Scenario` configuration.

```yaml title="mgmt_tap app default example"
spec:
  apps:
    - name: mgmt_tap
```

This creates tap interfaces on all hosts with IPs `172.16.0.1/16`, `172.16.0.2/16`, etc. on the `MGMT` VLAN.

The following is an example of how the `mgmt_tap` app can be configured via a `Scenario` configuration with optional settings `subnet` and `vlan`.

```yaml title="mgmt_tap app optional example"
spec:
  apps:
    - name: mgmt_tap
      metadata:
        subnet: 10.0.0.0/24
        vlan: INTERNAL
```

This creates tap interfaces with IPs `10.0.0.1/24`, `10.0.0.2/24`, etc. on the `INTERNAL` VLAN.

The optional `bridge` setting names the bridge to create the taps on. It defaults to the default bridge of the experiment.

### Example

A very contrived example of a simple _user app_, which changes the disk image
used for every node in the experiment topology, is in
[A Minimal User App](examples.md#a-minimal-user-app).

!!! tip
    Additional information on runnable reference user apps can be found in the [Examples section](examples.md).

## See Also

* [Example Applications](examples.md): user apps in Go and Python to start from.
* [phenix experiment apps](cli/experiment.md#phenix-experiment-apps): list the apps that a scenario can use.
* [phenix experiment trigger](cli/experiment.md#phenix-experiment-trigger): run a stage of an app by hand.
* [Scenario](configuration.md#scenario): how a scenario names and configures apps.
* [phenix util app-json](cli/util.md#phenix-util-app-json): print the JSON that a user app reads.
* [Troubleshooting](troubleshooting.md#an-app-or-scorch-component-is-not-found): when phēnix cannot find an app.
