// JSON Forms adapter.
//
// The inspector never hard codes fields: the UI schema is generated from the
// (server or bundled) JSON Schema, so fields the server adds appear
// automatically. The adapter also owns the mapping between an element and the
// *working copy* the inspector edits, which is what makes Apply/Cancel possible
// without touching the document on every keystroke.

import {
  and,
  Generate,
  isBooleanControl,
  isDateControl,
  isDateTimeControl,
  isEnumControl,
  isIntegerControl,
  isNumberControl,
  isOneOfControl,
  isOneOfEnumControl,
  isStringControl,
  isTimeControl,
  not,
  optionIs,
  or,
  rankWith,
  schemaMatches,
  schemaTypeIs,
  scopeEndIs,
  uiTypeIs,
} from '@jsonforms/core';
import { vanillaRenderers } from '@jsonforms/vue-vanilla';
import { markRaw } from 'vue';

import InspectorArrayRenderer from '../../components/builder/inspector/InspectorArrayRenderer.vue';
import InspectorColorControl from '../../components/builder/inspector/InspectorColorControl.vue';
import InspectorComboboxControl from '../../components/builder/inspector/InspectorComboboxControl.vue';
import InspectorEnumControl from '../../components/builder/inspector/InspectorEnumControl.vue';
import InspectorIconControl from '../../components/builder/inspector/InspectorIconControl.vue';
import InspectorInputControl from '../../components/builder/inspector/InspectorInputControl.vue';
import InspectorMapRenderer from '../../components/builder/inspector/InspectorMapRenderer.vue';
import InspectorOneOfRenderer from '../../components/builder/inspector/InspectorOneOfRenderer.vue';
import InspectorSectionRenderer from '../../components/builder/inspector/InspectorSectionRenderer.vue';
import {
  isTextOrList,
  numberBranch,
} from '../../components/builder/inspector/control.js';
import { kindMeta } from '../catalog.js';
import {
  COMBINATORS,
  errorMessage,
  errorPath,
  errorRelevance,
} from '../form-validator.js';
import {
  connectionEndLabel,
  deviceHandles,
  findNetwork,
  findNode,
  includedFrom,
  LOOK_KEYS,
  lookOf,
  networkOfSwitch,
  networkRefusal,
  nextInterfaceName,
  setDocumentInfo,
  updateEdge,
  updateNetwork,
  updateNode,
} from '../model.js';
import {
  MAP_KEYWORD,
  normalizeSchemaBundle,
  schemaForKind,
  SUGGESTIONS_KEYWORD,
} from '../schema.js';

import { networkStyle } from './vueflow.js';

const MULTILINE_KEYS = ['description', 'text', 'comment'];

// The Inspector's renderers outrank their vanilla counterparts, which stay
// registered for everything else (layouts, groups, objects, dates). See
// components/builder/inspector/ for what each one fixes.
const isPlainInputControl = or(
  and(
    isStringControl,
    not(or(isDateControl, isTimeControl, isDateTimeControl)),
  ),
  isIntegerControl,
  isNumberControl,
  isBooleanControl,
);

