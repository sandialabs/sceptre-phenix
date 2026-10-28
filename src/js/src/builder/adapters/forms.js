// JSON Forms adapter. The UI schema comes from the JSON Schema, so new server
// fields show with no code change. The Inspector edits a working copy, which
// Apply writes to the document.

import { iconLabel } from '../iconLabels.js';
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
import InspectorGroupRenderer from '../../components/builder/inspector/InspectorGroupRenderer.vue';
import InspectorIconControl from '../../components/builder/inspector/InspectorIconControl.vue';
import InspectorInputControl from '../../components/builder/inspector/InspectorInputControl.vue';
import InspectorMapRenderer from '../../components/builder/inspector/InspectorMapRenderer.vue';
import InspectorOneOfRenderer from '../../components/builder/inspector/InspectorOneOfRenderer.vue';
import InspectorLayerRenderer from '../../components/builder/inspector/InspectorLayerRenderer.vue';
import InspectorSectionRenderer from '../../components/builder/inspector/InspectorSectionRenderer.vue';
import {
  isTextOrList,
  numberBranch,
} from '../../components/builder/inspector/control.js';
import { DEFAULT_INTERFACE_MASK } from '../addressing.js';
import { kindMeta, nodeIconKey } from '../catalog.js';
import { foldKey } from '../goCase.js';
import {
  COMBINATORS,
  errorMessage,
  errorPath,
  errorRelevance,
} from '../form-validator.js';
import {
  connectionEndLabel,
  deviceHandles,
  documentIconSize,
  findNetwork,
  findNode,
  includedFrom,
  kindLabel,
  LOOK_KEYS,
  lookOf,
  networkOfSwitch,
  networkRefusal,
  nextInterfaceName,
  setDocumentInfo,
  sizeOf,
  updateEdge,
  updateNetwork,
  updateNode,
} from '../model.js';
import {
  BORDER_STYLE_TITLES,
  ICON_SIZE_TITLES,
  LINE_STYLE_TITLES,
  MAP_KEYWORD,
  normalizeSchemaBundle,
  schemaForKind,
  SUGGESTIONS_KEYWORD,
} from '../schema.js';
import { deepCopy, isPlainObject } from '../values.js';

import { networkStyle } from './vueflow.js';

const MULTILINE_KEYS = ['description', 'text', 'comment'];

// The UI schema element of the layer control. A form with the option
// `layer` (see uiSchemaForKind) has it directly after the node's look
// fields (the icon fields and the colors), above the node's spec. The
// canvas Inspector shows the layer in a section of its own instead.
export const LAYER_UI_TYPE = 'NodeLayer';

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

// Components are marked raw. JSON Forms keeps its renderer list in reactive
// state, and Vue warns about components made reactive (and they cost
// performance).
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
    // The icon of a device, a group or an icon drawing: a built-in icon or
    // a custom icon, chosen from one menu (see withOneIconField).
    {
      renderer: InspectorIconControl,
      tester: rankWith(17, and(uiTypeIs('Control'), scopeEndIs('iconKey'))),
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
    // A group of fields: a heading, or a group that collapses.
    {
      renderer: InspectorGroupRenderer,
      tester: rankWith(3, uiTypeIs('Group')),
    },
    // The node's rarely used fields, in a section that opens and closes.
    {
      renderer: InspectorSectionRenderer,
      tester: rankWith(16, and(uiTypeIs('Group'), optionIs('section', true))),
    },
    // The layer, in a form that shows it after the node's look (see
    // LAYER_UI_TYPE).
    {
      renderer: InspectorLayerRenderer,
      tester: rankWith(16, uiTypeIs(LAYER_UI_TYPE)),
    },
  ].map((entry) => ({ ...entry, renderer: markRaw(entry.renderer) })),
);

/** JSON Forms i18n that names the field in plain-language errors (see form-validator.js). */
export function inspectorI18n(schema) {
  return { translateError: (error) => errorMessage(error, schema) };
}

