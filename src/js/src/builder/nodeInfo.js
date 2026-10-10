// What a device or a switch says about itself on the canvas: the rows of
// its info tooltip (BuilderNodeTooltip.vue), shown on hover and keyboard
// focus, and the same facts as one sentence, which describes the node to
// assistive technology (see useNodeInfo in nodes/nodeTooltip.js). Note
// nodes, groups and connections have neither. A device or a switch with
// notes (see nodeNotes in model.js) lists them last, in both: the notes
// block below the node is hidden from assistive technology, so the
// sentence says them in its place, as much of them as the block shows.

import { count } from './announce.js';
import { nodeNotes } from './model.js';
import { noteCharacters, shownNotes } from './nodeNotes.js';

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

// The tooltip row of a node's notes, each cut to one line, and the sentence
// that says them: the notes the block below the node shows (shownNotes),
// each whole unless it is longer than the block can show at the node's
// width (noteCharacters), and then how many more there are, as the block's
// "+N more" line does. The notes are apart by semicolons, as a note may
// hold commas. Both are empty for a node without notes.
function notesInfo(node) {
  const notes = nodeNotes(node);

  if (!notes.length) {
    return { rows: [], text: [] };
  }

  const longest = noteCharacters(node);
  const { shown, more } = shownNotes(notes);
  const said = [
    ...shown.map((note) => cut(note, longest)),
    ...(more ? [`and ${count(more, 'more note')}`] : []),
  ].join('; ');
  // A sentence that ends a note ends the list.
  const end = /[.!?…]$/.test(said) ? '' : '.';

  return {
    rows: [
      {
        label: `Notes (${notes.length})`,
        lines: lines(
          notes.map((note) => cut(note)),
          (note) => note,
        ),
      },
    ],
    text: [`${count(notes.length, 'note')}: ${said}${end}`],
  };
}

/**
 * A device's info: its description, its interfaces with their addresses,
 * its OS type, and its notes when it has some. The sentence leaves the
 * description out, as the node's accessible name ends with it.
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
  const notes = notesInfo(node);

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
      ...notes.rows,
    ],
    text: [
      interfaces.length
        ? `Interfaces: ${spoken(interfaces, (entry) => `${entry.name} ${entry.address}`)}.`
        : 'No interfaces.',
      `OS type ${osType || 'not set'}.`,
      ...notes.text,
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
 * A switch's info: its network's name, VLAN alias and description, the
 * devices connected to this switch, by name, each with the addresses of
 * the interfaces it connects on, and the switch's notes when it has some.
 * The sentence leaves the network and the alias out, as the node's
 * accessible name says both.
 *
 * @param {object} [network] the switch's network
 * @param {{label: string, addresses: string[]}[]} [connected] see
 *   connectedDevices in adapters/vueflow.js
 * @param {object} [node] the switch node, whose notes are listed
 * @returns {{rows: {label: string, lines: string[]}[], text: string}}
 */
export function switchInfo(network, connected = [], node = null) {
  const devices = [...connected].sort(byLabel).map((device) => ({
    label: device.label,
    addresses: (device.addresses || []).join(', '),
  }));
  const description = cut(network?.description);
  const notes = notesInfo(node);

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
      ...notes.rows,
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
      ...notes.text,
    ].join(' '),
  };
}
