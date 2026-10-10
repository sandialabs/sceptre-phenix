// The outline and fill a device or a switch is given, as classes and custom
// properties on its node element (see nodeColors in colors.js, and the
// .builder-node--filled and --outlined rules in builder.css).
//
// The colors go on as custom properties, not as `background` and
// `border-color`, so the theme's own rules still decide what a selected
// node and forced colors look like.

import { computed } from 'vue';

import { nodeColors } from '@/builder/colors.js';

/**
 * @param {() => object|undefined} payload the node's device or switch
 *   payload
 * @returns {{colorClasses: import('vue').ComputedRef<object>,
 *   colorStyle: import('vue').ComputedRef<object|undefined>}} what the node
 *   element binds as a class and as its style
 */
export function useNodeColors(payload) {
  const colors = computed(() => nodeColors(payload()));

  const colorClasses = computed(() => ({
    'builder-node--filled': Boolean(colors.value.fill),
    'builder-node--outlined': Boolean(colors.value.outline),
    'is-low-light': Boolean(colors.value.low?.light),
    'is-low-dark': Boolean(colors.value.low?.dark),
  }));

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
