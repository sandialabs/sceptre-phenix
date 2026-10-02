// Arrow-key movement inside a one-row composite widget, such as the toolbar
// and the drafts tabs (WAI-ARIA APG toolbar and tabs patterns), or a
// one-column one, such as a menu: Left and Right (Up and Down in a column)
// move one item and wrap at the ends, Home and End jump to the ends.

const KEYS = {
  horizontal: { next: 'ArrowRight', previous: 'ArrowLeft' },
  vertical: { next: 'ArrowDown', previous: 'ArrowUp' },
};

/**
 * @param {string} key KeyboardEvent.key
 * @param {number} index position of the focused item
 * @param {number} count number of items
 * @param {object} [options]
 * @param {'horizontal'|'vertical'} [options.orientation] a row (the
 *   default) or a column
 * @returns {number|undefined} position to focus, or undefined when the key
 *   does not move focus
 */
export function rowTarget(
  key,
  index,
  count,
  { orientation = 'horizontal' } = {},
) {
  if (count < 1 || index < 0) {
    return undefined;
  }

  const { next, previous } = KEYS[orientation];

  return {
    [next]: (index + 1) % count,
    [previous]: (index - 1 + count) % count,
    Home: 0,
    End: count - 1,
  }[key];
}
