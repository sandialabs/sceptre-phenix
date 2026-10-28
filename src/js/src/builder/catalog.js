// Palette catalog and the bounded icon key registry. Icon keys mirror the
// server registry (phenix/types/builder icons.go) exactly, because the server
// rejects a document with any other key.

import { isIconName } from './icons.js';

/** Bounded icon key registry, identical to the server registry. */
const ICON_KEYS = [
  'alpine',
  'android',
  'apple',
  'blackhat',
  'bsd',
  'cell-tower',
  'centos',
  'container',
  'desktop',
  'distribution',
  'external',
  'fedora',
  'firewall',
  'fox',
  'hacker',
  'human',
  'kali',
  'laptop',
  'linux',
  'lizard',
  'minimega',
  'ntp',
  'opensuse',
  'pfsense',
  'phenix',
  'plc',
  'printer',
  'redhat',
  'relay',
  'robot',
  'router',
  'rtac',
  'rtu',
  'server',
  'smartphone',
  'switch',
  'terminal',
  'transmission',
  'ubuntu',
  'vlan',
  'windows',
  'wireshark',
  'zeek',
];

const DEFAULT_ICON_KEY = 'server';

/**
 * @param {string} key
 * @returns {boolean} true when key is a registry member
 */
export function isIconKey(key) {
  return ICON_KEYS.includes(key);
}

// The built-in device templates, equal to BuiltinTemplates in
// types/builder/template.go. testdata/builtin-templates.json checks both.
// `description` is the palette tooltip, so each spec's own description is
// empty. The templates are frozen, because each device made from one is a
// copy (see nodeOptionsFromTemplate in templates.js).
function virtualMachine(type, hostname, osType, image) {
  return {
    type,
    general: { hostname, description: '', vm_type: 'kvm' },
    hardware: { os_type: osType, drives: [{ image }] },
    network: { interfaces: [] },
  };
}

function deepFreeze(value) {
  if (value && typeof value === 'object') {
    Object.values(value).forEach(deepFreeze);
    Object.freeze(value);
  }

  return value;
}

export const BUILTIN_TEMPLATES = deepFreeze([
  {
    id: 'server',
    name: 'Server',
    description: 'Generic Linux server',
    device: {
      iconKey: 'server',
      spec: virtualMachine('VirtualMachine', 'server', 'linux', 'ubuntu.qc2'),
    },
  },
  {
    id: 'workstation',
    name: 'Workstation',
    description: 'Operator workstation',
    device: {
      iconKey: 'desktop',
      spec: virtualMachine(
        'VirtualMachine',
        'workstation',
        'windows',
        'windows10.qc2',
      ),
    },
  },
  {
    id: 'router',
    name: 'Router',
    description: 'Layer 3 router',
    device: {
      iconKey: 'router',
      spec: virtualMachine('Router', 'router', 'minirouter', 'minirouter.qc2'),
    },
  },
  {
    id: 'firewall',
    name: 'Firewall',
    description: 'Perimeter firewall',
    device: {
      iconKey: 'firewall',
      spec: virtualMachine('Firewall', 'firewall', 'vyos', 'vyos.qc2'),
    },
  },
  {
    id: 'external',
    name: 'External device',
    description: 'Hardware in the loop device',
    device: {
      iconKey: 'external',
      spec: {
        external: true,
        type: 'HIL',
        general: { hostname: 'external', description: '' },
        network: { interfaces: [] },
      },
    },
  },
]);

