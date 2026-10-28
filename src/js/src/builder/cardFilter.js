// The filter of the drafts page's card tabs. It matches as the palette does
// (matchItem in fuzzy.js), keeps list order and keeps its text per tab.

import { matchItem } from './fuzzy.js';
import { pageStorage } from './storage.js';

// The sessionStorage key of a tab's filter text, after which comes the tab's
// id. Logout removes it, as every phenix.builder.* key that is not a
// preference (see session.js).
const FILTER_KEY = 'phenix.builder.filter.';

// What a card is found by: its name as the title, and the users as fields.
function searchItem(item, name) {
  return {
    title: name,
    fields: [
      { label: 'Created by', value: String(item?.createdBy || '') },
      { label: 'Changed by', value: String(item?.lastModifiedBy || '') },
    ],
  };
}

/**
 * The items that match the filter, in list order: `items` itself when the
 * filter is empty. searchOf gives what an item is found by: {title, fields}.
 */
export function filterItems(items, query, searchOf) {
  const text = String(query || '').trim();

  if (!text) {
    return items;
  }

  return items.filter(
    (item) => matchItem(searchOf(item), text, { spread: true }) !== null,
  );
}

/**
 * The drafts or published diagrams that match the filter by name or user, in
 * list order: `items` itself when the filter is empty.
 */
export function filterCards(items, query, nameOf) {
  return filterItems(items, query, (item) => searchItem(item, nameOf(item)));
}

/**
 * @param {string} tabId
 * @param {Storage|null} [storage]
 * @returns {string} the filter text kept for the tab, or ''
 */
export function readCardFilter(tabId, storage = pageStorage('sessionStorage')) {
  try {
    return storage?.getItem(`${FILTER_KEY}${tabId}`) || '';
  } catch {
    return '';
  }
}

/**
 * Keeps the filter text of a tab, or removes it when empty. Blocked storage
 * keeps nothing.
 */
export function keepCardFilter(
  tabId,
  text,
  storage = pageStorage('sessionStorage'),
) {
  try {
    if (text) {
      storage?.setItem(`${FILTER_KEY}${tabId}`, text);
    } else {
      storage?.removeItem(`${FILTER_KEY}${tabId}`);
    }
  } catch {
    // The filter still works. It is not kept.
  }
}
