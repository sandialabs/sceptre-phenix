// Which items of a list are selected, for a list where each item has a
// checkbox and a Select all is above them (the drafts page's tabs, the Node
// Templates library, the lists of the Custom icons dialog). Also the keys
// that move through such a list and select in it.
//
// The selection is a set of keys, not of items. A list read again from the
// server holds new objects for the same items, and these stay selected. An
// item that leaves the list also leaves the selection, so if it comes back
// later it is not selected. (selection.js is the canvas's selection.)
//
// The keys (listKeyAction, onListKeydown):
//   - The list's items are one Tab stop. The arrow keys move it (roving
//     focus: Up and Down, and Left and Right in a grid of cards). Home and
//     End move it to the first and last item.
//   - Space selects or unselects the focused item.
//   - Shift with Space or an arrow key selects the range from the item last
//     pressed (the anchor) to the focused item. This applies whether that
//     press selected or unselected. A checkbox pressed with Shift sets the
//     range to the new state of the box.
//   - Mod+A (⌘A on macOS, Ctrl+A elsewhere) selects every item.
//   - Escape unselects all items.
//   - Delete (and Backspace on macOS) asks to delete the selection where the
//     list can.
// The keys never take typing in a field.

import { computed, ref, shallowRef, toValue, watch } from 'vue';

import { currentPlatform, matchesKey } from './keymap.js';
import { rowTarget } from './roving.js';

/**
 * @param {import('vue').MaybeRefOrGetter<object[]>} items the list's items,
 *   in list order
 * @param {(item: object) => string} keyOf names an item apart from the
 *   others
 * @param {object} [options]
 * @param {(item: object) => boolean} [options.selectable] whether an item
 *   can be selected (it has a checkbox). Every item by default. The other
 *   items are still in the list, for the keys to move to.
 * @returns {{
 *   has: (item: object) => boolean,
 *   toggle: (item: object, on?: boolean) => void,
 *   extendTo: (item: object, options?: {from?: object, on?: boolean}) =>
 *     void,
 *   press: (item: object, options: {checked: boolean, range?: boolean}) => void,
 *   toggleAll: () => void,
 *   selectAll: () => void,
 *   clear: () => void,
 *   remove: (keys: string[]) => void,
 *   tabStop: (item: object) => boolean,
 *   focused: (item: object) => void,
 *   selected: import('vue').ComputedRef<object[]>,
 *   count: import('vue').ComputedRef<number>,
 *   total: import('vue').ComputedRef<number>,
 *   state: import('vue').ComputedRef<'none'|'some'|'all'>,
 * }} selected holds the selected items in list order. total is how many
 *   can be selected. state is what Select all shows.
 */
