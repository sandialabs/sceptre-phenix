// The colors that people give networks, notes, groups and nodes, as the
// canvas draws them. A color that keeps less than 3:1 against a theme's
// canvas gets a casing or ring in the theme's text color (WCAG 1.4.11).
// Text on a fill is black or white, whichever reads better (WCAG 1.4.3).

import { DEFAULT_NETWORK_COLORS, HEX_COLOR } from './model.js';

// What a connection is drawn over in each theme: the canvas (--bx-bg-alt)
// and its grid lines (--bx-grid). A line keeps 3:1 against both.
const CANVAS = {
  light: ['#f4f6f9', '#dfe4ec'],
  dark: ['#1a212b', '#27303c'],
};

// WCAG 1.4.11's minimum for a graphical object.
const MIN_CONTRAST = 3;

// What a node's icon is drawn over in each theme without a fill: the node's
// surface (--bx-surface), a selected node's fill (--bx-selected-bg), and
// the canvas and its grid behind a group.
const NODE_GROUNDS = {
  light: ['#ffffff', '#e4ecf5', ...CANVAS.light],
  dark: ['#1e2631', '#2d3b4e', ...CANVAS.dark],
};

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
 * A color as drawn: the trimmed value when the browser can draw it, else ''.
 */
export function drawnColor(value) {
  const color = String(value ?? '').trim();

  return color && drawable(color) ? color : '';
}

/**
 * N of the theme token --bx-net-N for the Nth color that addNetwork picks, or
 * -1.
 */
export function networkColorToken(value) {
  return DEFAULT_NETWORK_COLORS.indexOf(drawnColor(value).toLowerCase());
}

/**
 * A network color that the user chose, or '' for none, a token color or a
 * non-color.
 */
export function customNetworkColor(value) {
  return networkColorToken(value) === -1 ? drawnColor(value) : '';
}

/**
 * A network color as the canvas draws it, for a swatch: the theme token for a
 * picked color, else the color.
 */
export function drawnNetworkColor(value) {
  const token = networkColorToken(value);

  return token === -1 ? drawnColor(value) : `var(--bx-net-${token})`;
}

/**
 * The [r, g, b, a] channels of a hex or rgb() color, or null for any other
 * color. a is from 0 to 1.
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
 * A hex or rgb() color as the #rrggbb that a native color input takes, or null.
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
 * The contrast ratio of two opaque colors, or null when either is unreadable.
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
 * The themes in which a line of this color needs a casing: below 3:1 against
 * the canvas or the grid. Both when the contrast is unreadable.
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
 * Black or white, whichever has more contrast with an opaque fill. The
 * better one keeps at least 4.58:1 on any fill, so text meets WCAG 1.4.3.
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
 * The fill, outline, ink and low-contrast themes of a device or a switch.
 * A value that is not #rrggbb is not drawn. A node without its own outline
 * takes `layerColor` (see layerOutlineColor in model.js).
 */
export function nodeColors(payload, layerColor = '') {
  const hex = (value) =>
    typeof value === 'string' && HEX_COLOR.test(value) ? value : '';
  const fill = hex(payload?.fillColor);
  const outline = hex(payload?.outlineColor) || hex(layerColor);

  return {
    fill,
    outline,
    ink: fill ? inkOn(fill) : '',
    low: outline ? needsCasing(outline) : null,
  };
}

/**
 * The themes in which a node's icon takes the node's text color: where the
 * outline keeps less than 3:1 against what the icon is drawn over. A theme
 * token keeps 3:1 against the theme, but not against a user's fill.
 */
export function iconInk(outline, fill = '') {
  if (!outline || outline.startsWith('var(')) {
    return { light: Boolean(fill), dark: Boolean(fill) };
  }

  const low = (grounds) =>
    grounds.some((ground) => {
      const ratio = contrastRatio(outline, ground);

      return ratio === null || ratio < MIN_CONTRAST;
    });

  if (fill) {
    const onFill = low([fill]);

    return { light: onFill, dark: onFill };
  }

  return { light: low(NODE_GROUNDS.light), dark: low(NODE_GROUNDS.dark) };
}
