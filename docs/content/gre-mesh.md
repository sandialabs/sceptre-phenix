# GRE Mesh

Generic Routing Encapsulation (GRE) is a tunneling protocol that wraps one
packet inside another, so the inner packet can cross an IP network between two
tunnel endpoints. Layer 2 GRE carries whole Ethernet frames, which makes two
switches joined by a GRE tunnel behave as if a cable connected them. For more
background, see [Generic Routing Encapsulation](https://en.wikipedia.org/wiki/Generic_Routing_Encapsulation)
and [RFC 2784](https://www.rfc-editor.org/rfc/rfc2784.html).

Use `--use-gre-mesh` when an experiment spans multiple minimega nodes and its
virtual networks need to connect VMs on different hosts. phēnix uses minimega
to connect the hosts' Open vSwitch (OVS) bridges with Layer 2 GRE tunnels.
These tunnels trunk experiment VLANs across the host IP network, so VMs on
the same experiment network communicate transparently across hosts, including
Ethernet broadcast traffic such as ARP. Guest addresses and network membership
do not need to change when VMs run on different hosts.

For example, two VMs on the same experiment VLAN can communicate even when
one runs on minimega node A and the other on node B. Their Ethernet frames
travel from A's OVS bridge through GRE to B's OVS bridge. The physical network
carries the encapsulated traffic without needing to trunk each experiment
VLAN itself. See minimega's [namespace bridge documentation](https://sandia-minimega.github.io/minimega/articles/namespaces/#private-bridge).

## Prerequisites

The minimega nodes must already belong to a working mesh, with IP connectivity
between tunnel endpoints and GRE traffic (IP protocol 47) permitted along the
path. The flag configures experiment network connectivity; VM placement still
depends on the experiment's schedule and available hosts. A single-host
experiment does not need GRE for communication between its local VMs.

Prepare the physical network MTU before starting experiments; see
[MTU and fragmentation](#mtu-and-fragmentation) below.

## Enable GRE

Enable GRE when creating an experiment (the default is disabled):

```bash
phenix experiment create my-exp -t my-topology --use-gre-mesh
phenix experiment start my-exp
```

For an existing experiment, stop it, run `phenix experiment edit my-exp`,
and set `spec.useGREMesh` to `true` before starting it again. Passing the flag
only to `experiment start` does not change this stored setting.

The Web-UI has no GRE option. REST API clients can enable GRE for a single
experiment by setting `use_gre_mesh` to `true` in the `POST /api/v1/experiments`
request body. To enable GRE for every experiment created or updated through
the Web-UI or REST API, start the server with `phenix ui --use-gre-mesh`. CLI
commands inherit an enabled server setting; `--use-gre-mesh=false` cannot
override it. See [Settings & Configuration](settings.md) for configuration file
and environment variable alternatives.

## Bridge Scope

GRE mesh setup applies to the experiment's **default bridge**. Interfaces
assigned to other bridges explicitly in the topology need their own
connectivity arrangements. [Bridge Mode](bridge-mode.md) explains how phēnix
selects the default bridge; `--bridge-mode auto` gives each experiment its own
bridge and can be combined with `--use-gre-mesh` at creation.

## MTU and Fragmentation

The physical network needs a larger MTU than the guests to accommodate the
encapsulated Ethernet frame and IP/GRE headers. For 1500-byte guest MTUs,
a host IP MTU of 1600 provides headroom for ordinary GRE over IPv4; additional
encapsulation may require more. Configure sufficient MTU/frame size across
**all host interfaces, routed hops, and physical switch ports** carrying the
tunnels. phēnix does not configure these for you.

An undersized path can cause fragmentation, reduced performance, or dropped
packets even when small pings succeed. If increasing the physical network MTU
is not possible, reduce guest MTUs to leave room for encapsulation. See the
[OVS tunneling guide](https://docs.openvswitch.org/en/latest/howto/tunneling/)
and [RFC 7588](https://www.rfc-editor.org/rfc/rfc7588.html) for details.

## Verify Connectivity

Confirm that the communicating VMs actually run on different minimega hosts
and share an experiment VLAN. On each host, `ovs-vsctl show` should show GRE
interfaces attached to the experiment's default bridge. Check the configured
remote endpoints and verify that the host network can reach them.

Test both small and full-size packets between guests. For a Linux guest with
a 1500-byte IPv4 MTU, for example:

```bash
ping -M do -s 1472 <peer-guest-ip>
```

The 1472-byte payload plus IPv4 and ICMP headers makes a 1500-byte guest IP
packet. This checks guest connectivity at that size, but does not prove the
outer GRE packets avoided fragmentation. Inspect traffic on the physical
interfaces or fragmentation counters on the hosts to verify that separately.
