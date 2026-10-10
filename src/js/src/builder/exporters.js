// The files Download saves: JSON, YAML, the Topology config and images.
//
// The geometry math is pure and unit tested; the DOM/rasterization side takes
// injected dependencies (html-to-image, file-saver, getComputedStyle) so tests
// never touch a real canvas, and the Topology config the API call.

import YAML from 'js-yaml';

import { sentence } from './api.js';
import { footprintBounds } from './nodeNotes.js';
import { configName } from './publish.js';

export const IMAGE_PADDING = 40;

/**
 * @param {object} doc
 * @returns {string} pretty printed JSON document
 */
export function toJSONString(doc) {
  return `${JSON.stringify(doc, null, 2)}\n`;
}

/**
 * @param {object} doc
 * @returns {string} YAML document
 */
export function toYAMLString(doc) {
  return YAML.dump(doc, { noRefs: true, sortKeys: false });
}

/**
 * Filesystem-safe name of a downloaded file.
 *
 * @param {object} doc
 * @param {string} extension without the dot
 * @returns {string}
 */
export function exportFileName(doc, extension) {
  const base = String(doc?.metadata?.name || 'topology')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '');

  return `${base || 'topology'}.${extension}`;
}

/**
 * Bounding box covering every node in the document, padded, so an image
 * always includes the whole diagram rather than the visible viewport. A
 * device's or a switch's notes, below its box, are in it while the canvas
 * shows them (see nodeFootprint).
 *
 * @param {object} doc
 * @param {number} [padding]
 * @param {{showNotes?: boolean}} [options] whether the canvas shows node
 *   notes, true by default
 * @returns {{x: number, y: number, width: number, height: number}}
 */
export function documentBounds(doc, padding = IMAGE_PADDING, options = {}) {
  const nodes = doc?.nodes || [];

  if (nodes.length === 0) {
    return { x: 0, y: 0, width: 320, height: 240 };
  }

  const bounds = footprintBounds(nodes, options);

  return {
    x: bounds.x - padding,
    y: bounds.y - padding,
    width: bounds.width + padding * 2,
    height: bounds.height + padding * 2,
  };
}

/**
 * Viewport transform that fits `bounds` into an image of the returned size.
 *
 * @param {{x: number, y: number, width: number, height: number}} bounds
 * @param {object} [options] maxWidth, maxHeight, minZoom, maxZoom
 * @returns {{width: number, height: number, zoom: number, x: number, y: number, transform: string}}
 */
export function computeExportViewport(bounds, options = {}) {
  const maxWidth = options.maxWidth ?? 4096;
  const maxHeight = options.maxHeight ?? 4096;
  const minZoom = options.minZoom ?? 0.2;
  const maxZoom = options.maxZoom ?? 2;

  const width = Math.max(1, bounds.width);
  const height = Math.max(1, bounds.height);
  const fit = Math.min(maxWidth / width, maxHeight / height, maxZoom);
  const zoom = Math.max(minZoom, Math.min(maxZoom, fit));

  const outWidth = Math.ceil(width * zoom);
  const outHeight = Math.ceil(height * zoom);
  const x = -bounds.x * zoom;
  const y = -bounds.y * zoom;

  return {
    width: outWidth,
    height: outHeight,
    zoom,
    x,
    y,
    transform: `translate(${x}px, ${y}px) scale(${zoom})`,
  };
}

/**
 * Editing affordances that an image of the diagram leaves out: connection
 * handles (a device's "+" new-interface handle is one too), a
 * connection's or a line's pointer hit area and keyboard focus band, the
 * handles of a line's points and of a node's resizer, the marks and text
 * of what the diagram checks found, and the hidden text of a node's info
 * tooltip.
 */
const EXPORT_EXCLUDED_SELECTOR =
  '.vue-flow__handle, .builder-edge__hit, .builder-edge__focus, ' +
  '.builder-line__hit, .builder-line__focus, .builder-line__point, ' +
  '.builder-resizer, .vue-flow__resize-control, ' +
  '.builder-node__issue, .builder-node__issue-text, .builder-node__info';

/**
 * SVG attributes that point at another element of the drawing by
 * url(#id): the arrowheads of a line (see LineNode.vue).
 */
const URL_REFERENCES = ['marker-start', 'marker-mid', 'marker-end'];

// How many copies exportCopy has made, which keeps the IDs of each copy
// apart from those of the canvas and of any other copy.
let copiesMade = 0;