// Components are marked raw: JSON Forms keeps its renderer list in reactive
// state, and Vue warns about (and pays for) components made reactive.
export const inspectorRenderers = Object.freeze(
  [
    ...vanillaRenderers,
    {
      renderer: InspectorInputControl,
      tester: rankWith(11, isPlainInputControl),
    },
    { renderer: InspectorEnumControl, tester: rankWith(12, isEnumControl) },
    { renderer: InspectorOneOfRenderer, tester: rankWith(13, isOneOfControl) },
    // A number or text is a number field, and one value or a list is a
    // text field, in place of a picker for their kind.
    {
      renderer: InspectorInputControl,
      tester: rankWith(
        14,
        and(
          uiTypeIs('Control'),
          schemaMatches(
            (schema) =>
              numberBranch(schema) !== undefined || isTextOrList(schema),
          ),
        ),
      ),
    },
    {
      renderer: InspectorEnumControl,
      tester: rankWith(15, isOneOfEnumControl),
    },
    // The color of a network, note, group or connection, and the outline
    // and the fill of a device or a switch: a text field with a color
    // picker.
    {
      renderer: InspectorColorControl,
      tester: rankWith(
        14,
        and(
          isStringControl,
          or(
            scopeEndIs('color'),
            scopeEndIs('outlineColor'),
            scopeEndIs('fillColor'),
          ),
        ),
      ),
    },
    // The custom icon of a device or a group: what it is, and a dialog to
    // choose one.
    {
      renderer: InspectorIconControl,
      tester: rankWith(14, and(isStringControl, scopeEndIs('icon'))),
    },
    // A text field that suggests values: a drive's image.
    {
      renderer: InspectorComboboxControl,
      tester: rankWith(
        14,
        and(
          isStringControl,
          schemaMatches((schema) => Boolean(schema?.[SUGGESTIONS_KEYWORD])),
        ),
      ),
    },
    // Below the vanilla enum-array renderer (5), which the builder schema
    // does not use.
    {
      renderer: InspectorArrayRenderer,
      tester: rankWith(4, schemaTypeIs('array')),
    },
    // Keys and values: advanced settings, labels, annotations.
    {
      renderer: InspectorMapRenderer,
      tester: rankWith(
        16,
        and(
          uiTypeIs('Control'),
          schemaMatches((schema) => Boolean(schema?.[MAP_KEYWORD])),
        ),
      ),
    },
    // The node's rarely used fields, in a section that opens and closes.
    {
      renderer: InspectorSectionRenderer,
      tester: rankWith(16, and(uiTypeIs('Group'), optionIs('section', true))),
    },
  ].map((entry) => ({ ...entry, renderer: markRaw(entry.renderer) })),
);

/**
 * JSON Forms i18n settings that phrase the Inspector's validation errors in
 * plain language, naming the field (see form-validator.js).
 *
 * @param {object} schema the form's schema, for field titles
 * @returns {{translateError: Function}}
 */
export function inspectorI18n(schema) {
  return { translateError: (error) => errorMessage(error, schema) };
}

/**
 * The errors a field shows, one per field: of the errors one value breaks
 * in one schema, only the most relevant (see errorRelevance). The errors of
 * a oneOf or anyOf, which say which alternatives failed, are kept, as are
 * those of each alternative: JSON Forms shows a field those of the
 * alternative it renders.
 *
 * @param {object[]} errors ajv errors
 * @returns {object[]} the errors kept, in their order
 */
export function relevantErrors(errors) {
  const best = new Map();

  for (const error of errors || []) {
    if (COMBINATORS.includes(error.keyword)) {
      continue;
    }

    const key = JSON.stringify([
      errorPath(error),
      String(error.schemaPath || '').replace(/\/[^/]*$/, ''),
    ]);
    const kept = best.get(key);

    if (!kept || errorRelevance(error) < errorRelevance(kept)) {
      best.set(key, error);
    }
  }

  const keep = new Set(best.values());

  return (errors || []).filter(
    (error) => COMBINATORS.includes(error.keyword) || keep.has(error),
  );
}

// The border a group is drawn with when it names none (see builder.css).
const GROUP_BORDER = 'dashed';

// What phenix itself puts in a node spec's unset fields as it runs an
// experiment (setDefaults in src/go/types/version/v1/node.go, and
// Drive.InjectPartition), keyed like SPEC_BOUNDS in schema.js. A device
// phenix does not deploy (external) gets none. They are what an unset
// field comes to, so they win over the schema's `default`, and say that
// phenix uses them.
export const PHENIX_DEFAULTS = {
  'general.vm_type': 'kvm',
  'general.snapshot': true,
  'general.do_not_boot': false,
  'hardware.cpu': 'Broadwell',
  'hardware.vcpus': 1,
  'hardware.memory': 512,
  'hardware.os_type': 'linux',
  'drives.inject_partition': 1,
};

// The entry of a table keyed like SPEC_BOUNDS for the field at a spec data
// path ("hardware.drives.0.inject_partition"), the longest key first.
function specEntry(table, path) {
  const keys = path.split('.').filter((key) => !/^\d+$/.test(key));

  for (let start = 0; start < keys.length; start += 1) {
    const entry = table[keys.slice(start).join('.')];

    if (entry !== undefined) {
      return entry;
    }
  }

  return undefined;
}

