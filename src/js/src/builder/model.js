// Library independent builder document model: the JS twin of the wire contract
// in phenix/types/builder (document.go). Edits return a new document, and
// nothing here knows Vue. Only the server sets the metadata's stamps (withStamp).

import {
  assignAddresses,
  DEFAULT_INTERFACE_MASK,
  needsAddress,
  suggestedSubnet,
  withNetworkGateway,
} from './addressing.js';
import { iconKeyForSpec, isIconKey, kindMeta } from './catalog.js';
import { isBuilderAnnotation } from './configs.js';
import { foldKey, goToLower } from './goCase.js';
import { isIconName } from './icons.js';
import { newId, uniqueName } from './ids.js';
import {
  MATCH_CONFLICT_HIGHEST,
  MATCH_CONFLICT_LOWEST,
  MATCH_CONFLICTS,
  MAX_LAYER_MATCHERS,
  MAX_MATCHER_PATH_BYTES,
  MAX_MATCHER_PATTERN_BYTES,
  MAX_MATCHER_REPEATS,
  matchedLayer,
  matcherPathRule,
  matcherPatternRule,
} from './layerMatchers.js';
import {
  hasControlCharacters,
  hasControlCharactersInLines,
  isBlank,
  utf8Length,
} from './text.js';
import { deepCopy, isPlainObject } from './values.js';

export const SCHEMA_URI = 'https://phenix.sandia.gov/schemas/builder/v1';
export const SCHEMA_REVISION = 1;
export const DEFAULT_GRID_SIZE = 16;
// The bounds of a grid size, in whole pixels (MinGridSize and MaxGridSize in
// document.go).
export const MIN_GRID_SIZE = 4;
export const MAX_GRID_SIZE = 200;

// The step the editor snaps a position to: the grid size while the user's Snap
// to grid setting (settings.js) is on, else 0 for whole pixels.
export function snapStep(doc, snap) {
  return snap ? doc?.grid?.size || DEFAULT_GRID_SIZE : 0;
}

// A value on a step of `step`, or a whole pixel for a step of 0. Never -0,
// which Object.is (stable.js) tells apart from 0.
function onStep(value, step) {
  const stepped =
    step > 0 ? Math.round(value / step) * step : Math.round(value);

  return stepped === 0 ? 0 : stepped;
}

// A point on the grid while the user's Snap to grid setting is on, else on
// whole pixels (see snapStep).
export function snappedPoint(doc, point, snap) {
  const step = snapStep(doc, snap);

  return { x: onStep(point.x, step), y: onStep(point.y, step) };
}

const DEFAULT_SIZES = {
  device: { width: 160, height: 96 },
  switch: { width: 180, height: 72 },
  note: { width: 200, height: 120 },
  group: { width: 320, height: 240 },
  shape: { width: 160, height: 96 },
  icon: { width: 64, height: 64 },
};

// The size a new circle takes, which a rectangle would make an ellipse.
const CIRCLE_SIZE = { width: 120, height: 120 };

// The kinds of nodes a document holds, each the key of its payload (Node in
// document.go).
export const NODE_KINDS = [
  'device',
  'switch',
  'note',
  'group',
  'shape',
  'icon',
  'line',
];

// The kinds of nodes that are drawings only: shapes, icons and lines. Like
// notes and groups they never reach a config and take no connections.
export const DRAWING_KINDS = ['shape', 'icon', 'line'];

// The figures a shape draws (ShapeFigures in validate.go).
export const SHAPE_FIGURES = ['rectangle', 'circle'];

// The fewest and the most points a line has (MinLinePoints and
// MaxLinePoints in document.go): its two ends, and the bends between them.
export const MIN_LINE_POINTS = 2;
export const MAX_LINE_POINTS = 64;

// How far apart the two ends of a new line are, across.
const NEW_LINE_LENGTH = 160;

// The icon a new icon node shows.
const DEFAULT_DRAWN_ICON = 'external';

// The fields of a shape, an icon and a line written only when set.
const SHAPE_OPTIONAL_KEYS = [
  'label',
  'fillColor',
  'outlineColor',
  'borderStyle',
];
const ICON_OPTIONAL_KEYS = ['iconKey', 'icon', 'label'];
const LINE_OPTIONAL_KEYS = ['label', 'color', 'lineStyle'];
const LINE_ARROW_KEYS = ['startArrow', 'endArrow'];

// The colors addNetwork gives networks in turn. A network with one of them is
// drawn in its theme token instead (see colors.js).
export const DEFAULT_NETWORK_COLORS = [
  '#2f6fbf',
  '#b05c17',
  '#1f7a5a',
  '#7a3fb0',
  '#a3273f',
  '#0f6f77',
  '#6b6f18',
  '#8a4b8f',
];

// The dash patterns a network or a connection may name, and the border
// patterns a group may (LineStyles and BorderStyles in validate.go). Unset
// leaves the pattern to the editor.
export const LINE_STYLES = ['solid', 'dashed', 'dotted', 'dash-dot'];
export const BORDER_STYLES = ['solid', 'dashed', 'dotted', 'double'];

// The one form the outline or fill color of a node takes: #rrggbb, in
// either case (hexColor in validate.go).
export const HEX_COLOR = /^#[0-9a-fA-F]{6}$/;

// The colors a device or a switch node may have of its own on the canvas.
const NODE_COLOR_KEYS = ['outlineColor', 'fillColor'];

// The sizes at which a node's icon is drawn (IconSizes in validate.go),
// smallest first, and the side of each in pixels. Small is the size that
// icons always had. Medium is one and a half times it, and Large is twice
// it. A document that names no size draws Small.
export const ICON_SIZES = ['small', 'medium', 'large'];
export const ICON_SIZE_PIXELS = { small: 16, medium: 24, large: 32 };
export const DEFAULT_ICON_SIZE = 'small';

// The kinds of nodes that draw an icon at the document's icon size, or at
// one of their own.
export const ICON_SIZE_KINDS = ['device', 'switch', 'group'];

/** The icon size of a document: the one it names, or Small. */
export function documentIconSize(doc) {
  return ICON_SIZES.includes(doc?.iconSize) ? doc.iconSize : DEFAULT_ICON_SIZE;
}

// The size a node's icon is drawn at: the override (the viewer's Node icon
// size setting, settings.js), then the node's own, then the document's.
export function nodeIconSize(doc, node, override) {
  if (ICON_SIZES.includes(override)) {
    return override;
  }

  const own = ICON_SIZE_KINDS.includes(node?.kind)
    ? node[node.kind]?.iconSize
    : undefined;

  return ICON_SIZES.includes(own) ? own : documentIconSize(doc);
}

/** The side in pixels of an icon drawn at a size, Small for any other value. */
export function iconPixels(size) {
  return ICON_SIZE_PIXELS[size] ?? ICON_SIZE_PIXELS[DEFAULT_ICON_SIZE];
}

// The document drawing its node icons at a size. Small leaves out `iconSize`,
// so the bytes match a document that never set one.
export function setIconSize(doc, size) {
  const wanted = ICON_SIZES.includes(size) ? size : DEFAULT_ICON_SIZE;
  const kept = wanted === DEFAULT_ICON_SIZE ? undefined : wanted;

  if (doc.iconSize === kept) {
    return doc;
  }

  const next = { ...doc };

  if (kept === undefined) {
    delete next.iconSize;
  } else {
    next.iconSize = kept;
  }

  return next;
}

// The layers of a diagram (Layer and its bounds in document.go). They never
// reach a config. `layers` is sorted by ID, the highest first, and left out
// when it holds no layer or only the placeholders.
export const MAX_LAYERS = 32;
export const MAX_LAYER_ID = 99;
export const MAX_LAYER_LABEL_BYTES = 64;
export const PLACEHOLDER_LAYERS = 7;

// The kinds of nodes that may name a layer.
export const LAYER_KINDS = ['device', 'switch'];

/**
 * @param {*} value
 * @returns {boolean} whether value is a layer ID: an integer from 0 to
 *   MAX_LAYER_ID
 */
export function isLayerId(value) {
  return Number.isInteger(value) && value >= 0 && value <= MAX_LAYER_ID;
}

// The layer ID a select or text value names: null for no layer (empty, null or
// undefined), or undefined for a value that is not a layer ID.
export function layerIdOf(value) {
  if (value === undefined || value === null || value === '') {
    return null;
  }

  const id =
    typeof value === 'string' && /^\d{1,2}$/.test(value.trim())
      ? Number(value.trim())
      : value;

  return isLayerId(id) ? id : undefined;
}

/** The label of a layer that the diagram does not define: "Layer 3". */
export function placeholderLabel(id) {
  return `Layer ${id}`;
}

/** A new list of the placeholder layers, "Layer N", the highest ID first. */
export function placeholderLayers() {
  return Array.from({ length: PLACEHOLDER_LAYERS }, (_, at) => {
    const id = PLACEHOLDER_LAYERS - 1 - at;

    return { id, label: placeholderLabel(id) };
  });
}

// Why the server would refuse a layer label (validateLayers in validate.go), as
// a sentence for the editor, or ''.
export function layerLabelProblem(label) {
  const text = typeof label === 'string' ? label : '';

  if (isBlank(text)) {
    return 'Type a label for the layer.';
  }

  if (utf8Length(text) > MAX_LAYER_LABEL_BYTES) {
    return `A layer label holds at most ${MAX_LAYER_LABEL_BYTES} bytes in UTF-8.`;
  }

  if (hasControlCharacters(text)) {
    return 'A layer label cannot hold control characters, such as a tab.';
  }

  return '';
}

// Why a layer color cannot be written, as a sentence for the editor, or ''.
export function layerColorProblem(color) {
  return color === undefined ||
    color === null ||
    color === '' ||
    (typeof color === 'string' && HEX_COLOR.test(color))
    ? ''
    : 'A layer color is # and 6 hex digits, such as #2f6fbf.';
}

// Why a node matcher's field path cannot be written, as a sentence, or ''.
export function matcherPathProblem(path) {
  return (
    {
      required: 'Type a field, such as general.hostname.',
      'too-long': `A field holds at most ${MAX_MATCHER_PATH_BYTES} bytes.`,
      invalid:
        'A field is keys of letters, digits, _ or -, joined by dots, such as general.hostname.',
    }[matcherPathRule(path)] || ''
  );
}

// Why a node matcher's pattern cannot be written, as a sentence, or ''.
export function matcherPatternProblem(pattern) {
  return (
    {
      required: 'Type a regular expression, such as ^plc-.',
      'too-long': `A pattern holds at most ${MAX_MATCHER_PATTERN_BYTES} bytes.`,
      invalid:
        'This is not a regular expression that the server and the editor read the same way. Do not use lookarounds, backreferences, flags or \\p classes.',
      nested:
        'Do not repeat a group that has a repeat or | in it, as in (a+)+. Such a pattern can stop the editor.',
      repeats: `Use at most ${MAX_MATCHER_REPEATS} of *, + and {n,} in a pattern. More can stop the editor.`,
    }[matcherPatternRule(pattern)] || ''
  );
}

// Why the node matchers of a layer cannot be written, as a sentence, or ''.
export function matchersProblem(matchers) {
  if (matchers === undefined || matchers === null) {
    return '';
  }

  if (typeof matchers !== 'object' || Array.isArray(matchers)) {
    return 'The node matchers are not an object of patterns by field.';
  }

  const fields = Object.keys(matchers);

  if (fields.length > MAX_LAYER_MATCHERS) {
    return `A layer has at most ${MAX_LAYER_MATCHERS} node matchers.`;
  }

  for (const field of fields) {
    const problem =
      matcherPathProblem(field) || matcherPatternProblem(matchers[field]);

    if (problem) {
      return problem;
    }
  }

  return '';
}

// Why one layer, apart from its ID, cannot be written, as a sentence, or ''.
export function layerProblem(layer) {
  const label = layerLabelProblem(layer?.label);

  if (label) {
    return label;
  }

  if (!unsetText(layer?.iconKey) && !isIconKey(layer.iconKey)) {
    return 'The layer icon is not a built-in icon.';
  }

  if (!unsetText(layer?.icon) && !isIconName(layer.icon)) {
    return 'The layer custom icon is not an icon name.';
  }

  return layerColorProblem(layer?.color) || matchersProblem(layer?.matchers);
}

// Whether a text field of a layer is not set.
function unsetText(value) {
  return value === undefined || value === null || value === '';
}

// Why a list of layers cannot be written into a document or a layer set, as a
// sentence for the editor, or ''.
export function layersProblem(layers) {
  if (!Array.isArray(layers)) {
    return 'The layers are not a list.';
  }

  if (layers.length > MAX_LAYERS) {
    return `A diagram has at most ${MAX_LAYERS} layers.`;
  }

  const seen = new Set();

  for (const layer of layers) {
    if (!isLayerId(layer?.id)) {
      return `A layer ID is an integer from 0 to ${MAX_LAYER_ID}.`;
    }

    if (seen.has(layer.id)) {
      return `Two layers use layer ID ${layer.id}.`;
    }

    seen.add(layer.id);

    const problem = layerProblem(layer);

    if (problem) {
      return problem;
    }
  }

  return '';
}

// The optional text fields of a layer: its icon and its color.
const LAYER_LOOK_KEYS = ['iconKey', 'icon', 'color'];

// A layer as the document keeps it, in the server's field order. Empty values
// are left out and the matchers are sorted, so the bytes match the server's.
export function normalizedLayer(layer) {
  const kept = { id: layer.id, label: layer.label };

  for (const key of LAYER_LOOK_KEYS) {
    if (typeof layer[key] === 'string' && layer[key] !== '') {
      kept[key] = layer[key];
    }
  }

  const matchers = layer.matchers;

  if (isPlainObject(matchers) && Object.keys(matchers).length > 0) {
    kept.matchers = Object.fromEntries(
      Object.keys(matchers)
        .sort()
        .map((field) => [field, matchers[field]]),
    );
  }

  return kept;
}

// Layers sorted by ID, the highest first, each a new layer (see
// normalizedLayer).
function sortedLayers(layers) {
  return layers.map(normalizedLayer).sort((a, b) => b.id - a.id);
}

// Whether two lists of layers, each as normalizedLayer gives them, hold the
// same layers in the same order.
function sameLayers(a, b) {
  return a.length === b.length && JSON.stringify(a) === JSON.stringify(b);
}

// Layers as the document keeps them: sorted, the highest ID first, and empty
// when they are only the placeholders.
export function normalizedLayers(layers) {
  const sorted = sortedLayers(layers);

  return sameLayers(sorted, placeholderLayers()) ? [] : sorted;
}

// The layers a list stands for, the highest ID first, or the placeholders when
// it is empty. Entries that are not layers are left out.
export function effectiveLayerList(layers) {
  const listed = (Array.isArray(layers) ? layers : []).filter(
    (layer) => isLayerId(layer?.id) && typeof layer.label === 'string',
  );

  return listed.length > 0 ? sortedLayers(listed) : placeholderLayers();
}

/** The layers a diagram offers, the highest ID first. */
export function effectiveLayers(doc) {
  return effectiveLayerList(doc?.layers);
}

// The editor's name for each layer, by ID: its label, with "(ID 3)" added when
// another layer has the same label.
export function layerTitles(layers) {
  const uses = new Map();

  for (const { label } of layers) {
    uses.set(label, (uses.get(label) || 0) + 1);
  }

  return new Map(
    layers.map(({ id, label }) => [
      id,
      uses.get(label) > 1 ? `${label} (ID ${id})` : label,
    ]),
  );
}

// The editor's name for a layer (see layerTitles). An ID that the diagram does
// not define is "Layer N", as a node can name such a layer.
export function layerLabel(doc, id) {
  const layers = effectiveLayers(doc);

  if (!layers.some((layer) => layer.id === id)) {
    layers.push({ id, label: placeholderLabel(id) });
  }

  return layerTitles(layers).get(id);
}