// The id a url(#id) value points at, or '' for any other value.
function urlReference(value) {
  const match = /^url\(\s*['"]?#([^'")]+)['"]?\s*\)$/.exec(
    String(value ?? '').trim(),
  );

  return match ? match[1] : '';
}

/**
 * Keeps the IDs of a copy of the canvas from repeating the canvas's own
 * while the copy is in the document. An element of the copy that an SVG
 * element of the copy points at by url(#id), such as the marker a line's
 * arrowhead is drawn with, is given an ID of its own, and the references
 * point at that ID, so the image still draws it; every other ID is removed.
 *
 * @param {Element} copy
 */
function settleCopyIds(copy) {
  copiesMade += 1;

  const references = [];
  const wanted = new Set();

  for (const item of copy.querySelectorAll('svg *')) {
    for (const name of URL_REFERENCES) {
      const id = urlReference(item.getAttribute(name));

      if (id) {
        wanted.add(id);
        references.push({ item, name, id });
      }
    }
  }

  const renamed = new Map();

  for (const item of copy.querySelectorAll('[id]')) {
    const id = item.id;

    if (wanted.has(id) && !renamed.has(id)) {
      renamed.set(id, `${id}-image-${copiesMade}`);
      item.id = renamed.get(id);
    } else {
      item.removeAttribute('id');
    }
  }

  for (const { item, name, id } of references) {
    if (renamed.has(id)) {
      item.setAttribute(name, `url(#${renamed.get(id)})`);
    }
  }
}

/**
 * Classes that mark a selected node, connection or connection label: the
 * builder's own, and Vue Flow's on its wrappers. An image shows the diagram,
 * not what is selected in the editor.
 */
const SELECTION_CLASSES = ['is-selected', 'selected'];

/**
 * SVG presentation properties copied inline before an image is rendered, so
 * a shape keeps the paint builder.css gives it by class.
 */
const SVG_PAINT_PROPERTIES = [
  'display',
  'visibility',
  'opacity',
  'fill',
  'fill-opacity',
  'stroke',
  'stroke-width',
  'stroke-opacity',
  'stroke-dasharray',
  'stroke-dashoffset',
  'stroke-linecap',
  'stroke-linejoin',
];

/**
 * Inline style of the element that holds an image's copy: it covers the
 * canvas's pane, as the pane does, but is transparent and lets the pointer
 * through.
 */
const EXPORT_HOLDER_STYLE = {
  position: 'absolute',
  inset: '0',
  opacity: '0',
  'pointer-events': 'none',
};

/**
 * Adds a copy of `element` beside it, to render in its place: the diagram
 * without the editing affordances and with nothing selected. The copy's
 * position among the canvas's elements gives it the same styles, and it
 * leaves the live canvas as it is.
 *
 * The copy sits in a holder that is transparent, inert and hidden from
 * assistive technology, so nothing on screen or in the accessibility tree
 * changes while the image renders. The holder carries those, not the copy:
 * the image keeps the attributes of the element it is rendered from, and an
 * SVG file marked aria-hidden or inert exposes none of its text. Remove the
 * holder afterwards.
 *
 * @param {Element} element
 * @returns {{copy: Element, holder: Element}} the copy, to render, and its
 *   holder, to remove
 */
export function exportCopy(element) {
  const copy = element.cloneNode(true);

  for (const affordance of copy.querySelectorAll(EXPORT_EXCLUDED_SELECTOR)) {
    affordance.remove();
  }

  const selected = SELECTION_CLASSES.map((name) => `.${name}`).join(', ');

  for (const item of copy.querySelectorAll(selected)) {
    item.classList.remove(...SELECTION_CLASSES);
  }

  // On the canvas each node and connection is a toggle button. In a file it is
  // a labelled picture: nothing to press, no Tab stop, and no description,
  // whose keyboard hint is not in the file. Vue Flow sets the camelCase
  // tabIndex on connections, which removing tabindex leaves on an SVG element.
  for (const item of copy.querySelectorAll('[role="button"]')) {
    item.setAttribute('role', 'img');
    for (const name of [
      'tabindex',
      'tabIndex',
      'aria-pressed',
      'aria-describedby',
    ]) {
      item.removeAttribute(name);
    }
  }

  settleCopyIds(copy);

  const holder = element.ownerDocument.createElement('div');

  holder.setAttribute('aria-hidden', 'true');
  holder.setAttribute('inert', '');
  for (const [name, value] of Object.entries(EXPORT_HOLDER_STYLE)) {
    holder.style.setProperty(name, value);
  }
  holder.append(copy);
  element.after(holder);

  return { copy, holder };
}

