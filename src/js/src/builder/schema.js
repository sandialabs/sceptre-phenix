// Builder document schema access. The Inspector is schema driven. It uses the
// server's schema (GET /api/v1/schemas/builder/v1), and the bundled copy only
// when it cannot get that. It then reports the failure to the user.

import bundledSchema from './schema/builder-v1.schema.json';

import { itemNoun, startCase, SUBNET_HOST_KEYWORD } from './form-validator.js';
import {
  BORDER_STYLES,
  ICON_SIZE_PIXELS,
  ICON_SIZES,
  LINE_STYLES,
  MAX_LINE_POINTS,
  MIN_LINE_POINTS,
  MINIMUM_SIZES,
  SHAPE_FIGURES,
} from './model.js';

const PHENIX_DEF_PREFIX = 'phenix.v1.';
export const MINIMEGA_NODE_DEF = `${PHENIX_DEF_PREFIX}minimega_node`;
export const EXTERNAL_NODE_DEF = `${PHENIX_DEF_PREFIX}external_node`;

export const builderSchemaV1 = bundledSchema;

/** Whether a payload is a builder schema bundle that can drive forms. */
export function isSchemaBundle(payload) {
  const candidate = payload?.schema || payload;

  return Boolean(
    candidate &&
      typeof candidate === 'object' &&
      candidate.$defs &&
      typeof candidate.$defs === 'object',
  );
}

// A usable bundle from a bare schema or an API envelope. A payload without
// `$defs` is not a bundle and is ignored.
export function normalizeSchemaBundle(payload) {
  const candidate = payload?.schema || payload;

  if (!candidate || typeof candidate !== 'object' || !candidate.$defs) {
    return builderSchemaV1;
  }

  return candidate;
}

function omit(value, keys) {
  return Object.fromEntries(
    Object.entries(value).filter(([key]) => !keys.includes(key)),
  );
}

// The `$defs` name of a device spec's variant: `external_node` or
// `minimega_node`. JSON Forms gets the variant, not the `oneOf`, so all of its
// fields show.
export function specDefName(spec) {
  if (spec?.external === true || spec?.type === 'HIL') {
    return EXTERNAL_NODE_DEF;
  }

  return MINIMEGA_NODE_DEF;
}

// Inlines every local `$ref` and merges plain-object `allOf` parts. JSON Forms
// compiles each `oneOf` branch alone, and a branch with a `#/$defs` ref or an
// untyped `allOf` renders without end and freezes the page. References that
// would recurse forever stay, with the `$defs` they need.
function selfContained(schema) {
  const defs = schema?.$defs || {};
  let unresolved = false;

  const inline = (value, trail) => {
    if (Array.isArray(value)) {
      return value.map((item) => inline(item, trail));
    }

    if (!value || typeof value !== 'object') {
      return value;
    }

    const ref = typeof value.$ref === 'string' ? value.$ref : '';
    const name = ref.startsWith('#/$defs/') ? ref.slice('#/$defs/'.length) : '';

    if (name && defs[name] && !trail.includes(name)) {
      const { $ref: _, ...siblings } = value;

      return inline({ ...defs[name], ...siblings }, [...trail, name]);
    }

    if (name) {
      unresolved = true;
    }

    const copy = {};
    for (const [key, child] of Object.entries(value)) {
      if (key !== '$defs') {
        copy[key] = inline(child, trail);
      }
    }

    return copy;
  };

  const result = flattenAllOf(inline(schema, []));

  if (unresolved) {
    result.$defs = defs;
  }

  return result;
}

// Keywords that make an allOf part conditional or alternative. Parts with
// these keywords stay as they are.
const NON_MERGEABLE = ['if', 'then', 'else', 'not', 'oneOf', 'anyOf', '$ref'];

function flattenAllOf(value) {
  if (Array.isArray(value)) {
    return value.map(flattenAllOf);
  }

  if (!value || typeof value !== 'object') {
    return value;
  }

  const node = Object.fromEntries(
    Object.entries(value).map(([key, child]) => [key, flattenAllOf(child)]),
  );

  if (!Array.isArray(node.allOf)) {
    return node;
  }

  const { allOf, ...rest } = node;
  const merged = mergeParts([rest, ...allOf]);

  return merged ? flattenAllOf(merged) : node;
}

