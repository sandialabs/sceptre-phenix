// Palette catalog and the bounded icon key registry.
//
// Icon keys mirror the server registry (phenix/types/builder icons.go)
// exactly. The server rejects a document that carries any other key, so the
// palette can only produce keys from this list. Each node shows a shape and a
// text label, so color is never the only cue to node identity.

import { isIconName } from './icons.js';

/** Bounded icon key registry, identical to the server registry. */
const ICON_KEYS = [
  'centos',
  'container',
  'desktop',
  'external',
  'firewall',
  'linux',
  'printer',
  'redhat',
  'router',
  'server',
  'switch',
  'vlan',
  'windows',
];

const DEFAULT_ICON_KEY = 'server';

/**
 * Icon keys kept in the registry so that saved documents still load. New
 * choices do not offer them. phenix does not have printer nodes any more.
 */
export const RETIRED_ICON_KEYS = ['printer'];

/**
 * @param {string} key
 * @returns {boolean} true when key is a registry member
 */
export function isIconKey(key) {
  return ICON_KEYS.includes(key);
}

// The built-in device templates, in the shape of a diagram or library
// template: {id, name, description, device: {iconKey, spec}}. They are equal
// to the templates that every template library on the server starts with
// (BuiltinTemplates in types/builder/template.go). One fixture,
// testdata/builtin-templates.json, is checked against both.
//
// Every spec is a complete, valid phenix node spec. `description` is the
// tooltip of the palette entry, never the node's description, so each spec's
// own description is empty. `type` is the node type that phenix acts on. Its
// vrouter app configures routing and rulesets only on nodes of type Router or
// Firewall, through their router OS types (minirouter, vyatta or vyos). For
// the deprecated linux OS type, it writes a Vyatta config into the image.
// The Router template runs minirouter and the Firewall template runs VyOS,
// each on the image named after its OS type.
//
// The ids are names, not the UUIDs of a diagram's templates. A copy kept in a
// diagram gets its own id (see addTemplate in model.js). The templates are
// frozen, because every device made from one is a copy (see
// nodeOptionsFromTemplate in templates.js).
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
    shape: 'rectangle',
    hint: 'A virtual machine, container or external device',
  },
  {
    kind: 'switch',
    id: 'switch',
    label: 'Switch',
    iconKey: 'switch',
    shape: 'hexagon',
    hint: 'A visual hub bound to exactly one network',
  },
  {
    kind: 'note',
    id: 'note',
    label: 'Note',
    iconKey: 'vlan',
    shape: 'note',
    hint: 'Free-form annotation with no phenix semantics',
  },
  {
    kind: 'group',
    id: 'group',
    label: 'Group',
    iconKey: 'container',
    shape: 'container',
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
    shape: 'shape',
    options: { shape: 'rectangle' },
    hint: 'A rectangle drawn on the canvas, with no phenix semantics',
  },
  {
    kind: 'shape',
    id: 'circle',
    label: 'Circle',
    iconKey: 'circle',
    shape: 'shape',
    options: { shape: 'circle' },
    hint: 'A circle drawn on the canvas, with no phenix semantics',
  },
  {
    kind: 'icon',
    id: 'icon',
    label: 'Icon',
    iconKey: 'external',
    shape: 'icon',
    hint: 'A built-in or custom icon drawn on the canvas, with no phenix semantics',
  },
  {
    kind: 'line',
    id: 'line',
    label: 'Line',
    iconKey: 'line',
    shape: 'line',
    hint: 'A line drawn on the canvas, joined to no node or network, with optional arrowheads',
  },
];

const KIND_META = {
  device: { iconKey: 'server', shape: 'rectangle', label: 'Device' },
  switch: { iconKey: 'switch', shape: 'hexagon', label: 'Switch' },
  note: { iconKey: 'vlan', shape: 'note', label: 'Note' },
  group: { iconKey: 'container', shape: 'container', label: 'Group' },
  shape: { iconKey: 'vlan', shape: 'shape', label: 'Shape' },
  icon: { iconKey: 'external', shape: 'icon', label: 'Icon' },
  line: { iconKey: 'vlan', shape: 'line', label: 'Line' },
};

/**
 * The palette entry of an id, as a palette entry dragged onto the canvas
 * names it.
 *
 * @param {string} id
 * @returns {object|undefined}
 */
export function paletteEntry(id) {
  return PALETTE.find((item) => item.id === id);
}

/**
 * @param {string} kind
 * @returns {{iconKey: string, shape: string, label: string}}
 */
export function kindMeta(kind) {
  return KIND_META[kind] || KIND_META.device;
}

/**
 * Icon key for a node. A device carries its own key. A group can carry a key
 * in place of the key of its kind. Other kinds use the key of their kind.
 *
 * @param {object} node builder document node
 * @returns {string}
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
 * The custom icon that a node names: a device's, a group's or an icon node's.
 * The canvas draws it in place of the icon of the node's key when the
 * document's copy of it or the icon library resolves it (see iconSrc in
 * icons.js). Other kinds of nodes have none.
 *
 * @param {object} node builder document node
 * @returns {string} an icon name, or '' for none and for a value that is not
 *   an icon name
 */
export function nodeIcon(node) {
  const name =
    node?.device?.icon ||
    node?.group?.icon ||
    (node?.kind === 'icon' ? node.icon?.icon : '');

  return isIconName(name) ? name : '';
}

/**
 * Icon key implied by a device spec's node type or operating system. Imports
 * and generated documents that carry no explicit key use it.
 *
 * @param {object} spec phenix node spec
 * @returns {string}
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
