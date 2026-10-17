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
 * walk, in reading order, where it overlaps no node (see freeSpot). With
 * `area` (the view), the walk is centred in it, and a slot fully in view
 * comes first. Without it, the walk starts at (80, 80), five slots across.
 * Positions are on the diagram's grid while `snap` is on, else whole pixels.
 *
 * @param {object} doc
 * @param {object} [options]
 * @param {string} [options.kind] the new node's kind, for its size
 * @param {string} [options.shape] a new shape's figure, for its size
 * @param {{x: number, y: number, width: number, height: number}} [options.area]
 * @param {boolean} [options.snap] the user's Snap to grid setting
 * @returns {{x: number, y: number}}
 */
export function nextPosition(
  doc,
  { kind = 'device', shape, area = null, snap = false } = {},
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

    // A slot fully in view first, else the full walk below, out of view.
    // A wide node keeps the columns, so the view moves less far to it.
    const inView = freeSpot(doc, size, {
      origin,
      slot: SLOT,
      columns,
      within: area,
      snap,
    });

    if (inView) {
      return inView;
    }
  }

  return freeSpot(doc, size, { origin, slot: SLOT, columns, snap }) || origin;
}

/**
 * Adds a node where a click on a palette entry or an Add command puts it:
 * nextPosition's free spot in the part of the canvas in view, on the grid
 * while the user's Snap to grid setting is on. The view then moves to that
 * spot if it is not in view (a diagram whose view is full). Focus stays
 * where it is.
 *
 * @param {{store: object, view?: object}} context the command context: the
 *   Builder store, and the view adapter for the canvas's visible area and
 *   the Snap to grid setting (VIEW_API in commands.js)
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
      snap: view?.snapToGrid === true,
    }),
  });

  if (node) {
    view?.revealNode?.(node.id);
  }

  return node;
}
