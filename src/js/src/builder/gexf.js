// GEXF 1.3: the diagram as a graph for Gephi
// (https://gexf.net/schema.html).
//
// Devices and networks are the nodes. Each connection (a device's interface
// on a network) is an edge from the device to its network. All the switches
// of one network are one node, so a network drawn twice is still one hub in
// Gephi's statistics. Notes and group boxes are not nodes. They are not part
// of the network, and as nodes with no connection they would change its
// statistics. A node's groups are columns instead (group, groups).
//
// A device's scenario apps are also columns (apps, disabled_apps). These are
// the apps of the scenarios that the diagram lists and that list the
// device's hostname among their hosts. The document names its scenarios but
// holds none of their content. Thus the columns are written only when
// Download first read every one of the scenarios from the server.
//
// What the file uses of GEXF 1.3:
// - the 1.3 namespaces, and the schema's location, as the 1.3 primer and
//   Gephi write them.
// - meta: the day of the diagram's last change, creator, keywords and the
//   diagram's description.
// - typed columns (string, integer, long, double, boolean, liststring), with
//   a default where it is true for every node or edge (external, qinq and
//   autostart are false). Each label and annotation key has a column.
// - viz: position (y up, as Gephi draws it), color as r, g, b and a, one
//   size for every node, and node and edge shapes (Gephi ignores shapes).
// - kind, on the edges of a device with more than one interface on one
//   network. Without it, GEXF readers merge them into one edge.
//
// What it leaves out, and why:
// - pid, nested nodes and parents: Gephi 0.11 reports each pid as a
//   deprecated hierarchy (SEVERE) and ignores it, and graphology drops it.
// - anyURI columns, which Gephi drops.
// - <options>, whose text Gephi reads as the column's default.
// - hex colors, which networkx cannot read.
// - the other list types and short, byte, char, bigdecimal and biginteger,
//   which networkx cannot read. Numbers in a list are written as text.
// - float, because double holds every number that a document can hold.
// - dynamic mode, spells and timestamps: a diagram has no time.
// - the metadata that a scenario gives an app for each host. It configures
//   the app, not the network, and can be large.
// - directed edges (the model has none), and edge weight and thickness:
//   all connections are the same.
//
// Gephi Lite reads list columns as text. Thus the lists of values that
// repeat across devices (VLANs, interfaces, labels, apps) are also written as
// one text value, "a|b", which it can split into keywords. The edges carry
// the values of each interface (network, address, MAC) as single values.
//
// The file keeps the schema's location (xsi:schemaLocation) so that XML
// Schema tools find gexf.xsd. The official RelaxNG grammar, gexf.rng, rejects
// it: <gexf> can carry only version and variant, and no element of the
// grammar accepts an attribute that it does not name. gexf.net says to remove
// it before you check a file with xmllint. To check a file as saved, use a
// grammar that includes gexf.rng unchanged and adds an optional
// xsi:schemaLocation attribute to its gexf-content define (only <gexf> uses
// it) with combine="interleave". The file has no xml-model instruction that
// names gexf.rng, because the tools that follow one would report the same
// error.

import { networkStyle } from './adapters/vueflow.js';
import { colorChannels, nodeColors } from './colors.js';
import {
  DEFAULT_NETWORK_COLORS,
  deviceHandles,
  documentScenarios,
  LINE_STYLES,
  nodeLabel,
  scenarioApps,
  sizeOf,
  specInterfaceFor,
  specInterfaces,
} from './model.js';

export const GEXF_MIME = 'application/xml';

// Every node's size. Gephi's import scales sizes and positions by one ratio
// (the smallest size becomes 4), so nodes of one size keep the diagram's
// proportions.
const NODE_SIZE = 10;

// Devices are colored and shaped by what phenix runs them as. A device
// given a fill color, or else an outline color, has that color instead.
const DEVICE_LOOKS = {
  Router: { color: '#37474f', shape: 'diamond' },
  Firewall: { color: '#8e2c2c', shape: 'triangle' },
  external: { color: '#8a5a00', shape: 'disc' },
  device: { color: '#6b7c93', shape: 'disc' },
};

