// The keys of an editable combobox with list autocomplete (WAI-ARIA APG),
// as an Inspector field that suggests values uses them
// (InspectorComboboxControl.vue). It is pure, so it is tested without a
// browser.

/**
 * What a key does in an Inspector field that suggests values: an action and
 * whether to cancel the key's default, or null to leave the key to the browser.
 */
export function comboboxKey(event, { expanded, active, count }) {
  // An input method editor (IME) uses Enter and the arrow keys to compose text.
  if (
    event.isComposing ||
    event.keyCode === 229 ||
    event.ctrlKey ||
    event.metaKey
  ) {
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
