// Builder document schema access.
//
// The inspector is entirely schema driven: whatever the server returns from
// GET /api/v1/schemas/builder/v1 wins. The bundle checked in beside this module
// is generated from the same Go source that serves that endpoint and is used
// only as a fallback; when the server copy cannot be fetched the editor keeps
// working but the failure is reported to the user rather than swallowed.

import bundledSchema from './schema/builder-v1.schema.json';

import { RETIRED_ICON_KEYS } from './catalog.js';
import { itemNoun, startCase } from './form-validator.js';
import { SCHEMA_URI } from './model.js';

export const BUILDER_SCHEMA_ID = SCHEMA_URI;
const PHENIX_DEF_PREFIX = 'phenix.v1.';
export const MINIMEGA_NODE_DEF = `${PHENIX_DEF_PREFIX}minimega_node`;
export const EXTERNAL_NODE_DEF = `${PHENIX_DEF_PREFIX}external_node`;

export const builderSchemaV1 = bundledSchema;

/**
 * True when a payload is a builder schema bundle this editor can drive forms
 * from.
 *
 * @param {object} payload
 * @returns {boolean}
 */
export function isSchemaBundle(payload) {
  const candidate = payload?.schema || payload;

  return Boolean(
    candidate &&
      typeof candidate === 'object' &&
      candidate.$defs &&
      typeof candidate.$defs === 'object',
  );
}

/**
 * Accepts either a bare schema or an API envelope and returns a usable bundle.
 * A payload without `$defs` is not a builder schema bundle and is ignored.
 *
 * @param {object} payload
 * @returns {object} schema bundle
 */
export function normalizeSchemaBundle(payload) {
  const candidate = payload?.schema || payload;

  if (!candidate || typeof candidate !== 'object' || !candidate.$defs) {
    return builderSchemaV1;
  }

  return candidate;
}

/**
 * Resolves a local `#/$defs/...` reference against a bundle.
 *
 * @param {object} bundle
 * @param {string} pointer
 * @returns {object|undefined}
 */
export function resolveRef(bundle, pointer) {
  if (typeof pointer !== 'string' || !pointer.startsWith('#/$defs/')) {
    return undefined;
  }

  return bundle?.$defs?.[pointer.slice('#/$defs/'.length)];
}

/**
 * Resolves a schema that may be a `$ref`, one level deep.
 *
 * @param {object} bundle
 * @param {object} schema
 * @returns {object}
 */
export function deref(bundle, schema) {
  if (!schema || typeof schema !== 'object') {
    return {};
  }

  if (schema.$ref) {
    return resolveRef(bundle, schema.$ref) || {};
  }

  if (Array.isArray(schema.allOf) && schema.allOf.length === 1) {
    return { ...deref(bundle, schema.allOf[0]), ...omit(schema, ['allOf']) };
  }

  return schema;
}

function omit(value, keys) {
  return Object.fromEntries(
    Object.entries(value).filter(([key]) => !keys.includes(key)),
  );
}

/**
 * The `$defs` name of the phenix spec variant a device spec belongs to.
 *
 * External devices are described by `external_node`, everything else by
 * `minimega_node`; picking the concrete variant (rather than handing JSON Forms
 * the `oneOf`) keeps the complete applicable phenix schema on screen.
 *
 * @param {object} spec phenix node spec
 * @returns {string} def name
 */
export function specDefName(spec) {
  if (spec?.external === true || spec?.type === 'HIL') {
    return EXTERNAL_NODE_DEF;
  }

  return MINIMEGA_NODE_DEF;
}

/**
 * Complete phenix node spec schema for a device node.
 *
 * @param {object} bundle
 * @param {object} spec
 * @returns {object} JSON Schema with `$defs` retained for `$ref` resolution
 */
export function specSchema(bundle, spec) {
  const source = normalizeSchemaBundle(bundle);
  const name = specDefName(spec);
  const definition = source.$defs?.[name] || {};

  return {
    $schema: source.$schema,
    title: 'Node',
    ...definition,
    $defs: source.$defs,
  };
}

