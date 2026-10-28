// IPv4 addressing of the Builder's networks (Network.Subnet and Gateway in
// types/builder/document.go, which a publish never writes), and random MACs.
// This module imports nothing from model.js or validate.js, which use it.

import {
  goEqualFold,
  goToLower,
  goTrimSpace,
  trimASCIISpace,
} from './goCase.js';
import { deepCopy } from './values.js';

// --- addresses -------------------------------------------------------------

// The mask a new static interface starts with: the phenix schema's default.
// An address assigned from a subnet replaces it with the subnet's.
export const DEFAULT_INTERFACE_MASK = 24;

const IPV4_BITS = 32;
// A number of an IPv4 address: 0 to 255, without leading zeros, as Go's
// netip.ParseAddr reads it (ipv4Octets in validate.js).
const OCTET = /^(0|[1-9]\d{0,2})$/;
// A prefix length: 0 to 32, without a sign or leading zeros.
const PREFIX_LENGTH = /^(3[0-2]|[12]?\d)$/;

/** An IPv4 address as a number, or null when text is not one. */
export function parseIPv4(text) {
  if (typeof text !== 'string') {
    return null;
  }

  const parts = text.split('.');

  if (
    parts.length !== 4 ||
    !parts.every((part) => OCTET.test(part) && Number(part) <= 255)
  ) {
    return null;
  }

  return parts.reduce((value, part) => value * 256 + Number(part), 0);
}

/**
 * @param {number} value an IPv4 address as a number
 * @returns {string} the address, such as 10.1.0.2
 */
export function formatIPv4(value) {
  return [24, 16, 8, 0]
    .map((shift) => Math.floor(value / 2 ** shift) % 256)
    .join('.');
}

/**
 * A subnet as the server reads it (ParseSubnet in types/builder/validate.go),
 * with no host bits, or null.
 */
export function parseSubnet(text) {
  if (typeof text !== 'string') {
    return null;
  }

  const slash = text.indexOf('/');
  const base = slash < 0 ? null : parseIPv4(text.slice(0, slash));
  const length = slash < 0 ? '' : text.slice(slash + 1);

  if (base === null || !PREFIX_LENGTH.test(length)) {
    return null;
  }

  const bits = Number(length);
  const size = 2 ** (IPV4_BITS - bits);

  return base % size === 0 ? { base, bits, size } : null;
}

/**
 * @param {{base: number, bits: number}} subnet
 * @returns {string} such as 10.1.0.0/24
 */
export function formatSubnet(subnet) {
  return `${formatIPv4(subnet.base)}/${subnet.bits}`;
}

/**
 * The host addresses of a subnet. A /31 or /32 leaves out no network or
 * broadcast address (IsSubnetHost in types/builder/validate.go).
 */
export function hostRange(subnet) {
  const last = subnet.base + subnet.size - 1;

  return subnet.size <= 2
    ? { first: subnet.base, last }
    : { first: subnet.base + 1, last: last - 1 };
}

/**
 * @param {{base: number, size: number}} subnet
 * @param {number} value an IPv4 address as a number
 * @returns {boolean} whether the address is a host address of the subnet
 */
export function isSubnetHost(subnet, value) {
  const { first, last } = hostRange(subnet);

  return value >= first && value <= last;
}

/**
 * @param {{base: number, size: number}} a
 * @param {{base: number, size: number}} b
 * @returns {boolean} whether the two subnets share an address
 */
export function subnetsOverlap(a, b) {
  return a.base < b.base + b.size && b.base < a.base + a.size;
}

/** The first free host address of a subnet, or null when all are taken. */
export function nextFreeHost(subnet, taken) {
  const { first, last } = hostRange(subnet);

  for (let value = first; value <= last; value += 1) {
    if (!taken.has(value)) {
      return value;
    }
  }

  return null;
}