// A connection's dash pattern on the canvas (its own line style, or else its
// network's line style, see adapters/vueflow.js), as the nearest edge shape
// that GEXF has.
const EDGE_SHAPES = {
  solid: 'solid',
  dashed: 'dashed',
  dotted: 'dotted',
  'dash-dot': 'dashed',
};

// [id, title, type, default]. graphology (Gephi Lite) names a column by its
// id, Gephi and networkx by its title.
const NODE_COLUMNS = [
  ['kind', 'Node kind', 'string'],
  ['hostname', 'Hostname', 'string'],
  ['description', 'Description', 'string'],
  ['device_type', 'Device type', 'string'],
  ['vm_type', 'VM type', 'string'],
  ['os_type', 'OS type', 'string'],
  ['cpu', 'CPU model', 'string'],
  ['vcpus', 'vCPUs', 'integer'],
  ['memory_mb', 'Memory (MB)', 'integer'],
  ['disk_images', 'Disk images', 'liststring'],
  ['snapshot', 'Snapshot', 'boolean'],
  ['do_not_boot', 'Do not boot', 'boolean'],
  ['external', 'External', 'boolean', false],
  ['icon', 'Icon', 'string'],
  ['included_from', 'Included from', 'string'],
  ['group', 'Group', 'string'],
  ['groups', 'Groups', 'liststring'],
  ['interfaces', 'Interfaces', 'liststring'],
  ['interfaces_text', 'Interfaces (text)', 'string'],
  ['interface_count', 'Interface count', 'integer'],
  ['vlans', 'VLANs', 'liststring'],
  ['vlans_text', 'VLANs (text)', 'string'],
  ['ip_addresses', 'IP addresses', 'liststring'],
  ['cidrs', 'IP addresses (CIDR)', 'liststring'],
  ['mac_addresses', 'MAC addresses', 'liststring'],
  ['gateways', 'Gateways', 'liststring'],
  ['routes', 'Routes', 'liststring'],
  ['route_count', 'Route count', 'integer'],
  ['rulesets', 'Rulesets', 'liststring'],
  ['injections', 'Injections', 'liststring'],
  ['injection_count', 'Injection count', 'integer'],
  ['apps', 'Scenario apps', 'liststring'],
  ['apps_text', 'Scenario apps (text)', 'string'],
  ['app_count', 'Scenario app count', 'integer'],
  ['disabled_apps', 'Disabled scenario apps', 'liststring'],
  ['labels', 'Labels', 'liststring'],
  ['labels_text', 'Labels (text)', 'string'],
  ['network', 'Network', 'string'],
  ['vlan_alias', 'VLAN alias', 'integer'],
  ['attached_interfaces', 'Attached interfaces', 'integer'],
];

const EDGE_COLUMNS = [
  ['device', 'Device', 'string'],
  ['network', 'Network', 'string'],
  ['vlan_alias', 'VLAN alias', 'integer'],
  ['interface', 'Interface', 'string'],
  ['interface_type', 'Interface type', 'string'],
  ['proto', 'Protocol', 'string'],
  ['ip_address', 'IP address', 'string'],
  ['mask', 'Prefix length', 'integer'],
  ['cidr', 'IP address (CIDR)', 'string'],
  ['gateway', 'Gateway', 'string'],
  ['mac', 'MAC address', 'string'],
  ['mtu', 'MTU', 'integer'],
  ['driver', 'Driver', 'string'],
  ['bridge', 'Bridge', 'string'],
  ['dns', 'DNS servers', 'liststring'],
  ['ruleset_in', 'Ruleset in', 'string'],
  ['ruleset_out', 'Ruleset out', 'string'],
  ['udp_port', 'UDP port', 'integer'],
  ['baud_rate', 'Baud rate', 'integer'],
  ['serial_device', 'Serial device', 'string'],
  ['qinq', 'QinQ', 'boolean', false],
  ['autostart', 'Autostart', 'boolean', false],
];

// --- XML ---------------------------------------------------------------------
//
// The file is built as a tree of elements whose names come only from this
// module. Every attribute value and text is escaped when it is written.

// Characters XML 1.0 does not allow, lone surrogates among them.
const NOT_XML = /[^\t\n\r\u0020-\uD7FF\uE000-\uFFFD\u{10000}-\u{10FFFF}]/gu;