// Merges object schemas the way allOf combines them, or returns null when a
// part cannot be merged without changing what validates.
function mergeParts(parts) {
  const merged = {};

  for (const part of parts) {
    if (!part || typeof part !== 'object' || Array.isArray(part)) {
      return null;
    }

    for (const [key, value] of Object.entries(part)) {
      if (NON_MERGEABLE.includes(key)) {
        return null;
      }

      if (key === 'properties') {
        merged.properties = { ...(merged.properties || {}) };
        for (const [name, property] of Object.entries(value)) {
          merged.properties[name] =
            name in merged.properties
              ? { allOf: [merged.properties[name], property] }
              : property;
        }
      } else if (key === 'required') {
        merged.required = [...new Set([...(merged.required || []), ...value])];
      } else if (!(key in merged)) {
        merged[key] = value;
      } else if (JSON.stringify(merged[key]) !== JSON.stringify(value)) {
        return null;
      }
    }
  }

  return merged;
}

// Inspector schemas by bundle, then by the key that selects one (see
// schemaKey). The Inspector asks for a schema on every document change. A
// new object each time made JSON Forms compile it again. It also made ajv,
// which keeps every schema it compiles, grow with each edit.
const kindSchemas = new WeakMap();

// What an Inspector schema depends on besides its bundle: the variant of a
// device's spec, and whether the device is a template's (see DEVICE_HELP).
function schemaKey(kind, context) {
  if (kind !== 'device') {
    return kind;
  }

  return `device ${specDefName(context.spec)}${context.template ? ' template' : ''}`;
}

// The Inspector schema for an element's editable working copy, without the
// ids, geometry and handles the canvas owns. It is cached by bundle, kind and
// variant and shared, so do not change it.
export function schemaForKind(bundle, kind, context = {}) {
  const source = normalizeSchemaBundle(bundle);
  const key = schemaKey(kind, context);
  let schemas = kindSchemas.get(source);

  if (!schemas) {
    schemas = new Map();
    kindSchemas.set(source, schemas);
  }

  if (!schemas.has(key)) {
    schemas.set(key, buildSchema(source, kind, context));
  }

  return schemas.get(key);
}

function buildSchema(bundle, kind, context) {
  const schema = selfContained(kindSchema(bundle, kind, context));

  if (kind === 'device' && schema.properties?.spec) {
    schema.properties.spec = optionalVLANs(
      readableSpec(
        withNodeMaps(schema.properties.spec),
        deviceHelp(context).specHostname,
      ),
    );
  }

  return schema;
}

// Free-form objects (`advanced`, labels, annotations) that the Inspector edits
// as keys and values. MAP_KEYWORD is 'text' (as typed) or 'value' (true, false,
// null, numbers and JSON, as YAML reads them). MAP_KEYS_KEYWORD suggests keys,
// and MAP_VALUES_KEYWORD the values of a key. Validation ignores all three.
export const MAP_KEYWORD = 'x-builder-map';
export const MAP_KEYS_KEYWORD = 'x-builder-keys';
export const MAP_VALUES_KEYWORD = 'x-builder-values';

// The word that a list of choices uses for the choice of no value, in place
// of "Default" and "Not set" (see InspectorEnumControl). For example, a
// line style left to the canvas is "Auto". Validation ignores it.
export const UNSET_KEYWORD = 'x-builder-unset';

// Node fields that phenix reads from a topology but that its schema does
// not include. A served schema that has them wins. phenix stores an unset
// field as null, in experiments and the topologies generated from them.
const NODE_MAPS = {
  labels: {
    type: ['object', 'null'],
    title: 'Labels',
    description:
      'Tags of the VM in minimega, each a name and a text value. Some have a meaning in phenix, such as ntp-server and disable-injects.',
    additionalProperties: { type: 'string' },
    [MAP_KEYWORD]: 'text',
    [MAP_KEYS_KEYWORD]: ['ntp-server', 'disable-injects'],
    [MAP_VALUES_KEYWORD]: { 'ntp-server': 'interfaces' },
  },
  annotations: {
    type: ['object', 'null'],
    title: 'Annotations',
    description:
      'Hints for phenix apps, each a name and a value, such as phenix/default-apps set to false. The Builder keeps true, false, numbers and JSON as they are. Any other value is text.',
    [MAP_KEYWORD]: 'value',
    [MAP_KEYS_KEYWORD]: [
      'phenix/default-apps',
      'phenix/startup-autotunnel',
      'phenix/startup-via-cc',
      'vrouter/enable-ssh',
      'vrouter/vyos-password',
    ],
    [MAP_VALUES_KEYWORD]: { 'vrouter/enable-ssh': 'interfaces' },
  },
};

// minimega `vm config` settings, which a node's advanced settings are.
const ADVANCED_KEYS = [
  'qemu-append',
  'cpu',
  'machine',
  'cores',
  'sockets',
  'threads',
  'vga',
  'serial-ports',
  'virtio-ports',
  'usb-use-xhci',
  'tpm-socket',
];

function withNodeMaps(spec) {
  if (!spec?.properties) {
    return spec;
  }

  return {
    ...spec,
    properties: { ...NODE_MAPS, ...spec.properties },
  };
}

