// Arrow-key movement inside a one-row composite widget, such as the toolbar
// and the drafts tabs (WAI-ARIA APG toolbar and tabs patterns), or inside a
// one-column widget, such as a menu. Left and Right (Up and Down in a
// column) move one item and wrap at the ends. Home and End go to the ends.

const KEYS = {
  horizontal: { next: 'ArrowRight', previous: 'ArrowLeft' },
  vertical: { next: 'ArrowDown', previous: 'ArrowUp' },
};

// The position a key moves focus to in a row (or a column with
// options.orientation), or undefined when the key does not move focus.
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
  const targets = {
    [next]: (index + 1) % count,
    [previous]: (index - 1 + count) % count,
    Home: 0,
    End: count - 1,
  };

  // Own keys only: 'constructor' is a key name too.
  return Object.hasOwn(targets, key) ? targets[key] : undefined;
}

// The item a menu opens on (APG menu button): the last item for Up Arrow, else
// the checked radio item so the current choice is in view, else the first.
// It is -1 for an empty menu.
export function menuOpenTarget(items, at = 'first') {
  if (items.length === 0) {
    return -1;
  }

  if (at === 'last') {
    return items.length - 1;
  }

  const checked = items.findIndex((item) => item.checked === true);

  return checked < 0 ? 0 : checked;
}