export const PALETTE = [
  {
    kind: 'device',
    id: 'device',
    label: 'Device',
    iconKey: 'server',
    hint: 'A virtual machine, container or external device',
  },
  {
    kind: 'switch',
    id: 'switch',
    label: 'Switch',
    iconKey: 'switch',
    hint: 'A visual hub bound to exactly one network',
  },
  {
    kind: 'note',
    id: 'note',
    label: 'Note',
    iconKey: 'sticky-note',
    hint: 'Free-form annotation with no phenix semantics',
  },
  {
    kind: 'group',
    id: 'group',
    label: 'Group',
    iconKey: 'container',
    hint: 'A container that visually groups member nodes',
  },
  // Drawings: shapes, icons and lines with no phenix semantics. Each of the
  // two figures of a shape is a separate entry, so each is one click away.
  // `options` are the store.addNode options that an entry adds in addition to
  // its kind.
  {
    kind: 'shape',
    id: 'rectangle',
    label: 'Rectangle',
    iconKey: 'rectangle',
    options: { shape: 'rectangle' },
    hint: 'A rectangle drawn on the canvas, with no phenix semantics',
  },
  {
    kind: 'shape',
    id: 'circle',
    label: 'Circle',
    iconKey: 'circle',
    options: { shape: 'circle' },
    hint: 'A circle drawn on the canvas, with no phenix semantics',
  },
  {
    kind: 'icon',
    id: 'icon',
    label: 'Icon',
    iconKey: 'external',
    hint: 'A built-in or custom icon drawn on the canvas, with no phenix semantics',
  },
  {
    kind: 'line',
    id: 'line',
    label: 'Line',
    iconKey: 'line',
    hint: 'A line drawn on the canvas, joined to no node or network, with optional arrowheads',
  },
];

const KIND_META = {
  device: { iconKey: 'server', label: 'Device' },
  switch: { iconKey: 'switch', label: 'Switch' },
  note: { iconKey: 'sticky-note', label: 'Note' },
  group: { iconKey: 'container', label: 'Group' },
  shape: { iconKey: 'rectangle', label: 'Shape' },
  icon: { iconKey: 'external', label: 'Icon' },
  line: { iconKey: 'line', label: 'Line' },
};

/**
 * The palette entry of an id, as a palette entry dragged onto the canvas names
 * it.
 */
export function paletteEntry(id) {
  return PALETTE.find((item) => item.id === id);
}

/**
 * @param {string} kind
 * @returns {{iconKey: string, label: string}}
 */
export function kindMeta(kind) {
  return KIND_META[kind] || KIND_META.device;
}

/**
 * Icon key for a node. A device carries its own key, and a group can carry
 * one. Other kinds use the key of their kind.
 */
export function nodeIconKey(node) {
  if (!node) {
    return DEFAULT_ICON_KEY;
  }

  if (node.kind === 'device') {
    const key = node.device?.iconKey;
    return isIconKey(key) ? key : DEFAULT_ICON_KEY;
  }

  if (node.kind === 'group' && isIconKey(node.group?.iconKey)) {
    return node.group.iconKey;
  }

  if (node.kind === 'icon' && isIconKey(node.icon?.iconKey)) {
    return node.icon.iconKey;
  }

  return kindMeta(node.kind).iconKey;
}

/**
 * The custom icon name that a device, a group or an icon node names, or ''.
 * The canvas draws it when iconSrc in icons.js resolves it.
 */
export function nodeIcon(node) {
  const name =
    node?.device?.icon ||
    node?.group?.icon ||
    (node?.kind === 'icon' ? node.icon?.icon : '');

  return isIconName(name) ? name : '';
}

/**
 * Icon key implied by a device spec's node type or OS, for documents that carry
 * no key.
 */
export function iconKeyForSpec(spec) {
  if (spec?.external === true) {
    return 'external';
  }

  // A node type that tells what the node is (Router, Firewall, …) has
  // priority over its OS type, as in iconKeyForSpec in the server's
  // generate.go.
  const nodeType = String(spec?.type || '')
    .trim()
    .toLowerCase();

  if (nodeType && nodeType !== 'virtualmachine' && isIconKey(nodeType)) {
    return nodeType;
  }

  const osType = String(spec?.hardware?.os_type || '').toLowerCase();
  const vmType = String(spec?.general?.vm_type || '').toLowerCase();

  if (vmType === 'container') {
    return 'container';
  }

  switch (osType) {
    case 'windows':
      return 'windows';
    case 'centos':
      return 'centos';
    case 'rhel':
      return 'redhat';
    case 'minirouter':
    case 'vyatta':
    case 'vyos':
      return 'router';
    case 'linux':
      return 'linux';
    default:
      return DEFAULT_ICON_KEY;
  }
}
