// The sections of templates that the palette shows collapsed (see
// paletteTemplateGroups in templates.js), by section id. They are kept in
// localStorage under phenix.builder.palette, per browser:
// {"collapsed": ["library:<id>", "preloaded:<id>"]}. The ids name
// collections of the server, and logout clears the key (it is not one of
// the BUILDER_PREFERENCE_KEYS of session.js). Without storage, every
// section shows expanded, and a section stays collapsed until the page
// closes.

import { pageStorage } from './storage.js';

export const PALETTE_STORAGE_KEY = 'phenix.builder.palette';

// The most section ids kept. A collapse past it forgets the oldest id.
export const MAX_COLLAPSED_SECTIONS = 200;

/**
 * @param {Storage|null} [storage]
 * @returns {string[]} the ids of the collapsed sections, oldest first
 */
export function loadCollapsedSections(storage = pageStorage()) {
  try {
    const saved = JSON.parse(storage?.getItem(PALETTE_STORAGE_KEY) || 'null');
    const ids = Array.isArray(saved?.collapsed) ? saved.collapsed : [];

    return [
      ...new Set(ids.filter((id) => typeof id === 'string' && id !== '')),
    ];
  } catch {
    return [];
  }
}

/**
 * Keeps the ids of the collapsed sections. With none, it removes the key.
 *
 * @param {string[]} ids oldest first
 * @param {Storage|null} [storage]
 */
export function saveCollapsedSections(ids, storage = pageStorage()) {
  try {
    const kept = [...new Set(ids)].slice(-MAX_COLLAPSED_SECTIONS);

    if (kept.length > 0) {
      storage?.setItem(
        PALETTE_STORAGE_KEY,
        JSON.stringify({ collapsed: kept }),
      );
    } else {
      storage?.removeItem(PALETTE_STORAGE_KEY);
    }
  } catch {
    // Storage is full or blocked: the section stays as it is until the page
    // closes.
  }
}

/**
 * The collapsed sections after a press on a section's header: without the
 * section when it was collapsed, else with it, last.
 *
 * @param {string[]} ids
 * @param {string} id the section's id
 * @returns {string[]}
 */
export function toggledSections(ids, id) {
  return ids.includes(id) ? ids.filter((other) => other !== id) : [...ids, id];
}