export function useListSelection(
  items,
  keyOf,
  { selectable = () => true } = {},
) {
  const keys = shallowRef(new Set());
  // Every item, and those that can be selected.
  const all = computed(() => toValue(items) || []);
  const list = computed(() => all.value.filter((item) => selectable(item)));
  const selected = computed(() =>
    list.value.filter((item) => keys.value.has(keyOf(item))),
  );
  const count = computed(() => selected.value.length);
  const total = computed(() => list.value.length);
  const state = computed(() => {
    if (count.value === 0) {
      return 'none';
    }

    return count.value === total.value ? 'all' : 'some';
  });

  // The item that a range starts from (the last item pressed), and the
  // selection at that time. A range is applied over that selection, so a
  // shorter range gives back what a longer range took. Select all and clear
  // leave no anchor.
  let anchor = null;
  let base = new Set();
  // The item that is the list's Tab stop, by key.
  const current = ref(null);

  function set(next) {
    keys.value = new Set(next);
  }

  // A change that is not a range: the next range is laid over it.
  function settle(next) {
    set(next);
    base = new Set(keys.value);
  }

  function canSelect(item) {
    return Boolean(item) && list.value.some((entry) => entry === item);
  }

  function hasAnchor() {
    return (
      anchor !== null && all.value.some((entry) => keyOf(entry) === anchor)
    );
  }

  // Keys whose items have left the list go, and the anchor with them.
  watch(list, (now) => {
    const present = new Set(now.map(keyOf));
    const kept = [...keys.value].filter((key) => present.has(key));

    if (kept.length !== keys.value.size) {
      set(kept);
    }

    base = new Set([...base].filter((key) => present.has(key)));

    if (anchor !== null && !present.has(anchor)) {
      anchor = null;
    }
  });

  function toggle(item, on = !keys.value.has(keyOf(item))) {
    if (!canSelect(item)) {
      return;
    }

    const key = keyOf(item);
    const next = new Set(keys.value);

    if (on) {
      next.add(key);
    } else {
      next.delete(key);
    }

    settle(next);
    anchor = key;
  }

  // Selects every item that can be selected from the anchor to `item`, or
  // with `on` false unselects them (a checkbox cleared with Shift). The keys
  // always select, whatever the press that set the anchor did. Without an
  // anchor, the range starts at `from` (the item focus was on).
  function extendTo(item, { from = item, on = true } = {}) {
    const order = all.value.map(keyOf);
    const target = order.indexOf(keyOf(item));

    if (target < 0) {
      return;
    }

    if (!hasAnchor()) {
      const start = from && order.includes(keyOf(from)) ? from : item;

      base = new Set(keys.value);
      anchor = keyOf(start);
    }

    const start = order.indexOf(anchor);
    const [low, high] = start <= target ? [start, target] : [target, start];
    const next = new Set(base);

    for (const entry of all.value.slice(low, high + 1)) {
      if (!selectable(entry)) {
        continue;
      }

      if (on) {
        next.add(keyOf(entry));
      } else {
        next.delete(keyOf(entry));
      }
    }

    set(next);
  }

  return {
    has(item) {
      return keys.value.has(keyOf(item));
    },

    // Selects the item, or with `on` false unselects it. Without `on`, it
    // toggles the item. The item becomes the anchor.
    toggle,

    extendTo,

    // A checkbox was pressed. With Shift (range), every item from the anchor
    // to its item gets the new state of the box (checked selects the range,
    // cleared unselects it). Without Shift, or with no anchor (no press
    // since Select all or clear), only its item changes, and it becomes the
    // anchor.
    press(item, { checked, range = false }) {
      if (range && hasAnchor()) {
        extendTo(item, { on: checked });
      } else {
        toggle(item, checked);
      }
    },

    // Select all: with none or some selected, selects every item. With all
    // selected, selects none.
    toggleAll() {
      settle(state.value === 'all' ? [] : list.value.map(keyOf));
      anchor = null;
    },

    selectAll() {
      settle(list.value.map(keyOf));
      anchor = null;
    },

    clear() {
      settle([]);
      anchor = null;
    },

    remove(gone) {
      const drop = new Set(gone || []);

      set([...keys.value].filter((key) => !drop.has(key)));
      base = new Set([...base].filter((key) => !drop.has(key)));

      if (drop.has(anchor)) {
        anchor = null;
      }
    },

    // Whether an item is the list's Tab stop: the item that last had focus
    // in the list, or the first item while that item is not in the list.
    tabStop(item) {
      const key = keyOf(item);
      const listed = all.value.some((entry) => keyOf(entry) === current.value);

      return listed ? key === current.value : all.value[0] === item;
    },

    // Focus is on an item, or on a control of it.
    focused(item) {
      current.value = keyOf(item);
    },

    selected,
    count,
    total,
    state,
  };
}

// --- keys ------------------------------------------------------------------

// Fields that take typed text keep every key.
const NOT_TYPED = [
  'checkbox',
  'radio',
  'button',
  'submit',
  'reset',
  'file',
  'color',
  'image',
  'range',
];

/**
 * Whether a key press is typing: in a text field, a text area, a select or
 * editable content.
 *
 * @param {Element|null} target
 * @returns {boolean}
 */
export function isTyping(target) {
  if (!target?.tagName) {
    return false;
  }

  if (target.tagName === 'INPUT') {
    return !NOT_TYPED.includes(String(target.type || 'text').toLowerCase());
  }

  return (
    ['TEXTAREA', 'SELECT'].includes(target.tagName) ||
    Boolean(target.isContentEditable)
  );
}

// Where an arrow key moves in a grid of `columns` columns. Left and Right
// move one item and wrap at the ends. Up and Down move one row and stop at
// the first and last rows. Down from a row above a shorter last row goes to
// the last item.
function gridTarget(key, index, count, columns) {
  switch (key) {
    case 'ArrowLeft':
    case 'ArrowRight':
      return rowTarget(key, index, count);
    case 'ArrowUp':
      return index - columns >= 0 ? index - columns : undefined;
    case 'ArrowDown':
      if (index + columns < count) {
        return index + columns;
      }

      return Math.floor(index / columns) < Math.floor((count - 1) / columns)
        ? count - 1
        : undefined;
    default:
      return rowTarget(key, index, count);
  }
}

const MOVES = [
  'ArrowUp',
  'ArrowDown',
  'ArrowLeft',
  'ArrowRight',
  'Home',
  'End',
];

/**
 * What a key pressed on an item of a selectable list does.
 *
 * @param {{key: string, shiftKey?: boolean, ctrlKey?: boolean,
 *   metaKey?: boolean, altKey?: boolean}} event
 * @param {object} options
 * @param {number} options.index the position of the focused item
 * @param {number} options.count how many items the list has
 * @param {number} [options.columns] items per row: 1 for a column, more for
 *   a grid of cards
 * @param {number} [options.selected] how many are selected
 * @param {boolean} [options.onItem] focus is on the item itself, not a
 *   control in it, which keeps Space
 * @param {boolean} [options.canDelete] the selection holds items the list
 *   can delete
 * @param {'mac'|'other'} [options.platform]
 * @returns {null|{type: 'focus'|'extend'|'toggle'|'range'|'all'|'clear'|
 *   'delete', index?: number}} null for a key the list leaves alone
 */