// The spec schema with each interface's VLAN optional. phenix requires one,
// but an unconnected interface has none, so its device could not be applied.
// The diagram checks and Publish still flag it. Documents keep the full schema.
function optionalVLANs(spec) {
  const network = spec?.properties?.network;
  const interfaces = network?.properties?.interfaces;

  if (!interfaces?.items) {
    return spec;
  }

  const relax = (item) => {
    if (!item || typeof item !== 'object' || Array.isArray(item)) {
      return item;
    }

    const next = { ...item };

    for (const keyword of ['allOf', 'oneOf', 'anyOf']) {
      if (Array.isArray(next[keyword])) {
        next[keyword] = next[keyword].map(relax);
      }
    }

    if (next.properties?.vlan) {
      next.properties = {
        ...next.properties,
        vlan: omit(next.properties.vlan, ['minLength']),
      };
    }

    if (Array.isArray(next.required)) {
      next.required = next.required.filter((key) => key !== 'vlan');
    }

    return next;
  };

  return {
    ...spec,
    properties: {
      ...spec.properties,
      network: {
        ...network,
        properties: {
          ...network.properties,
          interfaces: { ...interfaces, items: relax(interfaces.items) },
        },
      },
    },
  };
}

// Labels for phenix spec fields whose key makes a poor label: JSON Forms
// start-cases untitled keys into "Vm Type", "C 2" or "Qinq". A key matches
// anywhere in the spec. A title that the served schema sets itself wins.
const SPEC_TITLES = {
  advanced: 'Advanced settings',
  area_id: 'Area ID',
  area_networks: 'Area networks',
  baud_rate: 'Baud rate',
  c2: 'C2 dependencies',
  cache_mode: 'Cache mode',
  // The commands phenix gives miniccc to run in the VM. miniccc is the
  // tool's name, which stays lowercase (see capitalize in text.js).
  commands: 'miniccc commands',
  cpu: 'CPU',
  dead_interval: 'Dead interval',
  dns: 'DNS',
  do_not_boot: 'Do not boot',
  // An injection's file on the host and in the image.
  dst: 'Destination',
  hello_interval: 'Hello interval',
  id: 'ID',
  inject_partition: 'Inject partition',
  mac: 'MAC address',
  mtu: 'MTU',
  // A route's next hop.
  next: 'Next hop',
  os_type: 'OS type',
  ospf: 'OSPF',
  proto: 'Protocol',
  qinq: 'QinQ',
  retransmission_interval: 'Retransmission interval',
  router_id: 'Router ID',
  ruleset_in: 'Inbound ruleset',
  ruleset_out: 'Outbound ruleset',
  rulesets: 'Firewall rulesets',
  src: 'Source',
  udp_port: 'UDP port',
  useUUID: 'Use UUID',
  vcpus: 'VCPUs',
  vlan: 'VLAN',
  vm_type: 'VM type',
};

// Bounds of numeric spec fields that only the Inspector's form checks. A key is
// a field name or a dotted path, and it matches anywhere in the spec. Documents
// keep the served schema, so an imported config outside a bound still opens. A
// bound that the served schema sets wins. Each is the range phenix acts on.
const SPEC_BOUNDS = {
  // Vyatta and VyOS routers apply 68 to 16000. 0 is how phenix stores no
  // MTU, in experiments and the topologies generated from them.
  mtu: { minimum: 0, maximum: 16000 },
  // minimega reads both as unsigned numbers and phenix replaces 0 with its
  // default. Their ceiling is the host's and the QEMU machine type's.
  vcpus: { minimum: 1 },
  memory: { minimum: 1 },
  // 0 skips disk injection.
  inject_partition: { minimum: 0 },
  // The administrative distance of a Vyatta or VyOS static route.
  cost: { minimum: 1, maximum: 255 },
  area_id: { minimum: 0, maximum: 4294967295 },
  dead_interval: { minimum: 1, maximum: 65535 },
  hello_interval: { minimum: 1, maximum: 65535 },
  retransmission_interval: { minimum: 1, maximum: 65535 },
  // A VyOS firewall rule number. phenix numbers the rules it adds from 1.
  'rules.id': { minimum: 1, maximum: 999999 },
  // 0 matches any port.
  'source.port': { minimum: 0, maximum: 65535 },
  'destination.port': { minimum: 0, maximum: 65535 },
};

// Formats of free-text spec fields, keyed and checked like SPEC_BOUNDS.
// go-duration and ipv4-network are Builder formats (createFormValidator). They
// are what phenix reads there, IPv4 only like its interface addresses.
const SPEC_FORMATS = {
  'delay.timer': 'go-duration',
  'routes.destination': 'ipv4-network',
  'routes.next': 'ipv4',
  'ospf.router_id': 'ipv4',
  'area_networks.network': 'ipv4-network',
};