/**
 * The errors a field shows: the most relevant error per value and schema
 * (see errorRelevance). It keeps oneOf and anyOf errors and those of each
 * alternative, as JSON Forms shows the errors of the rendered alternative.
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

// How far past a line's end the Inspector puts a new point, on a diagram
// without a grid size.
const NEW_POINT_STEP = 16;

// What phenix puts in a node spec's unset fields when it runs an experiment
// (setDefaults in src/go/types/version/v1/node.go, Drive.InjectPartition),
// keyed like SPEC_BOUNDS in schema.js. They win over the schema's default.
// An external device gets none.
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
 * The value an unset Inspector field gets all the same, with a note that
 * says where it comes from, or undefined. The document gets the value only
 * when the field changes.
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

  // A shape's border and a line are solid while they name no pattern.
  if (
    (target?.kind === 'shape' && path === 'borderStyle') ||
    (target?.kind === 'line' && path === 'lineStyle')
  ) {
    return {
      value: 'solid',
      note: path === 'borderStyle' ? 'The default border' : 'The default line',
    };
  }

  if (target?.kind === 'group' && path === 'iconKey') {
    return { value: kindMeta('group').iconKey, note: 'The group icon' };
  }

  // A device with no icon draws the default icon. An icon drawing has no
  // default: it draws a built-in icon or a custom icon.
  if (target?.kind === 'device' && path === 'iconKey') {
    return { value: nodeIconKey({ kind: 'device' }), note: 'The default icon' };
  }

  // A node without an icon size of its own draws the diagram's. The device
  // of a library's template is in no diagram, so its target names none (see
  // inspectorTarget).
  if (path === 'iconSize' && target?.diagramIconSize) {
    return { value: target.diagramIconSize, note: "The diagram's icon size" };
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

// A node spec's fields in the order users look for them. The other fields
// come after them in the schema's order. The fields that users seldom set
// go in a section of their own that starts closed (see
// InspectorSectionRenderer).
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

  // A heading, not a box around all of the node's fields (see
  // InspectorGroupRenderer).
  return {
    type: 'Group',
    label: spec.title || 'Node',
    options: { heading: true },
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

// Puts the layer control after the last look field of a generated UI
// schema, or before the spec when it has no look field.
function withLayer(ui) {
  const elements = ui.elements || [];
  const keys = elements.map((element) =>
    String(element.scope || '')
      .split('/')
      .pop(),
  );
  const look = keys.findLastIndex((key) => LOOK_KEYS.includes(key));
  const spec = keys.indexOf('spec');
  const at = look >= 0 ? look + 1 : spec >= 0 ? spec : elements.length;

  return {
    ...ui,
    elements: [
      ...elements.slice(0, at),
      { type: LAYER_UI_TYPE },
      ...elements.slice(at),
    ],
  };
}

// A node with a built-in icon and a custom icon has one field for both:
// the Icon field (InspectorIconControl) shows and chooses either. The
// Custom icon has no control of its own.
function withOneIconField(ui, schema) {
  if (!schema.properties?.iconKey || !schema.properties?.icon) {
    return ui;
  }

  return {
    ...ui,
    elements: (ui.elements || []).filter(
      (element) => element.scope !== '#/properties/icon',
    ),
  };
}

// UI schemas by Inspector schema (the same object for the same element
// kind, see schemaForKind), then by the fields they lock. Each call gives
// the same object, so JSON Forms does not render it again.
const uiSchemas = new WeakMap();

/**
 * The JSON Forms UI schema for an element kind. context: spec, readonly
 * fields and layer (see LAYER_UI_TYPE). The result is shared: do not change it.
 */