// A layer as a message names it: "layer Control", or only the label when it
// says "layer" already, such as "Layer 3".
export function layerName(doc, id) {
  const label = layerLabel(doc, id);

  return /\blayer\b/i.test(label) ? label : `layer ${label}`;
}

// The layer the user chose for a device or a switch, or null. A matched layer
// does not count (see effectiveNodeLayer).
export function nodeLayer(node) {
  const own = LAYER_KINDS.includes(node?.kind)
    ? node[node.kind]?.layer
    : undefined;

  return isLayerId(own) ? own : null;
}

// The layer a device's fields match ({id, path}, see layerMatchers.js), or
// null. It ignores the layer the user chose. Only devices match.
export function matchedNodeLayer(doc, node) {
  if (node?.kind !== 'device' || !node.device) {
    return null;
  }

  return matchedLayer(node.device, doc?.layers, matchConflictOf(doc));
}

// The layer a node is at: the one the user chose, else the one its fields
// match. Everything that places, counts or colors nodes by layer uses it.
export function effectiveNodeLayer(doc, node) {
  return nodeLayer(node) ?? matchedNodeLayer(doc, node)?.id ?? null;
}

// What the Inspector shows of a node's layer: the effective layer, the chosen
// layer, and the matched layer with its field.
export function nodeLayerState(doc, node) {
  const manual = nodeLayer(node);
  const matched = matchedNodeLayer(doc, node);

  return { layer: manual ?? matched?.id ?? null, manual, matched };
}

// The number of devices and switches at each layer, by layer ID.
export function layerCounts(doc) {
  const counts = new Map();

  for (const node of doc?.nodes || []) {
    const id = effectiveNodeLayer(doc, node);

    if (id !== null) {
      counts.set(id, (counts.get(id) || 0) + 1);
    }
  }

  return counts;
}

// Whether any device or switch is at a layer.
export function someNodeOnLayer(doc) {
  return (doc?.nodes || []).some(
    (node) => effectiveNodeLayer(doc, node) !== null,
  );
}

// The layer the diagram offers for an ID (see effectiveLayers), or null.
function layerOf(doc, id) {
  return effectiveLayers(doc).find((layer) => layer.id === id) || null;
}

// The outline color a node takes from its layer while the layerOutlines option
// is on, else ''. A node's own outline color wins (nodeColors in colors.js).
export function layerOutlineColor(doc, node) {
  if (
    !layerSetOptions(doc).layerOutlines ||
    !LAYER_KINDS.includes(node?.kind)
  ) {
    return '';
  }

  const id = effectiveNodeLayer(doc, node);
  const color = id === null ? '' : layerOf(doc, id)?.color;

  return typeof color === 'string' && HEX_COLOR.test(color) ? color : '';
}

// The document with a device or a switch at a layer (see setNodeLayers). No
// layer (null or '') removes `layer`. The layer need not be one it defines.
export function setNodeLayer(doc, id, layer) {
  return setNodeLayers(doc, [id], layer).doc;
}

// The node at the layer `wanted` (a layer ID, or null for none), or the same
// node when it cannot take a layer or is at that layer already (see
// setNodeLayer).
function withNodeLayer(node, wanted) {
  if (
    !LAYER_KINDS.includes(node?.kind) ||
    !node[node.kind] ||
    includedFrom(node) ||
    wanted === undefined ||
    (node[node.kind].layer ?? null) === wanted
  ) {
    return node;
  }

  const payload = { ...node[node.kind] };

  if (wanted === null) {
    delete payload.layer;
  } else {
    payload.layer = wanted;
  }

  return { ...node, [node.kind]: payload };
}

// Puts some devices and switches at a layer in one pass, as setNodeLayer does.
// Returns the document and the ids that changed, in the document's order.
export function setNodeLayers(doc, ids, layer) {
  const wanted = layerIdOf(layer);
  const listed = new Set(ids);
  const changed = [];

  if (wanted === undefined || listed.size === 0) {
    return { doc, changed };
  }

  const nodes = (doc.nodes || []).map((node) => {
    if (!listed.has(node.id)) {
      return node;
    }

    const next = withNodeLayer(node, wanted);

    if (next !== node) {
      changed.push(node.id);
    }

    return next;
  });

  return changed.length
    ? { doc: { ...doc, nodes }, changed }
    : { doc, changed };
}

// The ID for a new layer: one above the highest, else the lowest free ID, or
// null when the list is full.
export function nextLayerId(layers) {
  const effective = effectiveLayerList(layers);

  if (effective.length >= MAX_LAYERS) {
    return null;
  }

  const used = new Set(effective.map((layer) => layer.id));
  const above = Math.max(-1, ...used) + 1;

  if (above <= MAX_LAYER_ID) {
    return above;
  }

  for (let id = 0; id <= MAX_LAYER_ID; id += 1) {
    if (!used.has(id)) {
      return id;
    }
  }

  return null;
}

// The list with a new layer at the next free ID, or null when it is full or
// the label is bad. An empty list stands for the placeholders, which stay.
export function withLayerAdded(layers, label) {
  const id = nextLayerId(layers);

  if (id === null) {
    return null;
  }

  const layer = { id, label: label ?? placeholderLabel(id) };

  if (layerLabelProblem(layer.label)) {
    return null;
  }

  return {
    layers: normalizedLayers([...effectiveLayerList(layers), layer]),
    layer,
  };
}

// The list with a new label for a layer, or null for an unknown ID or a bad
// label.
export function withLayerRelabeled(layers, id, label) {
  const effective = effectiveLayerList(layers);

  if (!effective.some((layer) => layer.id === id) || layerLabelProblem(label)) {
    return null;
  }

  return normalizedLayers(
    effective.map((layer) => (layer.id === id ? { ...layer, label } : layer)),
  );
}

// The list with new field values for a layer, where an empty value removes a
// field. Null for an unknown ID or field, or a layer that cannot be written.
export function withLayerUpdated(layers, id, changes) {
  const effective = effectiveLayerList(layers);
  const current = effective.find((layer) => layer.id === id);

  if (
    !current ||
    !changes ||
    Object.keys(changes).some((key) => !LAYER_FIELDS.includes(key))
  ) {
    return null;
  }

  const layer = { ...current, ...changes };

  if (layerProblem(layer)) {
    return null;
  }

  return normalizedLayers(
    effective.map((entry) => (entry.id === id ? layer : entry)),
  );
}

// The fields of a layer that withLayerUpdated changes.
const LAYER_FIELDS = ['label', ...LAYER_LOOK_KEYS, 'matchers'];

// The list with a new ID for a layer, or null for an unknown ID, or a new ID
// that is bad or in use.
export function withLayerRenumbered(layers, from, to) {
  const effective = effectiveLayerList(layers);
  const id = layerIdOf(to);

  if (
    !isLayerId(id) ||
    !effective.some((layer) => layer.id === from) ||
    (id !== from && effective.some((layer) => layer.id === id))
  ) {
    return null;
  }

  return normalizedLayers(
    effective.map((layer) => (layer.id === from ? { ...layer, id } : layer)),
  );
}

// The list without a layer, or null for an unknown ID. Removing a placeholder
// keeps the others, and an empty result stands for the placeholders again.
export function withLayerRemoved(layers, id) {
  const effective = effectiveLayerList(layers);

  if (!effective.some((layer) => layer.id === id)) {
    return null;
  }

  return normalizedLayers(effective.filter((layer) => layer.id !== id));
}

// The document with a list of layers (see normalizedLayers). `layers` is left
// out when empty, as the server does. A bad list changes nothing.
export function setLayers(doc, layers) {
  if (layersProblem(layers)) {
    return doc;
  }

  const kept = normalizedLayers(layers);
  const current = Array.isArray(doc.layers) ? doc.layers : undefined;

  if (
    current
      ? current.length > 0 &&
        sameLayers(
          current.map((layer) =>
            layer && typeof layer === 'object' ? normalizedLayer(layer) : layer,
          ),
          kept,
        )
      : kept.length === 0
  ) {
    return doc;
  }

  const next = { ...doc };

  if (kept.length > 0) {
    next.layers = kept;
  } else {
    delete next.layers;
  }

  return next;
}

// The document with one more layer (see withLayerAdded), and the layer or null.
export function addLayer(doc, label) {
  const added = withLayerAdded(doc.layers, label);

  return added
    ? { doc: setLayers(doc, added.layers), layer: added.layer }
    : { doc, layer: null };
}

// The document with new field values for a layer (see withLayerUpdated).
export function updateLayer(doc, id, changes) {
  const layers = withLayerUpdated(doc.layers, id, changes);

  return layers ? setLayers(doc, layers) : doc;
}

// The document with a new label for a layer (see withLayerRelabeled).
export function relabelLayer(doc, id, label) {
  const layers = withLayerRelabeled(doc.layers, id, label);

  return layers ? setLayers(doc, layers) : doc;
}

// The devices and switches of a document, and the devices of its templates,
// that are at the layer `from`, moved to the layer `to`. An included device
// is not changed.
function movedLayer(doc, from, to) {
  const moved = (payload) => {
    if (payload?.layer !== from) {
      return payload;
    }

    return { ...payload, layer: to };
  };
  const nodeIds = [];
  const nodes = (doc.nodes || []).map((node) => {
    if (nodeLayer(node) !== from || includedFrom(node)) {
      return node;
    }

    nodeIds.push(node.id);

    return { ...node, [node.kind]: moved(node[node.kind]) };
  });
  const next = nodeIds.length > 0 ? { ...doc, nodes } : { ...doc };

  if (
    Array.isArray(doc.templates) &&
    doc.templates.some((template) => template?.device?.layer === from)
  ) {
    next.templates = doc.templates.map((template) =>
      template?.device?.layer === from
        ? { ...template, device: moved(template.device) }
        : template,
    );
  }

  return { doc: next, nodeIds };
}

// The document with a new ID for a layer (see withLayerRenumbered). Its nodes
// and templates move with it, but an included device keeps its layer.
export function renumberLayer(doc, from, to) {
  const layers = withLayerRenumbered(doc.layers, from, to);
  const id = layerIdOf(to);

  if (!layers || id === from) {
    return doc;
  }

  return setLayers(movedLayer(doc, from, id).doc, layers);
}

// The document without a layer, and the ids of the nodes at it. They keep the
// ID, which the editor then names "Layer N".
export function removeLayer(doc, id) {
  const layers = withLayerRemoved(doc.layers, id);

  if (!layers) {
    return { doc, nodeIds: [] };
  }

  const nodeIds = (doc.nodes || [])
    .filter((node) => nodeLayer(node) === id)
    .map((node) => node.id);

  return { doc: setLayers(doc, layers), nodeIds };
}

// The layer set a diagram's layers were copied from (DocumentLayerSet and its
// bounds in layerset.go). The diagram keeps a full copy. `layerSet` keeps only
// options that are not at their default, and the {id, version} link.
export const MAX_LAYER_SET_NAME_BYTES = 128;
export const MAX_LAYER_SET_DESCRIPTION_BYTES = 1024;
export const MAX_LAYER_SET_ID_BYTES = 128;
const LAYER_SET_ID = /^[A-Za-z0-9_.:-]+$/;

export { MATCH_CONFLICT_HIGHEST, MATCH_CONFLICT_LOWEST };

/**
 * @param {*} id
 * @returns {boolean} whether id has the form of the ID of a layer set (see
 *   IsLayerSetID in layerset.go)
 */
export function isLayerSetId(id) {
  return (
    typeof id === 'string' &&
    id !== '' &&
    utf8Length(id) <= MAX_LAYER_SET_ID_BYTES &&
    LAYER_SET_ID.test(id)
  );
}

// The match conflict rule of a set or of a diagram's layer set options:
// highest, or else lowest.
function conflictRule(value) {
  return value === MATCH_CONFLICT_HIGHEST
    ? MATCH_CONFLICT_HIGHEST
    : MATCH_CONFLICT_LOWEST;
}

// The match conflict rule of a diagram (see layerSetOptions).
function matchConflictOf(doc) {
  return conflictRule(doc?.layerSet?.matchConflict);
}

// The link of a diagram's layers to a layer set, or null when it is not
// one: an ID of a layer set and a version of 1 or more.
function linkOf(source) {
  return isPlainObject(source) &&
    isLayerSetId(source.id) &&
    Number.isInteger(source.version) &&
    source.version >= 1
    ? { id: source.id, version: source.version }
    : null;
}

// A diagram's layer set options, each at its default when unset: name '',
// matchConflict lowest, layerOutlines false, and source (the link) null.
export function layerSetOptions(doc) {
  const options = isPlainObject(doc?.layerSet) ? doc.layerSet : {};

  return {
    name: typeof options.name === 'string' ? options.name : '',
    matchConflict: conflictRule(options.matchConflict),
    layerOutlines: options.layerOutlines === true,
    source: linkOf(options.source),
  };
}

// The options as the document keeps them: only those that are not at their
// default, or null when none is left.
function keptLayerSetOptions({ name, matchConflict, layerOutlines, source }) {
  const kept = {};

  if (name) {
    kept.name = name;
  }

  if (matchConflict === MATCH_CONFLICT_HIGHEST) {
    kept.matchConflict = matchConflict;
  }

  if (layerOutlines) {
    kept.layerOutlines = true;
  }

  if (source) {
    kept.source = { id: source.id, version: source.version };
  }

  return Object.keys(kept).length ? kept : null;
}

// Why a layer set name cannot be written, as a sentence, or ''. A set needs a
// name, but a diagram's options do not (`required` false).
export function layerSetNameProblem(name, { required = true } = {}) {
  const text = typeof name === 'string' ? name : '';

  if (required && isBlank(text)) {
    return 'Type a name for the layer set.';
  }

  if (utf8Length(text) > MAX_LAYER_SET_NAME_BYTES) {
    return `A layer set name holds at most ${MAX_LAYER_SET_NAME_BYTES} bytes in UTF-8.`;
  }

  return hasControlCharacters(text)
    ? 'A layer set name cannot hold control characters, such as a tab.'
    : '';
}

// Why a layer set description cannot be written, as a sentence, or ''.
export function layerSetDescriptionProblem(description) {
  const text = typeof description === 'string' ? description : '';

  if (utf8Length(text) > MAX_LAYER_SET_DESCRIPTION_BYTES) {
    return `A layer set description holds at most ${MAX_LAYER_SET_DESCRIPTION_BYTES} bytes in UTF-8.`;
  }

  return hasControlCharacters(text)
    ? 'A layer set description cannot hold control characters, such as a line break.'
    : '';
}

// The document with new layer set options (see layerSetOptions). A null source
// unlinks. The layers do not change. A bad value changes nothing.
export function setLayerSetOptions(doc, changes = {}) {
  const current = layerSetOptions(doc);
  const wanted = { ...current, ...changes };

  if (
    layerSetNameProblem(wanted.name, { required: false }) ||
    !MATCH_CONFLICTS.includes(wanted.matchConflict) ||
    typeof wanted.layerOutlines !== 'boolean' ||
    (wanted.source !== null && !linkOf(wanted.source))
  ) {
    return doc;
  }

  const kept = keptLayerSetOptions(wanted);

  if (
    JSON.stringify(kept) === JSON.stringify(keptLayerSetOptions(current)) &&
    (kept === null) === (doc.layerSet === undefined)
  ) {
    return doc;
  }

  const next = { ...doc };

  if (kept) {
    next.layerSet = kept;
  } else {
    delete next.layerSet;
  }

  return next;
}

// The document with a copy of a layer set: its layers, its options, and a link
// to its ID and version when it has them. Nodes keep their chosen layer IDs.
export function withLayerSetCopy(doc, set) {
  if (!set || layersProblem(set.layers) || set.layers.length === 0) {
    return doc;
  }

  const next = setLayers(doc, set.layers);
  const kept = keptLayerSetOptions({
    name: layerSetNameProblem(set.name) ? '' : set.name,
    matchConflict: conflictRule(set.matchConflict),
    layerOutlines: set.layerOutlines === true,
    source: linkOf({ id: set.id, version: set.version }),
  });

  if (
    next === doc &&
    JSON.stringify(kept) ===
      JSON.stringify(keptLayerSetOptions(layerSetOptions(doc))) &&
    (kept === null) === (doc.layerSet === undefined)
  ) {
    return doc;
  }

  const copy = next === doc ? { ...doc } : next;

  if (kept) {
    copy.layerSet = kept;
  } else {
    delete copy.layerSet;
  }

  return copy;
}