function xmlText(value) {
  return String(value)
    .replace(NOT_XML, '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/\r/g, '&#13;');
}

// Tabs and line breaks are written as references: a parser turns them into
// spaces otherwise.
function xmlAttribute(value) {
  return xmlText(value)
    .replace(/"/g, '&quot;')
    .replace(/\t/g, '&#9;')
    .replace(/\n/g, '&#10;');
}

function element(name, attributes = {}, children = []) {
  return { name, attributes, children };
}

function writeElement(item, depth, lines) {
  const indent = '  '.repeat(depth);
  const attributes = Object.entries(item.attributes)
    .filter(([, value]) => value !== undefined)
    .map(([name, value]) => ` ${name}="${xmlAttribute(value)}"`)
    .join('');
  const { name, children } = item;

  if (children.length === 0) {
    lines.push(`${indent}<${name}${attributes}/>`);
  } else if (children.every((child) => typeof child === 'string')) {
    lines.push(
      `${indent}<${name}${attributes}>${xmlText(children.join(''))}</${name}>`,
    );
  } else {
    lines.push(`${indent}<${name}${attributes}>`);
    for (const child of children) {
      writeElement(child, depth + 1, lines);
    }
    lines.push(`${indent}</${name}>`);
  }
}

// --- values ------------------------------------------------------------------

// A label or kind is an xsd:token: no leading, trailing or repeated spaces.
function token(value) {
  return String(value ?? '')
    .replace(NOT_XML, '')
    .replace(/\s+/g, ' ')
    .trim();
}

function present(value) {
  return value !== undefined && value !== null && value !== '';
}

// A whole number Gephi keeps in an integer column (32 bits), or undefined.
function integer(value) {
  const number =
    typeof value === 'string' && value.trim() ? Number(value) : value;

  return Number.isInteger(number) && Math.abs(number) < 2 ** 31
    ? number
    : undefined;
}

// A positive whole number: phenix reads 0 as not set.
function positive(value) {
  const number = integer(value);

  return number > 0 ? number : undefined;
}

function distinct(items) {
  return [...new Set(items.filter(present).map(String))];
}

// A GEXF list, "[a, b]". An item is quoted when it holds a comma, bracket,
// quote or backslash, or starts or ends with a space.
function listValue(items) {
  const written = items.map((item) => {
    const text = String(item);

    if (text && !/[,[\]'"\\]|^\s|\s$/.test(text)) {
      return text;
    }

    const quote = text.includes("'") && !text.includes('"') ? '"' : "'";

    return `${quote}${text.replace(/\\/g, '\\\\').replaceAll(quote, `\\${quote}`)}${quote}`;
  });

  return `[${written.join(', ')}]`;
}

// A list, or nothing for no items.
function listOrNothing(items) {
  const kept = distinct(items);

  return kept.length ? listValue(kept) : undefined;
}

// The same items as one text value, for Gephi Lite's keywords.
function textOrNothing(items) {
  const kept = distinct(items);

  return kept.length ? kept.join('|') : undefined;
}

function cidr(iface) {
  const mask = integer(iface?.mask);

  return present(iface?.address) && mask >= 0 && mask <= 128
    ? `${iface.address}/${mask}`
    : undefined;
}

// The column type of an annotation value: 'json' for one only JSON can hold.
function valueType(value) {
  if (typeof value === 'boolean') {
    return 'boolean';
  }

  if (typeof value === 'number' && Number.isFinite(value)) {
    if (!Number.isInteger(value)) {
      return 'double';
    }

    if (integer(value) !== undefined) {
      return 'integer';
    }

    return Number.isSafeInteger(value) ? 'long' : 'double';
  }

  if (typeof value === 'string') {
    return 'string';
  }

  const scalar = (item) =>
    ['string', 'number', 'boolean'].includes(typeof item);

  return Array.isArray(value) && value.every(scalar) ? 'liststring' : 'json';
}

const NUMBER_TYPES = ['integer', 'long', 'double'];

// The type of a column that holds values of both types: the wider number,
// else text.
function widerType(first, second) {
  if (first === second) {
    return first;
  }

  if (NUMBER_TYPES.includes(first) && NUMBER_TYPES.includes(second)) {
    return NUMBER_TYPES[
      Math.max(NUMBER_TYPES.indexOf(first), NUMBER_TYPES.indexOf(second))
    ];
  }

  return 'string';
}

function annotationValue(value, type) {
  if (value === null || value === undefined) {
    return undefined;
  }

  if (type === 'liststring') {
    return listOrNothing(value);
  }

  if (type === 'string') {
    return typeof value === 'string' ? value : JSON.stringify(value);
  }

  return typeof value === 'number' && !Number.isFinite(value)
    ? undefined
    : String(value);
}

// A key in the form that a column id accepts: its letters, digits, _ and -,
// at most 48. The column's title keeps the key unchanged. The cut is by
// characters, not UTF-16 code units. A cut through a character would leave
// half of it, which the file cannot hold, and two ids that differ only there
// would become one.
function slug(key) {
  const kept = String(key)
    .replace(/[^\p{L}\p{N}_-]+/gu, '_')
    .replace(/^_+|_+$/g, '');

  return Array.from(kept).slice(0, 48).join('') || 'key';
}

// A unique column id for each key. A key that is its own slug keeps it. The
// other keys add a number when their slug is taken.
function columnIds(prefix, keys) {
  const ids = new Map();
  const used = new Set();
  const plain = (key) => slug(key) === key;

  for (const key of [...keys.filter(plain), ...keys.filter((k) => !plain(k))]) {
    let id = `${prefix}.${slug(key)}`;

    for (let n = 2; used.has(id); n += 1) {
      id = `${prefix}.${slug(key)}_${n}`;
    }

    used.add(id);
    ids.set(key, id);
  }

  return ids;
}

// One column for each label key and each annotation key of the devices, in
// key order, typed from the values seen.
function specColumns(devices) {
  const labels = new Set();
  const annotations = new Map();

  for (const node of devices) {
    const spec = node.device?.spec || {};

    for (const key of Object.keys(spec.labels || {})) {
      labels.add(key);
    }

    for (const [key, value] of Object.entries(spec.annotations || {})) {
      if (value === null || value === undefined) {
        continue;
      }

      const type = valueType(value);
      const seen = annotations.get(key);

      annotations.set(key, seen ? widerType(seen, type) : type);
    }
  }

  const labelKeys = [...labels].sort();
  const annotationKeys = [...annotations.keys()].sort();
  const labelIds = columnIds('label', labelKeys);
  const annotationIds = columnIds('annotation', annotationKeys);

  return {
    labels: labelKeys.map((key) => ({
      key,
      id: labelIds.get(key),
      title: `Label: ${key}`,
      type: 'string',
    })),
    annotations: annotationKeys.map((key) => {
      const type = annotations.get(key);

      return {
        key,
        id: annotationIds.get(key),
        title: `Annotation: ${key}`,
        type: type === 'json' ? 'string' : type,
      };
    }),
  };
}

// --- viz ---------------------------------------------------------------------

// viz:color's attributes for a hex or rgb() color, or null for any other.
// GEXF's channels are whole numbers, so rgb()'s decimals are rounded.
function vizColor(value) {
  const channels = colorChannels(value);

  if (!channels || channels.slice(0, 3).some((channel) => channel > 255)) {
    return null;
  }

  const [r, g, b] = channels.slice(0, 3).map(Math.round);

  return { r, g, b, a: decimal(channels[3]) };
}

// A number as written in the file: at most three decimals, and no -0.
function decimal(value) {
  const rounded = Math.round(value * 1000) / 1000;

  return String(rounded === 0 ? 0 : rounded);
}

// Where the node's center is, with y pointing up.
function vizPosition(node) {
  const size = sizeOf(node);

  return {
    x: decimal((node.position?.x ?? 0) + size.width / 2),
    y: decimal(-((node.position?.y ?? 0) + size.height / 2)),
    z: '0',
  };
}

// --- the graph ---------------------------------------------------------------

// A node's or edge's <attvalues>, for the values it has, noting the columns
// they are in in `used`.
function attvalues(values, used) {
  const rows = Object.entries(values).filter(([, value]) => present(value));

  for (const [id] of rows) {
    used.add(id);
  }

  return rows.length
    ? [
        element(
          'attvalues',
          {},
          rows.map(([id, value]) =>
            element('attvalue', { for: id, value: String(value) }),
          ),
        ),
      ]
    : [];
}

// The columns of one class, those something has a value for or that have a
// default.
function attributes(cls, columns, used) {
  return element(
    'attributes',
    { class: cls, mode: 'static' },
    columns
      .filter((column) => used.has(column.id) || column.default !== undefined)
      .map((column) =>
        element(
          'attribute',
          { id: column.id, title: column.title, type: column.type },
          column.default === undefined
            ? []
            : [element('default', {}, [String(column.default)])],
        ),
      ),
  );
}

function staticColumn([id, title, type, fallback]) {
  return { id, title, type, default: fallback };
}

function deviceLook(device) {
  const spec = device?.spec;
  const look =
    spec?.external === true
      ? DEVICE_LOOKS.external
      : DEVICE_LOOKS[spec?.type] || DEVICE_LOOKS.device;
  const chosen = nodeColors(device);

  return {
    color: vizColor(chosen.fill || chosen.outline || look.color),
    shape: look.shape,
  };
}

// The scenario apps of each host, by hostname, as {apps, disabled}, from the
// content of scenarios (v2 Scenario specs), in their order. An app that two
// scenarios run on a host is named once. null when there is no content.
function appsByHost(contents) {
  const known = (Array.isArray(contents) ? contents : []).filter(
    (content) => content && typeof content === 'object',
  );

  if (known.length === 0) {
    return null;
  }

  const hosts = new Map();

  for (const content of known) {
    for (const app of scenarioApps(content)) {
      for (const hostname of app.hosts) {
        const host = hosts.get(hostname) || { apps: [], disabled: [] };
        const list = app.disabled ? host.disabled : host.apps;

        if (!list.includes(app.name)) {
          list.push(app.name);
        }

        hosts.set(hostname, host);
      }
    }
  }

  return hosts;
}

// A device's values. A connected interface's VLAN is its network's name, as
// publishing writes it (`networkOf`). Its scenario apps are known only with
// `hostApps` (see appsByHost).
function deviceValues(node, columns, networkOf, hostApps) {
  const spec = node.device?.spec || {};
  const general = spec.general || {};
  const hardware = spec.hardware || {};
  const network = spec.network || {};
  const interfaces = specInterfaces(node).filter(Boolean);
  const list = (value) => (Array.isArray(value) ? value.filter(Boolean) : []);
  const routes = list(network.routes);
  const injections = list(spec.injections);
  const labels = Object.entries(spec.labels || {}).map(
    ([key, value]) => `${key}=${value}`,
  );
  const vlans = interfaces.map(
    (iface) => networkOf(node, iface.name) ?? iface.vlan,
  );
  const names = interfaces.map((iface) => iface.name);
  const apps = hostApps?.get(node.device?.hostname) || {
    apps: [],
    disabled: [],
  };
  const values = {
    hostname: node.device?.hostname,
    description: general.description,
    device_type: spec.type,
    vm_type: general.vm_type,
    os_type: hardware.os_type,
    cpu: hardware.cpu,
    vcpus: positive(hardware.vcpus),
    memory_mb: positive(hardware.memory),
    disk_images: listOrNothing(
      list(hardware.drives).map((drive) => drive.image),
    ),
    snapshot: typeof general.snapshot === 'boolean' ? general.snapshot : null,
    do_not_boot:
      typeof general.do_not_boot === 'boolean' ? general.do_not_boot : null,
    external: spec.external === true ? true : null,
    icon: node.device?.iconKey,
    included_from: node.device?.includedFrom,
    interfaces: listOrNothing(names),
    interfaces_text: textOrNothing(names),
    interface_count: interfaces.length,
    vlans: listOrNothing(vlans),
    vlans_text: textOrNothing(vlans),
    ip_addresses: listOrNothing(interfaces.map((iface) => iface.address)),
    cidrs: listOrNothing(interfaces.map(cidr)),
    mac_addresses: listOrNothing(interfaces.map((iface) => iface.mac)),
    gateways: listOrNothing(interfaces.map((iface) => iface.gateway)),
    routes: listOrNothing(
      routes.map((route) =>
        [
          route.destination,
          present(route.next) && `via ${route.next}`,
          present(route.cost) && `cost ${route.cost}`,
        ]
          .filter(Boolean)
          .join(' '),
      ),
    ),
    route_count: routes.length,
    rulesets: listOrNothing(list(network.rulesets).map((set) => set.name)),
    injections: listOrNothing(injections.map((injection) => injection.dst)),
    injection_count: injections.length,
    apps: listOrNothing(apps.apps),
    apps_text: textOrNothing(apps.apps),
    app_count: hostApps ? distinct(apps.apps).length : undefined,
    disabled_apps: listOrNothing(apps.disabled),
    labels: listOrNothing(labels),
    labels_text: textOrNothing(labels),
  };

  for (const column of columns.labels) {
    values[column.id] = spec.labels?.[column.key];
  }

  for (const column of columns.annotations) {
    values[column.id] = annotationValue(
      spec.annotations?.[column.key],
      column.type,
    );
  }

  return values;
}

// The edge's own values: those of the interface it connects.
function edgeValues(device, iface, handle, network) {
  const mask = integer(iface?.mask);

  return {
    device: device.device?.hostname,
    network: network?.name,
    vlan_alias: integer(network?.alias),
    interface: iface?.name ?? handle?.name,
    interface_type: iface?.type,
    proto: iface?.proto,
    ip_address: iface?.address,
    mask: present(iface?.address) ? mask : undefined,
    cidr: cidr(iface),
    gateway: iface?.gateway,
    mac: iface?.mac,
    mtu: positive(iface?.mtu),
    driver: iface?.driver,
    bridge: iface?.bridge,
    dns: listOrNothing(Array.isArray(iface?.dns) ? iface.dns : []),
    ruleset_in: iface?.ruleset_in,
    ruleset_out: iface?.ruleset_out,
    udp_port: positive(iface?.udp_port),
    baud_rate: positive(iface?.baud_rate),
    serial_device: iface?.device,
    qinq: iface?.qinq === true ? true : null,
    autostart: iface?.autostart === true ? true : null,
  };
}

// The groups a node is in, outermost first.
function groupPath(node, byId) {
  const path = [];
  const seen = new Set([node.id]);

  for (
    let parent = byId.get(node.parentId);
    parent && !seen.has(parent.id);
    parent = byId.get(parent.parentId)
  ) {
    seen.add(parent.id);

    if (parent.kind === 'group') {
      path.unshift(nodeLabel(parent));
    }
  }

  return path;
}

// A connection as a device, one of its interfaces and a switch, whichever end
// is which. null for a connection that is not of this form.
function connectionEnds(edge, byId) {
  const source = byId.get(edge.sourceNodeId);
  const target = byId.get(edge.targetNodeId);

  if (source?.kind === 'device' && target?.kind === 'switch') {
    return { device: source, handleId: edge.sourceHandleId, sw: target };
  }

  if (source?.kind === 'switch' && target?.kind === 'device') {
    return { device: target, handleId: edge.targetHandleId, sw: source };
  }

  return null;
}

// Each network's node, by network id: the network's first switch, with the
// color and dash pattern its connections are drawn in.
function networkHubs(doc) {
  const networks = new Map(
    (doc?.networks || []).map((network) => [network.id, network]),
  );
  const hubs = new Map();

  for (const node of doc?.nodes || []) {
    const networkId = node.kind === 'switch' ? node.switch?.networkId : '';

    if (networkId && !hubs.has(networkId)) {
      const network = networks.get(networkId);
      const style = networkStyle(doc, networkId);

      hubs.set(networkId, {
        node,
        network,
        pattern: style.pattern,
        color:
          vizColor(network?.color) ||
          vizColor(DEFAULT_NETWORK_COLORS[style.token]),
        attached: 0,
      });
    }
  }

  return hubs;
}

// The connections between a device and a network, each with the device's
// interface and a unique id.
function connectionsOf(doc, byId, hubs) {
  const connections = [];
  const ids = new Set();

  for (const edge of doc?.edges || []) {
    const ends = connectionEnds(edge, byId);
    const hub = ends && hubs.get(ends.sw.switch?.networkId);

    if (!hub) {
      continue;
    }

    let id = String(edge.id);

    for (let n = 2; ids.has(id); n += 1) {
      id = `${edge.id} (${n})`;
    }

    ids.add(id);
    hub.attached += 1;
    connections.push({
      id,
      edge,
      hub,
      device: ends.device,
      handle: deviceHandles(ends.device).find(
        (handle) => handle.id === ends.handleId,
      ),
      iface: specInterfaceFor(ends.device, ends.handleId),
      kind: undefined,
    });
  }

  assignKinds(connections);

  return connections;
}

// The edges of a device with more than one interface on one network need a
// kind that is unique between them: the interface's name, or its position
// when it has no name or shares the name.
function assignKinds(connections) {
  const pairs = new Map();

  for (const connection of connections) {
    const key = `${connection.device.id}\n${connection.hub.node.id}`;

    if (!pairs.has(key)) {
      pairs.set(key, []);
    }

    pairs.get(key).push(connection);
  }

  for (const pair of pairs.values()) {
    const used = new Set();

    for (const connection of pair.length > 1 ? pair : []) {
      let kind = token(connection.handle?.name);

      if (!kind || used.has(kind)) {
        kind = `#${(connection.handle?.index ?? 0) + 1}`;
      }

      for (let n = 2, base = kind; used.has(kind); n += 1) {
        kind = `${base} (${n})`;
      }

      used.add(kind);
      connection.kind = kind;
    }
  }
}

// The <node> of a device or network. null for any other node, and for the
// other switches of a network.
function nodeElement(node, context) {
  const { byId, hubs, columns, networkOf, hostApps, used } = context;
  let label;
  let values;
  let look;

  if (node.kind === 'device') {
    label = node.device?.hostname || nodeLabel(node);
    values = {
      kind: 'device',
      ...deviceValues(node, columns, networkOf, hostApps),
    };
    look = deviceLook(node.device);
  } else if (node.kind === 'switch') {
    const hub = hubs.get(node.switch?.networkId);

    if (hub?.node !== node) {
      return null;
    }

    const name = hub.network?.name;

    label = name || nodeLabel(node);
    values = {
      kind: 'switch',
      description: hub.network?.description,
      vlans: listOrNothing([name]),
      vlans_text: name,
      network: name,
      vlan_alias: integer(hub.network?.alias),
      attached_interfaces: hub.attached,
    };
    look = { color: hub.color, shape: 'square' };
  } else {
    return null;
  }

  const path = groupPath(node, byId);

  values.group = path[path.length - 1];
  values.groups = listOrNothing(path);

  return element(
    'node',
    { id: String(node.id), label: token(label) || undefined },
    [
      ...attvalues(values, used),
      look.color && element('viz:color', look.color),
      element('viz:position', vizPosition(node)),
      element('viz:size', { value: String(NODE_SIZE) }),
      element('viz:shape', { value: look.shape }),
    ].filter(Boolean),
  );
}

// A connection's <edge>, labelled with its own label, or its interface's
// name and address.
function edgeElement(connection, used) {
  const { edge, hub, device, handle, iface } = connection;
  const name = iface?.name ?? handle?.name;
  const label =
    token(edge.label) || token([name, cidr(iface)].filter(present).join(' '));
  const color = vizColor(edge.color) || hub.color;

  return element(
    'edge',
    {
      id: connection.id,
      source: String(device.id),
      target: String(hub.node.id),
      label: label || undefined,
      kind: connection.kind,
    },
    [
      ...attvalues(edgeValues(device, iface, handle, hub.network), used),
      color && element('viz:color', color),
      element('viz:shape', {
        value:
          EDGE_SHAPES[
            LINE_STYLES.includes(edge.lineStyle) ? edge.lineStyle : hub.pattern
          ] || 'solid',
      }),
    ].filter(Boolean),
  );
}

/**
 * When the diagram last changed, which gives the file its date. In order of
 * preference:
 *   1. the last change made in this tab
 *   2. the save that stored the content shown (the metadata's updatedAt,
 *      which a published diagram opened read only also has)
 *   3. the server's last change to the draft.
 *
 * @param {object} [times] changedAt: the tab's last change. doc: the
 *   diagram. updated: the draft record's last change.
 * @returns {string} a time, or '' when none is known
 */
export function lastModified({ changedAt, doc, updated } = {}) {
  return changedAt || doc?.metadata?.updatedAt || updated || '';
}

// YYYY-MM-DD of the day `value` falls on here, or of today when it is no
// date.
function day(value, now) {
  const date = new Date(value ?? NaN);
  const when = Number.isNaN(date.getTime()) ? now() : date;
  const pad = (number) => String(number).padStart(2, '0');

  return `${when.getFullYear()}-${pad(when.getMonth() + 1)}-${pad(when.getDate())}`;
}

function metaElement(doc, lastChange) {
  const source = doc?.source || {};
  const keywords = distinct([
    'phenix',
    'Builder',
    source.kind,
    source.name,
    source.topology,
    ...documentScenarios(doc),
  ]);
  const description = doc?.metadata?.description || doc?.metadata?.name;

  return element('meta', { lastmodifieddate: lastChange }, [
    element('creator', {}, ['phēnix Builder']),
    element('keywords', {}, [keywords.join(', ')]),
    ...(description ? [element('description', {}, [description])] : []),
  ]);
}

/**
 * The diagram as a GEXF 1.3 graph (see the top of this file). The same
 * document and options always give the same file.
 *
 * @param {object} doc
 * @param {object} [options] modified: when the diagram last changed (a date
 *   or its text, today when not given). scenarios: the content (v2 Scenario
 *   specs) of the scenarios that the diagram lists, whose apps each device
 *   lists (none when not given, because the document holds no scenario
 *   content). now: the clock, for tests.
 * @returns {{text: string, devices: number, networks: number,
 *   connections: number}} the file, and how many devices, networks and
 *   connections it holds
 */
export function toGEXF(doc, options = {}) {
  const { modified, now = () => new Date() } = options;
  const hostApps = appsByHost(options.scenarios);
  const nodes = doc?.nodes || [];
  const byId = new Map(nodes.map((node) => [node.id, node]));
  const hubs = networkHubs(doc);
  const devices = nodes.filter((node) => node.kind === 'device');
  const columns = specColumns(devices);
  const connections = connectionsOf(doc, byId, hubs);
  const connected = new Map(
    connections.map(({ device, handle, hub }) => [
      `${device.id}\n${handle?.name}`,
      hub.network?.name,
    ]),
  );
  const networkOf = (node, name) => connected.get(`${node.id}\n${name}`);
  const usedByNodes = new Set();
  const usedByEdges = new Set();
  const context = {
    byId,
    hubs,
    columns,
    networkOf,
    hostApps,
    used: usedByNodes,
  };
  const nodeElements = nodes
    .map((node) => nodeElement(node, context))
    .filter(Boolean);
  const edgeElements = connections.map((connection) =>
    edgeElement(connection, usedByEdges),
  );

  const gexf = element(
    'gexf',
    {
      xmlns: 'http://gexf.net/1.3',
      'xmlns:viz': 'http://gexf.net/1.3/viz',
      'xmlns:xsi': 'http://www.w3.org/2001/XMLSchema-instance',
      'xsi:schemaLocation': 'http://gexf.net/1.3 http://gexf.net/1.3/gexf.xsd',
      version: '1.3',
    },
    [
      metaElement(doc, day(modified, now)),
      element(
        'graph',
        { mode: 'static', defaultedgetype: 'undirected', idtype: 'string' },
        [
          attributes(
            'node',
            [
              ...NODE_COLUMNS.map(staticColumn),
              ...columns.labels,
              ...columns.annotations,
            ],
            usedByNodes,
          ),
          attributes('edge', EDGE_COLUMNS.map(staticColumn), usedByEdges),
          element(
            'nodes',
            { count: String(nodeElements.length) },
            nodeElements,
          ),
          element(
            'edges',
            { count: String(edgeElements.length) },
            edgeElements,
          ),
        ],
      ),
    ],
  );
  const lines = ['<?xml version="1.0" encoding="UTF-8"?>'];

  writeElement(gexf, 0, lines);

  return {
    text: `${lines.join('\n')}\n`,
    devices: devices.length,
    networks: hubs.size,
    connections: connections.length,
  };
}