// The subnets suggestSubnet offers, in order: 10.1.0.0/24 to
// 10.255.0.0/24, then the same with a third number of 1, and so on.
const SUGGESTED_BITS = 24;

/** The first 10.<a>.<b>.0/24 that overlaps no subnet in taken and holds no address in used, or ''. */
export function suggestSubnet(taken, used = []) {
  const size = 2 ** (IPV4_BITS - SUGGESTED_BITS);

  for (let third = 0; third < 256; third += 1) {
    for (let second = 1; second < 256; second += 1) {
      const candidate = {
        base: (10 * 256 + second) * 65536 + third * 256,
        bits: SUGGESTED_BITS,
        size,
      };

      if (
        !taken.some((subnet) => subnetsOverlap(subnet, candidate)) &&
        !used.some(
          (value) =>
            value >= candidate.base && value < candidate.base + candidate.size,
        )
      ) {
        return formatSubnet(candidate);
      }
    }
  }

  return '';
}

// --- MAC addresses -----------------------------------------------------------

/** A MAC address as twelve lowercase hex digits, or '' (interfaceMAC in validate.js). */
export function macKey(text) {
  const key =
    typeof text === 'string'
      ? text.trim().toLowerCase().replace(/[:.-]/g, '')
      : '';

  return /^[0-9a-f]{12}$/.test(key) ? key : '';
}

// How many random addresses randomMac tries before it gives up. The chance
// that a diagram's addresses take all of them is nil.
const MAC_TRIES = 64;

function cryptoBytes(bytes) {
  return globalThis.crypto.getRandomValues(bytes);
}

/**
 * A random locally administered unicast MAC that is not in taken, so never a
 * vendor's or a multicast address. '' when every try was taken.
 */
export function randomMac(taken = new Set(), fill = cryptoBytes) {
  for (let attempt = 0; attempt < MAC_TRIES; attempt += 1) {
    const bytes = fill(new Uint8Array(6));

    bytes[0] = (bytes[0] & 0xfc) | 0x02;

    const text = [...bytes]
      .map((byte) => byte.toString(16).padStart(2, '0'))
      .join(':');

    if (!taken.has(macKey(text))) {
      return text;
    }
  }

  return '';
}

// --- the document ------------------------------------------------------------

function deviceNodes(doc) {
  return (doc?.nodes || []).filter(
    (node) => node?.kind === 'device' && node.device,
  );
}

function specInterfacesOf(node) {
  const interfaces = node.device?.spec?.network?.interfaces;

  return Array.isArray(interfaces) ? interfaces : [];
}

function trimmed(value) {
  return typeof value === 'string' ? value.trim() : '';
}

// The address an interface has, without a prefix length, as a number.
function interfaceAddress(iface) {
  return parseIPv4(trimmed(iface?.address).split('/')[0]);
}

/** The MAC addresses of the document's device interfaces, as macKey keys. */
export function documentMacs(doc) {
  return new Set(
    deviceNodes(doc)
      .flatMap((node) =>
        specInterfacesOf(node).map((iface) => macKey(iface?.mac)),
      )
      .filter(Boolean),
  );
}

/** Whether an interface is static or OSPF with no address: one that assignment fills. */
export function needsAddress(iface) {
  const proto = trimmed(iface?.proto).toLowerCase();

  return (
    Boolean(iface) &&
    typeof iface === 'object' &&
    ['static', 'ospf'].includes(proto) &&
    iface.type !== 'serial' &&
    trimmed(iface.address) === ''
  );
}

// The networks by name, to find the network a VLAN names: the one of
// exactly that name, else one of that name in another case (networkByName in
// model.js). The first of each name.
function networkNames(doc) {
  const exact = new Map();
  const folded = new Map();

  for (const network of doc?.networks || []) {
    if (typeof network?.name !== 'string') {
      continue;
    }

    if (!exact.has(network.name)) {
      exact.set(network.name, network);
    }

    if (!folded.has(goToLower(network.name))) {
      folded.set(goToLower(network.name), network);
    }
  }

  return { exact, folded };
}