/**
 * The icon key schema the inspector offers: retired keys are dropped unless
 * the node already uses one, so saved documents keep validating.
 *
 * @param {object | undefined} def bundled iconKey definition
 * @param {string | undefined} current the node's current icon key
 * @returns {object}
 */
export function offeredIconKeys(def, current) {
  if (!def || !Array.isArray(def.enum)) {
    return def || { type: 'string' };
  }

  return {
    ...def,
    enum: def.enum.filter(
      (key) => !RETIRED_ICON_KEYS.includes(key) || key === current,
    ),
  };
}

/**
 * Inlines every local `$ref` so each part of the schema stands on its own.
 *
 * JSON Forms compiles each `oneOf` branch (for example the static, DHCP and
 * serial interface variants) with ajv on its own, outside the root schema. A
 * branch that still points at `#/$defs/...` cannot be compiled, and JSON Forms
 * then re-evaluates it on every render; with two or more interfaces that
 * never settles and freezes the page. A self-contained schema compiles.
 * References that would recurse forever are left in place, with the `$defs`
 * they need.
 *
 * It also merges `allOf` compositions of plain object parts (the static,
 * DHCP and serial interface variants are built that way) into one object
 * schema: the JSON Forms vanilla renderers have no allOf renderer, and an
 * untyped `allOf` sends the object renderer into endless recursion.
 *
 * @param {object} schema schema with a `$defs` table
 * @returns {object} equivalent schema without resolvable local references
 */
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

// Keywords that make an allOf part conditional or alternative; such parts are
// left as they are.
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

// Inspector schemas by bundle, then by what picks one (see schemaKey). The
// Inspector asks again on every document change, and a new object each time
// had JSON Forms compile it again, and ajv, which keeps every schema it
// compiles, grow with each edit.
const kindSchemas = new WeakMap();

// What an Inspector schema depends on besides its bundle: a device's is
// its spec's variant, and a retired icon key its node still uses (see
// offeredIconKeys).
function schemaKey(kind, context) {
  if (kind !== 'device') {
    return kind;
  }

  const icon = RETIRED_ICON_KEYS.includes(context.iconKey)
    ? context.iconKey
    : '';

  return `device ${specDefName(context.spec)} ${icon}`;
}

/**
 * Inspector schema for an element.
 *
 * The returned schema always describes the *editable working copy* of the
 * element, not its wire form: identifiers, geometry and interface handles are
 * owned by the canvas and are therefore not offered as form fields.
 *
 * The same object comes back for the same bundle, kind and variant, so it
 * is compiled once. It is shared: do not change it.
 *
 * @param {object} bundle schema bundle
 * @param {'device'|'switch'|'note'|'group'|'edge'|'network'|'document'} kind
 * @param {object} [context] element being edited (used to pick a spec variant)
 * @returns {object} JSON Schema
 */
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
      readableSpec(withNodeMaps(schema.properties.spec)),
    );
  }

  return schema;
}

// Objects of free-form keys (`advanced`, labels, annotations), which the
// Inspector edits as a list of keys and values (see InspectorMapRenderer).
// The keyword says what a value is: 'text' is kept as typed, and 'value'
// reads true, false, null, a number and JSON as such, as a topology's YAML
// would (an annotation need not be text: phenix/default-apps is false).
// MAP_KEYS_KEYWORD lists keys to suggest. Validation ignores both.
export const MAP_KEYWORD = 'x-builder-map';
export const MAP_KEYS_KEYWORD = 'x-builder-keys';