export function uiSchemaForKind(bundle, kind, context = {}) {
  const schema = schemaForKind(normalizeSchemaBundle(bundle), kind, context);
  const readonly = context.readonly || [];
  const key = `${context.layer ? 'layer:' : ''}${readonly.join(' ')}`;
  let byLock = uiSchemas.get(schema);

  if (!byLock) {
    byLock = new Map();
    uiSchemas.set(schema, byLock);
  }

  if (!byLock.has(key)) {
    const generated = withOneIconField(
      decorate(
        Generate.uiSchema(schema, undefined, undefined, schema),
        readonly,
      ),
      schema,
    );
    const ui = context.layer ? withLayer(generated) : generated;
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
 * What the Inspector must not change because a device from an included
 * topology depends on it: all of such a device, or the name of its network.
 */
export function inspectorLock(doc, selection) {
  const target = inspectorTarget(doc, selection);
  const from = target?.kind === 'device' ? includedFrom(target.target) : '';

  if (from) {
    return {
      all: true,
      fields: [],
      note:
        `Included topology ${from} defines this device. It is read only ` +
        `here. Change it in ${from}, and then import again. You can also ` +
        'combine the included nodes into a new draft to edit them here. ' +
        'You can still move it.',
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
 * The item the Inspector's Add button appends to a list, or undefined for
 * the schema's default item. A new interface is a static Ethernet interface
 * named as a canvas connection names one (see nextInterfaceName).
 */
export function newListItem(doc, selection, path, data) {
  const target = inspectorTarget(doc, selection);

  // A line's new point is its new end, a grid step on from the last one.
  if (target?.kind === 'line' && path === 'points') {
    const points = data?.points ?? target.data.points;
    const last = points[points.length - 1] || target.target.position;
    const step = doc.grid?.size || NEW_POINT_STEP;

    return { x: Number(last.x) + step, y: Number(last.y) };
  }

  if (target?.kind !== 'device' || path !== 'spec.network.interfaces') {
    return undefined;
  }

  const spec = data?.spec ?? target.data.spec;
  const name = nextInterfaceName({
    ...target.target,
    device: { ...target.target.device, spec },
  });

  // An external device's interfaces have no type. The mask is the one the
  // schema suggests, so only the address is left to fill in.
  return spec?.external === true
    ? { name, proto: 'static', mask: DEFAULT_INTERFACE_MASK }
    : {
        name,
        type: 'ethernet',
        proto: 'static',
        mask: DEFAULT_INTERFACE_MASK,
      };
}

/**
 * The item that "Insert … after" puts after item index, or undefined for a
 * list that takes none. A line gets a bend halfway to the next point, as
 * insertLinePoint does.
 */
export function insertedListItem(doc, selection, path, index, data) {
  const target = inspectorTarget(doc, selection);

  if (target?.kind !== 'line' || path !== 'points') {
    return undefined;
  }

  const points = data?.points ?? target.data.points;
  const from = points?.[index];
  const to = points?.[index + 1];

  if (!from || !to) {
    return undefined;
  }

  return {
    x: (Number(from.x) + Number(to.x)) / 2,
    y: (Number(from.y) + Number(to.y)) / 2,
  };
}

// What each of a device's presentation fields is called in the
// announcement of its change (see LOOK_KEYS in model.js).
const LOOK_NAMES = {
  icon: 'custom icon',
  outlineColor: 'outline color',
  fillColor: 'fill color',
};

/**
 * The label and Undo name of a change of a device's look, named by the
 * first field that differs, such as "Removed the fill color of Device web-01".
 * '' when nothing differs.
 */
export function lookChangeLabel(title, before, after) {
  const key = LOOK_KEYS.find((name) => before[name] !== after[name]);

  if (!key) {
    return '';
  }

  if (key === 'iconKey') {
    return `Changed the icon of ${title} to ${after.iconKey ? iconLabel(after.iconKey) : 'the default'}`;
  }

  if (key === 'iconSize') {
    return `Changed the icon size of ${title} to ${ICON_SIZE_TITLES[after.iconSize] || 'the diagram default'}`;
  }

  if (!after[key]) {
    return `Removed the ${LOOK_NAMES[key]} of ${title}`;
  }

  return `Changed the ${LOOK_NAMES[key]} of ${title} to ${after[key]}`;
}

// The fields of each kind of element that the Inspector applies at once,
// without Apply, each change as one Undo step (see BuilderInspector). They
// change how the element looks, not what it is. In the order that names a
// change of more than one of them (see instantChangeLabel).
const INSTANT_KEYS = Object.freeze({
  device: LOOK_KEYS,
  switch: ['color', 'lineStyle', 'outlineColor', 'fillColor', 'iconSize'],
  group: ['color', 'borderStyle', 'iconKey', 'icon', 'iconSize'],
  note: ['color'],
  shape: ['fillColor', 'outlineColor', 'borderStyle'],
  icon: ['iconKey', 'icon'],
  line: ['label', 'color', 'lineStyle'],
  edge: ['label', 'color', 'lineStyle'],
});

/** The data paths of a kind's fields that apply at once, without Apply. */
export function instantKeys(kind) {
  return INSTANT_KEYS[kind] || [];
}

/** The instantKeys(kind) fields of data, each as text, '' when not set. */
export function instantValues(kind, data) {
  return Object.fromEntries(
    instantKeys(kind).map((key) => [key, String(data?.[key] ?? '')]),
  );
}

// The names of the instant fields that are not in a device's look (see
// LOOK_NAMES), in the label of their change.
const INSTANT_NAMES = {
  color: 'color',
  label: 'label',
  lineStyle: 'line style',
  borderStyle: 'border pattern',
};

/**
 * The label and Undo name of a change of the instant fields, named by the
 * first field that differs, such as "Changed the line style of Line to
 * Dashed". Look fields use the names lookChangeLabel gives.
 */
export function instantChangeLabel(kind, title, before, after) {
  const key = instantKeys(kind).find((name) => before[name] !== after[name]);

  if (!key) {
    return '';
  }

  if (LOOK_KEYS.includes(key)) {
    return lookChangeLabel(
      title,
      { [key]: before[key] },
      { [key]: after[key] },
    );
  }

  const value = after[key];
  const name =
    kind === 'switch' && key === 'color' ? 'edge color' : INSTANT_NAMES[key];

  if (key === 'lineStyle' || key === 'borderStyle') {
    const titles =
      key === 'lineStyle' ? LINE_STYLE_TITLES : BORDER_STYLE_TITLES;
    // A connection and a network left to the canvas show Auto.
    const unset = kind === 'edge' || kind === 'switch' ? 'Auto' : 'the default';

    return `Changed the ${name} of ${title} to ${titles[value] || value || unset}`;
  }

  if (!value) {
    return `Removed the ${name} of ${title}`;
  }

  return key === 'label'
    ? `Changed the ${name} of ${title} to "${value}"`
    : `Changed the ${name} of ${title} to ${value}`;
}

// The data path of the Description field of each kind of node that the canvas
// draws a description for. The Inspector puts the show or hide toggle there.
const DESCRIPTION_PATHS = Object.freeze({
  device: 'spec.general.description',
  group: 'description',
});

/** The data path of a kind's Description field, or '' for a kind with none. */
export function descriptionPath(kind) {
  return DESCRIPTION_PATHS[kind] || '';
}

/** The label and Undo name of showing or hiding a node's description on the canvas. */
export function descriptionShownLabel(title, shown) {
  return `${shown ? 'Showed' : 'Hid'} the description of ${title} on the canvas`;
}

/**
 * Describes what the Inspector edits. A switch edits its network and its own
 * colors. A template from a library names no diagram icon size, because it
 * takes the size of the diagram it goes into.
 */
export function inspectorTarget(doc, selection, { template = false } = {}) {
  if (!doc || !selection) {
    return null;
  }

  const diagramIconSize =
    template && !doc.iconSize ? undefined : documentIconSize(doc);

  if (selection.type === 'document') {
    return {
      kind: 'document',
      title: 'Diagram',
      target: null,
      data: {
        name: doc.metadata?.name || '',
        description: doc.metadata?.description || '',
      },
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
      // The value its Line style gets while it has none (see fieldDefault).
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
          spec: deepCopy(node.device.spec ?? {}),
        },
        interfaces: deviceHandles(node),
        // The value its Icon size gets while it has none (see fieldDefault).
        diagramIconSize,
      };
    case 'switch': {
      const network = networkOfSwitch(doc, node);

      return {
        kind: 'switch',
        title: `Network ${network ? network.name : ''}`.trim(),
        target: node,
        network,
        networkStyle: network ? networkStyle(doc, network.id) : undefined,
        diagramIconSize,
        // The network's fields, then the colors and icon size of this switch
        // node itself.
        data: {
          name: network?.name || '',
          ...(Number.isInteger(network?.alias) ? { alias: network.alias } : {}),
          ...(network?.subnet ? { subnet: network.subnet } : {}),
          ...(network?.gateway ? { gateway: network.gateway } : {}),
          description: network?.description || '',
          color: network?.color || '',
          lineStyle: network?.lineStyle || '',
          outlineColor: node.switch?.outlineColor || '',
          fillColor: node.switch?.fillColor || '',
          iconSize: node.switch?.iconSize || '',
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
        diagramIconSize,
        data: {
          title: node.group?.title || '',
          description: node.group?.description || '',
          color: node.group?.color || '',
          borderStyle: node.group?.borderStyle || '',
          iconKey: node.group?.iconKey || '',
          icon: node.group?.icon || '',
          iconSize: node.group?.iconSize || '',
        },
      };
    case 'shape': {
      const size = sizeOf(node);

      return {
        kind: 'shape',
        title: kindLabel(node),
        target: node,
        data: {
          shape: node.shape?.shape || 'rectangle',
          label: node.shape?.label || '',
          fillColor: node.shape?.fillColor || '',
          outlineColor: node.shape?.outlineColor || '',
          borderStyle: node.shape?.borderStyle || '',
          width: size.width,
          height: size.height,
        },
      };
    }
    case 'icon': {
      const size = sizeOf(node);

      return {
        kind: 'icon',
        title: 'Icon',
        target: node,
        data: {
          iconKey: node.icon?.iconKey || '',
          icon: node.icon?.icon || '',
          label: node.icon?.label || '',
          width: size.width,
          height: size.height,
        },
      };
    }
    case 'line':
      // Its points where they are on the canvas, not relative to the line.
      return {
        kind: 'line',
        title: 'Line',
        target: node,
        data: {
          label: node.line?.label || '',
          color: node.line?.color || '',
          lineStyle: node.line?.lineStyle || '',
          startArrow: node.line?.startArrow === true,
          endArrow: node.line?.endArrow === true,
          points: (node.line?.points || []).map((point) => ({
            x: node.position.x + point.x,
            y: node.position.y + point.y,
          })),
        },
      };
    default:
      return null;
  }
}

/** Names what the Inspector edits, for announcements: "device web-01", "diagram Lab". */
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
    case 'shape':
    case 'icon':
    case 'line':
      return named(target.title.toLowerCase(), target.data.label);
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
 * Whether a working copy differs from the element's data. An unset field
 * and '' count as the same, and so does a different key order.
 */
export function formDataChanged(data, base) {
  return (
    JSON.stringify(comparable(data ?? {})) !==
    JSON.stringify(comparable(base ?? {}))
  );
}

// Whether two values are the same edit. As in formDataChanged, an unset
// text field is the same as an empty one.
function same(a, b) {
  return !formDataChanged({ value: a }, { value: b });
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
 * Whether a working copy changed a field, for the mark on it. Unset and
 * empty count as the same. With key, the field is a map and only its entry
 * of that key is checked.
 */
export function fieldChanged(data, base, path, key) {
  const [edited, was] = [valueAt(data, path), valueAt(base, path)];

  if (key !== undefined) {
    const has = (map) => isPlainObject(map) && Object.hasOwn(map, key);

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

// A list whose items each have a name that no other item has: interfaces,
// rulesets. The name identifies each such item.
function isNamedList(list) {
  const names = list.map((item) =>
    isPlainObject(item) ? item.name : undefined,
  );

  return (
    names.every((name) => typeof name === 'string' && name !== '') &&
    new Set(names).size === names.length
  );
}

// Merges a named list: edits go to the current item of the same name, a
// renamed item keeps its place, and added and removed items apply. Items
// added or removed elsewhere since the copy stay so. The copy's order wins
// while the current list still has its old items.
function mergeNamedLists(base, edited, current) {
  const baseNames = base.map((item) => item.name);
  const editedNames = edited.map((item) => item.name);
  const currentNames = current.map((item) => item.name);
  const now = new Map(current.map((item) => [item.name, item]));
  // The item of `base` that each edited item was, by name. When its name
  // is new and the name of the item at its place is gone, it is that item
  // (a rename).
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

  // Each edited item is merged once, with the first current item that it
  // matches.
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
 * The working copy's changes merged field by field over the element's
 * current data, so an edit made elsewhere meanwhile stays. The working copy
 * wins a conflict. Named items match by name, others by place.
 */
export function mergeFormData(base, edited, current) {
  if (same(edited, base)) {
    return current;
  }

  if (same(current, base)) {
    return edited;
  }

  if (isPlainObject(edited) && isPlainObject(current)) {
    const was = isPlainObject(base) ? base : {};
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
 * Whether the Inspector keeps its working copy when the document changes,
 * instead of a reload: while the form holds input that a reload would lose.
 */
export function keepsWorkingCopy({
  dirty = false,
  typing = false,
  held = false,
  holding = false,
  unsent = false,
  problems = 0,
} = {}) {
  return Boolean(dirty || typing || held || holding || unsent || problems > 0);
}

// A copy of a JSON value, which shares nothing with it.
function copyOf(value) {
  return value === undefined ? undefined : deepCopy(value);
}

// `next`, with each part that is the same as in `kept` taken from `kept`,
// and the other parts copied. A form's field whose data is the same object
// as before keeps what it holds, and does not show its data again.
function keptWhereSame(kept, next) {
  if (same(kept, next)) {
    return kept;
  }

  if (isPlainObject(kept) && isPlainObject(next)) {
    return Object.fromEntries(
      Object.entries(next).map(([key, value]) => [
        key,
        keptWhereSame(kept[key], value),
      ]),
    );
  }

  if (
    Array.isArray(kept) &&
    Array.isArray(next) &&
    kept.length === next.length
  ) {
    return next.map((item, index) => keptWhereSame(kept[index], item));
  }

  return copyOf(next);
}

/**
 * The working copy rebased onto the element's current data (see
 * mergeFormData), or null when the data is unchanged or gone. Unchanged
 * parts stay the same objects, so their fields keep what they hold.
 */
export function rebasedWorkingCopy(loaded, draft, current) {
  if (current === undefined || !formDataChanged(current, loaded)) {
    return null;
  }

  return {
    loaded: copyOf(current),
    draft: keptWhereSame(draft, mergeFormData(loaded, draft, current)),
  };
}

/** Applies an Inspector working copy to the document. options.snap is Snap to grid. */
export function applyFormData(doc, selection, data, options = {}) {
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
      return updateNode(
        doc,
        target.target.id,
        {
          device: {
            hostname: data.hostname,
            ...lookOf(data),
            spec: deepCopy(data.spec ?? {}),
          },
        },
        options,
      );
    case 'switch': {
      const network =
        target.network || findNetwork(doc, target.target.switch?.networkId);

      if (!network) {
        return doc;
      }

      // The network's fields and the switch's own colors and icon size, as one
      // Undo step. An icon size that the form emptied is the diagram's.
      return updateNode(
        updateNetwork(doc, network.id, {
          name: data.name,
          alias: data.alias === undefined ? null : data.alias,
          subnet: data.subnet ?? '',
          gateway: data.gateway ?? '',
          description: data.description ?? '',
          color: data.color ?? '',
          lineStyle: data.lineStyle ?? '',
        }),
        target.target.id,
        {
          switch: {
            outlineColor: data.outlineColor ?? '',
            fillColor: data.fillColor ?? '',
            iconSize: data.iconSize ?? '',
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
          iconSize: data.iconSize ?? '',
        },
      });
    case 'shape':
      return updateNode(doc, target.target.id, {
        shape: {
          shape: data.shape,
          label: data.label ?? '',
          fillColor: data.fillColor ?? '',
          outlineColor: data.outlineColor ?? '',
          borderStyle: data.borderStyle ?? '',
        },
        ...formSize(target, data),
      });
    case 'icon':
      // A custom icon is drawn in place of the built-in one, which the node
      // then no longer names (see drawnIcon in model.js).
      return updateNode(doc, target.target.id, {
        icon: {
          iconKey: data.icon ? '' : data.iconKey || '',
          icon: data.icon ?? '',
          label: data.label ?? '',
        },
        ...formSize(target, data),
      });
    case 'line': {
      const { position } = target.target;

      // The points back relative to the line, which then moves to their
      // top left corner (see placedLine in model.js).
      return updateNode(doc, target.target.id, {
        line: {
          label: data.label ?? '',
          color: data.color ?? '',
          lineStyle: data.lineStyle ?? '',
          startArrow: data.startArrow === true,
          endArrow: data.endArrow === true,
          points: (data.points || []).map((point) => ({
            x: Number(point?.x) - position.x,
            y: Number(point?.y) - position.y,
          })),
        },
      });
    }
    default:
      return doc;
  }
}

// The size that a form for a shape or an icon sets, when its Width and
// Height hold one. An emptied field keeps the node's value.
function formSize(target, data) {
  const size = sizeOf(target.target);
  const width = Number.isFinite(data.width) && data.width > 0;
  const height = Number.isFinite(data.height) && data.height > 0;

  return {
    size: {
      width: width ? data.width : size.width,
      height: height ? data.height : size.height,
    },
  };
}

// The name that an issue message uses for a document element: "device
// web-01", not the validator's nodes[3].
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
      // Devices are numbered among devices. The number shows when the
      // hostname alone does not identify the device.
      const devices = nodes.filter((entry) => entry.kind === 'device');
      const number = `#${devices.indexOf(node) + 1}`;
      const hostname = String(node.device?.hostname || '').trim();
      const shared = devices.some(
        (entry) =>
          entry !== node &&
          foldKey(entry.device?.hostname) === foldKey(hostname),
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
    case 'shape':
    case 'icon':
    case 'line': {
      const kind = kindLabel(node).toLowerCase();
      const label = node[node.kind]?.label;

      return label ? `${kind} ${label}` : `a ${kind}`;
    }
    default:
      return `node #${index + 1}`;
  }
}

/** The Inspector text of a document issue, with indexed paths as element names. */
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