/**
 * What an Inspector field shows while it is not set: the value it comes to
 * all the same, which is not written into the document until the field is
 * changed. A connection's label is its network's name, which the canvas
 * draws for it; a line style is the pattern the canvas picks (a network's
 * by its place in the diagram, a connection's its network's); a group's
 * border is dashed and its icon the group icon; a device's spec field is
 * the value phenix gives it (see PHENIX_DEFAULTS), else its schema's
 * `default`.
 *
 * @param {object|null} target the Inspector's target (see inspectorTarget)
 * @param {string} path the field's data path
 * @param {object} [schema] the field's schema
 * @returns {{value: unknown, note: string}|undefined} note says where the
 *   value comes from; undefined for a field with no such value
 */
export function fieldDefault(target, path, schema) {
  if (target?.kind === 'edge' && path === 'label') {
    return target.network?.name
      ? { value: target.network.name, note: "The network's name" }
      : undefined;
  }

  if (path === 'lineStyle' && target?.networkStyle) {
    return target.kind === 'edge'
      ? {
          value: target.networkStyle.pattern,
          note: "Auto: its network's line style",
        }
      : {
          value: target.networkStyle.autoPattern,
          note: "Auto: chosen by the network's place in the diagram",
        };
  }

  if (target?.kind === 'group' && path === 'borderStyle') {
    return { value: GROUP_BORDER, note: 'The default border' };
  }

  if (target?.kind === 'group' && path === 'iconKey') {
    return { value: kindMeta('group').iconKey, note: 'The group icon' };
  }

  const spec = target?.kind === 'device' ? target.data?.spec : undefined;
  const fromPhenix =
    spec && spec.external !== true && path.startsWith('spec.')
      ? specEntry(PHENIX_DEFAULTS, path.slice('spec.'.length))
      : undefined;

  if (fromPhenix !== undefined) {
    return {
      value: fromPhenix,
      note: 'phenix uses this value while the field is empty',
    };
  }

  const value = schema?.default;

  return value === undefined || value === null || value === ''
    ? undefined
    : { value, note: 'The default, used while the field is empty' };
}

