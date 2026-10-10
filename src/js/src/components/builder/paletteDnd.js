// Shared drag-and-drop contract between the palette and the canvas.
// Kept in its own module so both sides agree on the MIME types, and a
// palette entry dropped on the canvas adds the node a click on it adds.

import { paletteEntry } from '@/builder/catalog.js';
import { freeSpot, newNodeSize, sizeOf } from '@/builder/model.js';
import { nodeOptionsFromTemplate } from '@/builder/templates.js';

// A palette entry's id: its kind, or for a shape the figure it draws.
export const PALETTE_MIME = 'application/x-phenix-builder-kind';
// A device template's key (see templateKey in templates.js), sent beside
// the kind 'device'.
export const PALETTE_TEMPLATE_MIME = 'application/x-phenix-builder-template';

/**
 * The store.addNode options for a palette entry, less its position. A
 * template's custom icon is a name, which the device names too and the icon
 * library resolves.
 *
 * @param {object} store the Builder store, which knows the templates and
 *   the diagram the node is for
 * @param {string} kind node kind, or the id of a palette entry (see
 *   PALETTE in catalog.js), which adds the entry's options too: "circle"
 *   adds a shape that is a circle
 * @param {string} [key] a device template's key. A key that names no
 *   template adds a plain device
 * @returns {object}
 */
export function paletteNode(store, kind, key) {
  const template = key ? store.templateByKey(key) : undefined;

  if (!template) {
    const entry = paletteEntry(kind);

    return entry ? { kind: entry.kind, ...entry.options } : { kind };
  }

  return nodeOptionsFromTemplate(template, store.doc);
}

// The grid walk: one slot per node, a node's box and the room around it.
const SLOT = { width: 220, height: 160 };
const ORIGIN = { x: 80, y: 80 };
const DEVICE = sizeOf({ kind: 'device' });
const COLUMNS = 5;

/**
 * Where a click, Enter or a command adds a node: the first slot of a grid
 * walk, in reading order, where the node would not overlap a node already
 * there, a group's box included, so it never lands on top of another (see
 * freeSpot).
 * With `area`, the part of the canvas in view (flow coordinates), the grid
 * is centred in it, as many slots across as fit, with more rows below.
 * Without it, the grid starts at (80, 80), five slots across. Positions
 * follow the document's grid while it snaps.
 *
 * @param {object} doc
 * @param {object} [options]
 * @param {string} [options.kind] the new node's kind, for its size
 * @param {string} [options.shape] a new shape's figure, for its size
 * @param {{x: number, y: number, width: number, height: number}} [options.area]
 * @returns {{x: number, y: number}}
 */
export function nextPosition(
  doc,
  { kind = 'device', shape, area = null } = {},
) {
  const size = newNodeSize({ kind, shape });
  let origin = ORIGIN;
  let columns = COLUMNS;

  if (area?.width > 0 && area?.height > 0) {
    columns = Math.max(1, Math.floor(area.width / SLOT.width));
    const rows = Math.max(1, Math.floor(area.height / SLOT.height));

    // The slots in view centred in it, a device centred in each. Every kind
    // starts at the slot's corner, so a row's nodes share their top.
    origin = {
      x:
        area.x +
        (area.width - columns * SLOT.width) / 2 +
        (SLOT.width - DEVICE.width) / 2,
      y:
        area.y +
        (area.height - rows * SLOT.height) / 2 +
        (SLOT.height - DEVICE.height) / 2,
    };
  }

  return freeSpot(doc, size, { origin, slot: SLOT, columns }) || origin;
}

/**
 * Adds a node where a click on a palette entry or an Add command puts it:
 * nextPosition's free spot in the part of the canvas in view. The view then
 * moves to that spot if it is not in view (a diagram whose view is full).
 * Focus stays where it is.
 *
 * @param {{store: object, view?: object}} context the command context: the
 *   Builder store, and the view adapter for the canvas's visible area
 * @param {object} options store.addNode's, less the position
 * @returns {object|null} the new node
 */
export function addInView({ store, view }, options) {
  const node = store.addNode({
    ...options,
    position: nextPosition(store.doc, {
      kind: options.kind,
      shape: options.shape,
      area: view?.visibleArea?.() || null,
    }),
  });

  if (node) {
    view?.revealNode?.(node.id);
  }

  return node;
}
