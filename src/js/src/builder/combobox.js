// The keys of an editable combobox with list autocomplete (WAI-ARIA APG),
// as an Inspector field that suggests values uses them
// (InspectorComboboxControl.vue). It is pure, so it is tested without a
// browser.

/**
 * What a key does in an Inspector field that suggests values.
 *
 * @param {{key: string, altKey?: boolean, ctrlKey?: boolean, metaKey?: boolean}} event
 * @param {{expanded: boolean, active: number, count: number}} state whether
 *   the list shows, the active suggestion (-1 for none), and how many there
 *   are
 * @returns {{action: 'open'|'move'|'choose'|'close'|'add', index?: number,
 *   prevent: boolean}|null} null leaves the key to the browser. prevent tells
 *   whether to cancel its default action.
 */
export function comboboxKey(event, { expanded, active, count }) {
  if (event.ctrlKey || event.metaKey) {
    return null;
  }

  switch (event.key) {
    case 'ArrowDown':
      if (event.altKey) {
        return { action: 'open', prevent: true };
      }

      if (count === 0) {
        return null;
      }

      return {
        action: 'move',
        index: expanded && active >= 0 ? (active + 1) % count : 0,
        prevent: true,
      };
    case 'ArrowUp':
      if (event.altKey || count === 0) {
        return null;
      }

      return {
        action: 'move',
        index:
          expanded && active >= 0 ? (active - 1 + count) % count : count - 1,
        prevent: true,
      };
    case 'Enter':
      return expanded && active >= 0
        ? { action: 'choose', index: active, prevent: true }
        : { action: 'add', prevent: true };
    case 'Escape':
      // A closed list leaves Escape to the dialog, which it closes.
      return expanded ? { action: 'close', prevent: true } : null;
    case 'Tab':
      return { action: 'close', prevent: false };
    default:
      return null;
  }
}