// The network a VLAN names (see networkNames).
function networkOfVLAN(names, vlan) {
  const name = typeof vlan === 'string' ? goTrimSpace(vlan) : '';

  return name === ''
    ? undefined
    : names.exact.get(name) || names.folded.get(goToLower(name));
}

/**
 * The spec interfaces on each network, by network id, found as the server
 * finds them (networkMembers in types/builder/warnings.go).
 */
export function interfacesByNetwork(doc) {
  const connected = connectedHandles(doc);
  const names = networkNames(doc);
  const members = new Map();

  for (const node of deviceNodes(doc)) {
    specInterfacesOf(node).forEach((iface, index) => {
      // The schema checks an entry of the wrong shape.
      if (!iface || typeof iface !== 'object') {
        return;
      }

      const id = networkIdOf(names, connected, node, iface);

      if (!id) {
        return;
      }

      if (!members.has(id)) {
        members.set(id, []);
      }

      members.get(id).push({ node, index, iface });
    });
  }

  return members;
}

/** The spec interfaces on a network (see interfacesByNetwork). */
export function interfacesOnNetwork(doc, networkId) {
  return interfacesByNetwork(doc).get(networkId) || [];
}

// The network of each connection point that a connection joins, by its id.
function connectedHandles(doc) {
  const connected = new Map();

  for (const edge of doc?.edges || []) {
    for (const handleId of [edge?.sourceHandleId, edge?.targetHandleId]) {
      if (handleId) {
        connected.set(handleId, edge.networkId);
      }
    }
  }

  return connected;
}

// The id of the network a spec interface is on: that of the connection of
// its connection point, else that of the network its VLAN names.
function networkIdOf(names, connected, node, iface) {
  const handle = (node.device.interfaces || []).find(
    (entry) => entry?.name === iface?.name,
  );

  return handle && connected.has(handle.id)
    ? connected.get(handle.id)
    : networkOfVLAN(names, iface?.vlan)?.id;
}

// Protos whose interface has no address of its own: a dhcp interface asks
// a DHCP server for one, and phenix starts a manual one with none.
export const UNADDRESSED_PROTOS = new Set(['dhcp', 'manual']);

/**
 * The IPv4 address an interface has as its own, as the server reads it
 * (hostIPv4 in types/builder/warnings.go). A dhcp or manual one has none.
 */
export function hostAddress(iface) {
  const proto = typeof iface?.proto === 'string' ? iface.proto : '';

  if (
    typeof iface?.address !== 'string' ||
    UNADDRESSED_PROTOS.has(goToLower(trimASCIISpace(proto)))
  ) {
    return null;
  }

  return parseIPv4(trimASCIISpace(iface.address.split('/')[0]));
}

/** Whether a spec type routes: the vrouter app configures Router or Firewall, in any case. */
export function isRoutingType(type) {
  return (
    typeof type === 'string' &&
    (goEqualFold(type, 'router') || goEqualFold(type, 'firewall'))
  );
}

// Whether a device node routes (see isRoutingType).
function routes(node) {
  return isRoutingType(node?.device?.spec?.type);
}

/**
 * The networks whose subnet overlaps another's (overlappingSubnets in
 * types/builder/warnings.go). Subnets overlap only when one holds the other,
 * so a sorted scan with a stack of holders finds them.
 */
export function overlappingSubnets(networks) {
  const ranges = (networks || [])
    .map((network, index) => ({ index, subnet: parseSubnet(network?.subnet) }))
    .filter(({ subnet }) => subnet)
    .map(({ index, subnet }) => ({
      index,
      base: subnet.base,
      end: subnet.base + subnet.size,
    }))
    .sort((a, b) => a.base - b.base || b.end - a.end || a.index - b.index);
  const holders = [];
  const found = [];

  ranges.forEach((range, position) => {
    while (holders.length && holders[holders.length - 1].end <= range.base) {
      holders.pop();
    }

    const next = ranges[position + 1];
    const other = holders.length
      ? holders[holders.length - 1]
      : next && next.base < range.end
        ? next
        : null;

    holders.push(range);

    if (other) {
      found.push({ index: range.index, other: other.index });
    }
  });

  return found;
}

