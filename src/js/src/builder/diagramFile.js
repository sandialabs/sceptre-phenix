// The JSON file Download saves for a diagram, with its custom icons.

import { iconLibrary } from './iconLibrary.js';
import { embedIcons, iconRefs } from './icons.js';

// The diagram that a queued record gives: the one that its last change in
// this browser made current.
export function queuedDiagram(record) {
  const entries = record.entries || [];
  let diagram = null;

  for (const op of record.queue || []) {
    const entry = entries.find(
      (item) =>
        (op.commitId && item.id === op.commitId) ||
        (op.snapshotId && item.serverSnapshotId === op.snapshotId),
    );

    diagram = entry?.snapshot || diagram;
  }

  return diagram;
}

// A diagram as Download saves it: a copy with every custom icon it names, from
// its own copies, else the server's library. A missing icon falls back to the
// built-in one.
export async function downloadedDiagram(doc, { library = iconLibrary } = {}) {
  if (iconRefs(doc).size === 0) {
    return embedIcons(doc).doc;
  }

  // The list has no images: the file waits for those it carries.
  await library.ensureImages(iconRefs(doc));

  return embedIcons(doc, library).doc;
}

// A diagram's Download file name and JSON text. downloadedDiagram makes the
// copy that carries its custom icons.
export function diagramFile(doc) {
  const base = String(doc?.metadata?.name || 'topology')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '');

  return {
    name: `${base || 'topology'}.json`,
    text: `${JSON.stringify(doc, null, 2)}\n`,
  };
}
