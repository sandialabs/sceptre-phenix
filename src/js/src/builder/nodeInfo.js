// What a device or a switch says about itself on the canvas: the rows of
// its info tooltip (BuilderNodeTooltip.vue), shown on hover and keyboard
// focus, and the same facts as one sentence, which describes the node to
// assistive technology (see useNodeInfo in nodes/nodeTooltip.js). Notes,
// groups and connections have neither.

import { count } from './announce.js';

// A description longer than this many characters is cut.
const DESCRIPTION_LENGTH = 80;
// A device lists this many interfaces, and a switch this many devices,
// before "+N more".
const LISTED = 8;

/**
 * A text on one line and no longer than `max` characters: runs of white
 * space become one space, and a longer text is cut there, with an ellipsis
 * after it.
 *
 * @param {string} text
 * @param {number} [max]
 * @returns {string}
 */
export function cut(text, max = DESCRIPTION_LENGTH) {
  const characters = Array.from(
    String(text ?? '')
      .replace(/\s+/g, ' ')
      .trim(),
  );

  return characters.length > max
    ? `${characters.slice(0, max).join('')}…`
    : characters.join('');
}

function isSet(value) {
  return value !== undefined && value !== null && String(value).trim() !== '';
}

function lower(value) {
  return typeof value === 'string' ? value.trim().toLowerCase() : '';
}

/**
 * The address an interface has, as the tooltips show it: "DHCP" for one
 * that asks a DHCP server (phenix then gives it none of its own), its
 * address with the mask after a slash (unless the address is written with
 * one), "serial" for a serial interface, and "no address" for any other.
 *
 * @param {object} [iface] spec interface
 * @returns {string}
 */
export function interfaceAddress(iface) {
  if (lower(iface?.proto) === 'dhcp') {
    return 'DHCP';
  }

  if (isSet(iface?.address)) {
    const address = String(iface.address).trim();

    return isSet(iface.mask) && !address.includes('/')
      ? `${address}/${iface.mask}`
      : address;
  }

  return lower(iface?.type) === 'serial' ? 'serial' : 'no address';
}

// The first entries of a list, and how many it leaves out.
function listed(entries) {
  return {
    shown: entries.slice(0, LISTED),
    more: Math.max(0, entries.length - LISTED),
  };
}

// The lines of a tooltip row: one for each entry shown, then "+N more".
function lines(entries, line) {
  const { shown, more } = listed(entries);

  if (!shown.length) {
    return ['None'];
  }

  return [...shown.map(line), ...(more ? [`+${more} more`] : [])];
}

// The same entries in a sentence: "a, b, and 2 more".
function spoken(entries, phrase) {
  const { shown, more } = listed(entries);

  return [...shown.map(phrase), ...(more ? [`and ${more} more`] : [])].join(
    ', ',
  );
}

/**
 * A device's info: its description, its interfaces with their addresses,
 * and its OS type. The sentence leaves the description out, as the node's
 * accessible name ends with it.
 *
 * @param {object} node device node
 * @returns {{rows: {label: string, lines: string[]}[], text: string}}
 */
export function deviceInfo(node) {
  const spec = node?.device?.spec;
  const interfaces = (
    Array.isArray(spec?.network?.interfaces) ? spec.network.interfaces : []
  ).map((iface) => ({
    name: isSet(iface?.name) ? String(iface.name) : 'unnamed',
    address: interfaceAddress(iface),
  }));
  const osType = isSet(spec?.hardware?.os_type)
    ? String(spec.hardware.os_type)
    : '';

  return {
    rows: [
      {
        label: 'Description',
        lines: [cut(spec?.general?.description) || 'None'],
      },
      {
        label: 'Interfaces',
        lines: lines(interfaces, (entry) => `${entry.name} — ${entry.address}`),
      },
      { label: 'OS type', lines: [osType || 'Not set'] },
    ],
    text: [
      interfaces.length
        ? `Interfaces: ${spoken(interfaces, (entry) => `${entry.name} ${entry.address}`)}.`
        : 'No interfaces.',
      `OS type ${osType || 'not set'}.`,
    ].join(' '),
  };
}

function byLabel(a, b) {
  return a.label.localeCompare(b.label, undefined, {
    numeric: true,
    sensitivity: 'base',
  });
}

/**
 * A switch's info: its network's name, VLAN alias and description, and the
 * devices connected to this switch, by name, each with the addresses of
 * the interfaces it connects on. The sentence leaves the network and the
 * alias out, as the node's accessible name says both.
 *
 * @param {object} [network] the switch's network
 * @param {{label: string, addresses: string[]}[]} [connected] see
 *   connectedDevices in adapters/vueflow.js
 * @returns {{rows: {label: string, lines: string[]}[], text: string}}
 */
export function switchInfo(network, connected = []) {
  const devices = [...connected].sort(byLabel).map((device) => ({
    label: device.label,
    addresses: (device.addresses || []).join(', '),
  }));
  const description = cut(network?.description);

  return {
    rows: [
      { label: 'Network', lines: [network?.name || 'No network'] },
      {
        label: 'VLAN alias',
        lines: [isSet(network?.alias) ? String(network.alias) : 'no alias'],
      },
      { label: 'Description', lines: [description || 'None'] },
      {
        label: `Connected devices (${devices.length})`,
        lines: lines(devices, (device) =>
          device.addresses
            ? `${device.label} — ${device.addresses}`
            : device.label,
        ),
      },
    ],
    text: [
      ...(description ? [`Description: ${description}.`] : []),
      devices.length
        ? `${count(devices.length, 'connected device')}: ${spoken(
            devices,
            (device) =>
              device.addresses
                ? `${device.label} ${device.addresses}`
                : device.label,
          )}.`
        : 'No connected devices.',
    ].join(' '),
  };
}