// A diagram's layers (the placeholders when none) and options as the content
// of a layer set (LayerSet in layerset.go), to save them to one.
export function layerSetContentOf(doc) {
  const { matchConflict, layerOutlines } = layerSetOptions(doc);

  return { matchConflict, layerOutlines, layers: effectiveLayers(doc) };
}

// Whether a diagram's layers and options equal a layer set's, the placeholders
// standing for an empty list.
export function sameLayerSetContent(doc, set) {
  const options = layerSetOptions(doc);

  return (
    options.matchConflict === conflictRule(set?.matchConflict) &&
    options.layerOutlines === (set?.layerOutlines === true) &&
    sameLayers(effectiveLayers(doc), effectiveLayerList(set?.layers))
  );
}

// How a diagram's layers stand to their linked set in a library: 'unlinked',
// 'missing' (deleted, or from another server), 'update' (a later version),
// 'changed' (the copy differs from its version) or 'linked'.
export function layerSetLink(doc, sets = []) {
  const { source } = layerSetOptions(doc);

  if (!source) {
    return { status: 'unlinked', set: null, source: null };
  }

  const set = (sets || []).find((entry) => entry?.id === source.id) || null;

  if (!set) {
    return { status: 'missing', set: null, source };
  }

  if (Number.isInteger(set.version) && set.version > source.version) {
    return { status: 'update', set, source };
  }

  return {
    status: sameLayerSetContent(doc, set) ? 'linked' : 'changed',
    set,
    source,
  };
}

// A device's presentation fields: its icon, the custom icon drawn in its
// place (an icon name, see icons.js), the size of its icon and its colors on
// the canvas. The Inspector applies a change to one of them at once, without
// Apply. A new device takes them from addNode's `look` option.
export const LOOK_KEYS = ['iconKey', 'icon', 'iconSize', ...NODE_COLOR_KEYS];

// Those written only when set. A device always has an icon key.
const OPTIONAL_LOOK_KEYS = LOOK_KEYS.filter((key) => key !== 'iconKey');

// The presentation fields of a device payload (see LOOK_KEYS) as text, '' when
// unset.
export function lookOf(payload) {
  return Object.fromEntries(
    LOOK_KEYS.map((key) => [key, String(payload?.[key] ?? '')]),
  );
}

// The fields of a group and of a switch that are written only when set.
const GROUP_OPTIONAL_KEYS = [
  'description',
  'borderStyle',
  'iconKey',
  'icon',
  'iconSize',
];
const SWITCH_OPTIONAL_KEYS = [...NODE_COLOR_KEYS, 'iconSize', 'layer'];

// Removes the keys of optional fields that hold no value. Thus a field that
// is set and then emptied leaves the payload, and the document's bytes,
// unchanged.
function dropEmpty(payload, keys) {
  for (const key of keys) {
    if ([undefined, null, ''].includes(payload[key])) {
      delete payload[key];
    }
  }

  return payload;
}

// The payload of an icon node with a patch applied. It keeps exactly one
// icon. A custom icon that the patch sets replaces the built-in key. A key
// that the patch sets replaces the custom icon. With neither left, the node
// shows the default icon.
function drawnIcon(current, patch) {
  const icon = dropEmpty({ ...current, ...patch }, ICON_OPTIONAL_KEYS);

  if (patch.icon) {
    delete icon.iconKey;
  } else if (patch.iconKey) {
    delete icon.icon;
  }

  if (icon.iconKey !== undefined && !isIconKey(icon.iconKey)) {
    delete icon.iconKey;
  }

  if (!icon.icon && !icon.iconKey) {
    icon.iconKey = DEFAULT_DRAWN_ICON;
  }

  // In the server's order: iconKey, icon, label.
  return dropEmpty(
    Object.fromEntries(ICON_OPTIONAL_KEYS.map((key) => [key, icon[key]])),
    ICON_OPTIONAL_KEYS,
  );
}

/** Size of a node, falling back to the default for its kind. */
export function sizeOf(node) {
  const fallback =
    node?.kind === 'line'
      ? lineExtent(node.line?.points)
      : node?.kind === 'shape' && node.shape?.shape === 'circle'
        ? CIRCLE_SIZE
        : DEFAULT_SIZES[node?.kind] || DEFAULT_SIZES.device;

  return {
    width: node?.size?.width ?? fallback.width,
    height: node?.size?.height ?? fallback.height,
  };
}

// The box around the nodes, with `padding` on every side, or all 0 for no
// nodes. Fit, zoom, groups and image downloads use it.
export function nodesBounds(nodes, padding = 0) {
  if (!nodes?.length) {
    return { x: 0, y: 0, width: 0, height: 0 };
  }

  let left = Infinity;
  let top = Infinity;
  let right = -Infinity;
  let bottom = -Infinity;

  for (const node of nodes) {
    const { width, height } = sizeOf(node);

    left = Math.min(left, node.position.x);
    top = Math.min(top, node.position.y);
    right = Math.max(right, node.position.x + width);
    bottom = Math.max(bottom, node.position.y + height);
  }

  return {
    x: left - padding,
    y: top - padding,
    width: right - left + padding * 2,
    height: bottom - top + padding * 2,
  };
}

// The size addNode gives a new node with no size: its kind's, a circle's, or
// for a line the box of its two ends.
export function newNodeSize(options = {}) {
  if (options.kind === 'line') {
    return { width: NEW_LINE_LENGTH, height: 1 };
  }

  return sizeOf({ kind: options.kind, shape: { shape: options.shape } });
}

// The box from a line node's position to its farthest point, at least one
// pixel on each side. A line without its own size takes this box.
function lineExtent(points) {
  const listed = Array.isArray(points) ? points : [];

  return {
    width: Math.max(1, ...listed.map((point) => Number(point?.x) || 0)),
    height: Math.max(1, ...listed.map((point) => Number(point?.y) || 0)),
  };
}

// A line's points as the model keeps them: the position moves to the top left
// of their box, each point is relative to it, and the size is the box.
export function placedLine(position, points) {
  const xs = points.map((point) => point.x);
  const ys = points.map((point) => point.y);
  const left = Math.min(...xs);
  const top = Math.min(...ys);

  return {
    position: { x: position.x + left, y: position.y + top },
    points: points.map((point) => ({ x: point.x - left, y: point.y - top })),
    size: {
      width: Math.max(1, Math.max(...xs) - left),
      height: Math.max(1, Math.max(...ys) - top),
    },
  };
}

// Whether a list of points is one a line can have: from MIN_LINE_POINTS to
// MAX_LINE_POINTS points, each a finite coordinate.
function linePoints(points) {
  return (
    Array.isArray(points) &&
    points.length >= MIN_LINE_POINTS &&
    points.length <= MAX_LINE_POINTS &&
    points.every(
      (point) => Number.isFinite(point?.x) && Number.isFinite(point?.y),
    )
  );
}

// Creates a new, valid, empty document. `init` takes metadata fields, layers
// (ignored when bad, see setLayers) and layerSet options.
export function createDocument(init = {}) {
  const metadata = {
    id: init.id || newId(),
    name: init.name || 'Untitled diagram',
    description: init.description || '',
  };

  if (Array.isArray(init.notes) && init.notes.length > 0) {
    metadata.notes = [...init.notes];
  }

  const doc = {
    $schema: SCHEMA_URI,
    revision: SCHEMA_REVISION,
    metadata,
    nodes: [],
    networks: [],
    edges: [],
    viewport: { x: 0, y: 0, zoom: 1 },
    grid: { enabled: true, size: DEFAULT_GRID_SIZE },
  };

  const layered = init.layers ? setLayers(doc, init.layers) : doc;

  return init.layerSet ? setLayerSetOptions(layered, init.layerSet) : layered;
}

/** @returns {object|undefined} */
export function findNode(doc, id) {
  return (doc?.nodes || []).find((node) => node.id === id);
}

/** @returns {object|undefined} */
export function findEdge(doc, id) {
  return (doc?.edges || []).find((edge) => edge.id === id);
}

/** @returns {object|undefined} */
export function findNetwork(doc, id) {
  return (doc?.networks || []).find((network) => network.id === id);
}

// The network of a name, the exact name first, else matched case-insensitively
// as Go lowercases (goCase.js). Like minimega VLANs, names can differ by case.
export function networkByName(doc, name) {
  const wanted = goToLower(String(name || ''));
  const networks = doc?.networks || [];

  return (
    networks.find((network) => network.name === name) ||
    networks.find((network) => goToLower(network.name) === wanted)
  );
}

/** Network of a switch node. */
export function networkOfSwitch(doc, node) {
  return node?.kind === 'switch'
    ? findNetwork(doc, node.switch?.networkId)
    : undefined;
}

/** Interface handles ({id, name, index}) of a device node. */
export function deviceHandles(node) {
  return node?.kind === 'device' ? node.device?.interfaces || [] : [];
}

/** Spec interface entries of a device node. */
export function specInterfaces(node) {
  const interfaces = node?.device?.spec?.network?.interfaces;

  return Array.isArray(interfaces) ? interfaces : [];
}

/** Spec interface entry a handle maps onto. */
export function specInterfaceFor(node, handleId) {
  const handle = deviceHandles(node).find((entry) => entry.id === handleId);

  if (!handle) {
    return undefined;
  }

  return specInterfaces(node).find((iface) => iface?.name === handle.name);
}

// --- included devices ------------------------------------------------------
// Devices of included topologies (device.includedFrom) are read only, as are
// their connections, switches and network names. Publishing leaves them out.

/** The topology an included device comes from, or '' for any other node. */
export function includedFrom(node) {
  return node?.kind === 'device' ? node.device?.includedFrom || '' : '';
}

// Why an included device cannot be changed, or '' for any other node.
export function includedReason(node) {
  const from = includedFrom(node);

  return from
    ? `Included topology ${from} defines ${nodeLabel(node)}. This device is read only here. Change it in ${from}.`
    : '';
}

// A new diagram where the included devices of `doc` are its own, as Detach in
// types/builder/detach.go does on Import. It links to no config and keeps only
// the unresolved includes.
export function combineIncluded(doc, name) {
  const { importedAt, unresolvedIncludes } = doc.source || {};
  const kept = (unresolvedIncludes || []).filter(Boolean);

  return {
    ...doc,
    metadata: orderedMetadata({ ...metadataOf(doc), id: newId(), name }),
    nodes: (doc.nodes || []).map((node) => {
      if (!includedFrom(node)) {
        return node;
      }

      const device = { ...node.device };

      delete device.includedFrom;

      return { ...node, device };
    }),
    source: {
      kind: 'manual',
      ...(importedAt ? { importedAt } : {}),
      ...(kept.length ? { includeTopologies: [...kept] } : {}),
    },
  };
}

// The included device an edge attaches, if any.
function includedEndpoint(doc, edge) {
  return [edge?.sourceNodeId, edge?.targetNodeId]
    .map((id) => findNode(doc, id))
    .find((node) => includedFrom(node));
}

// The included device on a network, which fixes the network's name: a rename
// would move the device to a VLAN that its own topology does not name.
function includedMember(doc, networkId) {
  for (const edge of doc?.edges || []) {
    const device = edge.networkId === networkId && includedEndpoint(doc, edge);

    if (device) {
      return device;
    }
  }

  return undefined;
}

// Why a removal would change an included device, or '' when it would not.
export function removalRefusal(doc, kind, id) {
  if (kind === 'nodes') {
    const node = findNode(doc, id);

    if (includedFrom(node)) {
      return includedReason(node);
    }

    return node?.kind === 'switch'
      ? networkRefusal(doc, node.switch?.networkId, 'removed')
      : '';
  }

  if (kind === 'edges') {
    const device = includedEndpoint(doc, findEdge(doc, id));

    return device ? includedReason(device) : '';
  }

  return networkRefusal(doc, id, 'removed');
}

// Why a network cannot be renamed or removed (`change`), or '' when it can.
export function networkRefusal(doc, networkId, change = 'renamed') {
  const device = includedMember(doc, networkId);
  const network = findNetwork(doc, networkId);

  const verb = change === 'removed' ? 'remove' : 'rename';

  return device
    ? `You cannot ${verb} ${network ? `network ${network.name}` : 'this network'}: ` +
        `${nodeLabel(device)} from included topology ` +
        `${includedFrom(device)} is on it.`
    : '';
}

// --- networks --------------------------------------------------------------

function nextNetworkColor(doc) {
  // The palette color fewest networks use, earliest first, so a network
  // added after another was removed does not repeat a color in use.
  const uses = DEFAULT_NETWORK_COLORS.map(
    (color) =>
      (doc.networks || []).filter((network) => network.color === color).length,
  );

  return DEFAULT_NETWORK_COLORS[uses.indexOf(Math.min(...uses))];
}

/** Whether a network in the document already uses the color. */
export function networkColorInUse(doc, color) {
  return (doc.networks || []).some((network) => network.color === color);
}

/** Adds a network (VLAN). */
export function addNetwork(doc, init = {}) {
  const base =
    String(init.name || 'EXP')
      .trim()
      .replace(/\s+/g, '-') || 'EXP';
  const name = uniqueName(
    base,
    (doc.networks || []).map((n) => n.name),
    foldKey,
  );

  const network = {
    id: init.id || newId(),
    name,
    description: init.description || '',
    color: init.color || nextNetworkColor(doc),
  };

  if (Number.isInteger(init.alias)) {
    network.alias = init.alias;
  }

  if (init.lineStyle) {
    network.lineStyle = init.lineStyle;
  }

  for (const key of ['subnet', 'gateway']) {
    if (init[key]) {
      network[key] = init[key];
    }
  }

  return {
    doc: { ...doc, networks: [...(doc.networks || []), network] },
    network,
  };
}

// Updates a network. A rename changes the VLAN of every connected interface, so
// the document and the spec agree. '' removes lineStyle, subnet and gateway.
export function updateNetwork(doc, id, patch = {}) {
  const network = findNetwork(doc, id);

  if (!network) {
    return doc;
  }

  const updated = { ...network };

  // An included device on the network keeps its name (see networkRefusal).
  if (patch.name !== undefined && !includedMember(doc, id)) {
    updated.name = String(patch.name).trim().replace(/\s+/g, '-');
  }

  if (patch.description !== undefined) {
    updated.description = patch.description;
  }

  if (patch.color !== undefined) {
    updated.color = patch.color;
  }

  // '' goes back to the pattern the canvas picks.
  if (patch.lineStyle === '') {
    delete updated.lineStyle;
  } else if (patch.lineStyle !== undefined) {
    updated.lineStyle = patch.lineStyle;
  }

  if (patch.alias !== undefined) {
    if (patch.alias === null || patch.alias === '') {
      delete updated.alias;
    } else {
      updated.alias = Number(patch.alias);
    }
  }

  // '' removes the subnet or the gateway.
  for (const key of ['subnet', 'gateway']) {
    if (patch[key] === '') {
      delete updated[key];
    } else if (patch[key] !== undefined) {
      updated[key] = patch[key];
    }
  }

  const next = {
    ...doc,
    networks: doc.networks.map((entry) => (entry.id === id ? updated : entry)),
  };

  return syncInterfaceVLANs(nameSwitches(next, id));
}

// A switch is named after its network: its label is the network's name,
// kept here for the places that name a node without the document. Every
// switch of the network is renamed with it.
function nameSwitches(doc, networkId) {
  const name = findNetwork(doc, networkId)?.name;

  if (name === undefined) {
    return doc;
  }

  return {
    ...doc,
    nodes: (doc.nodes || []).map((node) =>
      node.kind === 'switch' &&
      node.switch?.networkId === networkId &&
      node.label !== name
        ? { ...node, label: name }
        : node,
    ),
  };
}

