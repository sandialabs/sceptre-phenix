// IPv4 addressing of the Builder's networks, and random MAC addresses.
//
// A network can have a subnet and a gateway (Network.Subnet and
// Network.Gateway in types/builder/document.go). They are the Builder's own:
// a publish writes neither. The editor gives a static interface on such a
// network the next free host address of the subnet: when a connection joins
// it to a switch of the network, and with Assign addresses. It never changes
// an address that an interface has.
//
// This module reads the document's plain data and imports nothing from
// model.js, which uses it, or from validate.js, which uses it too.

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

/**
 * An IPv4 address as a number.
 *
 * @param {unknown} text
 * @returns {number|null} null when text is not an IPv4 address
 */
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
 * The subnet of a network, read as the server reads it (ParseSubnet in
 * types/builder/validate.go): an IPv4 address, a slash and a prefix length
 * from 0 to 32, without white space or host bits.
 *
 * @param {unknown} text
 * @returns {{base: number, bits: number, size: number}|null} its first
 *   address, prefix length and number of addresses. null when text is not
 *   such a subnet
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
 * The host addresses of a subnet: all its addresses but the network and
 * broadcast addresses. A /31 has none to leave out (RFC 3021), and a /32 is
 * its one host (IsSubnetHost in types/builder/validate.go).
 *
 * @param {{base: number, size: number}} subnet
 * @returns {{first: number, last: number}}
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

/**
 * The first host address of a subnet that is not taken.
 *
 * @param {{base: number, size: number}} subnet
 * @param {Set<number>} taken addresses as numbers
 * @returns {number|null} null when every host address is taken
 */
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

/**
 * The first subnet 10.<a>.<b>.0/24 that overlaps no subnet in `taken` and
 * holds no address in `used`.
 *
 * @param {{base: number, size: number}[]} taken
 * @param {number[]} [used] addresses as numbers
 * @returns {string} '' when there is none
 */
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

/**
 * A MAC address as a key that is the same in either case and with any
 * separators (interfaceMAC in validate.js).
 *
 * @param {unknown} text
 * @returns {string} twelve lowercase hex digits, or '' for no MAC address
 */
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
 * A random MAC address that is locally administered and unicast (the
 * second-lowest bit of its first number set, the lowest clear), so it is
 * never a vendor's address or a multicast address.
 *
 * @param {Set<string>} [taken] keys (see macKey) of addresses in use
 * @param {(bytes: Uint8Array) => Uint8Array} [fill] fills the bytes with
 *   random values: crypto.getRandomValues by default
 * @returns {string} such as 02:5e:a1:07:c3:9b, or '' when every try was taken
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

/**
 * Every MAC address that a device interface of the document uses, as keys
 * (see macKey).
 *
 * @param {object} doc
 * @returns {Set<string>}
 */
export function documentMacs(doc) {
  return new Set(
    deviceNodes(doc)
      .flatMap((node) =>
        specInterfacesOf(node).map((iface) => macKey(iface?.mac)),
      )
      .filter(Boolean),
  );
}

/**
 * Whether an interface is the static kind ("Static or OSPF") with no
 * address: one that assignment fills.
 *
 * @param {object} iface spec interface
 * @returns {boolean}
 */
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

// The network a VLAN names: the one of exactly that name, else one of that
// name in another case (networkByName in model.js).
function networkOfVLAN(doc, vlan) {
  const name = trimmed(vlan);
  const networks = doc?.networks || [];

  return name === ''
    ? undefined
    : networks.find((network) => network.name === name) ||
        networks.find(
          (network) => network.name?.toLowerCase() === name.toLowerCase(),
        );
}

/**
 * The spec interfaces on a network: each one whose connection point joins a
 * switch of the network, and each one without a connection whose VLAN
 * names the network.
 *
 * @param {object} doc
 * @param {string} networkId
 * @returns {{node: object, index: number, iface: object}[]} in the order of
 *   the nodes, then of each device's interfaces
 */
export function interfacesOnNetwork(doc, networkId) {
  const connected = new Map();

  for (const edge of doc?.edges || []) {
    for (const handleId of [edge?.sourceHandleId, edge?.targetHandleId]) {
      if (handleId) {
        connected.set(handleId, edge.networkId);
      }
    }
  }

  return deviceNodes(doc).flatMap((node) => {
    const handles = node.device.interfaces || [];

    return specInterfacesOf(node).flatMap((iface, index) => {
      const handle = handles.find((entry) => entry?.name === iface?.name);
      const on =
        handle && connected.has(handle.id)
          ? connected.get(handle.id) === networkId
          : networkOfVLAN(doc, iface?.vlan)?.id === networkId;

      return on ? [{ node, index, iface }] : [];
    });
  });
}

/**
 * The subnet the editor suggests for a new network: the first 10.<a>.<b>.0/24
 * that overlaps no network's subnet and holds no address of an interface or
 * a gateway of the document (see suggestSubnet).
 *
 * @param {object} doc
 * @returns {string} '' when there is none
 */
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
 * addresses of the network's subnet, with the subnet's prefix length as
 * their mask. A free address is a host address of the subnet that is not
 * the network's gateway and that no interface on the network uses. An
 * interface with no gateway gets the network's, if it has one. An address
 * that an interface has is never changed, and a device from an included
 * topology is never changed.
 *
 * @param {object} doc
 * @param {string} networkId
 * @param {object} [only] the one interface to fill: nodeId and the
 *   interface's name. Every interface on the network by default
 * @returns {{doc: object, assigned: {nodeId: string, name: string, address: string, mask: number}[], full: boolean, left: number}}
 *   `doc` is the same document when nothing was assigned. `full` is true
 *   when an interface needed an address and the subnet had none left.
 *   `left` counts the interfaces that needed one and did not get it
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
  const gateway = parseIPv4(network.gateway);

  if (gateway !== null) {
    taken.add(gateway);
  }

  const targets = members.filter(
    ({ node, iface }) =>
      needsAddress(iface) &&
      !node.device.includedFrom &&
      (!only || (node.id === only.nodeId && iface.name === only.name)),
  );
  const copies = new Map();
  const assigned = [];
  let full = false;

  for (const { node, index } of targets) {
    const value = nextFreeHost(subnet, taken);

    if (value === null) {
      full = true;
      break;
    }

    taken.add(value);

    if (!copies.has(node.id)) {
      copies.set(node.id, JSON.parse(JSON.stringify(node)));
    }

    const iface = specInterfacesOf(copies.get(node.id))[index];

    iface.address = formatIPv4(value);
    iface.mask = subnet.bits;

    if (gateway !== null && trimmed(iface.gateway) === '') {
      iface.gateway = network.gateway;
    }

    assigned.push({
      nodeId: node.id,
      name: iface.name,
      address: iface.address,
      mask: subnet.bits,
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
    },
    assigned,
    full,
    left,
  };
}
