// The outline and fill a device or a switch is given, as classes and custom
// properties on its node element (see nodeColors in colors.js, and the
// .builder-node--filled and --outlined rules in builder.css).
//
// The colors go on as custom properties, not as `background` and
// `border-color`, so the theme's own rules still decide what a selected
// node and forced colors look like.
//
// The node's icon is drawn in its outline color (--bx-node-outline). The
// classes is-icon-ink-light and is-icon-ink-dark draw it in the node's text
// color in a theme where that outline would not keep 3:1 (see iconInk).

import { computed } from 'vue';

import { iconInk, nodeColors } from '@/builder/colors.js';

/**
 * @param {() => object|undefined} payload the node's device or switch
 *   payload
 * @param {() => ({layerColor?: string, defaultOutline?: ({color: string,
 *   low: object|null}|null)}|undefined)} [outlines] the outlines the node
 *   takes without one of its own. layerColor is the color of its layer when
 *   the diagram's layer set has layerOutlines on (see layerOutlineColor in
 *   model.js, and the layerColor of a canvas node's data). defaultOutline is
 *   used when there is no layer color: a color as drawn (a theme token too),
 *   and in which themes it needs a ring (see needsCasing in colors.js), or
 *   null for none. A switch takes its network's color.
 * @returns {{colorClasses: import('vue').ComputedRef<object>,
 *   colorStyle: import('vue').ComputedRef<object|undefined>}} what the node
 *   element binds as a class and as its style
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
