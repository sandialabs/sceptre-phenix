// draw.io (diagrams.net): a one-way download of the diagram as one page of
// mxGraph cells, in the light theme's colors (LIGHT_COLORS).
// A label is HTML in an XML attribute, so it is escaped twice. A semicolon
// ends a style pair, so a style image URL omits the base64 marker.

import { lineDashArray, networkStyle } from './adapters/vueflow.js';
import { builtinIconSvg } from './builtinIcons.js';
import { nodeIcon, nodeIconKey } from './catalog.js';
import {
  customNetworkColor,
  drawnColor,
  iconInk,
  nodeColors,
  opaqueHex,
} from './colors.js';
import { iconLabel } from './iconLabels.js';
import { iconPath } from './iconPaths.js';
import { iconSrc } from './icons.js';
import {
  BORDER_STYLES,
  DEFAULT_GRID_SIZE,
  DEFAULT_ICON_SIZE,
  deviceHandles,
  deviceTypeLabel,
  drawnNodeComment,
  iconPixels,
  includedFrom,
  layerOutlineColor,
  LINE_STYLES,
  MIN_LINE_POINTS,
  nodeIconSize,
  nodeLabel,
  sizeOf,
  specInterfaces,
} from './model.js';
import { interfaceAddress } from './nodeInfo.js';
import { fitRoute, handleOffsetY } from './routes.js';
import { element, NOT_XML, writeElement } from './xml.js';

export const DRAWIO_MIME = 'application/vnd.jgraph.mxfile';

/**
 * The light theme's colors that the file uses, by token: the values of the
 * light block of builder.css (.builder-root). A test holds them to it.
 */
export const LIGHT_COLORS = Object.freeze({
  '--bx-bg': '#ffffff',
  '--bx-bg-alt': '#f4f6f9',
  '--bx-surface': '#ffffff',
  '--bx-border': '#c7ced9',
  '--bx-border-strong': '#7b8494',
  '--bx-text': '#1b2330',
  '--bx-text-muted': '#4d5768',
  '--bx-accent': '#1f5fa9',
  '--bx-net-0': '#1f5fa9',
  '--bx-net-1': '#8a5300',
  '--bx-net-2': '#1a6b3c',
  '--bx-net-3': '#7b3fa0',
  '--bx-net-4': '#a4262c',
  '--bx-net-5': '#0f6f7a',
  '--bx-net-6': '#5c5f00',
  '--bx-net-7': '#8e3f7e',
});

const light = (token) => LIGHT_COLORS[token];

// The ids of the two cells every page starts with: the root and its layer.
const ROOT_ID = '0';
const LAYER_ID = '1';

// The canvas's measures in pixels: corner radius (--bx-radius), room inside
// a border, room between an icon and its text, and text sizes.
const RADIUS = 6;
const PADDING = 8;
const ICON_GAP = 6;
const FONT_SIZE = 12;
const SMALL_FONT_SIZE = 11;

// shape=label puts its image its spacing and 5 more pixels in. A switch's
// text and icon sit further in, clear of its round ends (1.1rem on the canvas).
const LABEL_SPACING = 3;
const IMAGE_OFFSET = 5;
const SWITCH_INSET = 18;
const ICON_LABEL_LINE = 16;

// A note's header icon is always Small, as on the canvas.
const NOTE_ICON_PIXELS = 16;

// The stacking of each kind in its container: groups, shapes and lines under
// the other nodes, as on the canvas (see nodeZIndex in adapters/vueflow.js).
const UNDER = new Set(['group', 'shape', 'line']);

// The prefix of the address iconSrc gives a custom icon.
const PNG_SRC = 'data:image/png;base64,';

// --- text --------------------------------------------------------------------

const HTML_ESCAPES = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
};

