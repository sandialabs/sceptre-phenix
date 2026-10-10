// The colors that people give networks, notes and groups, as the canvas draws
// them.
//
// The canvas draws a network's connections in its color, with its dash
// pattern and label beside them, so color is never the only cue. The colors
// that addNetwork picks (the color picker suggests them) are drawn with the
// theme's network token at the same position (--bx-net-N for the Nth). These
// tokens keep 3:1 against the canvas in both themes. Any other color that the
// user chose is drawn as chosen. Where that color does not keep 3:1 against a
// theme's canvas, the line gets a casing in that theme's text color, so it
// never fades into the canvas (WCAG 1.4.11). Notes and groups show their
// color as an accent bar, and switches as a swatch. Both are decorative,
// because their names tell what they are.
//
// A device or a switch can also have its own fill and outline, each an opaque
// #rrggbb. Its text and icon on a fill are black or white, whichever is easier
// to read on it (WCAG 1.4.3). An outline that does not keep 3:1 against a
// theme's canvas gets a ring in that theme's text color, as a low-contrast
// line gets a casing.

import { DEFAULT_NETWORK_COLORS, HEX_COLOR } from './model.js';

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
  /^rgba?\(\s*(\d+(?:\.\d+)?)[\s,]+(\d+(?:\.\d+)?)[\s,]+(\d+(?:\.\d+)?)\s*(?:[,/]\s*(\d*\.?\d+)(%?)\s*)?\)$/i;

// A color the browser can draw. Without CSS.supports (outside a browser),
// hex colors only.
function drawable(value) {
  const supports = globalThis.CSS?.supports;

  return typeof supports === 'function'
    ? supports.call(globalThis.CSS, 'color', value)
    : HEX.test(value);
}

/**
 * A color as drawn: the trimmed value when the browser can draw it. '' when
 * there is no value or it is not a color.
 *
 * @param {string} value
 * @returns {string}
 */
export function drawnColor(value) {
  const color = String(value ?? '').trim();

  return color && drawable(color) ? color : '';
}

/**
 * The theme token that draws a network color: N of --bx-net-N for the Nth
 * color that addNetwork picks. -1 for any other color or for none.
 *
 * @param {string} value
 * @returns {number}
 */
export function networkColorToken(value) {
  return DEFAULT_NETWORK_COLORS.indexOf(drawnColor(value).toLowerCase());
}

/**
 * A network color that the user chose. '' for none, for a color that
 * addNetwork picks (drawn with the theme's token) or for a value that is not
 * a color.
 *
 * @param {string} value
 * @returns {string}
 */
export function customNetworkColor(value) {
  return networkColorToken(value) === -1 ? drawnColor(value) : '';
}

/**
 * A network color as the canvas draws it, for a swatch: the theme's token for
 * a color that addNetwork picks, or else the color as chosen. '' for none or
 * for a value that is not a color.
 *
 * @param {string} value
 * @returns {string}
 */
export function drawnNetworkColor(value) {
  const token = networkColorToken(value);

  return token === -1 ? drawnColor(value) : `var(--bx-net-${token})`;
}

/**
 * The channels of a hex (#rgb, #rgba, #rrggbb or #rrggbbaa) or rgb() color.
 * r, g and b are as written, decimals included. a is from 0 to 1.
 *
 * @param {string} value
 * @returns {number[]|null} [r, g, b, a], or null for any other color
 */
export function colorChannels(value) {
  const color = String(value ?? '').trim();
  const hex = HEX.exec(color)?.[1];

  if (hex) {
    const digits =
      hex.length <= 4
        ? [...hex].map((digit) => digit + digit)
        : hex.match(/../g);
    const [r, g, b, a = 255] = digits.map((pair) => parseInt(pair, 16));

    return [r, g, b, a / 255];
  }

  const rgb = RGB.exec(color);

  if (!rgb) {
    return null;
  }

  const [, r, g, b, alpha = '1', percent] = rgb;

  return [
    Number(r),
    Number(g),
    Number(b),
    Math.min(1, Number(alpha) / (percent ? 100 : 1)),
  ];
}

/**
 * A hex or rgb() color as the #rrggbb that a native color input takes: its
 * channels rounded and its alpha removed.
 *
 * @param {string} value
 * @returns {string|null} null for any other color, or one with a channel
 *   above 255
 */
export function opaqueHex(value) {
  const rgb = colorChannels(value)?.slice(0, 3);

  if (!rgb || rgb.some((channel) => channel > 255)) {
    return null;
  }

  return `#${rgb
    .map((channel) => Math.round(channel).toString(16).padStart(2, '0'))
    .join('')}`;
}

// [r, g, b] from 0 to 255 for an opaque hex or rgb() color. null for any
// other color, translucent colors included.
function channels(color) {
  const found = colorChannels(color);

  return found?.[3] === 1 ? found.slice(0, 3) : null;
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
 * The themes in which a line of this color needs a casing. These are the
 * themes whose canvas or grid it keeps less than 3:1 against. Both themes
 * when its contrast cannot be read (a named or translucent color).
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

/**
 * The color of text and icons on an opaque fill: black or white, whichever
 * has more contrast with it. The better of the two keeps at least 4.58:1 on
 * any fill. Thus text on it meets WCAG 1.4.3 and an icon meets WCAG 1.4.11.
 *
 * @param {string} fill a hex or rgb() color
 * @returns {string} '#000000' or '#ffffff'. '' when the fill is not an
 *   opaque color that this function can read.
 */
export function inkOn(fill) {
  const onBlack = contrastRatio(fill, '#000000');
  const onWhite = contrastRatio(fill, '#ffffff');

  if (onBlack === null || onWhite === null) {
    return '';
  }

  return onBlack >= onWhite ? '#000000' : '#ffffff';
}

/**
 * The colors of a device or a switch, from its payload. A value that is not
 * #rrggbb is not drawn. Only an edited file can hold such a value.
 *
 * @param {object} [payload] node.device or node.switch
 * @returns {{fill: string, outline: string, ink: string,
 *   low: {light: boolean, dark: boolean}|null}} fill and outline are '' for
 *   none. ink is the text color on the fill, or ''. low tells in which
 *   themes the outline needs a ring to show (see needsCasing), or null
 *   without an outline.
 */
export function nodeColors(payload) {
  const hex = (value) =>
    typeof value === 'string' && HEX_COLOR.test(value) ? value : '';
  const fill = hex(payload?.fillColor);
  const outline = hex(payload?.outlineColor);

  return {
    fill,
    outline,
    ink: fill ? inkOn(fill) : '',
    low: outline ? needsCasing(outline) : null,
  };
}
