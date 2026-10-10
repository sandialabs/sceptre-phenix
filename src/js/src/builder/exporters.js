// The files that Download saves: JSON, YAML, the Topology config and images.
//
// The geometry math is pure and unit tested. The DOM and rasterization code
// takes injected dependencies (html-to-image, file-saver, getComputedStyle),
// and the Topology config takes the API call. Thus tests never touch a real
// canvas.

import YAML from 'js-yaml';

import { listOf } from './announce.js';
import { sentence } from './api.js';
import { MAX_DOCUMENT_ICONS, embedIcons } from './icons.js';
import { footprintBounds } from './nodeNotes.js';
import { configName } from './publish.js';

export const IMAGE_PADDING = 40;

/**
 * The document that a Builder JSON or YAML download saves. It is a copy that
 * carries every custom icon that its nodes and templates name, from its own
 * copies or the server's icon library (see embedIcons). Thus the file is
 * complete, and an upload elsewhere brings its icons.
 *
 * @param {object} doc
 * @param {{lookup: Function}|null} [library] the icon library
 * @returns {{doc: object, note: string}} the copy, and what the dialog says
 *   of the icons it could not carry ('' for none)
 */
export function downloadDocument(doc, library = null) {
  const { doc: carried, missing, left } = embedIcons(doc, library);
  const notes = [];

  if (missing.length > 0) {
    notes.push(
      missing.length === 1
        ? `It does not carry the custom icon ${missing[0]}, which the server's icon library does not have: the nodes that name it show their built-in icon.`
        : `It does not carry the custom icons ${listOf(missing)}, which the server's icon library does not have: the nodes that name them show their built-in icon.`,
    );
  }

  if (left.length > 0) {
    notes.push(
      `It carries at most ${MAX_DOCUMENT_ICONS} custom icons, so ${listOf(left)} ${left.length === 1 ? 'was' : 'were'} left out.`,
    );
  }

  return { doc: carried, note: notes.join(' ') };
}

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
 * Bounding box of every node in the document, with padding, so an image
 * always includes the whole diagram and not only the visible viewport. It
 * includes the notes of a device or switch (below its box) while the canvas
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

// The largest width and height of a PNG or SVG download, in pixels.
export const MAX_IMAGE_SIZE = 4096;

// The least pixel ratio of a PNG, which only keeps the ratio above zero.
const MIN_PIXEL_RATIO = 0.01;

// Whether a side of the bounds can be fitted: finite and above zero.
function fittable(side) {
  return Number.isFinite(side) && side > 0;
}

/**
 * Viewport transform that fits `bounds` into an image of the returned size.
 * The image is never wider than maxWidth or higher than maxHeight, however
 * large the bounds. A large diagram is scaled down to fit whole, with its
 * aspect ratio kept, and below the canvas's minimum zoom (0.2) when
 * necessary.
 *
 * @param {{x: number, y: number, width: number, height: number}} bounds
 * @param {object} [options] maxWidth and maxHeight (MAX_IMAGE_SIZE by
 *   default), and maxZoom (2 by default)
 * @returns {{width: number, height: number, zoom: number, x: number, y: number, transform: string}}
 */
