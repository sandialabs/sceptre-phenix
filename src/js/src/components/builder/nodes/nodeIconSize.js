// The node layout for its icon size (see nodeIconSize in model.js). A Medium or
// Large icon stands left of the node's lines, because it does not fit in the
// name line. The box keeps its size.

import { computed } from 'vue';

import { DEFAULT_ICON_SIZE, ICON_SIZES } from '@/builder/model.js';
import { builderSettings } from '@/builder/settings.js';

/**
 * The class of a node whose icon is drawn at `size`: '' for Small, else
 * builder-node--icon-<size>.
 */
export function iconSizeClass(size) {
  return ICON_SIZES.includes(size) && size !== DEFAULT_ICON_SIZE
    ? `builder-node--icon-${size}`
    : '';
}

/**
 * Whether the node draws its icon, and its layout class (see iconSizeClass).
 * There is no class while icons are hidden.
 */
export function useNodeIcon(size) {
  const showIcon = computed(() => builderSettings.showNodeIcons);
  const iconClass = computed(() =>
    showIcon.value ? iconSizeClass(size()) : '',
  );

  return { showIcon, iconClass };
}