// The document with every switch named after its network (see nameSwitches),
// as when a document with a stale switch label is opened.
export function namedSwitches(doc) {
  return (doc?.networks || []).reduce(
    (next, network) => nameSwitches(next, network.id),
    doc,
  );
}

/** Removes networks together with their switch nodes and edges. */
export function removeNetworks(doc, ids) {
  const removing = new Set(
    (ids || []).filter((id) => !removalRefusal(doc, 'networks', id)),
  );

  if (removing.size === 0) {
    return doc;
  }

  const switches = (doc.nodes || [])
    .filter(
      (node) => node.kind === 'switch' && removing.has(node.switch?.networkId),
    )
    .map((node) => node.id);

  const pruned = removeElements(doc, { nodes: switches });

  return {
    ...pruned,
    networks: pruned.networks.filter((network) => !removing.has(network.id)),
  };
}

// Whether a connection, or a device interface whose VLAN names it (see
// networkByName), uses the network. Switches do not count.
function networkInUse(doc, networkId) {
  if ((doc?.edges || []).some((edge) => edge.networkId === networkId)) {
    return true;
  }

  return (doc?.nodes || []).some(
    (node) =>
      node.kind === 'device' &&
      specInterfaces(node).some((iface) => {
        const vlan = String(iface?.vlan ?? '').trim();

        return vlan !== '' && networkByName(doc, vlan)?.id === networkId;
      }),
  );
}

// The ids of the networks that have a switch on the canvas.
function switchedNetworks(doc) {
  return new Set(
    (doc?.nodes || [])
      .filter((node) => node.kind === 'switch')
      .map((node) => node.switch?.networkId),
  );
}

// Removes each network whose last switch the edit from `before` to `after`
// removed, when nothing else uses it (see networkInUse). A network with no
// switch before the edit stays, so a document that was read in keeps its
// networks. A network that is still in use stays, and the Diagram checks
// report it (network.switch.missing).
function dropUnusedNetworks(before, after) {
  const had = switchedNetworks(before);
  const has = switchedNetworks(after);
  const dropped = new Set(
    (after.networks || [])
      .filter(
        (network) =>
          had.has(network.id) &&
          !has.has(network.id) &&
          !networkInUse(after, network.id),
      )
      .map((network) => network.id),
  );

  return dropped.size > 0
    ? {
        ...after,
        networks: after.networks.filter((network) => !dropped.has(network.id)),
      }
    : after;
}

// --- nodes -----------------------------------------------------------------

// The spec of a new device that is given none: the palette's plain Device.
// A device made from a template comes with the template's spec (see
// nodeOptionsFromTemplate in templates.js).
function deviceSpec(hostname) {
  return {
    type: 'VirtualMachine',
    general: {
      hostname,
      description: '',
      vm_type: 'kvm',
    },
    hardware: {
      os_type: 'linux',
      drives: [{ image: 'ubuntu.qc2' }],
    },
    network: { interfaces: [] },
  };
}

// A hostname that no device has, ignoring case: `wanted` with white space as
// dashes, then -2, -3 and so on when it is taken.
export function uniqueHostname(doc, wanted) {
  const base =
    String(wanted || 'node')
      .trim()
      .replace(/\s+/g, '-') || 'node';

  return uniqueName(
    base,
    (doc.nodes || [])
      .filter((node) => node.kind === 'device')
      .map((node) => String(node.device?.hostname ?? '')),
    foldKey,
  );
}

// Adds a node of any kind. A switch without a network creates one. `options`
// takes the node fields and the payload fields of its kind, such as a device's
// look (LOOK_KEYS) or a line's points, relative to `position`.
export function addNode(doc, options = {}) {
  const kind = options.kind || 'device';
  const position = {
    x: options.position?.x ?? 0,
    y: options.position?.y ?? 0,
  };

  const node = {
    id: options.id || newId(),
    kind,
    label: options.label || '',
    position,
  };

  if (options.size) {
    node.size = { ...options.size };
  }

  if (options.parentId) {
    node.parentId = options.parentId;
  }

  let next = doc;
  let network;

  switch (kind) {
    case 'device': {
      const hostname = uniqueHostname(
        doc,
        options.hostname || options.label || 'node',
      );
      const spec = options.spec ? deepCopy(options.spec) : deviceSpec(hostname);

      setSpecHostname(spec, hostname);

      const look = lookOf(options.look);

      node.label = node.label || hostname;
      node.device = {
        hostname,
        iconKey: pickIconKey(look.iconKey || options.iconKey, spec),
        spec,
        interfaces: [],
      };

      for (const key of OPTIONAL_LOOK_KEYS) {
        if (look[key]) {
          node.device[key] = look[key];
        }
      }

      if (isLayerId(options.layer)) {
        node.device.layer = options.layer;
      }

      // A template keeps it with the look, a paste as an option.
      if (
        options.hideDescription === true ||
        options.look?.hideDescription === true
      ) {
        node.device.hideDescription = true;
      }

      const wanted = Array.isArray(options.interfaces)
        ? options.interfaces
        : [];

      wanted.forEach((iface) => {
        appendInterface(node, iface);
      });

      break;
    }
    case 'switch': {
      if (options.networkId && findNetwork(doc, options.networkId)) {
        node.switch = { networkId: options.networkId };
      } else {
        // A new network gets a subnet of its own (see suggestedSubnet).
        const created = addNetwork(doc, {
          name: options.networkName || options.label || 'EXP',
          subnet: suggestedSubnet(doc),
        });

        next = created.doc;
        network = created.network;
        node.switch = { networkId: network.id };
      }

      for (const key of SWITCH_OPTIONAL_KEYS) {
        if (options[key] && key !== 'layer') {
          node.switch[key] = options[key];
        }
      }

      // As for a device, a value that is not a layer ID is no layer.
      if (isLayerId(options.layer)) {
        node.switch.layer = options.layer;
      }

      // Named after its network (see nameSwitches), whatever label it came
      // with.
      const bound = network || findNetwork(next, node.switch.networkId);
      node.label = bound.name;
      break;
    }
    case 'note':
      // Named after its first line (see nodeLabel) unless given a label.
      node.note = { text: options.text || '', color: options.color || '' };
      break;
    case 'group':
      // Numbered past the groups there are, so each has a name of its own
      // on the canvas, in its forms and in its announcements.
      node.group = {
        title:
          options.title ||
          options.label ||
          uniqueName(
            'Group',
            (doc.nodes || [])
              .filter((entry) => entry.kind === 'group')
              .map((entry) => entry.group?.title || entry.label),
            (value) => value,
            ' ',
          ),
        color: options.color || '',
        collapsed: false,
      };

      for (const key of GROUP_OPTIONAL_KEYS) {
        if (options[key]) {
          node.group[key] = options[key];
        }
      }

      if (options.hideDescription === true) {
        node.group.hideDescription = true;
      }

      node.label = node.label || node.group.title;
      break;
    case 'shape': {
      const figure = SHAPE_FIGURES.includes(options.shape)
        ? options.shape
        : SHAPE_FIGURES[0];

      node.shape = dropEmpty(
        {
          shape: figure,
          ...Object.fromEntries(
            SHAPE_OPTIONAL_KEYS.map((key) => [key, options[key]]),
          ),
        },
        SHAPE_OPTIONAL_KEYS,
      );
      node.size = node.size || {
        ...(figure === 'circle' ? CIRCLE_SIZE : DEFAULT_SIZES.shape),
      };
      node.label = node.shape.label || '';
      break;
    }
    case 'icon':
      // Exactly one icon: a custom one in place of a built-in key.
      node.icon = dropEmpty(
        {
          iconKey: options.icon
            ? ''
            : isIconKey(options.iconKey)
              ? options.iconKey
              : DEFAULT_DRAWN_ICON,
          icon: options.icon || '',
          label: options.label || '',
        },
        ICON_OPTIONAL_KEYS,
      );
      node.size = node.size || { ...DEFAULT_SIZES.icon };
      node.label = node.icon.label || '';
      break;
    case 'line': {
      const points = linePoints(options.points)
        ? options.points
        : [
            { x: 0, y: 0 },
            { x: NEW_LINE_LENGTH, y: 0 },
          ];
      const placed = placedLine(position, points);

      node.position = placed.position;
      node.size = placed.size;
      node.line = dropEmpty(
        {
          points: placed.points,
          ...Object.fromEntries(
            LINE_OPTIONAL_KEYS.map((key) => [key, options[key]]),
          ),
        },
        LINE_OPTIONAL_KEYS,
      );

      for (const key of LINE_ARROW_KEYS) {
        if (options[key] === true) {
          node.line[key] = true;
        }
      }

      node.label = node.line.label || '';
      break;
    }
    default:
      throw new Error(`Unknown node kind: ${kind}`);
  }

  return {
    doc: { ...next, nodes: [...(next.nodes || []), node] },
    node,
    network,
  };
}

function pickIconKey(wanted, spec) {
  if (isIconKey(wanted)) {
    return wanted;
  }

  return iconKeyForSpec(spec);
}

function setSpecHostname(spec, hostname) {
  if (!spec.general || typeof spec.general !== 'object') {
    spec.general = {};
  }

  spec.general.hostname = hostname;
}

// Updates node presentation and payload. `options.snap` is the user's Snap to
// grid setting, for a switch that a changed VLAN adds (see connectByVLAN).
export function updateNode(doc, id, patch = {}, options = {}) {
  const node = findNode(doc, id);

  if (!node) {
    return doc;
  }

  // An included device keeps its name and spec. Its position belongs to the
  // diagram.
  if (includedFrom(node)) {
    patch = { position: patch.position, size: patch.size };
  }

  const updated = { ...node };

  if (patch.label !== undefined) {
    updated.label = patch.label;
  }

  if (patch.position) {
    updated.position = { x: patch.position.x, y: patch.position.y };
  }

  if (patch.size !== undefined) {
    if (patch.size === null) {
      delete updated.size;
    } else {
      updated.size = { width: patch.size.width, height: patch.size.height };
    }
  }

  if (patch.device && node.kind === 'device') {
    const device = { ...node.device, ...patch.device };

    if (patch.device.spec) {
      device.spec = deepCopy(patch.device.spec);
    } else if (node.device.spec) {
      device.spec = deepCopy(node.device.spec);
    }

    if (patch.device.hostname !== undefined) {
      device.hostname = patch.device.hostname;
    } else if (device.spec?.general?.hostname) {
      device.hostname = device.spec.general.hostname;
    }

    setSpecHostname(device.spec, device.hostname);

    if (patch.device.interfaces) {
      device.interfaces = patch.device.interfaces.map((handle, index) => ({
        id: handle.id || newId(),
        name: handle.name,
        index: handle.index ?? index,
      }));
    }

    const networks = handleNetworks(doc);

    reconcileDeviceHandles(
      device,
      (handleId, vlan) =>
        networks.get(handleId)?.id === networkByName(doc, vlan)?.id,
    );

    // An emptied color is no color, an emptied custom icon none, and an
    // emptied layer none.
    updated.device = dropEmpty(device, [...OPTIONAL_LOOK_KEYS, 'layer']);
    updated.label = patch.label !== undefined ? patch.label : device.hostname;
  }

  // A patch that names no network keeps the switch on its own network. A
  // patch that names no color or icon size keeps the current values. An
  // emptied color is no color, and an emptied icon size is the document's
  // size. Thus the document's bytes are those of a switch that never had them.
  if (patch.switch && node.kind === 'switch') {
    updated.switch = dropEmpty(
      {
        ...node.switch,
        ...patch.switch,
        networkId: patch.switch.networkId ?? node.switch?.networkId,
      },
      SWITCH_OPTIONAL_KEYS,
    );
  }

  // Named after its network (see nameSwitches), whatever the patch says.
  if (node.kind === 'switch') {
    updated.label =
      findNetwork(doc, updated.switch?.networkId)?.name ?? updated.label;
  }

  if (patch.note && node.kind === 'note') {
    updated.note = { ...node.note, ...patch.note };
  }

  if (patch.group && node.kind === 'group') {
    updated.group = dropEmpty(
      { ...node.group, ...patch.group },
      GROUP_OPTIONAL_KEYS,
    );

    if (patch.group.title !== undefined && patch.label === undefined) {
      updated.label = patch.group.title;
    }
  }

  // A drawing's label is its payload's, and an emptied field is none.
  if (patch.shape && node.kind === 'shape') {
    updated.shape = dropEmpty(
      { ...node.shape, ...patch.shape },
      SHAPE_OPTIONAL_KEYS,
    );

    if (!SHAPE_FIGURES.includes(updated.shape.shape)) {
      updated.shape.shape = node.shape?.shape || SHAPE_FIGURES[0];
    }

    updated.label = updated.shape.label || '';
  }

  if (patch.icon && node.kind === 'icon') {
    updated.icon = drawnIcon(node.icon, patch.icon);
    updated.label = updated.icon.label || '';
  }

  if (node.kind === 'line') {
    updated.line = { ...node.line };

    if (patch.line) {
      updated.line = dropEmpty(
        { ...node.line, ...patch.line },
        LINE_OPTIONAL_KEYS,
      );

      for (const key of LINE_ARROW_KEYS) {
        if (updated.line[key] !== true) {
          delete updated.line[key];
        }
      }

      if (!linePoints(updated.line.points)) {
        updated.line.points = node.line?.points || [];
      }

      updated.label = updated.line.label || '';
    }

    // The position is the points' top left corner, and the size their box,
    // whatever the patch says of either.
    if (linePoints(updated.line.points)) {
      const placed = placedLine(updated.position, updated.line.points);

      updated.position = placed.position;
      updated.size = placed.size;
      updated.line.points = placed.points;
    }
  }

  const next = {
    ...doc,
    nodes: doc.nodes.map((entry) => (entry.id === id ? updated : entry)),
  };

  if (node.kind !== 'device') {
    return dropStaleRoutes(doc, next);
  }

  const live = new Set(deviceHandles(updated).map((handle) => handle.id));
  const pruned = {
    ...next,
    edges: (next.edges || []).filter((edge) => {
      const handleId = edge.sourceHandleId || edge.targetHandleId;
      const touchesNode = edge.sourceNodeId === id || edge.targetNodeId === id;

      return !touchesNode || !handleId || live.has(handleId);
    }),
  };

  // A spec applied from the Inspector sets the connections its changed
  // VLANs name.
  return dropStaleRoutes(
    doc,
    syncInterfaceVLANs(
      patch.device?.spec ? connectByVLAN(pruned, id, node, options) : pruned,
    ),
  );
}

// Aligns a device's handles (mutated) with its spec's interfaces by name, the
// only stable key in a spec. With equal counts, a renamed interface keeps the
// handle, and so the connection, of a lost name whose network its VLAN names.
function reconcileDeviceHandles(device, sameNetwork = () => true) {
  const handles = device.interfaces || [];
  // A spec is free-form: interfaces that are no list hold none.
  const listed = device.spec?.network?.interfaces;
  const specInterfaces = (Array.isArray(listed) ? listed : []).filter(
    (iface) => typeof iface?.name === 'string' && iface.name !== '',
  );
  const specNames = specInterfaces.map((iface) => iface.name);

  if (specNames.length === 0 && handles.length === 0) {
    return;
  }

  const byName = new Map(handles.map((handle) => [handle.name, handle]));
  const renamed = handles.filter((handle) => !specNames.includes(handle.name));
  const unnamed = specInterfaces.filter((iface) => !byName.has(iface.name));

  if (specNames.length === handles.length) {
    unnamed.forEach((iface) => {
      const index = renamed.findIndex((handle) =>
        sameNetwork(handle.id, iface.vlan),
      );

      if (index >= 0) {
        byName.set(iface.name, renamed.splice(index, 1)[0]);
      }
    });
  }

  const used = new Set();

  device.interfaces = specNames.map((name, index) => {
    const existing = byName.get(name);

    if (!existing || used.has(existing.id)) {
      return { id: newId(), name, index };
    }

    used.add(existing.id);

    return { ...existing, name, index };
  });
}