// The fields of an interface in the order users fill them in. A static
// interface starts with Address, so a new interface starts there. An external
// device's interface starts with Protocol. Other kinds keep the schema's order.
const INTERFACE_FIELDS = {
  static: ['address', 'mask', 'gateway', 'proto', 'name', 'vlan', 'mac'],
  external: ['proto', 'address', 'mask', 'gateway', 'name', 'vlan'],
};

// The order of the fields of one kind of interface (see INTERFACE_FIELDS),
// or undefined for a kind that keeps the schema's order.
function interfaceFieldOrder(properties) {
  if (!properties.address) {
    return undefined;
  }

  if (!properties.type) {
    return INTERFACE_FIELDS.external;
  }

  const type = properties.type.enum;

  return type?.length === 1 &&
    type[0] === 'ethernet' &&
    properties.proto?.enum?.includes('static')
    ? INTERFACE_FIELDS.static
    : undefined;
}

// The field order of a spec object or list item, keyed like SPEC_BOUNDS. Named
// fields come first, then the others in schema order. An entry is a list, or a
// function of the properties that returns a list or undefined.
const SPEC_FIELD_ORDER = {
  drives: ['image', 'inject_partition', 'cache_mode', 'interface'],
  'network.interfaces': interfaceFieldOrder,
};

// The list of field names that an entry of SPEC_FIELD_ORDER gives for an
// object with `properties`, or undefined.
function fieldOrder(entry, properties) {
  const order = typeof entry === 'function' ? entry(properties) : entry;

  return Array.isArray(order) ? order : undefined;
}

// The properties with the keys of `order` first, then the others. It returns
// `properties` itself when nothing moves.
export function orderedFields(properties, order) {
  const keys = Object.keys(properties);
  const first = order.filter((key) => Object.hasOwn(properties, key));
  const next = [...first, ...keys.filter((key) => !first.includes(key))];

  return next.every((key, index) => key === keys[index])
    ? properties
    : Object.fromEntries(next.map((key) => [key, properties[key]]));
}

// Free-text spec fields that suggest values as the user types, by the kind
// of values the Inspector provides (see INSPECTOR_SUGGESTIONS). The keyword
// is an annotation that validation ignores.
export const SUGGESTIONS_KEYWORD = 'x-builder-suggestions';
const SPEC_SUGGESTIONS = {
  'drives.image': 'disks',
  'interfaces.ruleset_in': 'rulesets',
  'interfaces.ruleset_out': 'rulesets',
};

// The entry of a table keyed like SPEC_BOUNDS for the field at `trail`, the
// longest key first.
function lookup(table, trail) {
  for (let start = 0; start < trail.length; start += 1) {
    const entry = table[trail.slice(start).join('.')];

    if (entry !== undefined) {
      return entry;
    }
  }

  return undefined;
}

function annotated(schema, trail) {
  const types = [schema.type].flat();
  const bounds = lookup(SPEC_BOUNDS, trail);
  const format = lookup(SPEC_FORMATS, trail);
  const suggest = lookup(SPEC_SUGGESTIONS, trail);
  const next = { ...schema };

  if (bounds && (types.includes('integer') || types.includes('number'))) {
    for (const [keyword, limit] of Object.entries(bounds)) {
      next[keyword] ??= limit;
    }
  }

  if (format && types.includes('string')) {
    next.format ??= format;
  }

  if (suggest && types.includes('string')) {
    next[SUGGESTIONS_KEYWORD] ??= suggest;
  }

  return next;
}

// Names for the alternatives of a `oneOf`: the vanilla picker otherwise
// offers "oneOf-0" and "oneOf-1".
const BRANCH_TITLES = { memory: { integer: 'Megabytes', string: 'Text' } };
const TYPE_TITLES = {
  integer: 'Whole number',
  number: 'Number',
  string: 'Text',
  boolean: 'Yes or no',
};
const ACRONYMS = { dhcp: 'DHCP', ospf: 'OSPF' };

function wordList(values) {
  const words = values
    .filter((value) => value !== '')
    .map((value) => ACRONYMS[value] || String(value).toLowerCase());

  return words.join(' or ');
}

function branchTitle(branch, key, index, branches) {
  const type = [branch.type].flat().find((entry) => entry && entry !== 'null');
  const variant = branch.properties?.type?.enum;
  const proto = branch.properties?.proto?.enum;

  if (variant) {
    // Named by what makes it different, so the name is short enough to show
    // whole in a narrow picker with wider text spacing (WCAG 1.4.12). The
    // name is its protocols when another branch has the same type ("Static
    // or OSPF"). Otherwise the name is its type ("Serial").
    const shared =
      proto &&
      branches.some(
        (other, at) =>
          at !== index &&
          wordList(other?.properties?.type?.enum || []) === wordList(variant),
      );
    const title = wordList(shared ? proto : variant);

    return title[0].toUpperCase() + title.slice(1);
  }

  return (
    BRANCH_TITLES[key]?.[type] || TYPE_TITLES[type] || `Option ${index + 1}`
  );
}

