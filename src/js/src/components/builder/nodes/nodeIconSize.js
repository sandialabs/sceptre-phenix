// How a device, a switch or a group lays itself out for the size its icon
// is drawn at (see nodeIconSize in model.js).
//
// A Small icon sits before the node's name, as icons always did. A Medium
// or a Large icon would make that line taller than the box has room for.
// For this reason, it stands left of the node's lines:
//
// - on a device, centered beside the label, type and interface lines
// - on a switch, centered beside its one line, its name
// - on a group, beside the title and description, at the top.
//
// The class puts the node in that layout (see builder.css). The box keeps
// its size, and its connection handles keep their places.
//
// While the setting showNodeIcons is off, the node draws no icon and takes
// no icon layout: its lines take the whole width. The document keeps the
// icon.

import { computed } from 'vue';

import { DEFAULT_ICON_SIZE, ICON_SIZES } from '@/builder/model.js';
import { builderSettings } from '@/builder/settings.js';

/**
 * The class of a node whose icon is drawn at `size`: none for Small, and
 * for a larger size the one that puts the icon beside the node's lines.
 *
 * @param {string} [size] one of ICON_SIZES
 * @returns {string} '' or builder-node--icon-<size>
 */
export function iconSizeClass(size) {
  return ICON_SIZES.includes(size) && size !== DEFAULT_ICON_SIZE
    ? `builder-node--icon-${size}`
    : '';
}

/**
 * Whether the node draws its icon, and the class of its layout for the size
 * the icon is drawn at (see iconSizeClass): none while icons are hidden.
 *
 * @param {() => string} size the size its icon is drawn at
 * @returns {{showIcon: import('vue').ComputedRef<boolean>,
 *   iconClass: import('vue').ComputedRef<string>}}
 */
export function useNodeIcon(size) {
  const showIcon = computed(() => builderSettings.showNodeIcons);
  const iconClass = computed(() =>
    showIcon.value ? iconSizeClass(size()) : '',
  );

  return { showIcon, iconClass };
}