// The gateway of a network as assignment uses it: the address, or null for
// none or one that does not parse. `blank` when the network has none, so a
// router may set it. `free` when it is a host address of the subnet that no
// interface on the network has in its address field, other than `except`,
// so a router may take it.
function gatewayState(network, subnet, members, except) {
  const gateway = parseIPv4(trimmed(network.gateway));
  const held = members.some(
    (member) =>
      member.iface !== except &&
      gateway !== null &&
      interfaceAddress(member.iface) === gateway,
  );

  return {
    gateway,
    blank: trimmed(network.gateway) === '',
    free: gateway !== null && isSubnetHost(subnet, gateway) && !held,
  };
}

/**
 * A free host address for an interface whose address another uses (Fix in
 * the Diagram checks), by the rules of assignAddresses. null for the first
 * router interface that holds the gateway: the others move.
 */
export function freeAddressFor(doc, nodeId, index) {
  const node = deviceNodes(doc).find((entry) => entry.id === nodeId);
  const iface = node ? specInterfacesOf(node)[index] : undefined;
  const own = iface ? interfaceAddress(iface) : null;

  if (own === null) {
    return null;
  }

  const networkId = networkIdOf(
    networkNames(doc),
    connectedHandles(doc),
    node,
    iface,
  );
  const network = (doc?.networks || []).find(
    (entry) => entry && entry.id === networkId,
  );
  const subnet = parseSubnet(network?.subnet);

  if (!subnet) {
    return null;
  }

  const members = interfacesOnNetwork(doc, network.id);
  const { gateway, blank, free } = gatewayState(
    network,
    subnet,
    members,
    iface,
  );
  const router = routes(node);

  if (router && own === gateway && isSubnetHost(subnet, gateway)) {
    const keeper = members.find(
      (member) =>
        routes(member.node) && interfaceAddress(member.iface) === gateway,
    );

    if (keeper?.iface === iface) {
      return null;
    }
  }

  if (router && free) {
    return {
      network,
      address: formatIPv4(gateway),
      mask: subnet.bits,
      holdsGateway: true,
    };
  }

  const taken = new Set([own]);

  for (const member of members) {
    const value = interfaceAddress(member.iface);

    if (value !== null) {
      taken.add(value);
    }
  }

  if (gateway !== null) {
    taken.add(gateway);
  }

  const value = nextFreeHost(subnet, taken);

  if (value === null) {
    return { network, full: true };
  }

  const found = { network, address: formatIPv4(value), mask: subnet.bits };

  if (router) {
    return blank ? { ...found, holdsGateway: true, setGateway: true } : found;
  }

  return gateway !== null && trimmed(iface.gateway) === ''
    ? { ...found, gateway: trimmed(network.gateway) }
    : found;
}

/** The subnet suggested for a new network: one free of the document's subnets and addresses. */
export function suggestedSubnet(doc) {
  const taken = (doc?.networks || [])
    .map((network) => parseSubnet(network?.subnet))
    .filter(Boolean);
  const used = [
    ...deviceNodes(doc).flatMap((node) =>
      specInterfacesOf(node).map(interfaceAddress),
    ),
    ...(doc?.networks || []).map((network) => parseIPv4(network?.gateway)),
  ].filter((value) => value !== null);

  return suggestSubnet(taken, used);
}

/**
 * Gives static interfaces on a network with no address the next free host
 * addresses of its subnet. Router interfaces go first and hold or set the
 * gateway. It never changes a set address or an included device.
 */