// Whether a node's connections meet it at the same points as before: same
// position, size and group, and for a device the same interfaces in the
// same order, because they set the spacing of its handles along its sides.
function sameAnchors(a, b) {
  const sizeA = sizeOf(a);
  const sizeB = sizeOf(b);
  const handles = (node) =>
    deviceHandles(node)
      .map((handle) => handle.id)
      .join('\n');

  return (
    a.position?.x === b.position?.x &&
    a.position?.y === b.position?.y &&
    sizeA.width === sizeB.width &&
    sizeA.height === sizeB.height &&
    (a.parentId || '') === (b.parentId || '') &&
    handles(a) === handles(b)
  );
}

// Drops edge.route from each connection whose end nodes moved, resized, changed
// group or interfaces, or whose handles changed between `before` and `after`.
function dropStaleRoutes(before, after) {
  const edges = after?.edges || [];

  if (before === after || !edges.some((edge) => edge.route)) {
    return after;
  }

  const was = new Map((before?.nodes || []).map((node) => [node.id, node]));
  const moved = new Set();

  for (const node of after.nodes || []) {
    const old = was.get(node.id);

    if (old !== node && (!old || !sameAnchors(old, node))) {
      moved.add(node.id);
    }
  }

  const wasEdge = new Map((before?.edges || []).map((edge) => [edge.id, edge]));
  let dropped = false;
  const kept = edges.map((edge) => {
    const old = wasEdge.get(edge.id);
    const stale =
      edge.route &&
      (moved.has(edge.sourceNodeId) ||
        moved.has(edge.targetNodeId) ||
        !old ||
        old.sourceNodeId !== edge.sourceNodeId ||
        old.targetNodeId !== edge.targetNodeId ||
        (old.sourceHandleId || '') !== (edge.sourceHandleId || '') ||
        (old.targetHandleId || '') !== (edge.targetHandleId || ''));

    if (!stale) {
      return edge;
    }

    const rest = { ...edge };

    delete rest.route;
    dropped = true;

    return rest;
  });

  return dropped ? { ...after, edges: kept } : after;
}

// Moves several nodes at once, so a multi-node drag is one history commit.
export function moveNodes(doc, moves = []) {
  if (moves.length === 0) {
    return doc;
  }

  const byId = new Map(moves.map((move) => [move.id, move.position]));

  // Positions are absolute, so a group that moves must move its members (and
  // their members). Members that are moved explicitly keep their new position.
  const shift = new Map();
  const nodes = doc.nodes || [];

  for (const node of nodes) {
    const position = byId.get(node.id);

    if (node.kind !== 'group' || !position) {
      continue;
    }

    const dx = position.x - node.position.x;
    const dy = position.y - node.position.y;

    if (dx === 0 && dy === 0) {
      continue;
    }

    for (const member of descendantsOf(nodes, node.id)) {
      if (!byId.has(member.id) && !shift.has(member.id)) {
        shift.set(member.id, { dx, dy });
      }
    }
  }

  return dropStaleRoutes(doc, {
    ...doc,
    nodes: nodes.map((node) => {
      const position = byId.get(node.id);

      if (position) {
        return { ...node, position: { x: position.x, y: position.y } };
      }

      const delta = shift.get(node.id);

      return delta
        ? {
            ...node,
            position: {
              x: node.position.x + delta.dx,
              y: node.position.y + delta.dy,
            },
          }
        : node;
    }),
  });
}

// Every node nested under a group, at any depth.
function descendantsOf(nodes, groupId) {
  const found = [];
  const queue = [groupId];

  while (queue.length) {
    const parent = queue.shift();

    for (const node of nodes) {
      if (node.parentId === parent && !found.includes(node)) {
        found.push(node);
        queue.push(node.id);
      }
    }
  }

  return found;
}

/** The document with a node at a new size. */
export function resizeNode(doc, id, size) {
  return updateNode(doc, id, { size });
}

// Sets a line's points, relative to its position, then places it (placedLine).
// Points that a line cannot have change nothing.
export function setLinePoints(doc, id, points) {
  const node = findNode(doc, id);

  if (node?.kind !== 'line' || !linePoints(points)) {
    return doc;
  }

  return updateNode(doc, id, {
    line: { points: points.map((point) => ({ x: point.x, y: point.y })) },
  });
}

// Moves one point of a line, an end or a bend, to a point relative to the
// line node's current position.
export function moveLinePoint(doc, id, index, point) {
  const points = findNode(doc, id)?.line?.points || [];

  if (index < 0 || index >= points.length) {
    return doc;
  }

  return setLinePoints(
    doc,
    id,
    points.map((entry, at) => (at === index ? point : entry)),
  );
}

// Adds a bend in the segment that ends at point `index` (1 to the count less
// one), at its middle unless a point is given. A full line takes no more.
export function insertLinePoint(doc, id, index, point) {
  const points = findNode(doc, id)?.line?.points || [];

  if (index < 1 || index >= points.length) {
    return doc;
  }

  const before = points[index - 1];
  const after = points[index];
  const bend = point || {
    x: (before.x + after.x) / 2,
    y: (before.y + after.y) / 2,
  };

  return setLinePoints(doc, id, [
    ...points.slice(0, index),
    bend,
    ...points.slice(index),
  ]);
}

// The segment of a line nearest a point, as the index of the point that ends
// it (insertLinePoint's form). 1 for a line of fewer than two points.
export function nearestSegment(points, point) {
  let best = 1;
  let bestDistance = Infinity;

  for (let index = 1; index < (points?.length || 0); index += 1) {
    const from = points[index - 1];
    const to = points[index];
    const dx = to.x - from.x;
    const dy = to.y - from.y;
    const length = dx * dx + dy * dy;
    // How far along the segment the point's foot is, from 0 to 1.
    const along =
      length > 0
        ? Math.min(
            1,
            Math.max(
              0,
              ((point.x - from.x) * dx + (point.y - from.y) * dy) / length,
            ),
          )
        : 0;
    const distance = Math.hypot(
      from.x + along * dx - point.x,
      from.y + along * dy - point.y,
    );

    if (distance < bestDistance) {
      best = index;
      bestDistance = distance;
    }
  }

  return best;
}

// A line point's name for its handle and announcements: "the start of uplink",
// "bend 2 of uplink" or "the end of uplink".
export function linePointName(node, index) {
  const count = node?.line?.points?.length || 0;
  const name = nodeLabel(node);

  if (index === 0) {
    return `the start of ${name}`;
  }

  return index === count - 1
    ? `the end of ${name}`
    : `bend ${index} of ${name}`;
}

// A line point, relative to the line's position, on the grid while
// `grid.snap` (the user's Snap to grid setting) is on, else on a whole pixel.
export function snappedLinePoint(node, point, grid) {
  const size = snapStep({ grid }, grid?.snap === true);
  const origin = node?.position || { x: 0, y: 0 };
  const snap = (value, start) =>
    size > 0 ? onStep(start + value, size) - start : onStep(value, 0);

  return { x: snap(point.x, origin.x), y: snap(point.y, origin.y) };
}

// How far an arrow key moves a point of a line with Shift, in pixels.
const LINE_POINT_FINE_STEP = 1;
const LINE_POINT_ARROWS = {
  ArrowLeft: [-1, 0],
  ArrowRight: [1, 0],
  ArrowUp: [0, -1],
  ArrowDown: [0, 1],
};

// What a key does on the focused handle of a line point (LineNode.vue): an
// arrow moves it a grid step, or a pixel with Shift. Delete or Backspace
// removes it. Escape goes to the line. An arrow key with Alt, Ctrl or ⌘, and
// any other key, gives null.
export function linePointKey(node, index, event, grid) {
  if (event.key === 'Delete' || event.key === 'Backspace') {
    return { action: 'remove' };
  }

  if (event.key === 'Escape') {
    return { action: 'leave' };
  }

  const arrow = LINE_POINT_ARROWS[event.key];
  const point = node?.line?.points?.[index];

  if (!arrow || !point || event.altKey || event.ctrlKey || event.metaKey) {
    return null;
  }

  const step = event.shiftKey
    ? LINE_POINT_FINE_STEP
    : grid?.size || DEFAULT_GRID_SIZE;
  const target = {
    x: point.x + arrow[0] * step,
    y: point.y + arrow[1] * step,
  };

  return {
    action: 'move',
    point: event.shiftKey ? target : snappedLinePoint(node, target, grid),
  };
}

/** Removes a point of a line, and never leaves fewer than MIN_LINE_POINTS. */
export function removeLinePoint(doc, id, index) {
  const points = findNode(doc, id)?.line?.points || [];

  return setLinePoints(
    doc,
    id,
    points.filter((_, at) => at !== index),
  );
}

// Room between a group's border and its members, as groupNodes leaves it.
const GROUP_PADDING = 40;
// Room kept around a node that a change of group moves.
const GROUP_GAP = 20;
// A group is never resized smaller than this, so its title stays readable.
const GROUP_MIN_SIZE = { width: 120, height: 80 };

function boxOf(node) {
  return { ...node.position, ...sizeOf(node) };
}

function overlaps(a, b) {
  return (
    a.x < b.x + b.width &&
    b.x < a.x + a.width &&
    a.y < b.y + b.height &&
    b.y < a.y + a.height
  );
}

function contains(outer, inner) {
  return (
    inner.x >= outer.x &&
    inner.y >= outer.y &&
    inner.x + inner.width <= outer.x + outer.width &&
    inner.y + inner.height <= outer.y + outer.height
  );
}

// A node's groups, nearest first.
function ancestorsOf(doc, node) {
  const found = [];
  let current = node;

  while (current?.parentId) {
    const parent = findNode(doc, current.parentId);

    if (!parent || found.includes(parent)) {
      break;
    }

    found.push(parent);
    current = parent;
  }

  return found;
}

// Clear space freeSpot keeps between the node it places and the nodes
// already there.
const CLEARANCE = 20;
// Enough slots for any diagram: a walk stops at the first free one.
const MAX_SLOTS = 10000;

// The first slot of a grid walk, in reading order, where a node of `size`
// overlaps no node outside `ignore`, with space to spare. The walk ends below
// the `within` box. Null when no slot is free.
export function freeSpot(
  doc,
  size,
  { origin, slot, columns, within = null, ignore = [], snap = false },
) {
  const skip = new Set(ignore);
  const boxes = (doc?.nodes || [])
    .filter((node) => !skip.has(node.id))
    .map(boxOf);
  const step = snapStep(doc, snap);
  const across = Math.max(1, Math.floor(columns) || 1);

  for (let index = 0; index < MAX_SLOTS; index += 1) {
    const position = {
      x: onStep(origin.x + (index % across) * slot.width, step),
      y: onStep(origin.y + Math.floor(index / across) * slot.height, step),
    };

    if (within && position.y + size.height > within.y + within.height) {
      return null;
    }

    if (within && !contains(within, { ...position, ...size })) {
      continue;
    }

    const room = {
      x: position.x - CLEARANCE,
      y: position.y - CLEARANCE,
      width: size.width + CLEARANCE * 2,
      height: size.height + CLEARANCE * 2,
    };

    if (!boxes.some((box) => overlaps(room, box))) {
      return position;
    }
  }

  return null;
}

// The ids of a node, the nodes nested under it and the groups it is in.
function familyOf(doc, node) {
  return [
    node.id,
    ...descendantsOf(doc.nodes || [], node.id).map((entry) => entry.id),
    ...ancestorsOf(doc, node).map((entry) => entry.id),
  ];
}

// A walk for freeSpot of slots the size of `box`, with a gap between them.
function slotsFor(box, origin, columns) {
  return {
    origin,
    slot: { width: box.width + GROUP_GAP, height: box.height + GROUP_GAP },
    columns,
  };
}

// Where a node that moves to group `target` (null for none) goes, and whether
// that group must grow: a free spot in it or below its members, or a free spot
// beside the outermost group it leaves. Position is null when it can stay.
function regroupedPosition(doc, node, target, options = {}) {
  const box = boxOf(node);
  const group = target ? findNode(doc, target) : null;
  const staying = new Set(group ? [group, ...ancestorsOf(doc, group)] : []);
  const left = ancestorsOf(doc, node).filter((entry) => !staying.has(entry));
  const clear = left.every((entry) => !overlaps(box, boxOf(entry)));
  const moving = [
    node.id,
    ...descendantsOf(doc.nodes || [], node.id).map((entry) => entry.id),
  ];

  if (group) {
    if (clear && contains(boxOf(group), box)) {
      return { position: null, grow: false };
    }

    const room = boxOf(group);
    const inner = {
      x: room.x + GROUP_PADDING,
      y: room.y + GROUP_PADDING,
      width: room.width - GROUP_PADDING * 2,
      height: room.height - GROUP_PADDING * 2,
    };
    const spot =
      inner.width >= box.width && inner.height >= box.height
        ? freeSpot(doc, box, {
            ...slotsFor(
              box,
              inner,
              (inner.width + GROUP_GAP) / (box.width + GROUP_GAP),
            ),
            within: inner,
            ignore: [...moving, ...[...staying].map((entry) => entry.id)],
            snap: options.snap,
          })
        : null;

    if (spot) {
      return { position: spot, grow: false };
    }

    const members = (doc.nodes || []).filter(
      (entry) => entry.parentId === group.id && entry.id !== node.id,
    );

    return {
      position: {
        x: group.position.x + GROUP_PADDING,
        y: members.length
          ? Math.max(
              ...members.map(
                (entry) => entry.position.y + sizeOf(entry).height,
              ),
            ) + GROUP_GAP
          : group.position.y + GROUP_PADDING,
      },
      grow: true,
    };
  }

  if (clear) {
    return { position: null, grow: false };
  }

  // Beside the outermost group it leaves, level with where it was, or the
  // first free spot after that.
  const outer = boxOf(left[left.length - 1]);

  return {
    position: freeSpot(doc, box, {
      ...slotsFor(box, { x: outer.x + outer.width + GROUP_GAP, y: box.y }, 4),
      ignore: moving,
      snap: options.snap,
    }),
    grow: false,
  };
}

// Moves the nodes that a grown group covers out of its way, each to the
// first free spot below it (on the grid while `options.snap` is on), with
// their contents. A node in a group that holds the grown group can stay in
// that group, which in turn grows to hold it (see fitGroups, which takes
// `options`).
function clearGroup(doc, id, options = {}) {
  const group = findNode(doc, id);

  if (!group) {
    return doc;
  }

  const box = boxOf(group);
  const family = new Set(familyOf(doc, group));
  const covered = (doc.nodes || []).filter(
    (entry) => !family.has(entry.id) && overlaps(box, boxOf(entry)),
  );
  const outermost = covered.filter(
    (entry) =>
      !ancestorsOf(doc, entry).some((other) => covered.includes(other)),
  );
  let next = doc;

  for (const entry of outermost) {
    const current = findNode(next, entry.id);
    const own = boxOf(current);
    const position = freeSpot(next, own, {
      ...slotsFor(own, { x: own.x, y: box.y + box.height + GROUP_GAP }, 4),
      ignore: familyOf(next, current),
      snap: options.snap,
    });

    if (position) {
      next = moveNodes(next, [{ id: entry.id, position }]);

      if (current.parentId) {
        next = fitGroups(next, [current.parentId]);
      }
    }
  }

  return next;
}

// Grows each group, and the groups that hold it, to hold its members
// (nodesBounds) with space to spare. Groups never shrink here.
export function fitGroups(doc, ids = []) {
  let next = doc;
  const queue = [...ids];
  const seen = new Set();

  while (queue.length) {
    const id = queue.shift();
    const group = findNode(next, id);

    if (seen.has(id) || group?.kind !== 'group') {
      continue;
    }

    seen.add(id);

    const members = next.nodes.filter((entry) => entry.parentId === id);

    if (members.length) {
      const need = nodesBounds(members, GROUP_PADDING);
      const has = boxOf(group);
      const x = Math.min(has.x, need.x);
      const y = Math.min(has.y, need.y);
      const width = Math.max(has.x + has.width, need.x + need.width) - x;
      const height = Math.max(has.y + has.height, need.y + need.height) - y;

      if (
        x !== has.x ||
        y !== has.y ||
        width !== has.width ||
        height !== has.height
      ) {
        next = {
          ...next,
          nodes: next.nodes.map((entry) =>
            entry.id === id
              ? { ...entry, position: { x, y }, size: { width, height } }
              : entry,
          ),
        };
      }
    }

    if (group.parentId) {
      queue.push(group.parentId);
    }
  }

  return next;
}

