// The outline and fill of a device or a switch, as classes and custom
// properties (see nodeColors in colors.js). Custom properties, not
// `background`, let the theme still style selection and forced colors.

import { computed } from 'vue';

import { iconInk, nodeColors } from '@/builder/colors.js';

/**
 * The node element's color classes and style. Without its own outline, it takes
 * its layer color, else `defaultOutline` (see layerOutlineColor in model.js).
 */
export function useNodeColors(payload, outlines = () => ({})) {
  const colors = computed(() => {
    const { layerColor = '', defaultOutline = null } = outlines() || {};
    const own = nodeColors(payload(), layerColor || '');
    const fallback = own.outline ? null : defaultOutline;

    return fallback?.color
      ? { ...own, outline: fallback.color, low: fallback.low || null }
      : own;
  });

  const colorClasses = computed(() => {
    const ink = iconInk(colors.value.outline, colors.value.fill);

    return {
      'builder-node--filled': Boolean(colors.value.fill),
      'builder-node--outlined': Boolean(colors.value.outline),
      'is-low-light': Boolean(colors.value.low?.light),
      'is-low-dark': Boolean(colors.value.low?.dark),
      'is-icon-ink-light': ink.light,
      'is-icon-ink-dark': ink.dark,
    };
  });

  const colorStyle = computed(() => {
    const { fill, ink, outline } = colors.value;

    if (!fill && !outline) {
      return undefined;
    }

    return {
      ...(fill ? { '--bx-node-fill': fill, '--bx-node-ink': ink } : {}),
      ...(outline ? { '--bx-node-outline': outline } : {}),
    };
  });

  return { colorClasses, colorStyle };
}
