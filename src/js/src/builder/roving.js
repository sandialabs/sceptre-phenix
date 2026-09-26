// Arrow-key movement inside a one-row composite widget, such as the toolbar
// and the drafts tabs (WAI-ARIA APG toolbar and tabs patterns): Left and
// Right move one item and wrap at the ends, Home and End jump to the ends.

/**
 * @param {string} key KeyboardEvent.key
 * @param {number} index position of the focused item
 * @param {number} count number of items
 * @returns {number|undefined} position to focus, or undefined when the key
 *   does not move focus
 */
export function rowTarget(key, index, count) {
  if (count < 1 || index < 0) {
    return undefined;
  }

  return {
    ArrowRight: (index + 1) % count,
    ArrowLeft: (index - 1 + count) % count,
    Home: 0,
    End: count - 1,
  }[key];
}