export function assignAddresses(doc, networkId, only) {
  const network = (doc?.networks || []).find(
    (entry) => entry?.id === networkId,
  );
  const subnet = parseSubnet(network?.subnet);
  const none = { doc, assigned: [], full: false, left: 0 };

  if (!subnet) {
    return none;
  }

  const members = interfacesOnNetwork(doc, networkId);
  const taken = new Set(
    members
      .map(({ iface }) => interfaceAddress(iface))
      .filter((v) => v !== null),
  );
  const state = gatewayState(network, subnet, members);
  let gatewayText = trimmed(network.gateway);
  let gateway = state.gateway;
  // Whether a router may still take the gateway, or set it.
  let free = state.free;
  let blank = state.blank;

  if (gateway !== null) {
    taken.add(gateway);
  }

  const targets = members.filter(
    ({ node, iface }) =>
      needsAddress(iface) &&
      !node.device.includedFrom &&
      (!only || (node.id === only.nodeId && iface.name === only.name)),
  );
  const ordered = [
    ...targets.filter(({ node }) => routes(node)),
    ...targets.filter(({ node }) => !routes(node)),
  ];
  const copies = new Map();
  const assigned = [];
  let setGateway = false;
  let full = false;

  for (const { node, index } of ordered) {
    const router = routes(node);
    const holds = router && free;
    const value = holds ? gateway : nextFreeHost(subnet, taken);

    if (value === null) {
      full = true;
      break;
    }

    taken.add(value);

    if (!copies.has(node.id)) {
      copies.set(node.id, deepCopy(node));
    }

    const iface = specInterfacesOf(copies.get(node.id))[index];
    let role = {};

    iface.address = formatIPv4(value);
    iface.mask = subnet.bits;

    if (holds) {
      free = false;
      role = { holdsGateway: true };
    } else if (router && blank) {
      blank = false;
      gateway = value;
      gatewayText = iface.address;
      setGateway = true;
      role = { holdsGateway: true, setGateway: true };
    } else if (!router && gateway !== null && trimmed(iface.gateway) === '') {
      iface.gateway = gatewayText;
    }

    assigned.push({
      nodeId: node.id,
      name: iface.name,
      address: iface.address,
      mask: subnet.bits,
      ...role,
    });
  }

  const left = targets.length - assigned.length;

  if (copies.size === 0) {
    return { ...none, full, left };
  }

  return {
    doc: {
      ...doc,
      nodes: doc.nodes.map((node) => copies.get(node?.id) || node),
      ...(setGateway
        ? {
            networks: doc.networks.map((entry) =>
              entry === network ? { ...entry, gateway: gatewayText } : entry,
            ),
          }
        : {}),
    },
    assigned,
    full,
    left,
  };
}

/**
 * Gives the network's gateway to non-router interfaces that got addresses
 * before a router set it in the same edit (connectDevices in model.js).
 */
export function withNetworkGateway(doc, networkId, entries) {
  const network = (doc?.networks || []).find(
    (entry) => entry?.id === networkId,
  );
  const gateway = trimmed(network?.gateway);
  const copies = new Map();

  if (parseIPv4(gateway) === null) {
    return doc;
  }

  for (const { nodeId, name } of entries) {
    const node =
      copies.get(nodeId) || deviceNodes(doc).find((n) => n.id === nodeId);
    const index = node
      ? specInterfacesOf(node).findIndex((iface) => iface?.name === name)
      : -1;
    const iface = index >= 0 ? specInterfacesOf(node)[index] : undefined;

    if (!iface || routes(node) || trimmed(iface.gateway) !== '') {
      continue;
    }

    const copy = copies.get(nodeId) || deepCopy(node);

    specInterfacesOf(copy)[index].gateway = gateway;
    copies.set(nodeId, copy);
  }

  return copies.size === 0
    ? doc
    : {
        ...doc,
        nodes: doc.nodes.map((node) => copies.get(node?.id) || node),
      };
}