// The smallest size of a group that holds its members (see fitGroups) with a
// grid step to spare, and keeps its title readable.
export function groupMinimumSize(doc, id) {
  const group = findNode(doc, id);
  const members = (doc?.nodes || []).filter((entry) => entry.parentId === id);
  const spare = doc?.grid?.size || DEFAULT_GRID_SIZE;

  if (!group || members.length === 0) {
    return { ...GROUP_MIN_SIZE };
  }

  const bounds = nodesBounds(members);

  return {
    width: Math.max(
      GROUP_MIN_SIZE.width,
      bounds.x + bounds.width + spare - group.position.x,
    ),
    height: Math.max(
      GROUP_MIN_SIZE.height,
      bounds.y + bounds.height + spare - group.position.y,
    ),
  };
}

// The kinds of nodes that can be resized, with the mouse or the keyboard, and
// the minimum size of each kind except group: enough for its label, or for an
// icon to show.
const RESIZABLE_KINDS = ['note', 'group', 'shape', 'icon'];
export const MINIMUM_SIZES = {
  note: { width: 80, height: 48 },
  shape: { width: 16, height: 16 },
  icon: { width: 24, height: 24 },
};

// The smallest size of a node: a group's holds its members (groupMinimumSize),
// and a note, shape or icon has a fixed minimum.
export function minimumSize(doc, id) {
  const node = findNode(doc, id);

  if (node?.kind === 'group') {
    return groupMinimumSize(doc, id);
  }

  return { ...(MINIMUM_SIZES[node?.kind] || MINIMUM_SIZES.shape) };
}

// How each arrow key resizes a node from its bottom right corner.
const RESIZE_ARROWS = {
  ArrowRight: [1, 0],
  ArrowLeft: [-1, 0],
  ArrowDown: [0, 1],
  ArrowUp: [0, -1],
};

// The size that Alt+Shift and an arrow key give a resizable node, from its
// bottom right corner (BuilderCanvas.vue). Shrinking stops at minimumSize. Null
// when the node cannot resize or the key is not an arrow.
export function keyResizedSize(doc, id, key, step) {
  const node = findNode(doc, id);
  const arrow = RESIZE_ARROWS[key];

  if (!RESIZABLE_KINDS.includes(node?.kind) || !arrow) {
    return null;
  }

  const size = sizeOf(node);
  const least = minimumSize(doc, id);
  const resized = (now, delta, min) =>
    delta < 0 ? Math.min(now, Math.max(min, now + delta)) : now + delta;

  return {
    width: resized(size.width, arrow[0] * step, least.width),
    height: resized(size.height, arrow[1] * step, least.height),
  };
}

// The box a node resized with the mouse takes: the dragged box in whole pixels,
// at least its minimum size. A group also grows to hold its members.
export function resizedBox(doc, id, box) {
  const node = findNode(doc, id);
  const least = MINIMUM_SIZES[node?.kind] || GROUP_MIN_SIZE;
  let left = onStep(box.x, 0);
  let top = onStep(box.y, 0);
  let right = left + Math.max(least.width, Math.round(box.width));
  let bottom = top + Math.max(least.height, Math.round(box.height));
  const members = (doc?.nodes || []).filter((entry) => entry.parentId === id);

  if (node?.kind === 'group' && members.length) {
    const spare = doc?.grid?.size || DEFAULT_GRID_SIZE;
    const need = nodesBounds(members, spare);

    left = Math.min(left, need.x);
    top = Math.min(top, need.y);
    right = Math.max(right, need.x + need.width);
    bottom = Math.max(bottom, need.y + need.height);
  }

  return {
    position: { x: left, y: top },
    size: { width: right - left, height: bottom - top },
  };
}

// Moves a node into a group, into another group or out (null). It refuses
// cycles and parents that are not groups. The node moves to a free spot, and
// groups grow and clear space, so no node overlaps a group that it is not in.
export function setParent(doc, id, parentId, options = {}) {
  const node = findNode(doc, id);
  const target = parentId || null;

  if (!node || (node.parentId || null) === target) {
    return doc;
  }

  if (target) {
    const parent = findNode(doc, target);

    if (
      !parent ||
      parent.kind !== 'group' ||
      target === id ||
      isDescendant(doc, target, id)
    ) {
      return doc;
    }
  }

  const { position, grow } = regroupedPosition(doc, node, target, options);
  const regrouped = {
    ...doc,
    nodes: doc.nodes.map((entry) => {
      if (entry.id !== id) {
        return entry;
      }

      const updated = { ...entry };

      if (target) {
        updated.parentId = target;
      } else {
        delete updated.parentId;
      }

      return updated;
    }),
  };
  let next = position ? moveNodes(regrouped, [{ id, position }]) : regrouped;

  if (!grow) {
    return dropStaleRoutes(doc, next);
  }

  // The group, and each group it is in, grows to hold the node, and moves
  // what it then covers out of its way, innermost first.
  const joined = findNode(next, target);

  for (const group of [joined, ...ancestorsOf(next, joined)]) {
    next = clearGroup(fitGroups(next, [group.id]), group.id, options);
  }

  return dropStaleRoutes(doc, next);
}

/** Whether `candidateId` is nested under `ancestorId`, at any depth. */
export function isDescendant(doc, candidateId, ancestorId) {
  let current = findNode(doc, candidateId);
  const seen = new Set();

  while (current?.parentId) {
    if (current.parentId === ancestorId) {
      return true;
    }

    if (seen.has(current.parentId)) {
      return false;
    }

    seen.add(current.parentId);
    current = findNode(doc, current.parentId);
  }

  return false;
}

// Wraps nodes in a new group node, around the nodes (see fitGroups).
export function groupNodes(doc, ids, options = {}) {
  const members = (ids || [])
    .map((id) => findNode(doc, id))
    .filter((node) => node && !node.parentId);

  if (members.length === 0) {
    return { doc, group: null };
  }

  const padding = options.padding ?? 40;
  const bounds = nodesBounds(members, padding);

  const created = addNode(doc, {
    kind: 'group',
    title: options.title,
    position: { x: bounds.x, y: bounds.y },
    size: { width: bounds.width, height: bounds.height },
  });

  const memberIds = new Set(members.map((node) => node.id));

  return {
    doc: dropStaleRoutes(doc, {
      ...created.doc,
      nodes: created.doc.nodes.map((node) =>
        memberIds.has(node.id) ? { ...node, parentId: created.node.id } : node,
      ),
    }),
    group: created.node,
  };
}

/** Removes a group node and moves its members to the group's own parent. */
export function ungroup(doc, groupId) {
  const group = findNode(doc, groupId);

  if (!group || group.kind !== 'group') {
    return doc;
  }

  return dropStaleRoutes(doc, {
    ...doc,
    nodes: doc.nodes
      .filter((node) => node.id !== groupId)
      .map((node) => {
        if (node.parentId !== groupId) {
          return node;
        }

        const updated = { ...node };

        if (group.parentId) {
          updated.parentId = group.parentId;
        } else {
          delete updated.parentId;
        }

        return updated;
      }),
  });
}

// The nodes a removal of `ids` takes: those nodes and every node nested in
// them, less what would change an included device (see removalRefusal).
// `kept` holds the refusal of each node left in place.
function nodeRemoval(doc, ids = []) {
  const removing = new Set(ids);

  let changed = true;

  while (changed) {
    changed = false;
    (doc.nodes || []).forEach((node) => {
      if (
        node.parentId &&
        removing.has(node.parentId) &&
        !removing.has(node.id)
      ) {
        removing.add(node.id);
        changed = true;
      }
    });
  }

  const kept = [];

  removing.forEach((id) => {
    const reason = removalRefusal(doc, 'nodes', id);

    if (reason) {
      removing.delete(id);
      kept.push(reason);
    }
  });

  return { removing, kept };
}

// Why a removal keeps some targets: the refusal (removalRefusal) of each
// selected connection, node or nested node that removeElements keeps.
export function removalKept(doc, selection = {}) {
  return [
    ...nodeRemoval(doc, selection.nodes).kept,
    ...(selection.edges || [])
      .map((id) => removalRefusal(doc, 'edges', id))
      .filter(Boolean),
  ];
}

// Removes nodes with their descendants, edges and networks. A network whose
// last switch goes, and that nothing else uses, goes too (dropUnusedNetworks).
export function removeElements(doc, selection = {}) {
  const { removing } = nodeRemoval(doc, selection.nodes);

  // A node left inside a removed group, such as an included device, moves to
  // the nearest group that stays, as ungroup does.
  const survivingParent = (node) => {
    let parentId = node.parentId;
    const seen = new Set();

    while (parentId && removing.has(parentId) && !seen.has(parentId)) {
      seen.add(parentId);
      parentId = findNode(doc, parentId)?.parentId;
    }

    return parentId && !removing.has(parentId) ? parentId : '';
  };

  const removedEdges = new Set(
    (selection.edges || []).filter((id) => !removalRefusal(doc, 'edges', id)),
  );
  const nodes = (doc.nodes || [])
    .filter((node) => !removing.has(node.id))
    .map((node) => {
      if (!removing.has(node.parentId)) {
        return node;
      }

      const kept = { ...node };
      const parentId = survivingParent(node);

      if (parentId) {
        kept.parentId = parentId;
      } else {
        delete kept.parentId;
      }

      return kept;
    });
  const edges = (doc.edges || []).filter(
    (edge) =>
      !removedEdges.has(edge.id) &&
      !removing.has(edge.sourceNodeId) &&
      !removing.has(edge.targetNodeId),
  );

  const next = { ...doc, nodes, edges };
  const networks = new Set(selection.networks || []);

  const pruned = networks.size > 0 ? removeNetworks(next, [...networks]) : next;
  const remaining = new Set(pruned.edges.map((edge) => edge.id));
  const disconnected = (doc.edges || [])
    .filter((edge) => !remaining.has(edge.id))
    .flatMap((edge) => [edge.sourceHandleId, edge.targetHandleId]);

  return dropStaleRoutes(
    doc,
    dropUnusedNetworks(
      doc,
      clearInterfaceVLANs(syncInterfaceVLANs(pruned), disconnected),
    ),
  );
}

// --- interfaces ------------------------------------------------------------

function appendInterface(node, init = {}) {
  const base = String(init.name || '').trim() || nextInterfaceName(node);
  const name = uniqueName(
    base,
    deviceHandles(node).map((handle) => handle.name),
  );

  const handle = {
    id: init.id || newId(),
    name,
    index: deviceHandles(node).length,
  };

  node.device.interfaces = [...deviceHandles(node), handle];

  if (
    !node.device.spec.network ||
    typeof node.device.spec.network !== 'object'
  ) {
    node.device.spec.network = { interfaces: [] };
  }

  if (!Array.isArray(node.device.spec.network.interfaces)) {
    node.device.spec.network.interfaces = [];
  }

  // A spec entry of that name already describes the interface (a pasted
  // device brings its own). Thus only an interface created here is added, as
  // a static Ethernet interface ("Static or OSPF") with no address yet. A
  // connection to a network with a subnet gives it one (see connect).
  const interfaces = node.device.spec.network.interfaces;

  if (!interfaces.some((iface) => iface?.name === name)) {
    interfaces.push({
      name,
      type: init.type || 'ethernet',
      proto: init.proto || 'static',
      ...(init.proto && init.proto !== 'static'
        ? {}
        : { mask: DEFAULT_INTERFACE_MASK }),
      vlan: init.vlan || '',
    });
  }

  return handle;
}

// Adds a device interface: a stable handle plus the matching spec entry.
export function addInterface(doc, nodeId, init = {}) {
  const node = findNode(doc, nodeId);

  if (!node || node.kind !== 'device' || includedFrom(node)) {
    return { doc, handle: null };
  }

  const copy = deepCopy(node);
  const handle = appendInterface(copy, {
    ...init,
    name: init.name || nextInterfaceName(node),
  });

  return {
    doc: dropStaleRoutes(doc, {
      ...doc,
      nodes: doc.nodes.map((entry) => (entry.id === nodeId ? copy : entry)),
    }),
    handle,
  };
}

// A new interface's name: one past the highest ethN of the device's handles
// and spec entries, so eth1 and eth2 give eth3, not eth0. eth0 when none.
export function nextInterfaceName(node) {
  const numbers = [
    ...deviceHandles(node).map((handle) => handle.name),
    ...specInterfaces(node).map((iface) => iface?.name),
  ]
    .map((name) => /^eth(\d+)$/.exec(String(name ?? ''))?.[1])
    .filter((digits) => digits !== undefined)
    .map(Number);

  return `eth${numbers.length ? Math.max(...numbers) + 1 : 0}`;
}

/** Removes an interface handle, its spec entry and any edge using it. */
export function removeInterface(doc, nodeId, handleId) {
  const node = findNode(doc, nodeId);

  if (!node || node.kind !== 'device' || includedFrom(node)) {
    return doc;
  }

  const handle = deviceHandles(node).find((entry) => entry.id === handleId);

  if (!handle) {
    return doc;
  }

  const copy = deepCopy(node);

  copy.device.interfaces = deviceHandles(node)
    .filter((entry) => entry.id !== handleId)
    .map((entry, index) => ({ ...entry, index }));

  if (Array.isArray(copy.device.spec?.network?.interfaces)) {
    copy.device.spec.network.interfaces =
      copy.device.spec.network.interfaces.filter(
        (iface) => iface?.name !== handle.name,
      );
  }

  return dropStaleRoutes(doc, {
    ...doc,
    nodes: doc.nodes.map((entry) => (entry.id === nodeId ? copy : entry)),
    edges: (doc.edges || []).filter(
      (edge) =>
        edge.sourceHandleId !== handleId && edge.targetHandleId !== handleId,
    ),
  });
}

// --- edges -----------------------------------------------------------------

// Normalizes an edge's endpoints into (device node, handle id, switch node),
// or null. `find` looks up a node by id, for many connections.
export function edgeEndpoints(
  doc,
  connection = {},
  find = (id) => findNode(doc, id),
) {
  const source = find(connection.sourceNodeId);
  const target = find(connection.targetNodeId);

  if (!source || !target || source.id === target.id) {
    return null;
  }

  if (source.kind === 'device' && target.kind === 'switch') {
    return {
      device: source,
      handleId: connection.sourceHandleId,
      switchNode: target,
    };
  }

  if (source.kind === 'switch' && target.kind === 'device') {
    return {
      device: target,
      handleId: connection.targetHandleId,
      switchNode: source,
    };
  }

  return null;
}

/** Validates a proposed connection against the server's edge rules. */
export function validateConnection(doc, connection = {}) {
  const endpoints = edgeEndpoints(doc, connection);

  if (!endpoints) {
    return {
      valid: false,
      reason: 'A connection must join one device interface to one switch.',
    };
  }

  const { device, handleId, switchNode } = endpoints;

  if (includedFrom(device)) {
    return { valid: false, reason: includedReason(device) };
  }

  if (!findNetwork(doc, switchNode.switch?.networkId)) {
    return { valid: false, reason: 'That switch has no network.' };
  }

  if (handleId) {
    const handle = deviceHandles(device).find((entry) => entry.id === handleId);

    if (!handle) {
      return {
        valid: false,
        reason: `Unknown interface on device ${device.device.hostname}.`,
      };
    }

    const used = (doc.edges || []).some(
      (edge) =>
        edge.sourceHandleId === handleId || edge.targetHandleId === handleId,
    );

    if (used) {
      return {
        valid: false,
        reason: `Interface ${handle.name} is already connected.`,
      };
    }
  }

  return { valid: true };
}

