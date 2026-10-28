// The palette's collapsed template sections, by id, in localStorage under
// phenix.builder.palette. Logout clears the key, because the ids name server
// collections. Without storage, sections stay collapsed until the page closes.

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

// Keeps the ids of the collapsed sections, oldest first. With none, it
// removes the key.
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

// The collapsed sections after a press on a section's header: without the
// section when it was collapsed, else with it last.
export function toggledSections(ids, id) {
  return ids.includes(id) ? ids.filter((other) => other !== id) : [...ids, id];
}