/**
 * Copies the computed paint of every element inside an <svg> under `element`
 * into its inline style.
 *
 * html-to-image copies an <svg> whole (cloneNode(true)) and inlines computed
 * styles on the <svg> itself only, so a connection line, whose stroke and
 * width come from builder.css classes, was drawn with no stroke at all.
 *
 * @param {Element} element
 * @param {(el: Element) => CSSStyleDeclaration} computedStyle
 */
export function inlineSvgPaint(element, computedStyle) {
  for (const shape of element.querySelectorAll('svg *')) {
    const computed = computedStyle(shape);

    for (const name of SVG_PAINT_PROPERTIES) {
      shape.style.setProperty(name, computed.getPropertyValue(name));
    }
  }
}

/**
 * Renders the canvas's diagram to an image and saves it.
 *
 * `element` is the Vue Flow pane that carries the live pan and zoom. What is
 * rendered is a copy of it (see exportCopy) whose transform is the image's
 * own, so the image is the same however the canvas is panned or zoomed.
 *
 * @param {object} params element, doc, format ('png'|'svg'), toPng, toSvg,
 *   saveAs, backgroundColor, computedStyle (defaults to the element's
 *   window.getComputedStyle), showNotes (whether the canvas shows node
 *   notes, which the image then holds; true by default)
 * @returns {Promise<string>} data url
 */
export async function exportImage(params) {
  const {
    element,
    doc,
    format = 'png',
    toPng,
    toSvg,
    saveAs,
    backgroundColor,
    computedStyle = (el) => el.ownerDocument.defaultView.getComputedStyle(el),
    showNotes = true,
  } = params;

  if (!element) {
    throw new Error('No canvas element to download.');
  }

  const viewport = computeExportViewport(
    documentBounds(doc, IMAGE_PADDING, { showNotes }),
  );
  const render = format === 'svg' ? toSvg : toPng;
  const { copy, holder } = exportCopy(element);
  let dataUrl;

  try {
    inlineSvgPaint(copy, computedStyle);
    dataUrl = await render(copy, {
      backgroundColor,
      width: viewport.width,
      height: viewport.height,
      style: {
        width: `${viewport.width}px`,
        height: `${viewport.height}px`,
        transform: viewport.transform,
        transformOrigin: '0 0',
        overflow: 'visible',
      },
    });
  } finally {
    holder.remove();
  }

  if (saveAs) {
    saveAs(dataUrl, exportFileName(doc, format));
  }

  return dataUrl;
}

/**
 * Saves a text document using an injected saver (file-saver in the app).
 *
 * @param {object} params text, mime, fileName, saveAs, BlobCtor
 */
export function saveText(params) {
  const { text, mime, fileName, saveAs, BlobCtor = Blob } = params;
  const blob = new BlobCtor([text], { type: `${mime};charset=utf-8` });

  saveAs(blob, fileName);

  return blob;
}

/**
 * Saves the phenix Topology config the document publishes as, named as the
 * Publish dialog proposes, in <file name>.topology.yaml. The server makes
 * it (exportTopology in api.js), from the same projection Publish writes.
 *
 * @param {object} params doc, exportTopology (the API call), saveAs,
 *   BlobCtor
 * @returns {Promise<{fileName: string, name: string, warnings: string[],
 *   publishBlockers: string[]}>}
 */
export async function saveTopologyYAML(params) {
  const { doc, exportTopology, saveAs, BlobCtor } = params;
  const { yaml, ...result } = await exportTopology(
    doc,
    configName(doc?.metadata?.name),
  );
  const fileName = exportFileName(doc, 'topology.yaml');

  saveText({ text: yaml, mime: 'text/yaml', fileName, saveAs, BlobCtor });

  return { ...result, fileName };
}

/**
 * What the Download dialog says once Topology YAML is saved: the file, what
 * the projection left out or changed, and why publishing the topology would
 * be refused, which the file does not show.
 *
 * @param {{fileName: string, warnings?: string[], publishBlockers?: string[]}} saved
 * @returns {string}
 */
export function describeTopologyExport(saved) {
  const { fileName, warnings = [], publishBlockers = [] } = saved;
  const blocked = publishBlockers.length
    ? `This topology cannot be published yet: ${publishBlockers.join('; ')}.`
    : '';

  return [`Saved ${fileName}.`, ...warnings.map(sentence), blocked]
    .filter(Boolean)
    .join(' ');
}
