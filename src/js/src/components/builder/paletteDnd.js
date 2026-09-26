// Shared drag-and-drop contract between the palette and the canvas.
// Kept in its own module so both sides agree on the MIME types, and a
// palette entry dropped on the canvas adds the node a click on it adds.

import { deviceTemplate } from '@/builder/catalog.js';
import { freeSpot, sizeOf } from '@/builder/model.js';

export const PALETTE_MIME = 'application/x-phenix-builder-kind';
// A device template's id, sent beside the kind 'device'.
export const PALETTE_TEMPLATE_MIME = 'application/x-phenix-builder-template';

/**
 * The store.addNode options for a palette entry, less its position.
 *
 * @param {string} kind node kind
 * @param {string} [templateId] a device template's id; an unknown one adds
 *   a plain device
 * @returns {object}
 */
export function paletteNode(kind, templateId) {
  const template = templateId ? deviceTemplate(templateId) : undefined;

  if (!template) {
    return { kind };
  }

  return {
    kind: 'device',
    template,
    hostname: template.id,
    iconKey: template.iconKey,
  };
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
 * is centred in it, as many slots across as fit, its rows going on below;
 * without, it starts at (80, 80), five slots across. Positions follow the
 * document's grid while it snaps.
 *
 * @param {object} doc
 * @param {object} [options]
 * @param {string} [options.kind] the new node's kind, for its size
 * @param {{x: number, y: number, width: number, height: number}} [options.area]
 * @returns {{x: number, y: number}}
 */
export function nextPosition(doc, { kind = 'device', area = null } = {}) {
  const size = sizeOf({ kind });
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
 * nextPosition's free spot in the part of the canvas in view, which is then
 * brought into view if it is not (a diagram whose view is full). Focus stays
 * where it is.
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
      area: view?.visibleArea?.() || null,
    }),
  });

  if (node) {
    view?.revealNode?.(node.id);
  }

  return node;
}