export function computeExportViewport(bounds, options = {}) {
  const maxWidth = Math.max(1, options.maxWidth ?? MAX_IMAGE_SIZE);
  const maxHeight = Math.max(1, options.maxHeight ?? MAX_IMAGE_SIZE);
  const maxZoom = options.maxZoom ?? 2;

  // Bounds with a side that is zero, negative or not finite have nothing to
  // fit: they are drawn at zoom 1 in the smallest image. The fit of any
  // other bounds is above zero, however large they are.
  const drawable = fittable(bounds.width) && fittable(bounds.height);
  const width = drawable ? Math.max(1, bounds.width) : 1;
  const height = drawable ? Math.max(1, bounds.height) : 1;
  const zoom = drawable
    ? Math.min(maxWidth / width, maxHeight / height, maxZoom)
    : 1;

  // Rounding up to whole pixels never takes the image past its largest
  // size: the fit's product may come out a hair above it.
  const outWidth = Math.min(maxWidth, Math.ceil(width * zoom));
  const outHeight = Math.min(maxHeight, Math.ceil(height * zoom));
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
 * The pixel ratio at which a PNG of `viewport` is drawn. html-to-image
 * multiplies the image's width and height by it. Without a ratio, it uses the
 * screen's devicePixelRatio, which would make a 4096-pixel image 8192 pixels
 * wide on a 2x screen. The ratio is the screen's while the PNG stays within
 * maxSize pixels in each direction, so a small image is as sharp as the
 * screen. Otherwise the ratio is smaller.
 *
 * @param {{width: number, height: number}} viewport the image's size in CSS
 *   pixels (see computeExportViewport)
 * @param {number} [devicePixelRatio] the screen's ratio. 1 when it is not a
 *   number above zero.
 * @param {number} [maxSize] the largest width and height of the PNG
 * @returns {number}
 */
export function imagePixelRatio(
  viewport,
  devicePixelRatio = 1,
  maxSize = MAX_IMAGE_SIZE,
) {
  const screen = devicePixelRatio > 0 ? devicePixelRatio : 1;

  return Math.max(
    Math.min(
      screen,
      maxSize / Math.max(1, viewport.width),
      maxSize / Math.max(1, viewport.height),
    ),
    MIN_PIXEL_RATIO,
  );
}

/**
 * Editing affordances that an image of the diagram leaves out:
 *   - connection handles (a device's "+" new-interface handle is one too)
 *   - the pointer hit area and keyboard focus band of a connection or a line
 *   - the handles of a line's points and of a node's resizer
 *   - the marks and text of the diagram check results
 *   - the hidden text of a node's info tooltip.
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
 * Prevents the IDs of a copy of the canvas from repeating the canvas's own
 * IDs while the copy is in the document. Some elements of the copy are the
 * target of a url(#id) reference from an SVG element of the copy, such as
 * the marker that draws a line's arrowhead. Each such element gets its own
 * ID, and the references point at that ID, so the image still draws it. All
 * other IDs are removed.
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
 * Inline style of the element that holds an image's copy. It covers the
 * canvas's pane, as the pane does, but it is transparent and lets the pointer
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
 * position among the canvas's elements gives it the same styles. The live
 * canvas does not change.
 *
 * The copy is in a holder that is transparent, inert and hidden from
 * assistive technology, so nothing on screen or in the accessibility tree
 * changes while the image renders. The holder carries these attributes, not
 * the copy. The image keeps the attributes of the element it is rendered
 * from, and an SVG file marked aria-hidden or inert exposes none of its text.
 * Remove the holder after the render.
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
 * styles only on the <svg> itself. Without this function, a connection line,
 * whose stroke and width come from builder.css classes, has no stroke.
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
 * `element` is the Vue Flow pane that carries the live pan and zoom. The
 * render uses a copy of it (see exportCopy) with the image's own transform,
 * so the image is the same however the canvas is panned or zoomed.
 *
 * @param {object} params element, doc, format ('png'|'svg'), toPng, toSvg,
 *   saveAs, backgroundColor, computedStyle (defaults to the element's
 *   window.getComputedStyle), showNotes (whether the canvas shows node
 *   notes, which the image then holds. True by default.), devicePixelRatio
 *   (defaults to the ratio of the element's window. See imagePixelRatio.)
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
    devicePixelRatio = element?.ownerDocument?.defaultView?.devicePixelRatio,
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
      // A PNG's pixels are its size times this ratio. An SVG ignores it.
      pixelRatio: imagePixelRatio(viewport, devicePixelRatio),
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
 * What the Download dialog says after Topology YAML is saved: the file, what
 * the projection left out or changed, and why publishing the topology would
 * be refused. The file does not show the last item.
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