// Top-level controls named in `readonly` are shown but cannot be changed.
function decorate(element, readonly = []) {
  if (element.type === 'Control' && typeof element.scope === 'string') {
    const key = element.scope.split('/').pop();
    const options = { ...element.options };

    if (MULTILINE_KEYS.includes(key)) {
      options.multi = true;
    }

    if (readonly.includes(element.scope.replace(/^#\/properties\//, ''))) {
      options.readonly = true;
    }

    return Object.keys(options).length ? { ...element, options } : element;
  }

  if (Array.isArray(element.elements)) {
    return {
      ...element,
      elements: element.elements.map((child) => decorate(child, readonly)),
    };
  }

  return element;
}

// A node spec's fields in the order they are looked for, the rest after
// them in the schema's order, and those rarely set in a section of their
// own that starts closed (see InspectorSectionRenderer).
const SPEC_FIRST = ['type', 'external', 'general', 'hardware', 'network'];
const SPEC_MORE = [
  'commands',
  'delay',
  'injections',
  'advanced',
  'labels',
  'annotations',
];

// The UI schema of a device's spec, which its control renders in place of
// one generated in the schema's order.
function specLayout(spec, root) {
  const generated = Generate.uiSchema(spec, 'Group', undefined, root);
  const byKey = new Map(
    (generated.elements || []).map((element) => [
      String(element.scope || '')
        .split('/')
        .pop(),
      element,
    ]),
  );
  const first = SPEC_FIRST.filter((key) => byKey.has(key));
  const more = SPEC_MORE.filter((key) => byKey.has(key));
  const rest = [...byKey.keys()].filter(
    (key) => !first.includes(key) && !more.includes(key),
  );

  return {
    type: 'Group',
    label: spec.title || 'Node',
    elements: [
      ...[...first, ...rest].map((key) => byKey.get(key)),
      ...(more.length
        ? [
            {
              type: 'Group',
              label: 'More settings',
              options: { section: true },
              elements: more.map((key) => byKey.get(key)),
            },
          ]
        : []),
    ],
  };
}

// UI schemas by Inspector schema (the same object for the same element
// kind, see schemaForKind), then by the fields they lock: the same object
// each time, which JSON Forms does not render again.
const uiSchemas = new WeakMap();

/**
 * Generates a JSON Forms UI schema for an element kind.
 *
 * @param {object} bundle schema bundle
 * @param {string} kind device|switch|network|note|group|edge|document
 * @param {object} [context] element context: spec picks the phenix spec
 *   variant, readonly lists top-level fields that cannot be changed
 * @returns {object} UI schema, shared: do not change it
 */
export function uiSchemaForKind(bundle, kind, context = {}) {
  const schema = schemaForKind(normalizeSchemaBundle(bundle), kind, context);
  const readonly = context.readonly || [];
  const key = readonly.join(' ');
  let byLock = uiSchemas.get(schema);

  if (!byLock) {
    byLock = new Map();
    uiSchemas.set(schema, byLock);
  }

  if (!byLock.has(key)) {
    const ui = decorate(
      Generate.uiSchema(schema, undefined, undefined, schema),
      readonly,
    );
    const spec = schema.properties?.spec;

    byLock.set(
      key,
      kind === 'device' && spec
        ? {
            ...ui,
            elements: ui.elements.map((element) =>
              element.scope === '#/properties/spec'
                ? {
                    ...element,
                    options: {
                      ...element.options,
                      detail: specLayout(spec, schema),
                    },
                  }
                : element,
            ),
          }
        : ui,
    );
  }

  return byLock.get(key);
}

/**
 * What the Inspector must not change about an element because a device from
 * an included topology depends on it (see model.js): all of such a device,
 * and the name of a network one is on.
 *
 * @param {object} doc
 * @param {{type: string, id?: string}} selection
 * @returns {{all: boolean, fields: string[], note: string}} note says why,
 *   and is '' when nothing is locked
 */
export function inspectorLock(doc, selection) {
  const target = inspectorTarget(doc, selection);
  const from = target?.kind === 'device' ? includedFrom(target.target) : '';

  if (from) {
    return {
      all: true,
      fields: [],
      note:
        `Defined by included topology ${from}, so it is read only here. ` +
        `Change it in ${from} and import again, or combine the included ` +
        'nodes into a new draft to edit them here. It can still be moved.',
    };
  }

  const reason =
    target?.kind === 'switch' && target.network
      ? networkRefusal(doc, target.network.id)
      : '';

  return reason
    ? {
        all: false,
        fields: ['name'],
        note: `${reason} Its other fields can still change.`,
      }
    : { all: false, fields: [], note: '' };
}

/**
 * The item the Inspector's Add button appends to a list, for a list whose new
 * items need more than their schema's defaults, or undefined for the schema's
 * default item. A device's new interface is named the way a connection drawn
 * on the canvas names one (see nextInterfaceName), counting the interfaces of
 * the working copy as well as the device's, and is an Ethernet interface that
 * comes up with no address (proto manual) until it is given one.
 *
 * @param {object} doc
 * @param {{type: string, id?: string}} selection
 * @param {string} path the list's JSON Forms data path
 * @param {object} [data] the working copy
 * @returns {object|undefined}
 */
export function newListItem(doc, selection, path, data) {
  const target = inspectorTarget(doc, selection);

  if (target?.kind !== 'device' || path !== 'spec.network.interfaces') {
    return undefined;
  }

  const spec = data?.spec ?? target.data.spec;
  const name = nextInterfaceName({
    ...target.target,
    device: { ...target.target.device, spec },
  });

  // An external device's interfaces have no type.
  return spec?.external === true
    ? { name, proto: 'manual' }
    : { name, type: 'ethernet', proto: 'manual' };
}

function clone(value) {
  return JSON.parse(JSON.stringify(value ?? {}));
}

// What each of a device's presentation fields is called in the
// announcement of its change (see LOOK_KEYS in model.js).
const LOOK_NAMES = {
  iconKey: 'icon',
  icon: 'custom icon',
  outlineColor: 'outline color',
  fillColor: 'fill color',
};

/**
 * What a change of a device's presentation fields says, and undoes as: the
 * first of the fields that differs names it, as "Changed the fill color of
 * Device web-01 to #2f6fbf", "Removed the fill color of Device web-01",
 * "Changed the icon of Device web-01 to the default" or "Changed the custom
 * icon of Device web-01 to plc". A custom icon is said by its name, never
 * by its id.
 *
 * @param {string} title the device's title, from inspectorTarget
 * @param {object} before its look (see lookOf)
 * @param {object} after the look it is given
 * @param {(id: string) => string|undefined} [iconName] the name of a
 *   custom icon, by its id
 * @returns {string} '' when they are the same
 */
export function lookChangeLabel(title, before, after, iconName = () => '') {
  const key = LOOK_KEYS.find((name) => before[name] !== after[name]);

  if (!key) {
    return '';
  }

  if (key === 'iconKey') {
    return `Changed the icon of ${title} to ${after.iconKey || 'the default'}`;
  }

  if (!after[key]) {
    return `Removed the ${LOOK_NAMES[key]} of ${title}`;
  }

  const value =
    key === 'icon' ? iconName(after.icon) || 'an unnamed icon' : after[key];

  return `Changed the ${LOOK_NAMES[key]} of ${title} to ${value}`;
}

/**
 * Describes what the inspector is editing.
 *
 * A selected switch edits its network, and with it the two colors that are
 * the switch node's own: its outline and its fill.
 *
 * @param {object} doc
 * @param {{type: 'node'|'edge'|'document', id?: string}} selection
 * @returns {{kind: string, title: string, target: object|null, data: object}|null}
 */
export function inspectorTarget(doc, selection) {
  if (!doc || !selection) {
    return null;
  }

  if (selection.type === 'document') {
    return {
      kind: 'document',
      title: 'Diagram',
      target: null,
      data: { name: doc.name || '', description: doc.description || '' },
    };
  }

  if (selection.type === 'edge') {
    const edge = (doc.edges || []).find((entry) => entry.id === selection.id);

    if (!edge) {
      return null;
    }

    // Named by its ends and the interfaces there, as the canvas names it.
    const end = (nodeId, handleId) =>
      connectionEndLabel(findNode(doc, nodeId), handleId) || 'a node';

    return {
      kind: 'edge',
      title: `Connection from ${end(edge.sourceNodeId, edge.sourceHandleId)} to ${end(edge.targetNodeId, edge.targetHandleId)}`,
      target: edge,
      network: findNetwork(doc, edge.networkId),
      // What its Line style comes to while it has none (see fieldDefault).
      networkStyle: networkStyle(doc, edge.networkId),
      data: {
        label: edge.label || '',
        color: edge.color || '',
        lineStyle: edge.lineStyle || '',
      },
    };
  }

  const node = (doc.nodes || []).find((entry) => entry.id === selection.id);

  if (!node) {
    return null;
  }

  switch (node.kind) {
    case 'device':
      return {
        kind: 'device',
        title: `Device ${node.device.hostname}`,
        target: node,
        data: {
          hostname: node.device.hostname,
          ...lookOf(node.device),
          spec: clone(node.device.spec),
        },
        interfaces: deviceHandles(node),
      };
    case 'switch': {
      const network = networkOfSwitch(doc, node);

      return {
        kind: 'switch',
        title: `Network ${network ? network.name : ''}`.trim(),
        target: node,
        network,
        networkStyle: network ? networkStyle(doc, network.id) : undefined,
        // The network's fields, then the colors of this switch node itself.
        data: {
          name: network?.name || '',
          ...(Number.isInteger(network?.alias) ? { alias: network.alias } : {}),
          description: network?.description || '',
          color: network?.color || '',
          lineStyle: network?.lineStyle || '',
          outlineColor: node.switch?.outlineColor || '',
          fillColor: node.switch?.fillColor || '',
        },
      };
    }
    case 'note':
      return {
        kind: 'note',
        title: 'Note',
        target: node,
        data: { text: node.note?.text || '', color: node.note?.color || '' },
      };
    case 'group':
      return {
        kind: 'group',
        title: 'Group',
        target: node,
        data: {
          title: node.group?.title || '',
          description: node.group?.description || '',
          color: node.group?.color || '',
          borderStyle: node.group?.borderStyle || '',
          iconKey: node.group?.iconKey || '',
          icon: node.group?.icon || '',
        },
      };
    default:
      return null;
  }
}

/**
 * Names what the Inspector edits, for announcements: "device web-01",
 * "network MGMT", "diagram Lab", "connection from web-01 (eth0) to MGMT".
 *
 * @param {object} doc
 * @param {{type: string, id?: string}} selection
 * @returns {string}
 */
export function inspectorName(doc, selection) {
  const target = inspectorTarget(doc, selection);
  const named = (kind, name) => (name ? `${kind} ${name}` : kind);

  switch (target?.kind) {
    case 'document':
      return named('diagram', target.data.name);
    case 'edge':
      return elementName(doc, 'edges', doc.edges.indexOf(target.target));
    case 'device':
      return named('device', target.data.hostname);
    case 'switch':
      return named('network', target.data.name);
    case 'group':
      return named('group', target.data.title);
    case 'note':
      return 'note';
    default:
      return 'element';
  }
}

// A value as it compares for formDataChanged(): object keys in one order,
// and without the keys of empty and unset fields.
function comparable(value) {
  if (Array.isArray(value)) {
    return value.map(comparable);
  }

  if (value && typeof value === 'object') {
    return Object.fromEntries(
      Object.keys(value)
        .filter((key) => value[key] !== undefined && value[key] !== '')
        .sort()
        .map((key) => [key, comparable(value[key])]),
    );
  }

  return value;
}

/**
 * Whether an inspector working copy differs from the element's data, so that
 * Apply has something to change. inspectorTarget shows an unset text field
 * as '', while a field emptied in the form loses its key, so the two count
 * as the same. Key order does not count either: a field emptied and filled
 * again comes back as the last key.
 *
 * @param {object} data working copy
 * @param {object} base the element's data, from inspectorTarget
 * @returns {boolean}
 */
export function formDataChanged(data, base) {
  return (
    JSON.stringify(comparable(data ?? {})) !==
    JSON.stringify(comparable(base ?? {}))
  );
}

// Whether two values are the same edit: as formDataChanged compares them,
// an unset text field the same as an empty one.
function same(a, b) {
  return !formDataChanged({ value: a }, { value: b });
}

function isRecord(value) {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

// The value at a JSON Forms data path ("spec.hardware.memory").
function valueAt(data, path) {
  return String(path)
    .split('.')
    .reduce(
      (value, key) =>
        value !== null && typeof value === 'object' ? value[key] : undefined,
      data,
    );
}

/**
 * Whether a working copy changed a field from the element's data, for the
 * mark on the field. Lists and groups are not marked, the fields in them
 * are, and an unset field is the same as an empty one. With a key, the
 * field is a map (advanced settings, labels, annotations), and its entry
 * of that key is marked: added, removed or given another value.
 *
 * @param {object} data working copy
 * @param {object} base the element's data, from inspectorTarget
 * @param {string} path the field's data path
 * @param {string} [key] an entry's key, which may hold dots
 * @returns {boolean}
 */
export function fieldChanged(data, base, path, key) {
  const [edited, was] = [valueAt(data, path), valueAt(base, path)];

  if (key !== undefined) {
    const has = (map) => isRecord(map) && Object.hasOwn(map, key);

    return (
      has(edited) !== has(was) ||
      !same(
        has(edited) ? edited[key] : undefined,
        has(was) ? was[key] : undefined,
      )
    );
  }

  const container = (value) => value !== null && typeof value === 'object';

  return !container(edited) && !container(was) && !same(edited, was);
}

// A list whose items each have a name no other item has: interfaces,
// rulesets. Such items are told apart by name.
function isNamedList(list) {
  const names = list.map((item) => (isRecord(item) ? item.name : undefined));

  return (
    names.every((name) => typeof name === 'string' && name !== '') &&
    new Set(names).size === names.length
  );
}

// Merges a named list: the working copy's edits of each item go to the
// item of that name now, an item it renamed keeps its place, one it added
// is appended, and one it removed goes. Items added since it was taken
// stay, and items removed since stay removed. While the element's list
// still has the items it had, the working copy's order wins.
function mergeNamedLists(base, edited, current) {
  const baseNames = base.map((item) => item.name);
  const editedNames = edited.map((item) => item.name);
  const currentNames = current.map((item) => item.name);
  const now = new Map(current.map((item) => [item.name, item]));
  // The item of `base` each edited item was, by name, or at its place
  // when its name is new and that item's name is gone: a rename.
  const origin = edited.map((item, index) => {
    if (baseNames.includes(item.name)) {
      return base[baseNames.indexOf(item.name)];
    }

    const there = base[index];

    return there && !editedNames.includes(there.name) ? there : undefined;
  });
  const merge = (index) => {
    const was = origin[index];

    return was
      ? mergeFormData(was, edited[index], now.get(was.name) ?? was)
      : mergeFormData(undefined, edited[index], now.get(edited[index].name));
  };

  if (same(currentNames, baseNames)) {
    return edited.map((_, index) => merge(index));
  }

  // Each edited item is merged once, with the first item now it matches.
  const used = new Set();
  const mergeOnce = (index, item) => {
    if (index === -1 || used.has(index)) {
      return item;
    }

    used.add(index);

    return merge(index);
  };
  const kept = current.flatMap((item) => {
    if (!baseNames.includes(item.name)) {
      return [mergeOnce(editedNames.indexOf(item.name), item)];
    }

    const index = origin.findIndex((was) => was?.name === item.name);

    return index === -1 ? [] : [mergeOnce(index, item)];
  });
  const added = edited
    .map((_, index) => index)
    .filter((index) => !used.has(index) && !origin[index])
    .map((index) => merge(index));

  return [...kept, ...added];
}

/**
 * The Inspector's edits merged into the element as it is now: what the
 * working copy changed from the data it was taken from, each field on its
 * own, over the element's current data. So an edit made elsewhere while
 * the form had unapplied edits (an interface added and connected on the
 * canvas, a rename in the outline) stays when they are applied, rather
 * than the working copy replacing it. Where both changed a field, the
 * working copy's value wins. Named list items (interfaces) are matched by
 * name, and other list items by place while no list changed length.
 *
 * @param {unknown} base the element's data when the working copy was taken
 * @param {unknown} edited the working copy
 * @param {unknown} current the element's data now
 * @returns {unknown} the data to apply
 */
export function mergeFormData(base, edited, current) {
  if (same(edited, base)) {
    return current;
  }

  if (same(current, base)) {
    return edited;
  }

  if (isRecord(edited) && isRecord(current)) {
    const was = isRecord(base) ? base : {};
    const keys = [
      ...Object.keys(current),
      ...Object.keys(edited).filter((key) => !(key in current)),
    ];

    return Object.fromEntries(
      keys
        .map((key) => [key, mergeFormData(was[key], edited[key], current[key])])
        .filter(([, value]) => value !== undefined),
    );
  }

  if (Array.isArray(base) && Array.isArray(edited) && Array.isArray(current)) {
    if (isNamedList(base) && isNamedList(edited) && isNamedList(current)) {
      return mergeNamedLists(base, edited, current);
    }

    if (base.length === edited.length && base.length === current.length) {
      return edited.map((item, index) =>
        mergeFormData(base[index], item, current[index]),
      );
    }
  }

  return edited;
}

/**
 * Applies an inspector working copy back onto the document.
 *
 * @param {object} doc
 * @param {{type: string, id?: string}} selection
 * @param {object} data working copy
 * @returns {object} document
 */
export function applyFormData(doc, selection, data) {
  const target = inspectorTarget(doc, selection);

  if (!target) {
    return doc;
  }

  switch (target.kind) {
    case 'document':
      // An emptied Description has no key in the form, and clears it.
      return setDocumentInfo(doc, {
        name: data.name,
        description: data.description ?? '',
      });
    case 'edge':
      // An emptied field has no key in the form, and removes its value.
      return updateEdge(doc, target.target.id, {
        label: data.label ?? '',
        color: data.color ?? '',
        lineStyle: data.lineStyle ?? '',
      });
    case 'device':
      return updateNode(doc, target.target.id, {
        device: {
          hostname: data.hostname,
          ...lookOf(data),
          spec: clone(data.spec),
        },
      });
    case 'switch': {
      const network =
        target.network || findNetwork(doc, target.target.switch?.networkId);

      if (!network) {
        return doc;
      }

      // The network's fields, then the switch node's own colors, as one
      // document and so one Undo step.
      return updateNode(
        updateNetwork(doc, network.id, {
          name: data.name,
          alias: data.alias === undefined ? null : data.alias,
          description: data.description ?? '',
          color: data.color ?? '',
          lineStyle: data.lineStyle ?? '',
        }),
        target.target.id,
        {
          switch: {
            outlineColor: data.outlineColor ?? '',
            fillColor: data.fillColor ?? '',
          },
        },
      );
    }
    case 'note':
      return updateNode(doc, target.target.id, {
        note: { text: data.text ?? '', color: data.color ?? '' },
      });
    case 'group':
      // updateNode keeps the group's other fields, such as `collapsed`.
      return updateNode(doc, target.target.id, {
        group: {
          title: data.title ?? '',
          description: data.description ?? '',
          color: data.color ?? '',
          borderStyle: data.borderStyle ?? '',
          iconKey: data.iconKey ?? '',
          icon: data.icon ?? '',
        },
      });
    default:
      return doc;
  }
}

// The name an issue message uses for a document element: "device web-01"
// rather than the validator's nodes[3].
function elementName(doc, collection, index) {
  if (collection === 'networks') {
    const network = doc.networks?.[index];

    return network?.name ? `network ${network.name}` : `network #${index + 1}`;
  }

  if (collection === 'edges') {
    const edge = doc.edges?.[index];
    const device = (doc.nodes || []).find(
      (node) => node.id === edge?.sourceNodeId,
    );
    const network = findNetwork(doc, edge?.networkId);
    const handle = deviceHandles(device).find(
      (entry) => entry.id === edge?.sourceHandleId,
    );
    const hostname = device?.device?.hostname || 'a device';
    const from = handle?.name ? `${hostname} (${handle.name})` : hostname;

    return device && network
      ? `connection from ${from} to ${network.name}`
      : `connection #${index + 1}`;
  }

  const nodes = doc.nodes || [];
  const node = nodes[index];

  switch (node?.kind) {
    case 'device': {
      // Devices are numbered among devices; the number is shown when the
      // hostname alone does not tell them apart.
      const devices = nodes.filter((entry) => entry.kind === 'device');
      const number = `#${devices.indexOf(node) + 1}`;
      const hostname = String(node.device?.hostname || '').trim();
      const shared = devices.some(
        (entry) =>
          entry !== node &&
          String(entry.device?.hostname || '')
            .trim()
            .toLowerCase() === hostname.toLowerCase(),
      );

      if (!hostname) {
        return `device ${number}`;
      }

      return shared ? `device ${hostname} ${number}` : `device ${hostname}`;
    }
    case 'switch':
      return `switch ${networkOfSwitch(doc, node)?.name || `#${index + 1}`}`;
    case 'group':
      return node.group?.title ? `group ${node.group.title}` : 'a group';
    case 'note':
      return 'a note';
    default:
      return `node #${index + 1}`;
  }
}

/**
 * The text the Inspector shows for a document issue: validate.js reports
 * elements by index, in the server's path form, so name them instead.
 *
 * @param {object} doc
 * @param {{path: string, message: string}} issue
 * @returns {string} for example 'Device web #2: duplicate hostname "web"
 *   (also device web #1)'
 */
export function issueText(doc, issue) {
  const refer = (_, collection, index) =>
    elementName(doc, collection, Number(index));
  const message = String(issue.message || '').replace(
    /\b(nodes|networks|edges)\[(\d+)\]/g,
    refer,
  );
  const subject = /^(nodes|networks|edges)\[(\d+)\]/.exec(issue.path || '');

  if (!subject) {
    return message;
  }

  const name = refer('', subject[1], subject[2]);

  return `${name[0].toUpperCase()}${name.slice(1)}: ${message}`;
}
