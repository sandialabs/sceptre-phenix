// The built-in icons as the drafts page's Icons tab lists them
// (BuilderIcons.vue), and a built-in icon as a standalone SVG or PNG file.
//
// The list is every key of ICON_ORDER (iconLabels.js) that the icon key
// registry accepts (isIconKey in catalog.js), in that order.

import { isIconKey } from './catalog.js';
import { ICON_ORDER, iconLabel } from './iconLabels.js';
import { MAX_ICON_PIXELS, decodeIconData } from './icons.js';

/** The source a built-in icon's card and preview show. */
export const BUILTIN_ICON_OWNER = 'Built-in';

/**
 * The color of a downloaded built-in icon. On the canvas, an icon takes the
 * text color of the theme. A file has no theme, so it takes the light
 * theme's text color, which shows on a light background.
 */
export const BUILTIN_ICON_COLOR = '#1b2330';

// The width and height of a downloaded built-in icon, in pixels: the largest
// size of an uploaded icon.
export const BUILTIN_ICON_FILE_SIZE = MAX_ICON_PIXELS;

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

// How many icons the Icons tab counts: the built-in icons and the library's
// icons, or null until the library is read.
export function iconsTabCount(library) {
  if (!library?.loaded) {
    return null;
  }

  const held = Array.isArray(library.icons) ? library.icons.length : 0;

  return builtinIcons().length + held;
}

/** Whether the icon's label, key or source holds the words, ignoring case. */
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
 * A built-in icon as a standalone SVG file, with each currentColor set to a
 * fixed color so the file shows the same outside the Builder.
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

// Saves a built-in icon's SVG, drawn by `rasterize` (rasterizeIcon), as a PNG
// named after its key. Returns the file name, or '' without a drawing.
export async function saveBuiltinIconFile(icon, markup, { saveAs, rasterize }) {
  if (!isIconKey(icon?.key) || !markup) {
    return '';
  }

  const text = builtinIconSvg({ markup, label: iconLabel(icon.key) });
  const svg = new Blob([text], { type: 'image/svg+xml' });
  const bytes = decodeIconData((await rasterize(svg))?.data);

  if (!bytes) {
    return '';
  }

  const fileName = `${icon.key}.png`;

  saveAs(new Blob([bytes], { type: 'image/png' }), fileName);

  return fileName;
}