// Node fields phenix reads from a topology that its schema leaves out. A
// served schema that has them wins. null is how phenix stores an unset
// one, in experiments and the topologies generated from them.
const NODE_MAPS = {
  labels: {
    type: ['object', 'null'],
    title: 'Labels',
    description:
      'Tags of the VM in minimega, each a name and a text value. Some mean something to phenix, such as ntp-server and disable-injects.',
    additionalProperties: { type: 'string' },
    [MAP_KEYWORD]: 'text',
    [MAP_KEYS_KEYWORD]: ['ntp-server', 'disable-injects'],
  },
  annotations: {
    type: ['object', 'null'],
    title: 'Annotations',
    description:
      'Hints for phenix apps, each a name and a value, such as phenix/default-apps set to false. true, false, numbers and JSON are kept as such; anything else is text.',
    [MAP_KEYWORD]: 'value',
    [MAP_KEYS_KEYWORD]: [
      'phenix/default-apps',
      'phenix/startup-autotunnel',
      'phenix/startup-via-cc',
      'vrouter/enable-ssh',
      'vrouter/vyos-password',
    ],
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

/**
 * The spec schema with each interface's VLAN optional. A VLAN is the
 * network its interface is on (see connectByVLAN in model.js), so an
 * interface not connected yet has none, and emptying one disconnects its
 * interface. The phenix schema requires one: with it, no edit of a device
 * with an unconnected interface could be applied. The diagram checks flag
 * such an interface, and publishing refuses it (see validate.js); documents
 * and the server's checks keep the schema as it is.
 *
 * @param {object} spec self-contained spec schema
 * @returns {object}
 */
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
// anywhere in the spec; a title the served schema sets itself wins.
const SPEC_TITLES = {
  advanced: 'Advanced settings',
  area_id: 'Area ID',
  area_networks: 'Area networks',
  baud_rate: 'Baud rate',
  c2: 'C2 dependencies',
  cache_mode: 'Cache mode',
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
  src: 'Source',
  udp_port: 'UDP port',
  useUUID: 'Use UUID',
  vcpus: 'VCPUs',
  vlan: 'VLAN',
  vm_type: 'VM type',
};

// Bounds for numeric spec fields, which only the Inspector's form checks:
// JSON Forms marks a value outside them invalid, Apply refuses it, and the
// number input gets them as its min and max. Documents are validated against
// the served schema as it is, so a config imported with another value still
// opens. A key is a field's name, or the names of its parent fields and its
// own joined with dots, and matches anywhere in the spec; a bound the served
// schema sets itself wins. Each is the range phenix and minimega act on.
export const SPEC_BOUNDS = {
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
  // A VyOS firewall rule number; phenix numbers rules it adds from 1.
  'rules.id': { minimum: 1, maximum: 999999 },
  // 0 matches any port.
  'source.port': { minimum: 0, maximum: 65535 },
  'destination.port': { minimum: 0, maximum: 65535 },
};

// Formats of free-text spec fields, keyed like SPEC_BOUNDS and, like those,
// checked only by the Inspector's form; go-duration and ipv4-network are the
// builder's own (see createFormValidator). A format the served schema sets
// itself wins. Each is what phenix reads there: a delay timer with Go's
// time.ParseDuration, and routes and OSPF settings for the routers it
// configures, IPv4 only, as its interface addresses are.
export const SPEC_FORMATS = {
  'delay.timer': 'go-duration',
  'routes.destination': 'ipv4-network',
  'routes.next': 'ipv4',
  'ospf.router_id': 'ipv4',
  'area_networks.network': 'ipv4-network',
};

// Free-text spec fields that suggest values as the user types, by the kind
// of values the Inspector provides (see INSPECTOR_SUGGESTIONS). The keyword
// is an annotation that validation ignores.
export const SUGGESTIONS_KEYWORD = 'x-builder-suggestions';
const SPEC_SUGGESTIONS = { 'drives.image': 'disks' };

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

function branchTitle(branch, key, index) {
  const type = [branch.type].flat().find((entry) => entry && entry !== 'null');
  const variant = branch.properties?.type?.enum;
  const proto = branch.properties?.proto?.enum;

  if (variant) {
    const title = wordList(variant);

    return (
      title[0].toUpperCase() +
      title.slice(1) +
      (proto ? `, ${wordList(proto)}` : '')
    );
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

// `trail` names the fields from the spec down to this one (see SPEC_BOUNDS);
// list items and oneOf alternatives share their field's.
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
      next[keyword] = next[keyword].map((branch, index) => {
        const titled = readable(branch, undefined, trail);

        return titled && typeof titled === 'object' && !titled.title
          ? { ...titled, title: branchTitle(titled, key, index) }
          : titled;
      });
    }
  }

  return next;
}

/**
 * The phenix node spec schema with labels a person can read: titles for
 * cryptic keys, names for oneOf alternatives, and free-form objects as maps
 * of keys and values (see MAP_KEYWORD) rather than empty groups. Apart from
 * titles it adds the bounds of SPEC_BOUNDS, the formats of SPEC_FORMATS and
 * the suggestions of SPEC_SUGGESTIONS; the spec's hostname, which Apply
 * sets, is read only.
 *
 * @param {object} spec self-contained spec schema
 * @returns {object}
 */
function readableSpec(spec) {
  const next = readable(spec);
  const general = next.properties?.general;
  const hostname = general?.properties?.hostname;

  // The device Hostname above is the one to edit; Apply copies it here, over
  // whatever this field holds. So the field is read-only, and neither
  // required nor checked: an error it cannot fix would only block Apply.
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
          description:
            'Set from the device Hostname above when changes are applied.',
        },
      },
    };
  }

  return next;
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
    case 'device':
      return {
        ...base,
        title: 'Device',
        required: ['hostname', 'spec'],
        properties: {
          hostname: {
            ...(defs.device?.properties?.hostname || { type: 'string' }),
            title: 'Hostname',
            description:
              'Unique host name; Apply also sets it as the node hostname.',
          },
          iconKey: {
            ...offeredIconKeys(defs.iconKey, context.iconKey),
            title: 'Icon',
            description:
              'Canvas icon. Presentation only, so a new one applies at once, without Apply.',
          },
          spec: {
            ...(defs[specDefName(context.spec)] || {}),
            title: 'Node',
          },
        },
      };
    case 'switch':
    case 'network':
      return {
        ...base,
        title: 'Network',
        required: ['name'],
        properties: {
          name: {
            ...(defs.network?.properties?.name || { type: 'string' }),
            title: 'Name',
            description:
              'VLAN name used by every interface attached to this switch.',
          },
          alias: {
            ...(defs.network?.properties?.alias || { type: 'integer' }),
            title: 'VLAN alias',
            description:
              'Optional integer VLAN alias published to an experiment (1-4094).',
          },
          description: {
            ...(defs.network?.properties?.description || { type: 'string' }),
            title: 'Description',
          },
          color: {
            ...(defs.network?.properties?.color || { type: 'string' }),
            title: 'Color',
            description: 'Edge color. Never the only cue for network identity.',
          },
        },
      };
    case 'note':
      return {
        ...base,
        title: 'Note',
        required: ['text'],
        properties: {
          text: {
            ...(defs.note?.properties?.text || { type: 'string' }),
            title: 'Text',
          },
          color: {
            ...(defs.note?.properties?.color || { type: 'string' }),
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
            ...(defs.group?.properties?.title || { type: 'string' }),
            title: 'Title',
          },
          color: {
            ...(defs.group?.properties?.color || { type: 'string' }),
            title: 'Color',
          },
          collapsed: {
            ...(defs.group?.properties?.collapsed || { type: 'boolean' }),
            title: 'Collapsed',
          },
        },
      };
    case 'edge':
      return {
        ...base,
        title: 'Connection',
        properties: {
          label: {
            ...(defs.edge?.properties?.label || { type: 'string' }),
            title: 'Label',
          },
          color: {
            ...(defs.edge?.properties?.color || { type: 'string' }),
            title: 'Color',
            description:
              "Line color, in place of the network's. The line keeps its network's pattern and label.",
          },
        },
      };
    case 'document':
      return {
        ...base,
        title: 'Diagram',
        properties: {
          name: {
            ...(source.properties?.name || { type: 'string' }),
            title: 'Name',
          },
          description: {
            ...(source.properties?.description || { type: 'string' }),
            title: 'Description',
          },
        },
      };
    default:
      return { ...base, title: 'Element', properties: {} };
  }
}
