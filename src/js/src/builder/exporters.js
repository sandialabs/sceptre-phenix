// Export helpers: JSON, YAML and images.
//
// The geometry math is pure and unit tested; the DOM/rasterization side takes
// injected dependencies (html-to-image, file-saver, getComputedStyle) so tests
// never touch a real canvas.

import YAML from 'js-yaml';

import { boundsOf } from './model.js';

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
 * Filesystem-safe export file name.
 *
 * @param {object} doc
 * @param {string} extension without the dot
 * @returns {string}
 */
export function exportFileName(doc, extension) {
  const base = String(doc?.name || 'topology')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '');

  return `${base || 'topology'}.${extension}`;
}

/**
 * Bounding box covering every node in the document, padded, so image exports
 * always include the whole diagram rather than the visible viewport.
 *
 * @param {object} doc
 * @param {number} [padding]
 * @returns {{x: number, y: number, width: number, height: number}}
 */
export function documentBounds(doc, padding = IMAGE_PADDING) {
  const nodes = doc?.nodes || [];

  if (nodes.length === 0) {
    return { x: 0, y: 0, width: 320, height: 240 };
  }

  const bounds = boundsOf(nodes);

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
 * handles (a device's "+" new-interface handle is one too) and a
 * connection's pointer hit area and keyboard focus band.
 */
const EXPORT_EXCLUDED_SELECTOR =
  '.vue-flow__handle, .builder-edge__hit, .builder-edge__focus';

/**
 * Classes that mark a selected node, connection or connection label: the
 * builder's own, and Vue Flow's on its wrappers. An image shows the diagram,
 * not what is selected in the editor.
 */
const SELECTION_CLASSES = ['is-selected', 'selected'];

/**
 * SVG presentation properties copied inline before an image export, so a
 * shape keeps the paint builder.css gives it by class.
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
 * Inline style of the element that holds the export copy: it covers the
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

  // Unique IDs stay unique while the copy is in the document.
  for (const item of copy.querySelectorAll('[id]')) {
    item.removeAttribute('id');
  }

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
 * width come from builder.css classes, was exported with no stroke at all.
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
 * rendered is a copy of it (see exportCopy) whose transform is the export's
 * own, so the image is the same however the canvas is panned or zoomed.
 *
 * @param {object} params element, doc, format ('png'|'svg'), toPng, toSvg,
 *   saveAs, backgroundColor, computedStyle (defaults to the element's
 *   window.getComputedStyle)
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
  } = params;

  if (!element) {
    throw new Error('No canvas element to export.');
  }

  const viewport = computeExportViewport(documentBounds(doc));
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