// Text as HTML: no character that XML forbids, and every markup character
// escaped. Line breaks become <br>.
function html(value) {
  return String(value ?? '')
    .replace(NOT_XML, '')
    .replace(/[&<>"']/g, (char) => HTML_ESCAPES[char])
    .replace(/\r\n?|\n/g, '<br>');
}

// Smaller text in a color, as the canvas shows a node's second lines.
function small(text, color, italic = false) {
  const style = [
    `font-size:${SMALL_FONT_SIZE}px`,
    `color:${color}`,
    italic ? 'font-style:italic' : '',
  ]
    .filter(Boolean)
    .join(';');

  return `<span style="${style}">${html(text)}</span>`;
}

const bold = (text) => `<b>${html(text)}</b>`;

// --- styles ------------------------------------------------------------------

// A style from [key, value] pairs. An empty value leaves the pair out, null
// writes the key alone, and a ";" in a value is removed.
function style(pairs) {
  return pairs
    .filter(([, value]) => value !== undefined && value !== '')
    .map(([key, value]) =>
      value === null ? key : `${key}=${String(value).replace(/;/g, '')}`,
    )
    .join(';');
}

// A color as a style takes it: #rrggbb for a hex or rgb() color, a name for
// a named color, else '' (the default of the place).
function styleColor(value) {
  const color = drawnColor(value);

  if (!color) {
    return '';
  }

  return (
    opaqueHex(color) || (/^[a-z]+$/i.test(color) ? color.toLowerCase() : '')
  );
}

const netColor = (token) => light(`--bx-net-${token}`) || light('--bx-net-0');

// The dash pairs of a line style: dashed, the pattern in pixels (fixDash),
// or nothing for a solid line.
function dashPairs(dashArray) {
  return dashArray
    ? [
        ['dashed', 1],
        ['fixDash', 1],
        ['dashPattern', dashArray],
      ]
    : [];
}

// The pairs of a box's border pattern. A label cell cannot draw a double
// border, so it draws a thick solid one.
function borderPairs(pattern, fallback) {
  const chosen = BORDER_STYLES.includes(pattern) ? pattern : fallback;

  if (chosen === 'dashed') {
    return [['dashed', 1]];
  }

  if (chosen === 'dotted') {
    return [
      ['dashed', 1],
      ['fixDash', 1],
      ['dashPattern', '2 4'],
    ];
  }

  return chosen === 'double' ? [['strokeWidth', 4]] : [];
}

const ROUNDED = [
  ['rounded', 1],
  ['absoluteArcSize', 1],
  ['arcSize', RADIUS * 2],
];

// --- numbers and geometry ----------------------------------------------------

const finite = (value) => (Number.isFinite(value) ? value : 0);
const side = (value) => Math.max(0, finite(value));

// The size of a node as a cell has it: finite, and never below zero.
function boxOf(node) {
  const { width, height } = sizeOf(node);

  return { width: side(Number(width)), height: side(Number(height)) };
}

function geometry(at, box) {
  return element('mxGeometry', {
    x: String(at.x),
    y: String(at.y),
    width: String(box.width),
    height: String(box.height),
    as: 'geometry',
  });
}

function point(at, as) {
  return element('mxPoint', { x: String(at.x), y: String(at.y), as });
}

// --- icons -------------------------------------------------------------------

function base64(text) {
  const bytes = new TextEncoder().encode(text);
  let binary = '';

  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }

  return btoa(binary);
}

// The image of an icon in a style: the node's custom icon, else the built-in
// icon of its key in one color, made once for each key, color and size.
function iconImages(doc, library) {
  const made = new Map();

  return (node, key, color, pixels) => {
    const src = iconSrc(nodeIcon(node), doc?.icons, library);

    if (src.startsWith(PNG_SRC)) {
      return `data:image/png,${src.slice(PNG_SRC.length)}`;
    }

    const id = `${key}\n${color}\n${pixels}`;

    if (!made.has(id)) {
      const svg = builtinIconSvg({
        markup: iconPath(key),
        label: iconLabel(key),
        color,
        size: pixels,
      });

      made.set(id, `data:image/svg+xml,${base64(svg)}`);
    }

    return made.get(id);
  };
}

// The pairs of an image at the left of a label cell, `inset` pixels in, and
// the room that the text leaves for it.
function iconPairs(image, pixels, { vertical = 'top', inset = PADDING } = {}) {
  const spacing = inset - IMAGE_OFFSET;

  if (!image) {
    return [
      ['spacing', LABEL_SPACING],
      ['spacingLeft', inset - LABEL_SPACING],
    ];
  }

  return [
    ['spacing', spacing],
    ['image', image],
    ['imageWidth', pixels],
    ['imageHeight', pixels],
    ['imageAlign', 'left'],
    ['imageVerticalAlign', vertical],
    ['spacingLeft', inset + pixels + ICON_GAP - spacing],
  ];
}

// --- data --------------------------------------------------------------------

const scalar = (value) =>
  (typeof value === 'string' && value.trim() !== '') ||
  (typeof value === 'number' && Number.isFinite(value))
    ? value
    : undefined;

/**
 * The device settings that its cell carries as data for draw.io's Edit Data.
 * It is an allow-list: no injections, metadata or credentials.
 */
function deviceData(node) {
  const spec = node.device?.spec || {};
  const hardware = spec.hardware || {};
  const interfaces = specInterfaces(node)
    .filter((iface) => iface && typeof iface === 'object')
    .map(
      (iface) =>
        `${scalar(iface.name) ?? '?'}: VLAN ${scalar(iface.vlan) ?? 'none'}, ${interfaceAddress(iface)}`,
    );

  return {
    hostname: scalar(node.device?.hostname),
    type: scalar(spec.type),
    os_type: scalar(hardware.os_type),
    image: Array.isArray(hardware.drives)
      ? scalar(hardware.drives[0]?.image)
      : undefined,
    vcpus: scalar(hardware.vcpus),
    memory: scalar(hardware.memory),
    interfaces: interfaces.length ? interfaces.join('\n') : undefined,
  };
}

// --- cells -------------------------------------------------------------------

function vertex({ id, value, styleText, parent, at, box }) {
  return element(
    'mxCell',
    { id, value, style: styleText, vertex: '1', parent },
    [geometry(at, box)],
  );
}

// The look of a device or a switch: fill, outline, text and icon colors.
function nodeLook(doc, node) {
  const payload = node.kind === 'switch' ? node.switch : node.device;
  const own = nodeColors(payload, layerOutlineColor(doc, node));
  let outline = own.outline;
  let token = false;

  if (!outline && node.kind === 'switch') {
    const network = (doc.networks || []).find(
      (entry) => entry.id === node.switch?.networkId,
    );

    if (network) {
      outline = styleColor(customNetworkColor(network.color));
      token = !outline;
      outline ||= netColor(networkStyle(doc, network.id).token);
    }
  }

  const text = own.fill ? own.ink : light('--bx-text');
  // A token keeps 3:1 against the theme. Only a fill can make it fade.
  const plain = token ? Boolean(own.fill) : iconInk(outline, own.fill).light;

  return {
    fill: own.fill || light('--bx-surface'),
    outline,
    stroke: outline || light('--bx-border-strong'),
    text,
    muted: own.fill ? own.ink : light('--bx-text-muted'),
    icon: plain ? text : outline || light('--bx-accent'),
  };
}

function deviceCell(context, node, place) {
  const { doc, image, options } = context;
  const look = nodeLook(doc, node);
  const size = nodeIconSize(doc, node, options.iconSize);
  const pixels = iconPixels(size);
  const origin = includedFrom(node);
  const count = deviceHandles(node).length;
  const comment = drawnNodeComment(node, options.showNodeDescriptions);
  const label = [
    bold(nodeLabel(node)),
    origin
      ? small(`Included from ${origin}`, look.muted, true)
      : small(deviceTypeLabel(node), look.muted),
    small(`${count} ${count === 1 ? 'interface' : 'interfaces'}`, look.muted),
    comment ? small(comment, look.muted) : '',
  ]
    .filter(Boolean)
    .join('<br>');
  const icon =
    options.showNodeIcons && image(node, nodeIconKey(node), look.icon, pixels);
  const styleText = style([
    ['shape', 'label'],
    ['html', 1],
    ['whiteSpace', 'wrap'],
    ['overflow', 'hidden'],
    ['align', 'left'],
    ['verticalAlign', 'top'],
    ['spacingTop', 3],
    ['spacingRight', PADDING - LABEL_SPACING],
    ...ROUNDED,
    ['fillColor', look.fill],
    ['strokeColor', look.stroke],
    ['strokeWidth', 2],
    ...(origin ? [['dashed', 1]] : []),
    ['fontColor', look.text],
    ['fontSize', FONT_SIZE],
    // A Medium or a Large icon stands beside all the lines, at their middle.
    ...iconPairs(icon, pixels, {
      vertical: size === DEFAULT_ICON_SIZE ? 'top' : 'middle',
    }),
  ]);

  return element('object', { label, id: place.id, ...deviceData(node) }, [
    element('mxCell', { style: styleText, vertex: '1', parent: place.parent }, [
      geometry(place.at, place.box),
    ]),
  ]);
}

function switchCell(context, node, place) {
  const { doc, image, options } = context;
  const look = nodeLook(doc, node);
  const pixels = iconPixels(nodeIconSize(doc, node, options.iconSize));
  const icon =
    options.showNodeIcons && image(node, nodeIconKey(node), look.icon, pixels);

  return vertex({
    ...place,
    value: bold(nodeLabel(node)),
    styleText: style([
      ['shape', 'label'],
      ['html', 1],
      ['whiteSpace', 'wrap'],
      ['overflow', 'hidden'],
      ['align', 'left'],
      ['verticalAlign', 'middle'],
      ['rounded', 1],
      ['arcSize', 50],
      ['fillColor', look.fill],
      ['strokeColor', look.stroke],
      ['strokeWidth', 3],
      ['fontColor', look.text],
      ['fontSize', FONT_SIZE],
      ...iconPairs(icon, pixels, { vertical: 'middle', inset: SWITCH_INSET }),
    ]),
  });
}

function groupCell(context, node, place) {
  const { doc, image, options } = context;
  const pixels = iconPixels(nodeIconSize(doc, node, options.iconSize));
  const comment = drawnNodeComment(node, options.showNodeDescriptions);
  const icon =
    options.showNodeIcons &&
    image(node, nodeIconKey(node), light('--bx-accent'), pixels);

  return vertex({
    ...place,
    value: [
      bold(nodeLabel(node)),
      comment ? small(comment, light('--bx-text-muted')) : '',
    ]
      .filter(Boolean)
      .join('<br>'),
    styleText: style([
      ['shape', 'label'],
      ['container', 1],
      ['collapsible', 0],
      ['recursiveResize', 0],
      ['connectable', 0],
      ['html', 1],
      ['whiteSpace', 'wrap'],
      ['align', 'left'],
      ['verticalAlign', 'top'],
      ['spacingTop', 3],
      ...ROUNDED,
      ['fillColor', 'none'],
      [
        'strokeColor',
        styleColor(node.group?.color) || light('--bx-border-strong'),
      ],
      ['strokeWidth', 2],
      ...borderPairs(node.group?.borderStyle, 'dashed'),
      ['fontColor', light('--bx-text')],
      ['fontSize', FONT_SIZE],
      ...iconPairs(icon, pixels),
    ]),
  });
}

function noteCell(context, node, place) {
  const label = node.label && node.label !== 'Note' ? node.label : 'Note';
  const text = node.note?.text || '';

  return vertex({
    ...place,
    value: [bold(label), text ? small(text, light('--bx-text-muted')) : '']
      .filter(Boolean)
      .join('<br>'),
    styleText: style([
      ['shape', 'label'],
      ['html', 1],
      ['whiteSpace', 'wrap'],
      ['overflow', 'hidden'],
      ['align', 'left'],
      ['verticalAlign', 'top'],
      ['spacingTop', 3],
      ...ROUNDED,
      ['fillColor', light('--bx-bg-alt')],
      [
        'strokeColor',
        styleColor(node.note?.color) || light('--bx-border-strong'),
      ],
      ['strokeWidth', 2],
      ['dashed', 1],
      ['fontColor', light('--bx-text')],
      ['fontSize', FONT_SIZE],
      ...iconPairs(
        context.image(
          node,
          nodeIconKey(node),
          light('--bx-text'),
          NOTE_ICON_PIXELS,
        ),
        NOTE_ICON_PIXELS,
      ),
    ]),
  });
}

function shapeCell(_, node, place) {
  const shape = node.shape || {};
  const circle = shape.shape === 'circle';
  const own = nodeColors(shape);
  const double = shape.borderStyle === 'double';
  const figure = circle
    ? [
        ['ellipse', null],
        ['shape', double ? 'doubleEllipse' : ''],
      ]
    : [...ROUNDED, ['shape', double ? 'ext' : ''], ['double', double ? 1 : '']];

  return vertex({
    ...place,
    value: html(shape.label || ''),
    styleText: style([
      ...figure,
      ['html', 1],
      ['whiteSpace', 'wrap'],
      ['align', 'center'],
      ['verticalAlign', 'middle'],
      ['fillColor', own.fill || 'none'],
      ['strokeColor', own.outline || light('--bx-border-strong')],
      ['strokeWidth', 2],
      ...(double ? [] : borderPairs(shape.borderStyle, 'solid')),
      ['fontColor', own.fill ? own.ink : light('--bx-text')],
      ['fontSize', FONT_SIZE],
    ]),
  });
}

function iconCell(context, node, place) {
  const label = node.icon?.label || '';
  const pixels = Math.max(
    1,
    Math.min(
      place.box.width - 2 * PADDING,
      place.box.height - 2 * PADDING - (label ? ICON_LABEL_LINE : 0),
    ),
  );

  return vertex({
    ...place,
    value: html(label),
    styleText: style([
      ['shape', 'label'],
      ['html', 1],
      ['whiteSpace', 'wrap'],
      ['align', 'center'],
      ['verticalAlign', 'bottom'],
      ['spacing', LABEL_SPACING],
      ['fillColor', 'none'],
      ['strokeColor', 'none'],
      ['fontColor', light('--bx-text')],
      ['fontSize', FONT_SIZE],
      [
        'image',
        context.image(node, nodeIconKey(node), light('--bx-text'), pixels),
      ],
      ['imageWidth', pixels],
      ['imageHeight', pixels],
      ['imageAlign', 'center'],
      ['imageVerticalAlign', 'top'],
    ]),
  });
}

// The style pairs of an arrowhead at one end of a line.
function arrowPairs(end, drawn) {
  return drawn
    ? [
        [`${end}Arrow`, 'block'],
        [`${end}Fill`, 1],
      ]
    : [[`${end}Arrow`, 'none']];
}

function edgeLabelPairs() {
  return [
    ['fontColor', light('--bx-text')],
    ['fontSize', SMALL_FONT_SIZE],
    ['labelBackgroundColor', light('--bx-surface')],
    ['labelBorderColor', light('--bx-border-strong')],
  ];
}

// A line drawing: an edge with no ends, through its points. These are
// relative to the line's box, and the edge's to its container.
function lineCell(_, node, place) {
  const line = node.line || {};
  const points = (Array.isArray(line.points) ? line.points : []).map(
    (entry) => ({
      x: place.at.x + finite(Number(entry?.x)),
      y: place.at.y + finite(Number(entry?.y)),
    }),
  );

  if (points.length < MIN_LINE_POINTS) {
    return null;
  }

  const color = /^#[0-9a-f]{6}$/i.test(line.color || '')
    ? line.color.toLowerCase()
    : light('--bx-text');
  const bends = points.slice(1, -1);

  return element(
    'mxCell',
    {
      id: place.id,
      value: html(line.label || ''),
      style: style([
        ['edgeStyle', 'none'],
        ['html', 1],
        ['rounded', 0],
        ...arrowPairs('start', Boolean(line.startArrow)),
        ...arrowPairs('end', Boolean(line.endArrow)),
        ['strokeColor', color],
        ['strokeWidth', 2],
        ...dashPairs(lineDashArray(line.lineStyle)),
        ...edgeLabelPairs(),
      ]),
      edge: '1',
      parent: place.parent,
    },
    [
      element('mxGeometry', { relative: '1', as: 'geometry' }, [
        point(points[0], 'sourcePoint'),
        point(points[points.length - 1], 'targetPoint'),
        ...(bends.length
          ? [
              element(
                'Array',
                { as: 'points' },
                bends.map((bend) => point(bend)),
              ),
            ]
          : []),
      ]),
    ],
  );
}

const CELLS = {
  device: deviceCell,
  switch: switchCell,
  group: groupCell,
  note: noteCell,
  shape: shapeCell,
  icon: iconCell,
  line: lineCell,
};

// Where a connection meets a node's side, at its handle, as a fraction of
// the node's height (exitY and entryY).
function handleFraction(node, box, handleId) {
  return box.height > 0
    ? finite(handleOffsetY(node, handleId) / box.height)
    : 0.5;
}

// A connection: an edge between the cells of its ends, which a line or a
// missing node is not. It follows its route while the route meets its ends.
function connectionCell(context, edge) {
  const { doc, places } = context;
  const source = places.get(edge.sourceNodeId);
  const target = places.get(edge.targetNodeId);

  if (!context.drawn.has(source) || !context.drawn.has(target)) {
    return null;
  }

  const network = (doc.networks || []).find(
    (entry) => entry.id === edge.networkId,
  );
  const shared = networkStyle(doc, edge.networkId);
  const dashArray = LINE_STYLES.includes(edge.lineStyle)
    ? lineDashArray(edge.lineStyle)
    : shared.dashArray;
  const color =
    styleColor(edge.color) ||
    styleColor(customNetworkColor(network?.color)) ||
    netColor(shared.token);
  const exitY = handleFraction(source.node, source.box, edge.sourceHandleId);
  const entryY = handleFraction(target.node, target.box, edge.targetHandleId);
  const route = fitRoute(
    edge.route,
    {
      x: source.absolute.x + source.box.width,
      y: source.absolute.y + exitY * source.box.height,
    },
    { x: target.absolute.x, y: target.absolute.y + entryY * target.box.height },
    4,
  );
  const bends = route ? route.slice(1, -1) : [];

  return element(
    'mxCell',
    {
      id: context.edgeIds.get(edge),
      value: html(edge.label || network?.name || ''),
      style: style([
        ['edgeStyle', 'orthogonalEdgeStyle'],
        ['html', 1],
        ['rounded', 1],
        ['startArrow', 'none'],
        ['endArrow', 'none'],
        ['exitX', 1],
        ['exitY', exitY],
        ['exitDx', 0],
        ['exitDy', 0],
        ['entryX', 0],
        ['entryY', entryY],
        ['entryDx', 0],
        ['entryDy', 0],
        ['strokeColor', color],
        ['strokeWidth', 2],
        ...dashPairs(dashArray),
        ...edgeLabelPairs(),
      ]),
      edge: '1',
      parent: LAYER_ID,
      source: source.id,
      target: target.id,
    },
    [
      element(
        'mxGeometry',
        { relative: '1', as: 'geometry' },
        bends.length
          ? [
              element(
                'Array',
                { as: 'points' },
                bends.map((bend) => point(bend)),
              ),
            ]
          : [],
      ),
    ],
  );
}

// --- places ------------------------------------------------------------------

// Cell ids: the Builder's own. An id that is empty, taken, "0" or "1" gets
// a number after it.
function idMaker() {
  const used = new Set([ROOT_ID, LAYER_ID]);

  return (wanted) => {
    const base = String(wanted ?? '') || 'cell';
    let id = base;

    for (let next = 2; used.has(id); next += 1) {
      id = `${base}-${next}`;
    }

    used.add(id);

    return id;
  };
}

// Each node's cell id, container, position in it, position on the page and
// size. Unknown kinds and repeated ids are left out.
function placesOf(doc, newId) {
  const nodes = new Map();

  (Array.isArray(doc?.nodes) ? doc.nodes : []).forEach((node, index) => {
    if (node && Object.hasOwn(CELLS, node.kind) && !nodes.has(node.id)) {
      nodes.set(node.id, { node, index });
    }
  });

  const groupOf = (node) => {
    const parent = nodes.get(node.parentId)?.node;

    return parent?.kind === 'group' ? parent : null;
  };
  // Whether the groups above a node lead back to it. A group in such a loop
  // goes on the layer, which ends the loop there.
  const inLoop = (node) => {
    const seen = new Set();

    for (let at = groupOf(node); at && !seen.has(at); at = groupOf(at)) {
      if (at === node) {
        return true;
      }

      seen.add(at);
    }

    return false;
  };
  const parents = new Map();
  const parentOf = (node) => {
    if (!parents.has(node)) {
      parents.set(node, inLoop(node) ? null : groupOf(node));
    }

    return parents.get(node);
  };
  const depths = new Map();
  const depthOf = (node) => {
    if (!depths.has(node)) {
      const parent = parentOf(node);

      depths.set(node, parent ? depthOf(parent) + 1 : 0);
    }

    return depths.get(node);
  };
  const ordered = [...nodes.values()].sort(
    (a, b) =>
      depthOf(a.node) - depthOf(b.node) ||
      Number(!UNDER.has(a.node.kind)) - Number(!UNDER.has(b.node.kind)) ||
      a.index - b.index,
  );
  const places = new Map();

  for (const { node } of ordered) {
    const parent = parentOf(node) ? places.get(parentOf(node).id) : null;
    const absolute = {
      x: finite(Number(node.position?.x)),
      y: finite(Number(node.position?.y)),
    };

    places.set(node.id, {
      node,
      id: newId(node.id),
      parent: parent ? parent.id : LAYER_ID,
      absolute,
      at: parent
        ? {
            x: absolute.x - parent.absolute.x,
            y: absolute.y - parent.absolute.y,
          }
        : absolute,
      box: boxOf(node),
    });
  }

  return places;
}

/**
 * The diagram as a draw.io file: one page, uncompressed. Read the custom
 * icon images first (ensureImages).
 */
export function toDrawio(doc, options = {}) {
  const settings = {
    showNodeIcons: options.showNodeIcons ?? true,
    showNodeDescriptions: options.showNodeDescriptions ?? true,
    iconSize: options.iconSize,
  };
  const newId = idMaker();
  const places = placesOf(doc, newId);
  const edges = Array.isArray(doc?.edges) ? doc.edges.filter(Boolean) : [];
  const edgeIds = new Map(edges.map((edge) => [edge, newId(edge.id)]));
  const context = {
    doc,
    places,
    edgeIds,
    options: settings,
    image: iconImages(doc, options.library || null),
    // The places of the nodes a connection can end at: those with a cell,
    // but lines.
    drawn: new Set(),
  };
  const cells = [];

  for (const place of places.values()) {
    const cell = CELLS[place.node.kind](context, place.node, place);

    if (cell) {
      cells.push(cell);
      if (place.node.kind !== 'line') {
        context.drawn.add(place);
      }
    }
  }

  for (const edge of edges) {
    const cell = connectionCell(context, edge);

    if (cell) {
      cells.push(cell);
    }
  }

  const grid = doc?.grid || {};
  const gridSize = Number.isInteger(grid.size) ? grid.size : DEFAULT_GRID_SIZE;
  const file = element('mxfile', { host: 'phenix', type: 'device' }, [
    element(
      'diagram',
      {
        id: String(doc?.metadata?.id || 'phenix'),
        name: String(doc?.metadata?.name || 'Diagram'),
      },
      [
        element(
          'mxGraphModel',
          {
            dx: '0',
            dy: '0',
            grid: grid.enabled === false ? '0' : '1',
            gridSize: String(gridSize),
            guides: '1',
            tooltips: '1',
            connect: '1',
            arrows: '1',
            fold: '1',
            page: '0',
            pageScale: '1',
            pageWidth: '850',
            pageHeight: '1100',
            math: '0',
            shadow: '0',
            background: light('--bx-bg'),
          },
          [
            element('root', {}, [
              element('mxCell', { id: ROOT_ID }),
              element('mxCell', { id: LAYER_ID, parent: ROOT_ID }),
              ...cells,
            ]),
          ],
        ),
      ],
    ),
  ]);
  const lines = [];

  writeElement(file, 0, lines);

  return `${lines.join('\n')}\n`;
}
