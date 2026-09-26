// The colors people give networks, notes and groups, as the canvas draws
// them.
//
// A network's connections are drawn in its color, with its dash pattern and
// label beside it, so color is never the only cue. The colors addNetwork
// picks, which the color picker suggests, are drawn with the theme's network
// token of the same place (--bx-net-N for the Nth), which keeps 3:1 against
// the canvas in both themes. Any other color the user chose is drawn as
// chosen; where it would not keep 3:1 against a theme's canvas, the line is
// cased in that theme's text color there, so it never fades into the canvas
// (WCAG 1.4.11). Notes and groups show theirs as an accent bar, and
// switches as a swatch, both decorative: their names say what they are.

import { DEFAULT_NETWORK_COLORS } from './model.js';

// What a connection is drawn over in each theme: the canvas (--bx-bg-alt)
// and its grid lines (--bx-grid). A line keeps 3:1 against both.
const CANVAS = {
  light: ['#f4f6f9', '#dfe4ec'],
  dark: ['#1a212b', '#27303c'],
};

// WCAG 1.4.11's minimum for a graphical object.
const MIN_CONTRAST = 3;

const HEX = /^#([0-9a-f]{3,4}|[0-9a-f]{6}|[0-9a-f]{8})$/i;
const RGB =
  /^rgba?\(\s*(\d+(?:\.\d+)?)[\s,]+(\d+(?:\.\d+)?)[\s,]+(\d+(?:\.\d+)?)\s*(?:[,/]\s*([\d.]+%?)\s*)?\)$/i;

// A color the browser can draw. Without CSS.supports (outside a browser),
// hex colors only.
function drawable(value) {
  const supports = globalThis.CSS?.supports;

  return typeof supports === 'function'
    ? supports.call(globalThis.CSS, 'color', value)
    : HEX.test(value);
}

/**
 * A color as drawn: the value, trimmed, when it is one the browser can draw;
 * '' when there is none or it is not a color.
 *
 * @param {string} value
 * @returns {string}
 */
export function drawnColor(value) {
  const color = String(value ?? '').trim();

  return color && drawable(color) ? color : '';
}

/**
 * The theme token a network color is drawn with: N of --bx-net-N for the
 * Nth color addNetwork picks, -1 for any other color or none.
 *
 * @param {string} value
 * @returns {number}
 */
export function networkColorToken(value) {
  return DEFAULT_NETWORK_COLORS.indexOf(drawnColor(value).toLowerCase());
}

/**
 * A network color the user chose, or '' for none, one addNetwork picks
 * (drawn with the theme's token) or one that is not a color.
 *
 * @param {string} value
 * @returns {string}
 */
export function customNetworkColor(value) {
  return networkColorToken(value) === -1 ? drawnColor(value) : '';
}

/**
 * A network color as the canvas draws it, for a swatch of it: the theme's
 * token for one addNetwork picks, else the color as chosen; '' for none or
 * one that is not a color.
 *
 * @param {string} value
 * @returns {string}
 */
export function drawnNetworkColor(value) {
  const token = networkColorToken(value);

  return token === -1 ? drawnColor(value) : `var(--bx-net-${token})`;
}

// [r, g, b] from 0 to 255 for an opaque hex or rgb() color; null for any
// other, a translucent one included.
function channels(color) {
  const hex = HEX.exec(color)?.[1];

  if (hex) {
    const digits =
      hex.length <= 4
        ? [...hex].map((digit) => digit + digit)
        : hex.match(/../g);
    const [r, g, b, a] = digits.map((pair) => parseInt(pair, 16));

    return a === undefined || a === 255 ? [r, g, b] : null;
  }

  const rgb = RGB.exec(color);
  const alpha = rgb?.[4];

  if (!rgb) {
    return null;
  }

  if (
    alpha !== undefined &&
    parseFloat(alpha) / (alpha.endsWith('%') ? 100 : 1) < 1
  ) {
    return null;
  }

  return rgb.slice(1, 4).map(Number);
}

function luminance([r, g, b]) {
  const linear = (value) => {
    const c = value / 255;

    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };

  return 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);
}

/**
 * The contrast ratio of two opaque colors, or null when either is not one
 * this can read.
 *
 * @param {string} a
 * @param {string} b
 * @returns {number|null}
 */
export function contrastRatio(a, b) {
  const [first, second] = [channels(a), channels(b)];

  if (!first || !second) {
    return null;
  }

  const [lighter, darker] = [luminance(first), luminance(second)].sort(
    (x, y) => y - x,
  );

  return (lighter + 0.05) / (darker + 0.05);
}

/**
 * The themes in which a line of this color needs a casing: those whose
 * canvas or grid it keeps less than 3:1 against, and both when its contrast
 * cannot be read (a named or translucent color).
 *
 * @param {string} color a drawn color
 * @returns {{light: boolean, dark: boolean}}
 */
export function needsCasing(color) {
  const low = (backgrounds) =>
    backgrounds.some((background) => {
      const ratio = contrastRatio(color, background);

      return ratio === null || ratio < MIN_CONTRAST;
    });

  return { light: low(CANVAS.light), dark: low(CANVAS.dark) };
}