// An object with no declared fields (`advanced`): JSON Forms would draw it
// as an empty group, so the Inspector edits its keys and values instead.
function isFreeForm(schema) {
  const types = [schema?.type].flat();

  return (
    types.includes('object') &&
    !schema.properties &&
    !schema.oneOf &&
    !schema.anyOf &&
    !schema.allOf
  );
}

// A free-form object as a map of keys and values (see MAP_KEYWORD), text
// unless it says otherwise.
function asMap(schema, key) {
  return {
    ...schema,
    title: schema.title || SPEC_TITLES[key] || startCase(key),
    [MAP_KEYWORD]: schema[MAP_KEYWORD] || 'text',
    ...(key === 'advanced' && !schema[MAP_KEYS_KEYWORD]
      ? { [MAP_KEYS_KEYWORD]: ADVANCED_KEYS }
      : {}),
  };
}

// `trail` names the fields from the spec down to this one (see
// SPEC_BOUNDS). List items and oneOf alternatives share the trail of their
// field.
function readable(schema, key, trail = []) {
  if (!schema || typeof schema !== 'object' || Array.isArray(schema)) {
    return schema;
  }

  const next = annotated(schema, trail);

  if (key && !next.title && SPEC_TITLES[key]) {
    next.title = SPEC_TITLES[key];
  }

  if (next.properties) {
    next.properties = Object.fromEntries(
      Object.entries(next.properties).map(([name, child]) => [
        name,
        isFreeForm(child)
          ? asMap(child, name)
          : readable(child, name, [...trail, name]),
      ]),
    );

    const order = fieldOrder(lookup(SPEC_FIELD_ORDER, trail), next.properties);

    if (order) {
      next.properties = orderedFields(next.properties, order);
    }
  }

  if (
    next.items &&
    typeof next.items === 'object' &&
    !Array.isArray(next.items)
  ) {
    next.items = readable(next.items, undefined, trail);

    // The picker for a list item's kind ("Interface kind").
    if (next.items.oneOf && !next.items.title && key) {
      const noun = itemNoun(next.title || startCase(key));

      next.items.title = `${noun[0].toUpperCase()}${noun.slice(1)} kind`;
    }
  }

  for (const keyword of ['oneOf', 'anyOf']) {
    if (Array.isArray(next[keyword])) {
      const branches = next[keyword].map((branch) =>
        readable(branch, undefined, trail),
      );

      next[keyword] = branches.map((titled, index) =>
        titled && typeof titled === 'object' && !titled.title
          ? { ...titled, title: branchTitle(titled, key, index, branches) }
          : titled,
      );
    }
  }

  return next;
}

// The phenix node spec schema made readable: titles, oneOf names and maps for
// free-form objects, with SPEC_BOUNDS, SPEC_FORMATS and SPEC_SUGGESTIONS. The
// spec's hostname, which Apply sets, is read-only.
function readableSpec(spec, hostnameHelp) {
  const next = readable(spec);
  const general = next.properties?.general;
  const hostname = general?.properties?.hostname;

  // The user edits the device Hostname above. Apply copies it here, over
  // the value of this field. So the field is read-only, and neither
  // required nor checked: an error that the user cannot fix here would only
  // block Apply.
  if (hostname) {
    next.properties.general = {
      ...general,
      required: (general.required || []).filter((key) => key !== 'hostname'),
      properties: {
        ...general.properties,
        hostname: {
          ...omit(hostname, ['pattern', 'minLength', 'maxLength', 'format']),
          readOnly: true,
          title: 'Node hostname',
          description: hostnameHelp,
        },
      },
    };
  }

  return next;
}

// A field that takes one of a list of values, each named by a title, or no
// value. The values are the ones the served schema lists for it, without
// its empty value (which a document may hold for none). Without a served
// list, they are the editor's own list.
function choiceField(def, values, titles) {
  const offered = Array.isArray(def?.enum)
    ? def.enum.filter((value) => value !== '')
    : values;

  return {
    type: 'string',
    oneOf: offered.map((value) => ({
      const: value,
      title: titles[value] || startCase(String(value)),
    })),
  };
}

