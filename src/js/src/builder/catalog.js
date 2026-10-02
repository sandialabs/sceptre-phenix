// Palette catalog and the bounded icon key registry.
//
// Icon keys mirror the server registry (phenix/types/builder icons.go) exactly:
// a document carrying any other key is rejected by the server, so the palette
// may only ever produce keys from this list. Shape + text label mean node
// identity is never communicated by color alone.

import { ICON_ID } from './icons.js';

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
 * Icon keys kept in the registry so saved documents still load, but no longer
 * offered for new choices. Printer nodes were removed from phenix.
 */
export const RETIRED_ICON_KEYS = ['printer'];

/**
 * @param {string} key
 * @returns {boolean} true when key is a registry member
 */
export function isIconKey(key) {
  return ICON_KEYS.includes(key);
}

// The built-in device templates, in the shape of a template of a diagram or
// a library: {id, name, description, device: {iconKey, spec}}. They equal
// the templates every template library starts with on the server
// (BuiltinTemplates in types/builder/template.go; one fixture,
// testdata/builtin-templates.json, is checked against both).
//
// Every spec is a complete, valid phenix node spec. `description` is the
// palette entry's tooltip, never the node's description, so each spec's own
// description is empty. `type` is the node type phenix acts on: its vrouter
// app configures routing and rulesets only on nodes of type Router or
// Firewall, and there through their router OS types (minirouter, vyatta or
// vyos; for the deprecated linux it writes a Vyatta config into the image).
// The Router template runs minirouter and the Firewall template VyOS, each
// on the image named after its OS type.
//
// The ids are names, not the UUIDs a diagram's templates have: a copy kept
// in a diagram gets an id of its own (see addTemplate in model.js). They
// are frozen, as every device made from one is a copy (see
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
];

const KIND_META = {
  device: { iconKey: 'server', shape: 'rectangle', label: 'Device' },
  switch: { iconKey: 'switch', shape: 'hexagon', label: 'Switch' },
  note: { iconKey: 'vlan', shape: 'note', label: 'Note' },
  group: { iconKey: 'container', shape: 'container', label: 'Group' },
};

/**
 * @param {string} kind
 * @returns {{iconKey: string, shape: string, label: string}}
 */
export function kindMeta(kind) {
  return KIND_META[kind] || KIND_META.device;
}

/**
 * Icon key for a node: a device carries its own key, a group may carry one
 * in place of the key of its kind, and other kinds use the key of their
 * kind.
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

  return kindMeta(node.kind).iconKey;
}

/**
 * The custom icon a node names: a device's or a group's, which is drawn in
 * place of the icon of its key when the document carries it (see iconSrc in
 * icons.js). Other kinds of nodes have none.
 *
 * @param {object} node builder document node
 * @returns {string} an icon id, or '' for none and for a value that is no
 *   icon id
 */
export function nodeIcon(node) {
  const id = node?.device?.icon || node?.group?.icon;

  return typeof id === 'string' && ICON_ID.test(id) ? id : '';
}

/**
 * Icon key implied by a device spec's node type or operating system, used
 * when importing or generating documents that carry no explicit key.
 *
 * @param {object} spec phenix node spec
 * @returns {string}
 */
export function iconKeyForSpec(spec) {
  if (spec?.external === true) {
    return 'external';
  }

  // A node type phenix names by what the node is (Router, Firewall, …) says
  // so before its OS type does, as iconKeyForSpec in the server's
  // generate.go decides it.
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
