// Export helpers: JSON, YAML and images.
//
// The geometry math is pure and unit tested; the DOM/rasterization side takes
// injected dependencies (html-to-image + file-saver) so tests never touch a
// real canvas.

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
 * Renders the canvas viewport element to an image and saves it.
 *
 * @param {object} params element, doc, format ('png'|'svg'), toPng, toSvg,
 *   saveAs, backgroundColor
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
  } = params;

  if (!element) {
    throw new Error('No canvas element to export.');
  }

  const viewport = computeExportViewport(documentBounds(doc));
  const render = format === 'svg' ? toSvg : toPng;

  const dataUrl = await render(element, {
    backgroundColor,
    width: viewport.width,
    height: viewport.height,
    style: {
      width: `${viewport.width}px`,
      height: `${viewport.height}px`,
      transform: viewport.transform,
      transformOrigin: '0 0',
    },
  });

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
