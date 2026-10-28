// Shared drag-and-drop contract between the palette and the canvas.
// Kept in its own module so both sides agree on the MIME types, and a
// palette entry dropped on the canvas adds the node a click on it adds.

import { paletteEntry } from '@/builder/catalog.js';
import { NODE_KINDS, freeSpot, newNodeSize, sizeOf } from '@/builder/model.js';
import { nodeOptionsFromTemplate } from '@/builder/templates.js';

// A palette entry's id: its kind, or for a shape the figure it draws.
export const PALETTE_MIME = 'application/x-phenix-builder-kind';
// A device template's key (see templateKey in templates.js), sent beside
// the kind 'device'.
export const PALETTE_TEMPLATE_MIME = 'application/x-phenix-builder-template';

/**
 * Whether a drop's kind is one the palette sends. Another page can drop any
 * text with the same MIME type.
 */
export function isPaletteKind(kind) {
  return Boolean(paletteEntry(kind)) || NODE_KINDS.includes(kind);
}

/**
 * The store.addNode options for a palette entry or node kind, less its
 * position. A key that names no template adds a plain device.
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
 * Where a click, Enter or a command adds a node: the first free slot of a grid
 * walk (see freeSpot), centered in `area` when given, else from (80, 80).
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
 * Adds a node at nextPosition's free spot in view, and moves the view to it
 * when it is out of view. Focus stays where it is.
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
