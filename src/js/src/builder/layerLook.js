// How the editor shows a layer's color beside its name: as the color of the
// name's text where the text keeps 4.5:1 contrast against the backgrounds
// of the theme shown (WCAG 1.4.3), else as a swatch beside the name. No one
// color keeps 4.5:1 against both the light and the dark backgrounds, so each
// theme decides for itself.

import { isIconKey } from './catalog.js';
import { HEX_COLOR } from './model.js';
import { contrastRatio } from './colors.js';
import { iconSrc } from './icons.js';

// The least contrast of text against its background (WCAG 1.4.3).
export const MIN_TEXT_CONTRAST = 4.5;

// The backgrounds that a layer's name is shown on in each theme: --bx-bg,
// --bx-bg-alt and --bx-surface of builder.css.
export const LAYER_TEXT_BACKGROUNDS = Object.freeze({
  light: Object.freeze(['#ffffff', '#f4f6f9']),
  dark: Object.freeze(['#12171f', '#1a212b', '#1e2631']),
});

/**
 * The themes in which a layer's name takes the layer's color: those whose
 * backgrounds all keep MIN_TEXT_CONTRAST against it.
 */
export function layerInk(color) {
  if (typeof color !== 'string' || !HEX_COLOR.test(color)) {
    return { light: false, dark: false };
  }

  const reads = (backgrounds) =>
    backgrounds.every(
      (background) =>
        (contrastRatio(color, background) ?? 0) >= MIN_TEXT_CONTRAST,
    );

  return {
    light: reads(LAYER_TEXT_BACKGROUNDS.light),
    dark: reads(LAYER_TEXT_BACKGROUNDS.dark),
  };
}

/**
 * What a layer shows beside its name (see LayerName.vue): a built-in icon, a
 * resolved custom icon and a "#rrggbb" color, each '' when it does not show.
 */
export function layerLook(layer, icons = null, library = null) {
  return {
    iconKey: isIconKey(layer?.iconKey) ? layer.iconKey : '',
    src: layer?.icon ? iconSrc(layer.icon, icons, library) : '',
    color:
      typeof layer?.color === 'string' && HEX_COLOR.test(layer.color)
        ? layer.color
        : '',
  };
}

/** Whether a layer shows an icon or a color (see layerLook). */
export function hasLayerLook(layer, icons = null, library = null) {
  const { iconKey, src, color } = layerLook(layer, icons, library);

  return Boolean(iconKey || src || color);
}
