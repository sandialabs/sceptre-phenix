// The built-in icons as the drafts page's Icons tab lists them
// (BuilderIcons.vue), and a built-in icon as a standalone SVG file.
//
// The list is every key of ICON_ORDER (iconLabels.js) that the icon key
// registry accepts (isIconKey in catalog.js), in that order.

import { isIconKey } from './catalog.js';
import { ICON_ORDER, iconLabel } from './iconLabels.js';

/** The source a built-in icon's card and preview show. */
export const BUILTIN_ICON_OWNER = 'Built-in';

/**
 * The color of a downloaded built-in icon. On the canvas, an icon takes the
 * text color of the theme. A file has no theme, so it takes the light
 * theme's text color, which shows on a light background.
 */
export const BUILTIN_ICON_COLOR = '#1b2330';

/** The width and height of a downloaded built-in icon, in pixels. */
export const BUILTIN_ICON_FILE_SIZE = 96;

/**
 * @returns {{key: string, label: string, builtin: true}[]} the built-in
 *   icons, in the order the UI lists them
 */
export function builtinIcons() {
  return ICON_ORDER.filter(isIconKey).map((key) => ({
    key,
    label: iconLabel(key),
    builtin: true,
  }));
}

/**
 * @param {{key: string, label: string}} icon a built-in icon
 * @param {string} words what the filter field holds
 * @returns {boolean} whether the icon's label, key or source holds the
 *   words, ignoring case
 */
export function builtinIconMatches(icon, words) {
  const wanted = String(words || '')
    .trim()
    .toLowerCase();

  return (
    !wanted ||
    [icon.label, icon.key, BUILTIN_ICON_OWNER].some((text) =>
      String(text).toLowerCase().includes(wanted),
    )
  );
}

const XML_ESCAPES = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&apos;',
};

function escapeXml(text) {
  return String(text).replace(/[&<>"']/g, (char) => XML_ESCAPES[char]);
}

/**
 * A built-in icon as a standalone SVG file. The drawing is BuilderIcon's
 * (its 24 by 24 view box, round 1.6 pixel strokes), with each currentColor
 * set to a fixed color, so the file shows the same outside the Builder.
 *
 * @param {object} params
 * @param {string} params.markup the drawing: BuilderIcon's elements, as XML
 * @param {string} params.label the icon's label, for the file's title
 * @param {string} [params.color]
 * @param {number} [params.size] the width and height, in pixels
 * @returns {string} the SVG file's text
 */
export function builtinIconSvg({
  markup,
  label,
  color = BUILTIN_ICON_COLOR,
  size = BUILTIN_ICON_FILE_SIZE,
}) {
  const drawing = String(markup || '').replace(/currentColor/g, color);

  return (
    '<?xml version="1.0" encoding="UTF-8"?>\n' +
    `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" ` +
    `viewBox="0 0 24 24" fill="none" stroke="${color}" stroke-width="1.6" ` +
    'stroke-linecap="round" stroke-linejoin="round">' +
    `<title>${escapeXml(label)}</title>${drawing}</svg>\n`
  );
}

/**
 * Saves a built-in icon as an SVG file named after its key.
 *
 * @param {{key: string, label: string}} icon a built-in icon
 * @param {string} markup its drawing (see builtinIconSvg)
 * @param {{saveAs: Function}} deps saveAs from file-saver
 * @returns {string} the file's name, or '' when there is no drawing, which
 *   is not saved
 */
export function saveBuiltinIconFile(icon, markup, { saveAs }) {
  if (!isIconKey(icon?.key) || !markup) {
    return '';
  }

  const fileName = `${icon.key}.svg`;

  saveAs(
    new Blob([builtinIconSvg({ markup, label: iconLabel(icon.key) })], {
      type: 'image/svg+xml',
    }),
    fileName,
  );

  return fileName;
}