export const LINE_STYLE_TITLES = {
  solid: 'Solid',
  dashed: 'Dashed',
  dotted: 'Dotted',
  'dash-dot': 'Dash-dot',
};
export const BORDER_STYLE_TITLES = {
  solid: 'Solid',
  dashed: 'Dashed',
  dotted: 'Dotted',
  double: 'Double',
};
export const ICON_SIZE_TITLES = {
  small: 'Small',
  medium: 'Medium',
  large: 'Large',
};

// The word the Icon size of a device, a switch or a group uses for no size
// of its own: the node then draws the diagram's (see fieldDefault in
// adapters/forms.js, which names it).
const ICON_SIZE_UNSET = 'Diagram default';

// What the Icon size of a device, a switch or a group says of itself, after
// what it sizes ("Size of the group's icon").
function iconSizeHelp(subject) {
  const { small, medium, large } = ICON_SIZE_PIXELS;

  return `${subject} on the canvas: Small (${small} pixels), Medium (${medium}) or Large (${large}). Diagram default draws the size chosen for the whole diagram.`;
}

// The Icon size of a device, a switch or a group: Diagram default, or one
// of the three sizes.
function iconSizeField(defs, description) {
  return {
    ...choiceField(defs.iconSize, ICON_SIZES, ICON_SIZE_TITLES),
    title: 'Icon size',
    description,
    [UNSET_KEYWORD]: ICON_SIZE_UNSET,
  };
}

// An Inspector field starts from a Builder definition or property of the
// bundle, without the title, description and examples that document it in
// the bundle. The Inspector gives each field its own label and help, so
// the bundle's documentation never changes what a form shows.
const DOCUMENTATION = ['title', 'description', 'examples'];

function field(def, fallback) {
  return omit(def || fallback, DOCUMENTATION);
}

// The outline or fill color of a device or a switch: the definition
// itself, not the reference the bundle holds at the property. So the
// field's own schema has the pattern that selects its control and its
// error.
function hexColorField(defs) {
  return field(defs.hexColor, { type: 'string' });
}

// The custom icon of a device or a group: the definition itself, as for a
// color, so the field's own schema says an icon name is what it takes.
function iconRefField(defs) {
  return field(defs.iconRef, { type: 'string' });
}

// What the custom icon of a device, a group or an icon drawing says of
// itself. The Icon field chooses it (see InspectorIconControl), so only an
// error names it.
const CUSTOM_ICON_HELP =
  "An image of the server's icon library, drawn in place of the icon. The node names it, so a renamed icon keeps showing.";

// What the Icon field says of the custom icon it also chooses.
const CUSTOM_ICON_CHOICE =
  "Choose Custom icon… to use an image of the server's icon library.";

// What the fields of a device's form say of themselves. A device on the
// canvas takes its hostname with Apply. A template's device is edited in
// the template editor, where nothing is applied before Save. Its hostname
// is the base of the names of the devices made from it. Its icon also
// marks it under Templates.
const DEVICE_HELP = {
  canvas: {
    hostname: 'Unique hostname. Apply also sets it as the node hostname.',
    iconKey: `Icon of the node on the canvas. ${CUSTOM_ICON_CHOICE}`,
    icon: CUSTOM_ICON_HELP,
    iconSize: iconSizeHelp('Size of the icon'),
    outlineColor: 'Border color of the node on the canvas.',
    fillColor:
      'Background color of the node on the canvas. Text and icon turn black or white to stay readable.',
    specHostname: 'Apply sets it from the device Hostname above.',
  },
  template: {
    hostname:
      'Name new devices start from. Builder adds a number when another device has the name.',
    iconKey: `Icon drawn on the canvas and under Templates. ${CUSTOM_ICON_CHOICE}`,
    icon: "An image of the server's icon library, drawn in place of the icon. Devices made from the template name it too.",
    iconSize: iconSizeHelp('Size of the icon of the devices made from it'),
    outlineColor: 'Border color of the node on the canvas.',
    fillColor:
      'Background color of the node on the canvas. Text and icon turn black or white to stay readable.',
    specHostname: 'Save sets it from the Hostname above.',
  },
};

function deviceHelp(context) {
  return context.template ? DEVICE_HELP.template : DEVICE_HELP.canvas;
}

// The Width and Height of a shape or an icon, in canvas pixels, at least
// the least size the node can be resized to (see minimumSize in model.js).
function sizeFields(kind) {
  const least = MINIMUM_SIZES[kind];

  return {
    width: {
      type: 'number',
      minimum: least.width,
      title: 'Width',
      description: `Width on the canvas in pixels, at least ${least.width}.`,
    },
    height: {
      type: 'number',
      minimum: least.height,
      title: 'Height',
      description: `Height on the canvas in pixels, at least ${least.height}.`,
    },
  };
}

