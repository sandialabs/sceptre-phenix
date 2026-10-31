// The selection of a checkbox list with a Select all, and its keys. The
// selection holds keys, not items, so a list read again keeps it. An item that
// leaves the list leaves the selection. selection.js is the canvas's selection.

import { computed, ref, shallowRef, toValue, watch } from 'vue';

import { currentPlatform, matchesKey } from './keymap.js';
import { rowTarget } from './roving.js';

// The selection of a list, by keyOf. Items that options.selectable rejects
// have no checkbox but stay in the list for the keys. `selected` is in list
// order, `total` counts selectable items and `state` is what Select all shows.
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

// Whether a key press is typing: in a text field, a text area, a select or
// editable content.
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

// What a key pressed on an item of a selectable list does, or null for a key
// the list leaves alone. `onItem` means focus is on the item, not a control in
// it, so Space is the list's.
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

// Handles a key on an item of a selectable list (listKeyAction). It stops the
// browser default and stops Escape from reaching a dialog that holds the list.
// While `locked` (an action runs on the selection), the keys only move focus.
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

// Tells whether Shift was held when a checkbox was pressed. Some browsers do
// not put the key on the click from Space. Bind pointerdown, keydown and click,
// and call `take` in the change handler.
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

// What a key changes in the selection. Each change is said, as a press of a
// checkbox is.
const SELECTING = ['extend', 'toggle', 'range', 'all', 'clear'];

// The checkboxes, Select all and card keys of a reactive useListSelection of
// `items`. Each change goes to `announce` as "2 of 5 selected.".
export function useCardSelection(
  selection,
  { items, announce, selectable = () => true },
) {
  // Whether Shift was held as a card's checkbox was pressed.
  const shift = shiftPress();

  function said() {
    announce(`${selection.count} of ${selection.total} selected.`);
  }

  return {
    shift,

    // A card's checkbox: with Shift, the range from the card last pressed
    // becomes as the box now is. The box then shows what the selection holds.
    select(item, event) {
      selection.press(item, {
        checked: event.target.checked,
        range: shift.take(),
      });
      event.target.checked = selection.has(item);
      said();
    },

    selectAll() {
      selection.toggleAll();
      said();
    },

    // The keys of the card at `index` (onListKeydown), whose parent is the
    // list. options: locked, canDelete and onDelete.
    keydown(event, index, options = {}) {
      const list = event.currentTarget.parentElement;
      const shown = toValue(items);
      const action = onListKeydown(event, {
        ...options,
        selection,
        items: shown,
        index,
        columns: columnsOf(list),
        // The card at `to` takes focus and becomes the list's Tab stop.
        focus(to) {
          if (shown[to] && list.children[to]) {
            selection.focused(shown[to]);
            list.children[to].focus();
          }
        },
      });

      if (
        SELECTING.includes(action?.type) &&
        (action.type !== 'toggle' || selectable(shown[index]))
      ) {
        said();
      }

      return action;
    },
  };
}

// How many items a row of a laid-out list holds (at least 1): the items whose
// top is the same as the first item's top.
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