// Connects a device interface to a switch, adding an interface when no handle
// is given. Unless `assign` is false, the interface gets a free address of the
// network's subnet (`assigned`). `full` marks a full subnet, and `unaddressed`
// an interface that still needs an address.
export function connect(doc, connection = {}, { assign = true } = {}) {
  const check = validateConnection(doc, connection);

  if (!check.valid) {
    return { doc, edge: null, error: check.reason };
  }

  const endpoints = edgeEndpoints(doc, connection);
  let next = doc;
  let handleId = endpoints.handleId;

  if (!handleId) {
    const created = addInterface(doc, endpoints.device.id, {});

    next = created.doc;
    handleId = created.handle.id;
  }

  const deviceIsSource = endpoints.device.id === connection.sourceNodeId;

  const edge = {
    id: newId(),
    sourceNodeId: deviceIsSource
      ? endpoints.device.id
      : endpoints.switchNode.id,
    targetNodeId: deviceIsSource
      ? endpoints.switchNode.id
      : endpoints.device.id,
    networkId: endpoints.switchNode.switch.networkId,
  };

  if (deviceIsSource) {
    edge.sourceHandleId = handleId;
  } else {
    edge.targetHandleId = handleId;
  }

  if (connection.label) {
    edge.label = connection.label;
  }

  if (connection.color) {
    edge.color = connection.color;
  }

  if (connection.lineStyle) {
    edge.lineStyle = connection.lineStyle;
  }

  next = { ...next, edges: [...(next.edges || []), edge] };

  // A static interface with no address gets the next free one of the
  // network's subnet, if it has one (see assignAddresses), unless the
  // caller asks for no address.
  const handle = deviceHandles(findNode(next, endpoints.device.id)).find(
    (entry) => entry.id === handleId,
  );
  const synced = syncInterfaceVLANs(next);
  const addressed = assign
    ? assignAddresses(synced, edge.networkId, {
        nodeId: endpoints.device.id,
        name: handle?.name,
      })
    : { doc: synced, assigned: [], full: false };

  // A static interface that still has no address, on a network that has
  // no subnet or with no address asked for, needs an address that the
  // user types.
  const iface = specInterfaces(
    findNode(addressed.doc, endpoints.device.id),
  ).find((entry) => entry?.name === handle?.name);
  const unaddressed =
    !addressed.full && needsAddress(iface)
      ? { nodeId: endpoints.device.id, name: iface.name }
      : undefined;

  return {
    doc: addressed.doc,
    edge,
    assigned: addressed.assigned[0],
    full: addressed.full,
    unaddressed,
  };
}

// Connects any two nodes by drag-to-connect, as the legacy Builder did. A
// device joins a switch's network. Two devices get a new network and switch
// between them. Other pairs are refused with a reason. Results are as connect's.
export function connectNodes(doc, connection = {}, options = {}) {
  const check = canConnect(doc, connection);

  if (!check.valid) {
    return { doc, edges: [], error: check.reason };
  }

  const source = findNode(doc, connection.sourceNodeId);
  const target = findNode(doc, connection.targetNodeId);

  if (source.kind === 'device' && target.kind === 'device') {
    return connectDevices(doc, source, target, connection, options);
  }

  const result = connect(doc, connection, options);

  return result.error
    ? { doc, edges: [], error: result.error }
    : {
        doc: result.doc,
        edges: [result.edge],
        assigned: result.assigned ? [result.assigned] : [],
        full: result.full,
        unaddressed: result.unaddressed ? [result.unaddressed] : [],
      };
}

// Whether drag-to-connect can join two nodes, and why not. The canvas uses it
// while dragging, so refused targets never look valid.
export function canConnect(doc, connection = {}) {
  const source = findNode(doc, connection.sourceNodeId);
  const target = findNode(doc, connection.targetNodeId);

  if (!source || !target) {
    return { valid: false, reason: 'Choose two nodes to connect.' };
  }

  if (source.id === target.id) {
    return { valid: false, reason: 'You cannot connect a node to itself.' };
  }

  const kinds = [source.kind, target.kind];

  if (kinds.includes('note') || kinds.includes('group')) {
    return {
      valid: false,
      reason: 'Notes and groups do not take connections.',
    };
  }

  if (kinds.some((kind) => DRAWING_KINDS.includes(kind))) {
    return {
      valid: false,
      reason: 'Shapes, icons and lines do not take connections.',
    };
  }

  if (source.kind === 'switch' && target.kind === 'switch') {
    return {
      valid: false,
      reason:
        'You cannot connect two switches: each switch is one network. Connect devices to it instead.',
    };
  }

  const included = [source, target].find((node) => includedFrom(node));

  if (included) {
    return { valid: false, reason: includedReason(included) };
  }

  for (const [node, handleId] of [
    [source, connection.sourceHandleId],
    [target, connection.targetHandleId],
  ]) {
    if (node.kind !== 'device' || !handleId) {
      continue;
    }

    const handle = deviceHandles(node).find((entry) => entry.id === handleId);

    if (!handle) {
      return {
        valid: false,
        reason: `Unknown interface on device ${node.device.hostname}.`,
      };
    }

    const used = (doc.edges || []).some(
      (edge) =>
        edge.sourceHandleId === handleId || edge.targetHandleId === handleId,
    );

    if (used) {
      return {
        valid: false,
        reason: `Interface ${handle.name} is already connected.`,
      };
    }
  }

  if (source.kind !== target.kind) {
    const switchNode = source.kind === 'switch' ? source : target;

    if (!findNetwork(doc, switchNode.switch?.networkId)) {
      return { valid: false, reason: 'That switch has no network.' };
    }
  }

  return { valid: true };
}

// Joins two devices through a new switch at a free spot between them (see
// freeSpotBetween), in their group when they share one.
function connectDevices(doc, a, b, connection, options = {}) {
  const size = DEFAULT_SIZES.switch;
  const parentId = a.parentId && a.parentId === b.parentId ? a.parentId : '';
  const added = addNode(doc, {
    kind: 'switch',
    position: freeSpotBetween(doc, a, b, size, options.snap),
    ...(parentId ? { parentId } : {}),
  });

  const first = connect(
    added.doc,
    {
      sourceNodeId: a.id,
      sourceHandleId: connection.sourceHandleId,
      targetNodeId: added.node.id,
    },
    options,
  );
  const second = first.error
    ? first
    : connect(
        first.doc,
        {
          sourceNodeId: added.node.id,
          targetNodeId: b.id,
          targetHandleId: connection.targetHandleId,
        },
        options,
      );

  if (second.error) {
    return { doc, edges: [], error: second.error };
  }

  return {
    doc:
      second.assigned?.setGateway && first.assigned
        ? withNetworkGateway(second.doc, second.edge.networkId, [
            first.assigned,
          ])
        : second.doc,
    edges: [first.edge, second.edge],
    node: added.node,
    assigned: [first.assigned, second.assigned].filter(Boolean),
    full: Boolean(first.full || second.full),
    unaddressed: [first.unaddressed, second.unaddressed].filter(Boolean),
  };
}

// The top-left corner for a box of `size` near the middle of two nodes that
// overlaps no other node (groups excluded, because a box can be inside a
// group). It is on the grid while `snap`, the user's Snap to grid setting,
// is on, else on whole pixels.
function freeSpotBetween(doc, a, b, size, snap = false) {
  const box = (node) => {
    const { width, height } = sizeOf(node);

    return {
      left: node.position.x,
      top: node.position.y,
      right: node.position.x + width,
      bottom: node.position.y + height,
    };
  };
  const [first, second] = [box(a), box(b)];
  const gap = 40;
  const step = snapStep(doc, snap);
  const middle = {
    x: (first.left + first.right + second.left + second.right) / 4,
    y: (first.top + first.bottom + second.top + second.bottom) / 4,
  };
  const candidates = [
    { x: middle.x - size.width / 2, y: middle.y - size.height / 2 },
    {
      x: middle.x - size.width / 2,
      y: Math.max(first.bottom, second.bottom) + gap,
    },
    {
      x: middle.x - size.width / 2,
      y: Math.min(first.top, second.top) - gap - size.height,
    },
    {
      x: Math.max(first.right, second.right) + gap,
      y: middle.y - size.height / 2,
    },
  ].map((spot) => ({ x: onStep(spot.x, step), y: onStep(spot.y, step) }));
  const obstacles = (doc.nodes || [])
    .filter((node) => node.kind !== 'group')
    .map(box);
  const clear = (spot) =>
    obstacles.every(
      (other) =>
        spot.x + size.width <= other.left ||
        spot.x >= other.right ||
        spot.y + size.height <= other.top ||
        spot.y >= other.bottom,
    );

  return candidates.find(clear) || candidates[1];
}

// Sets a connection's label, color and lineStyle, which are drawn in place of
// its network's. '' removes any of them.
export function updateEdge(doc, id, patch = {}) {
  return {
    ...doc,
    edges: (doc.edges || []).map((edge) => {
      if (edge.id !== id) {
        return edge;
      }

      const updated = { ...edge };

      for (const key of ['label', 'color', 'lineStyle']) {
        if (patch[key] === '') {
          delete updated[key];
        } else if (patch[key] !== undefined) {
          updated[key] = patch[key];
        }
      }

      return updated;
    }),
  };
}

// --- interface VLANs -------------------------------------------------------
// A connected interface's VLAN is its network's name, and a VLAN typed in the
// Inspector picks the connection (connectByVLAN). An unconnected interface
// keeps its VLAN, as phenix makes VLANs by name. Publishing refuses an empty
// VLAN.

// The network each connected interface handle is on, by handle id.
function handleNetworks(doc) {
  const networks = new Map();

  (doc.edges || []).forEach((edge) => {
    const network = findNetwork(doc, edge.networkId);

    if (!network) {
      return;
    }

    [edge.sourceHandleId, edge.targetHandleId]
      .filter(Boolean)
      .forEach((handleId) => networks.set(handleId, network));
  });

  return networks;
}

// Sets the VLAN of the interface each handle maps onto to vlanOf(handle),
// or leaves it when that is undefined. Unchanged nodes are kept as they are.
function setInterfaceVLANs(doc, vlanOf) {
  return {
    ...doc,
    nodes: (doc.nodes || []).map((node) => {
      if (node.kind !== 'device') {
        return node;
      }

      const vlanByName = new Map();

      deviceHandles(node).forEach((handle) => {
        const vlan = vlanOf(handle);

        if (vlan !== undefined) {
          vlanByName.set(handle.name, vlan);
        }
      });

      const interfaces = specInterfaces(node);
      const changed = interfaces.some(
        (iface) =>
          vlanByName.has(iface?.name) &&
          iface.vlan !== vlanByName.get(iface.name),
      );

      if (!changed) {
        return node;
      }

      const copy = deepCopy(node);

      copy.device.spec.network.interfaces = interfaces.map((iface) =>
        vlanByName.has(iface?.name)
          ? { ...iface, vlan: vlanByName.get(iface.name) }
          : iface,
      );

      return copy;
    }),
  };
}

// Sets the VLAN of every connected device interface to its network's name.
// Unconnected interfaces keep theirs (see clearInterfaceVLANs).
export function syncInterfaceVLANs(doc) {
  const networks = handleNetworks(doc);

  return setInterfaceVLANs(doc, (handle) => networks.get(handle.id)?.name);
}

// Empties the VLAN of each of these interfaces that has no connection, so that
// publishing does not put it on the network that it lost.
export function clearInterfaceVLANs(doc, handleIds = []) {
  const clearing = new Set(handleIds.filter(Boolean));
  const connected = new Set(
    (doc.edges || []).flatMap((edge) => [
      edge.sourceHandleId,
      edge.targetHandleId,
    ]),
  );

  if (clearing.size === 0) {
    return doc;
  }

  return setInterfaceVLANs(doc, (handle) =>
    clearing.has(handle.id) && !connected.has(handle.id) ? '' : undefined,
  );
}

// A network's first switch, where a connection its VLAN makes goes.
function firstSwitch(doc, networkId) {
  return (doc.nodes || []).find(
    (node) => node.kind === 'switch' && node.switch?.networkId === networkId,
  );
}

// Connects each interface whose VLAN the edit changed or added to its first
// switch of the network that the VLAN names (case-insensitive), as the
// Inspector does. A network with no switch gets one beside the device. Another
// VLAN, or an empty one, leaves the interface unconnected.
function connectByVLAN(doc, nodeId, previous, options = {}) {
  const node = findNode(doc, nodeId);
  const edges = [...(doc.edges || [])];
  const emptied = [];
  const vlanOf = (iface) => String(iface?.vlan ?? '').trim();
  const before = new Map(
    specInterfaces(previous).map((iface) => [iface?.name, vlanOf(iface)]),
  );
  let next = doc;

  deviceHandles(node).forEach((handle) => {
    const vlan = vlanOf(specInterfaceFor(node, handle.id));

    if (before.has(handle.name) && before.get(handle.name) === vlan) {
      return;
    }

    const network = vlan ? networkByName(next, vlan) : undefined;
    let hub = network ? firstSwitch(next, network.id) : undefined;

    if (network && !hub) {
      const added = addNode(next, {
        kind: 'switch',
        networkId: network.id,
        position: freeSpotBetween(
          next,
          node,
          node,
          DEFAULT_SIZES.switch,
          options.snap,
        ),
        ...(node.parentId ? { parentId: node.parentId } : {}),
      });

      next = added.doc;
      hub = added.node;
    }

    const index = edges.findIndex(
      (edge) =>
        edge.sourceHandleId === handle.id || edge.targetHandleId === handle.id,
    );
    const edge = edges[index];

    if (!hub) {
      if (edge) {
        edges.splice(index, 1);
      }

      if (!vlan) {
        emptied.push(handle.id);
      }
    } else if (!edge) {
      edges.push({
        id: newId(),
        sourceNodeId: node.id,
        sourceHandleId: handle.id,
        targetNodeId: hub.id,
        networkId: network.id,
      });
    } else if (edge.networkId !== network.id) {
      // Moved, with the same id, label and color. The device keeps its end.
      edges[index] = {
        ...edge,
        [edge.sourceHandleId === handle.id ? 'targetNodeId' : 'sourceNodeId']:
          hub.id,
        networkId: network.id,
      };
    }
  });

  return clearInterfaceVLANs({ ...next, edges }, emptied);
}

// What an edit changed about a device's connections, for its announcement:
// each switch added, then each interface connected, moved or disconnected.
// Interfaces match by handle, else by name. Removed interfaces are not listed.
export function connectionChanges(before, after, nodeId) {
  const handlesBefore = deviceHandles(findNode(before, nodeId));
  const ids = new Set(handlesBefore.map((handle) => handle.id));
  const was = new Map(handlesBefore.map((handle) => [handle.name, handle.id]));
  const networksBefore = handleNetworks(before);
  const networksAfter = handleNetworks(after);
  const switches = new Set(
    (before.nodes || [])
      .filter((node) => node.kind === 'switch')
      .map((node) => node.id),
  );
  const added = (after.nodes || [])
    .filter((node) => node.kind === 'switch' && !switches.has(node.id))
    .map(
      (node) =>
        `added a switch for network ${networkOfSwitch(after, node)?.name}`,
    );

  const changed = deviceHandles(findNode(after, nodeId)).flatMap((handle) => {
    const from = networksBefore.get(
      ids.has(handle.id) ? handle.id : was.get(handle.name),
    );
    const to = networksAfter.get(handle.id);

    if (from?.id === to?.id) {
      return [];
    }

    if (!from) {
      return [`connected ${handle.name} to network ${to.name}`];
    }

    return to
      ? [`moved ${handle.name} from network ${from.name} to network ${to.name}`]
      : [`disconnected ${handle.name} from network ${from.name}`];
  });

  return [...added, ...changed];
}

// --- templates -------------------------------------------------------------