function kindSchema(bundle, kind, context = {}) {
  const source = normalizeSchemaBundle(bundle);
  const defs = source.$defs || {};
  const base = {
    $schema: source.$schema,
    type: 'object',
    additionalProperties: false,
    $defs: defs,
  };

  switch (kind) {
    case 'device': {
      const help = deviceHelp(context);

      return {
        ...base,
        title: 'Device',
        required: ['hostname', 'spec'],
        properties: {
          hostname: {
            ...field(defs.device?.properties?.hostname, { type: 'string' }),
            title: 'Hostname',
            description: help.hostname,
          },
          iconKey: {
            ...field(defs.iconKey, { type: 'string' }),
            title: 'Icon',
            description: help.iconKey,
          },
          icon: {
            ...iconRefField(defs),
            title: 'Custom icon',
            description: help.icon,
          },
          iconSize: iconSizeField(defs, help.iconSize),
          outlineColor: {
            ...hexColorField(defs),
            title: 'Outline Color',
            description: help.outlineColor,
          },
          fillColor: {
            ...hexColorField(defs),
            title: 'Fill Color',
            description: help.fillColor,
          },
          spec: {
            ...(defs[specDefName(context.spec)] || {}),
            title: 'Node',
          },
        },
      };
    }
    case 'switch':
    case 'network':
      return {
        ...base,
        title: 'Network',
        required: ['name'],
        properties: {
          name: {
            ...field(defs.network?.properties?.name, { type: 'string' }),
            title: 'Name',
            description:
              'VLAN name that every interface attached to this switch uses.',
          },
          alias: {
            ...field(defs.network?.properties?.alias, { type: 'integer' }),
            title: 'VLAN alias',
            description:
              'Optional integer VLAN alias (1-4094) that Publish writes to an experiment.',
          },
          // The Builder's own: publish writes neither (see addressing.js).
          subnet: {
            type: 'string',
            format: 'ipv4-network',
            title: 'Subnet',
            description:
              'IPv4 network of this network, such as 10.1.0.0/24. A static interface that you connect to it gets the next free address. Publish does not write it.',
          },
          gateway: {
            type: 'string',
            format: 'ipv4',
            [SUBNET_HOST_KEYWORD]: 'subnet',
            title: 'Gateway',
            description:
              'IPv4 address of the router on this network, in its subnet. A router interface that gets an address takes it. Other interfaces that get an address use it as their gateway. Publish does not write it.',
          },
          description: {
            ...field(defs.network?.properties?.description, {
              type: 'string',
            }),
            title: 'Description',
          },
          color: {
            ...field(defs.network?.properties?.color, { type: 'string' }),
            title: 'Edge Color',
            description:
              "Color of this network's connection lines. Builder never uses color as the only way to identify a network.",
          },
          lineStyle: {
            ...choiceField(defs.lineStyle, LINE_STYLES, LINE_STYLE_TITLES),
            title: 'Line style',
            description:
              "Dash pattern of this network's connection lines. Auto selects one from the network's position in the diagram, so networks look different without color.",
            [UNSET_KEYWORD]: 'Auto',
          },
          outlineColor: {
            ...hexColorField(defs),
            title: 'Outline Color',
            description: 'Border color of this switch on the canvas.',
          },
          fillColor: {
            ...hexColorField(defs),
            title: 'Fill Color',
            description:
              'Background color of this switch on the canvas. Text and icon turn black or white to stay readable.',
          },
          iconSize: iconSizeField(
            defs,
            iconSizeHelp("Size of this switch's icon"),
          ),
        },
      };
    case 'note':
      return {
        ...base,
        title: 'Note',
        required: ['text'],
        properties: {
          text: {
            ...field(defs.note?.properties?.text, { type: 'string' }),
            title: 'Text',
          },
          color: {
            ...field(defs.note?.properties?.color, { type: 'string' }),
            title: 'Color',
          },
        },
      };
    case 'group':
      return {
        ...base,
        title: 'Group',
        properties: {
          title: {
            ...field(defs.group?.properties?.title, { type: 'string' }),
            title: 'Title',
          },
          description: {
            ...field(defs.group?.properties?.description, { type: 'string' }),
            title: 'Description',
            description: 'Shown under the title on the canvas.',
          },
          color: {
            ...field(defs.group?.properties?.color, { type: 'string' }),
            title: 'Color',
          },
          borderStyle: {
            ...choiceField(
              defs.borderStyle,
              BORDER_STYLES,
              BORDER_STYLE_TITLES,
            ),
            title: 'Border pattern',
            description: "Line pattern of the group's border.",
          },
          iconKey: {
            ...field(defs.iconKey, { type: 'string' }),
            title: 'Icon',
            description: `Icon beside the group's title. ${CUSTOM_ICON_CHOICE}`,
          },
          icon: {
            ...iconRefField(defs),
            title: 'Custom icon',
            description: CUSTOM_ICON_HELP,
          },
          iconSize: iconSizeField(
            defs,
            iconSizeHelp("Size of the group's icon"),
          ),
          // Not `collapsed`: the canvas does not draw a collapsed group yet.
          // A document that has it keeps it (applyFormData in forms.js).
        },
      };
    case 'shape':
      return {
        ...base,
        title: 'Shape',
        required: ['shape'],
        properties: {
          shape: {
            ...choiceField(defs.shape?.properties?.shape, SHAPE_FIGURES, {
              rectangle: 'Rectangle',
              circle: 'Circle',
            }),
            title: 'Shape',
            description:
              'The figure drawn. A circle in a box that is not square is an ellipse.',
          },
          label: {
            ...field(defs.shape?.properties?.label, { type: 'string' }),
            title: 'Label',
            description: 'Shown at the center of the shape.',
          },
          fillColor: {
            ...hexColorField(defs),
            title: 'Fill Color',
            description:
              'Color inside the shape. Without one, the shape is transparent. The label turns black or white to stay readable.',
          },
          outlineColor: {
            ...hexColorField(defs),
            title: 'Outline Color',
            description: "Color of the shape's border.",
          },
          borderStyle: {
            ...choiceField(
              defs.borderStyle,
              BORDER_STYLES,
              BORDER_STYLE_TITLES,
            ),
            title: 'Border pattern',
            description:
              "Line pattern of the shape's border. Without one, the border is solid.",
          },
          ...sizeFields('shape'),
        },
      };
    case 'icon':
      return {
        ...base,
        title: 'Icon',
        properties: {
          iconKey: {
            ...field(defs.iconKey, { type: 'string' }),
            title: 'Icon',
            description: `The icon drawn. ${CUSTOM_ICON_CHOICE}`,
          },
          icon: {
            ...iconRefField(defs),
            title: 'Custom icon',
            description: CUSTOM_ICON_HELP,
          },
          label: {
            ...field(defs.iconNode?.properties?.label, { type: 'string' }),
            title: 'Label',
            description: 'Shown below the icon.',
          },
          ...sizeFields('icon'),
        },
      };
    case 'line':
      return {
        ...base,
        title: 'Line',
        required: ['points'],
        properties: {
          label: {
            ...field(defs.line?.properties?.label, { type: 'string' }),
            title: 'Label',
            description: 'Shown at the middle of the line.',
          },
          color: {
            ...hexColorField(defs),
            title: 'Color',
            description:
              'Color of the line. Without one, the line has the text color.',
          },
          lineStyle: {
            ...choiceField(defs.lineStyle, LINE_STYLES, LINE_STYLE_TITLES),
            title: 'Line style',
            description:
              'Dash pattern of the line. Without one, the line is solid.',
          },
          startArrow: {
            type: 'boolean',
            title: 'Arrowhead at the start',
            description: 'Draws an arrowhead at the first point.',
          },
          endArrow: {
            type: 'boolean',
            title: 'Arrowhead at the end',
            description: 'Draws an arrowhead at the last point.',
          },
          points: {
            type: 'array',
            title: 'Points',
            description: `Where the line runs, from its start to its end, on the canvas: ${MIN_LINE_POINTS} to ${MAX_LINE_POINTS} points.`,
            minItems: MIN_LINE_POINTS,
            maxItems: MAX_LINE_POINTS,
            items: {
              type: 'object',
              title: 'Point',
              additionalProperties: false,
              required: ['x', 'y'],
              properties: {
                x: { type: 'number', title: 'X' },
                y: { type: 'number', title: 'Y' },
              },
            },
          },
        },
      };
    case 'edge':
      return {
        ...base,
        title: 'Connection',
        properties: {
          label: {
            ...field(defs.edge?.properties?.label, { type: 'string' }),
            title: 'Label',
          },
          color: {
            ...field(defs.edge?.properties?.color, { type: 'string' }),
            title: 'Color',
            description: "Line color, in place of the network's.",
          },
          lineStyle: {
            ...choiceField(defs.lineStyle, LINE_STYLES, LINE_STYLE_TITLES),
            title: 'Line style',
            description:
              "Dash pattern of this line, in place of its network's. Its label and the switch it joins still name the network.",
            [UNSET_KEYWORD]: 'Auto',
          },
        },
      };
    case 'document':
      return {
        ...base,
        title: 'Diagram',
        properties: {
          name: {
            ...field(defs.metadata?.properties?.name, { type: 'string' }),
            title: 'Name',
          },
          description: {
            ...field(defs.metadata?.properties?.description, {
              type: 'string',
            }),
            title: 'Description',
          },
        },
      };
    default:
      return { ...base, title: 'Element', properties: {} };
  }
}
