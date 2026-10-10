// How a device, a switch or a group lays itself out for the size its icon
// is drawn at (see nodeIconSize in model.js).
//
// A Small icon sits before the node's name, as icons always did. A Medium
// or a Large one would make that line taller than the box has room for, so
// it stands left of the node's lines instead: centered beside the label,
// type and interface lines of a device and the two lines of a switch, and
// beside the title and description of a group, at its top. The class puts
// the node in that layout (see builder.css); the box keeps its size and its
// connection handles their places.

import { DEFAULT_ICON_SIZE, ICON_SIZES } from '@/builder/model.js';

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