export function listKeyAction(
  event,
  {
    index,
    count,
    columns = 1,
    selected = 0,
    onItem = true,
    canDelete = false,
    platform = currentPlatform(),
  },
) {
  if (!event || count < 1 || index < 0) {
    return null;
  }

  const { key } = event;
  const shift = Boolean(event.shiftKey);
  const ctrl = Boolean(event.ctrlKey);
  const meta = Boolean(event.metaKey);
  const alt = Boolean(event.altKey);

  if (alt) {
    return null;
  }

  // The letter A, or on a layout without Latin letters the key where A is
  // (matchesKey).
  if (matchesKey(event, 'Mod+A', platform)) {
    return { type: 'all' };
  }

  if (ctrl || meta) {
    return null;
  }

  if (MOVES.includes(key)) {
    const target =
      columns > 1
        ? gridTarget(key, index, count, columns)
        : ['ArrowLeft', 'ArrowRight'].includes(key)
          ? undefined
          : rowTarget(key, index, count, { orientation: 'vertical' });

    if (target === undefined) {
      return null;
    }

    return { type: shift ? 'extend' : 'focus', index: target };
  }

  if (key === ' ' || key === 'Spacebar') {
    if (!onItem) {
      return null;
    }

    return { type: shift ? 'range' : 'toggle' };
  }

  if (shift) {
    return null;
  }

  if (key === 'Escape') {
    return selected > 0 ? { type: 'clear' } : null;
  }

  if (key === 'Delete' || (key === 'Backspace' && platform === 'mac')) {
    return canDelete && selected > 0 ? { type: 'delete' } : null;
  }

  return null;
}

/**
 * Handles a key pressed on an item of a selectable list (see
 * listKeyAction). It moves focus with `focus`, changes the selection, or
 * calls onDelete. It then stops the browser from also handling the key, and
 * stops Escape from reaching a dialog that holds the list.
 *
 * @param {KeyboardEvent} event a keydown on the item or within it
 * @param {object} options
 * @param {object} options.selection useListSelection's
 * @param {object[]} options.items the list's items, as the keys move through
 *   them
 * @param {number} options.index the position of the focused item
 * @param {(index: number) => void} options.focus focuses the item at a
 *   position
 * @param {number} [options.columns] items per row
 * @param {boolean} [options.canDelete] the selection holds items the list
 *   can delete
 * @param {() => void} [options.onDelete] asks to delete the selection
 * @param {boolean} [options.locked] the selection cannot change now (an
 *   action runs on it): the keys only move focus
 * @param {'mac'|'other'} [options.platform]
 * @returns {object|null} what the key did (listKeyAction), or null
 */
export function onListKeydown(event, options) {
  const {
    selection,
    items,
    index,
    focus,
    columns = 1,
    canDelete = false,
    onDelete,
    locked = false,
    platform,
  } = options;

  if (event.defaultPrevented || event.isComposing || isTyping(event.target)) {
    return null;
  }

  const action = listKeyAction(event, {
    index,
    count: items.length,
    columns,
    selected: toValue(selection.count),
    onItem: event.target === event.currentTarget,
    canDelete: canDelete && Boolean(onDelete),
    ...(platform ? { platform } : {}),
  });

  if (!action || (locked && action.type !== 'focus')) {
    return null;
  }

  event.preventDefault();

  switch (action.type) {
    case 'focus':
      focus(action.index);
      break;
    case 'extend':
      focus(action.index);
      selection.extendTo(items[action.index], { from: items[index] });
      break;
    case 'toggle':
      selection.toggle(items[index]);
      break;
    case 'range':
      selection.extendTo(items[index], { from: items[index] });
      break;
    case 'all':
      selection.selectAll();
      break;
    case 'clear':
      event.stopPropagation();
      selection.clear();
      break;
    case 'delete':
      onDelete();
      break;
    default:
      break;
  }

  return action;
}

/**
 * Tells whether Shift was held when a checkbox was pressed, by a click or by
 * Space on the focused box. In some browsers, the click from Space does not
 * carry the key. Bind pointerdown, keydown and click, and call `take` in the
 * change handler.
 *
 * @returns {{pointerdown: () => void, keydown: (event: KeyboardEvent) =>
 *   void, click: (event: MouseEvent) => void, take: () => boolean}}
 */
export function shiftPress() {
  let shift = false;

  return {
    pointerdown() {
      shift = false;
    },
    keydown(event) {
      if (event.key === ' ') {
        shift = Boolean(event.shiftKey);
      }
    },
    click(event) {
      shift = shift || Boolean(event.shiftKey);
    },
    take() {
      const was = shift;

      shift = false;

      return was;
    },
  };
}

/**
 * How many items a row of a laid-out list holds: the items whose top is the
 * same as the first item's top.
 *
 * @param {Element|null} list the list element, whose children are its items
 * @returns {number} at least 1
 */
export function columnsOf(list) {
  const children = [...(list?.children || [])];
  const top = children[0]?.offsetTop;

  if (top === undefined) {
    return 1;
  }

  return Math.max(
    1,
    children.filter((child) => child.offsetTop === top).length,
  );
}
