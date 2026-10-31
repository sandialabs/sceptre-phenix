// The files that Download saves: JSON, YAML, the Topology config and images.
// The DOM and rasterization code takes injected dependencies, so tests never
// touch a real canvas.

import YAML from 'js-yaml';

import { listOf } from './announce.js';
import { sentence } from './api.js';
import {
  MAX_DOCUMENT_ICONS,
  embedIcons,
  iconRefs,
  resolveIcon,
} from './icons.js';
import { nodesBounds } from './model.js';
import { configName } from './publish.js';

export const IMAGE_PADDING = 40;

/**
 * The document that a JSON or YAML download saves: a copy that carries every
 * custom icon that it names (see embedIcons), so an upload elsewhere brings
 * its icons.
 */
export function downloadDocument(doc, library = null) {
  const { doc: carried, missing, left } = embedIcons(doc, library);
  const notes = [];

  if (missing.length > 0) {
    notes.push(
      missing.length === 1
        ? `It does not carry the custom icon ${missing[0]}. The server's icon library does not have it. The nodes that name it show their built-in icon.`
        : `It does not carry the custom icons ${listOf(missing)}. The server's icon library does not have them. The nodes that name them show their built-in icon.`,
    );
  }

  if (left.length > 0) {
    notes.push(
      `It carries at most ${MAX_DOCUMENT_ICONS} custom icons. It does not carry ${listOf(left)}.`,
    );
  }

  return { doc: carried, note: notes.join(' ') };
}

/**
 * What Download says when a picture or a draw.io file cannot show a custom
 * icon, because its image could not be read. '' when it shows every icon.
 */
export function pictureIconNote(
  doc,
  library,
  { showNodeIcons = true, subject = 'picture' } = {},
) {
  const nodes = (doc?.nodes || []).filter(
    (node) => showNodeIcons || node?.kind === 'icon',
  );
  const unread = [...iconRefs({ nodes })].filter((name) => {
    const icon = resolveIcon(name, doc?.icons, library);

    return icon !== null && typeof icon.data !== 'string';
  });

  if (unread.length === 0) {
    return '';
  }

  return unread.length === 1
    ? `The Builder could not read the image of the custom icon ${unread[0]}. The ${subject} does not show it. Try again later.`
    : `The Builder could not read the images of the custom icons ${listOf(unread)}. The ${subject} does not show them. Try again later.`;
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

/** Filesystem-safe name of a downloaded file. */
export function exportFileName(doc, extension) {
  const base = String(doc?.metadata?.name || 'topology')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '');

  return `${base || 'topology'}.${extension}`;
}

/**
 * Bounding box of every node, with padding, so an image holds the whole
 * diagram.
 */
export function documentBounds(doc) {
  const nodes = doc?.nodes || [];

  if (nodes.length === 0) {
    return { x: 0, y: 0, width: 320, height: 240 };
  }

  return nodesBounds(nodes, IMAGE_PADDING);
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
 * Viewport transform that fits `bounds` into an image no larger than
 * maxWidth by maxHeight, keeping the aspect ratio. It can zoom below the
 * canvas's minimum.
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
 * The pixel ratio for a PNG of `viewport`: the screen's while the PNG stays
 * within maxSize pixels, else smaller. Without it, html-to-image would make a
 * 4096-pixel image 8192 pixels wide on a 2x screen.
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

// Editing affordances that an image leaves out: handles, hit areas, focus
// bands, check marks and the hidden text of node info tooltips.
const EXPORT_EXCLUDED_SELECTOR =
  '.vue-flow__handle, .builder-edge__hit, .builder-edge__focus, ' +
  '.builder-line__hit, .builder-line__focus, .builder-line__point, ' +
  '.builder-resizer, .vue-flow__resize-control, ' +
  '.builder-node__issue, .builder-node__issue-text, .builder-node__info, ' +
  '.builder-edge__issue, .builder-edge__issue-text';

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
 * Prevents a canvas copy's IDs from repeating the canvas's own IDs. Each
 * url(#id) target gets its own ID, so the image still draws it. All other IDs
 * are removed.
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
 * Adds a copy of `element` beside it, to render without editing affordances
 * or selection. Its holder, not the copy, is inert and aria-hidden, because
 * the image keeps the copy's attributes. Remove the holder after the render.
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
 * Copies the computed paint of every element inside an <svg> into its inline
 * style. html-to-image inlines styles only on the <svg> itself, so a
 * connection line would otherwise have no stroke.
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
 * Renders the diagram to an image and saves it. It renders a copy of the Vue
 * Flow pane (exportCopy) with the image's own transform, so the canvas's pan
 * and zoom do not matter. Returns the data URL.
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
    devicePixelRatio = element?.ownerDocument?.defaultView?.devicePixelRatio,
  } = params;

  if (!element) {
    throw new Error('No canvas element to download.');
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
 * Saves the Topology config that the document publishes as, in
 * <file name>.topology.yaml. The server makes it from the same projection
 * that Publish writes.
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
 * the projection changed, and why publishing would be refused.
 */
export function describeTopologyExport(saved) {
  const { fileName, warnings = [], publishBlockers = [] } = saved;
  const blocked = publishBlockers.length
    ? `You cannot publish this topology yet. ${publishBlockers.map(sentence).join(' ')}`
    : '';

  return [`Saved ${fileName}.`, ...warnings.map(sentence), blocked]
    .filter(Boolean)
    .join(' ');
}