// What a device node has that a template's device does not have: its name
// (the template's base hostname, in its spec), and its handles and the
// topology it is included from, which belong to a diagram.
const NOT_TEMPLATE_KEYS = ['hostname', 'interfaces', 'includedFrom'];

// The device of a template, from a device node's payload or a template's: the
// payload less NOT_TEMPLATE_KEYS and empty fields, with a copy of the spec.
export function templateDevice(payload) {
  const device = {};

  for (const [key, value] of Object.entries(payload || {})) {
    if (NOT_TEMPLATE_KEYS.includes(key) || key === 'spec') {
      continue;
    }

    if (![undefined, null, ''].includes(value)) {
      device[key] = value;
    }
  }

  device.spec = deepCopy(payload?.spec ?? {});

  return device;
}

// A template as a document keeps it: its description only when it has one,
// and its source (the library template it was copied from, see
// linkedCopy in templates.js) only when it has one.
function templateEntry(id, { name, description, device, source }) {
  return {
    id,
    name,
    ...(description ? { description } : {}),
    device: templateDevice(device),
    ...(source && typeof source === 'object' ? { source: { ...source } } : {}),
  };
}

// Saves a device template in the diagram under a new id: a built-in's id is a
// name, and a library template can be in the diagram already.
export function addTemplate(doc, template) {
  const added = templateEntry(newId(), template);

  return {
    doc: { ...doc, templates: [...(doc.templates || []), added] },
    template: added,
  };
}

// Changes the name, description, device or source of a diagram template. An
// empty description or a null source is removed.
export function updateTemplate(doc, id, patch = {}) {
  const templates = doc.templates || [];
  const index = templates.findIndex((template) => template.id === id);

  if (index < 0) {
    return doc;
  }

  const current = templates[index];
  const updated = templateEntry(id, {
    name: patch.name ?? current.name,
    description: patch.description ?? current.description,
    device: patch.device ?? current.device,
    source: patch.source === undefined ? current.source : patch.source,
  });

  return {
    ...doc,
    templates: templates.map((template, at) =>
      at === index ? updated : template,
    ),
  };
}

// Removes a template. Devices made from it do not change, as they keep no link
// to it. With none left, the document has no `templates`.
export function removeTemplate(doc, id) {
  const templates = doc.templates || [];
  const kept = templates.filter((template) => template.id !== id);

  if (kept.length === templates.length) {
    return doc;
  }

  const next = { ...doc };

  if (kept.length > 0) {
    next.templates = kept;
  } else {
    delete next.templates;
  }

  return next;
}

// --- viewport / grid -------------------------------------------------------

/**
 * @param {object} doc
 * @param {{x: number, y: number, zoom: number}} viewport
 * @returns {object} document
 */
export function setViewport(doc, viewport) {
  return {
    ...doc,
    viewport: {
      x: viewport?.x ?? 0,
      y: viewport?.y ?? 0,
      zoom: viewport?.zoom ?? 1,
    },
  };
}

// Whether the server takes a grid size (document.grid.invalid in validate.go):
// a whole number from MIN_GRID_SIZE to MAX_GRID_SIZE.
export function isGridSize(size) {
  return (
    Number.isInteger(size) && size >= MIN_GRID_SIZE && size <= MAX_GRID_SIZE
  );
}

// Changes the diagram's grid. A size that isGridSize refuses changes nothing.
export function setGrid(doc, patch = {}) {
  if ('size' in patch && !isGridSize(patch.size)) {
    return doc;
  }

  const grid = { ...doc.grid, ...patch };

  return Object.keys(grid).every((key) => grid[key] === doc.grid?.[key])
    ? doc
    : { ...doc, grid };
}

// --- the document's metadata -------------------------------------------------

// The keys of a document's metadata, in the order the server's encoding has
// them (Metadata in document.go).
export const METADATA_KEYS = [
  'id',
  'name',
  'description',
  'createdBy',
  'createdAt',
  'updatedBy',
  'updatedAt',
  'notes',
];

// The metadata the server sets, after `description`.
export const STAMP_KEYS = ['createdBy', 'createdAt', 'updatedBy', 'updatedAt'];

// The most notes a diagram's metadata holds, and the longest one in UTF-8
// bytes (MaxDiagramNotes and MaxDiagramNoteBytes in document.go).
export const MAX_DIAGRAM_NOTES = 100;
export const MAX_DIAGRAM_NOTE_BYTES = 4096;

/** A document's metadata, or {} for a value that is not a document. */
export function metadataOf(doc) {
  return doc?.metadata && typeof doc.metadata === 'object' ? doc.metadata : {};
}

// The metadata with its keys in the server's order, and any other after.
function orderedMetadata(metadata) {
  return Object.fromEntries(
    [
      ...METADATA_KEYS.filter((key) => key in metadata),
      ...Object.keys(metadata).filter((key) => !METADATA_KEYS.includes(key)),
    ].map((key) => [key, metadata[key]]),
  );
}

// The document with the stamp of a create or save response (who and when it
// was created and last saved) in its metadata, in the server's key order. A
// value that the stamp lacks is removed, as the stored document has none.
export function withStamp(doc, stamp) {
  const current = metadataOf(doc);
  const metadata = Object.fromEntries(
    Object.entries(current).filter(([key]) => !STAMP_KEYS.includes(key)),
  );

  STAMP_KEYS.forEach((key) => {
    if (typeof stamp?.[key] === 'string' && stamp[key] !== '') {
      metadata[key] = stamp[key];
    }
  });

  const next = orderedMetadata(metadata);
  const unchanged =
    JSON.stringify(Object.keys(next)) ===
      JSON.stringify(Object.keys(current)) &&
    STAMP_KEYS.every((key) => next[key] === current[key]);

  return unchanged ? doc : { ...doc, metadata: next };
}

// The stamp of the document that a history snapshot holds: the creator from
// `doc`, and the snapshot's user and time, to the second, as the last save.
export function savedStamp(doc, snapshot) {
  const at = String(snapshot?.createdAt || '');
  // The server's times are UTC, with a fraction of a second.
  const whole = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.\d+)?Z$/.exec(at);
  const parsed = whole || !at ? null : new Date(at);

  return {
    createdBy: metadataOf(doc).createdBy,
    createdAt: metadataOf(doc).createdAt,
    updatedBy: snapshot?.createdBy,
    updatedAt: whole
      ? `${whole[1]}Z`
      : parsed && !Number.isNaN(parsed.getTime())
        ? `${parsed.toISOString().slice(0, 19)}Z`
        : '',
  };
}

// Whether two documents differ at most in what the server stamps (withStamp).
export function sameButStamp(a, b) {
  const same = (left, right, skipped) => {
    const keys = (value) =>
      Object.keys(value || {}).filter((key) => !skipped.includes(key));
    const own = keys(left);

    return (
      own.length === keys(right).length &&
      own.every((key) => left[key] === right[key])
    );
  };

  return (
    Boolean(a && b) &&
    same(a, b, ['metadata']) &&
    same(metadataOf(a), metadataOf(b), STAMP_KEYS)
  );
}

/**
 * @param {object} doc
 * @param {object} patch name, description
 * @returns {object} document
 */
export function setDocumentInfo(doc, patch = {}) {
  const metadata = { ...metadataOf(doc) };

  if (patch.name !== undefined) {
    metadata.name = patch.name;
  }

  if (patch.description !== undefined) {
    metadata.description = patch.description;
  }

  return { ...doc, metadata: orderedMetadata(metadata) };
}

// Why the server would refuse a diagram note (validateNotes in validate.go), as
// a sentence, or ''. Blank notes pass, as setDiagramNotes drops them.
export function diagramNoteProblem(note) {
  const text = String(note ?? '');

  if (utf8Length(text) > MAX_DIAGRAM_NOTE_BYTES) {
    return `A note holds at most ${MAX_DIAGRAM_NOTE_BYTES} bytes in UTF-8. This note is longer. Builder does not save it until it is shorter.`;
  }

  if (hasControlCharactersInLines(text)) {
    return 'A note cannot hold control characters other than newline and tab. Builder does not save it until you remove them.';
  }

  return '';
}

// The document with its diagram notes, less blank ones, and no `notes` when
// none is left. When a note cannot be written (diagramNoteProblem), the old
// notes stay, so no caller writes a note that the server would refuse.
export function setDiagramNotes(doc, notes) {
  const kept = (Array.isArray(notes) ? notes : []).filter(
    (note) => typeof note === 'string' && !isBlank(note),
  );

  if (kept.some((note) => diagramNoteProblem(note))) {
    return doc;
  }

  const current = metadataOf(doc).notes;
  const before = Array.isArray(current) ? current : [];

  if (
    kept.length === before.length &&
    kept.every((note, index) => note === before[index]) &&
    (kept.length > 0 || current === undefined)
  ) {
    return doc;
  }

  const metadata = { ...metadataOf(doc) };

  if (kept.length > 0) {
    metadata.notes = kept;
  } else {
    delete metadata.notes;
  }

  return { ...doc, metadata: orderedMetadata(metadata) };
}

/** The names of the Scenario configs the document lists, in order. */
export function documentScenarios(doc) {
  return (Array.isArray(doc?.scenarios) ? doc.scenarios : []).filter(
    (name) => typeof name === 'string' && name !== '',
  );
}

// Replaces the document's Scenario configs. An empty list removes `scenarios`.
export function setScenarios(doc, names = []) {
  const next = { ...doc };

  if (names.length === 0) {
    delete next.scenarios;

    return next;
  }

  next.scenarios = [...names];

  return next;
}

// The text annotations of the config the document was imported from, by key,
// without the Builder's own, as the Inspector lists them.
export function sourceAnnotations(doc) {
  const annotations = doc?.source?.annotations;

  if (!annotations || typeof annotations !== 'object') {
    return [];
  }

  return Object.entries(annotations)
    .filter(
      ([key, value]) => typeof value === 'string' && !isBuilderAnnotation(key),
    )
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
}

// The named apps of v2 Scenario content, in order, each with the hostnames of
// its hosts and whether it is disabled.
export function scenarioApps(content) {
  const apps = Array.isArray(content?.apps) ? content.apps : [];
  const named = (value) => typeof value === 'string' && value !== '';

  return apps
    .filter((app) => named(app?.name))
    .map((app) => ({
      name: app.name,
      hosts: (Array.isArray(app.hosts) ? app.hosts : [])
        .map((host) => host?.hostname)
        .filter(named),
      disabled: app.disabled === true,
    }));
}

// Summary counts for status text and the drafts list. The included* counts are
// for included devices, and for the switches, networks and links only they use.
export function documentSummary(doc) {
  const nodes = doc?.nodes || [];
  const edges = doc?.edges || [];
  const included = new Set(
    nodes.filter((node) => includedFrom(node)).map((node) => node.id),
  );
  const theirs = (edge) =>
    included.has(edge.sourceNodeId) || included.has(edge.targetNodeId);
  // Whether each node and each network is used only by included devices, by
  // id, from one pass over the connections. It becomes false when any other
  // connection uses it.
  const nodeTheirs = new Map();
  const networkTheirs = new Map();
  const use = (map, id, edge) =>
    map.set(id, (map.get(id) ?? true) && theirs(edge));

  for (const edge of edges) {
    use(nodeTheirs, edge.sourceNodeId, edge);
    use(nodeTheirs, edge.targetNodeId, edge);
    use(networkTheirs, edge.networkId, edge);
  }

  const switches = nodes.filter((node) => node.kind === 'switch');
  const networks = doc?.networks || [];

  return {
    devices: nodes.filter((node) => node.kind === 'device').length,
    included: included.size,
    switches: switches.length,
    includedSwitches: switches.filter((node) => nodeTheirs.get(node.id)).length,
    notes: nodes.filter((node) => node.kind === 'note').length,
    groups: nodes.filter((node) => node.kind === 'group').length,
    drawings: nodes.filter((node) => DRAWING_KINDS.includes(node.kind)).length,
    networks: networks.length,
    includedNetworks: networks.filter((network) =>
      networkTheirs.get(network.id),
    ).length,
    links: edges.length,
    includedLinks: edges.filter(theirs).length,
  };
}

// A note's first line, with its spaces collapsed. It names an unlabeled note.
function noteTitle(node) {
  const line = String(node?.note?.text || '')
    .split('\n')
    .find((entry) => entry.trim() !== '');

  return (line || '').replace(/\s+/g, ' ').trim();
}

/** Display label for a node, used by the canvas and dialogs. */
export function nodeLabel(node) {
  if (!node) {
    return '';
  }

  // A note labelled only with its kind, as notes once were, is named after
  // its text too.
  if (node.label && !(node.kind === 'note' && node.label === 'Note')) {
    return node.label;
  }

  switch (node.kind) {
    case 'device':
      return node.device?.hostname || 'device';
    case 'group':
      return node.group?.title || 'Group';
    case 'note':
      return noteTitle(node) || 'Note';
    case 'shape':
      return (
        node.shape?.label ||
        (node.shape?.shape === 'circle' ? 'Circle' : 'Rectangle')
      );
    case 'icon':
      return node.icon?.label || 'Icon';
    case 'line':
      return node.line?.label || 'Line';
    default:
      return kindMeta(node.kind).label;
  }
}

// The name of a node's kind for accessible names and announcements, such as
// "Device". A shape takes its figure's name: "Rectangle" or "Circle".
export function kindLabel(node) {
  if (node?.kind === 'shape') {
    return node.shape?.shape === 'circle' ? 'Circle' : 'Rectangle';
  }

  return kindMeta(node?.kind).label;
}

// A device's shown type: its phenix node type as stored, "External" for any
// external device, or "Device" when the spec names no type.
export function deviceTypeLabel(node) {
  const spec = node?.device?.spec;

  if (spec?.external === true) {
    return 'External';
  }

  const type = typeof spec?.type === 'string' ? spec.type.trim() : '';

  return type || 'Device';
}

// The label of a connection end: the node's label, plus the interface at a
// device, "web-01 (eth1)", so two links between the same nodes differ.
export function connectionEndLabel(node, handleId) {
  const label = nodeLabel(node);
  const handle =
    handleId && deviceHandles(node).find((entry) => entry.id === handleId);

  return handle?.name ? `${label} (${handle.name})` : label;
}

// A node's comment, at the end of its accessible name: a device's phenix
// description, a note's text or a group's description.
export function nodeComment(node) {
  if (node?.kind === 'device') {
    return node.device?.spec?.general?.description || '';
  }

  if (node?.kind === 'note') {
    return node.note?.text || '';
  }

  if (node?.kind === 'group') {
    return node.group?.description || '';
  }

  return '';
}

// The kinds of node whose description the canvas draws, and that can hide it.
const DESCRIPTION_KINDS = Object.freeze(['device', 'group']);

/** Whether a device or a group hides its description on the canvas. */
export function nodeDescriptionHidden(node) {
  return (
    DESCRIPTION_KINDS.includes(node?.kind) &&
    node[node.kind]?.hideDescription === true
  );
}

// The nodeComment a node draws. A device or a group draws no description when
// `shown` (the setting Show node descriptions) is off or the node hides it.
export function drawnNodeComment(node, shown = true) {
  if (DESCRIPTION_KINDS.includes(node?.kind)) {
    if (!shown || nodeDescriptionHidden(node)) {
      return '';
    }
  }

  return nodeComment(node);
}

// The document with the description of a device or a group hidden or shown on
// the canvas. Shown removes `hideDescription`. No change gives the same doc.
export function setDescriptionHidden(doc, nodeId, hidden) {
  const node = findNode(doc, nodeId);

  if (
    !DESCRIPTION_KINDS.includes(node?.kind) ||
    !node[node.kind] ||
    nodeDescriptionHidden(node) === Boolean(hidden)
  ) {
    return doc;
  }

  const { hideDescription: _, ...payload } = node[node.kind];

  if (hidden) {
    payload.hideDescription = true;
  }

  return {
    ...doc,
    nodes: doc.nodes.map((entry) =>
      entry === node ? { ...node, [node.kind]: payload } : entry,
    ),
  };
}
