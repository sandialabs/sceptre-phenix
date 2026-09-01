// Palette catalog and the bounded icon key registry.
//
// Icon keys mirror the server registry (phenix/types/builder icons.go) exactly:
// a document carrying any other key is rejected by the server, so the palette
// may only ever produce keys from this list. Shape + text label mean node
// identity is never communicated by color alone.

export const NODE_KINDS = ['device', 'switch', 'note', 'group'];

/** Bounded icon key registry, identical to the server registry. */
export const ICON_KEYS = [
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

export const DEFAULT_ICON_KEY = 'server';

/**
 * @param {string} key
 * @returns {boolean} true when key is a registry member
 */
export function isIconKey(key) {
  return ICON_KEYS.includes(key);
}

// Palette device templates. Every template produces a complete, valid phenix
// minimega_node spec; `iconKey` is a builder-local presentation hint.
export const DEVICE_TEMPLATES = [
  {
    id: 'server',
    label: 'Server',
    iconKey: 'server',
    description: 'Generic Linux server',
    osType: 'linux',
    image: 'ubuntu.qc2',
  },
  {
    id: 'workstation',
    label: 'Workstation',
    iconKey: 'desktop',
    description: 'Operator workstation',
    osType: 'windows',
    image: 'windows10.qc2',
  },
  {
    id: 'router',
    label: 'Router',
    iconKey: 'router',
    description: 'Layer 3 router',
    osType: 'minirouter',
    image: 'minirouter.qc2',
  },
  {
    id: 'firewall',
    label: 'Firewall',
    iconKey: 'firewall',
    description: 'Perimeter firewall',
    osType: 'linux',
    image: 'ubuntu.qc2',
  },
  {
    id: 'printer',
    label: 'Printer',
    iconKey: 'printer',
    description: 'Network printer',
    osType: 'linux',
    image: 'ubuntu.qc2',
  },
  {
    id: 'external',
    label: 'External device',
    iconKey: 'external',
    description: 'Hardware in the loop device',
    osType: 'linux',
    image: '',
    external: true,
  },
];

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
 * @param {string} id template id
 * @returns {object|undefined}
 */
export function deviceTemplate(id) {
  return DEVICE_TEMPLATES.find((template) => template.id === id);
}

/**
 * Icon key for a node: devices carry their own key, other kinds use the key of
 * their kind.
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

  return kindMeta(node.kind).iconKey;
}

/**
 * Icon key implied by a device spec's operating system, used when importing or
 * generating documents that carry no explicit key.
 *
 * @param {object} spec phenix node spec
 * @returns {string}
 */
export function iconKeyForSpec(spec) {
  if (spec?.external === true) {
    return 'external';
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
