// Which items of a list are selected, for a list whose items have a
// checkbox each and a Select all above them (the drafts page's tabs).
//
// The selection is a set of keys, not of items: a list read again from the
// server holds new objects for the same items, which stay selected. An item
// that leaves the list leaves the selection too, so one that comes back
// later is not selected. (selection.js is the canvas's selection.)

import { computed, shallowRef, toValue, watch } from 'vue';

/**
 * @param {import('vue').MaybeRefOrGetter<object[]>} items the items that
 *   can be selected, in list order
 * @param {(item: object) => string} keyOf names an item apart from the
 *   others
 * @returns {{
 *   has: (item: object) => boolean,
 *   toggle: (item: object, on?: boolean) => void,
 *   toggleAll: () => void,
 *   clear: () => void,
 *   remove: (keys: string[]) => void,
 *   selected: import('vue').ComputedRef<object[]>,
 *   count: import('vue').ComputedRef<number>,
 *   total: import('vue').ComputedRef<number>,
 *   state: import('vue').ComputedRef<'none'|'some'|'all'>,
 * }} selected holds the selected items in list order; total is how many
 *   can be selected; state is what Select all shows
 */
export function useListSelection(items, keyOf) {
  const keys = shallowRef(new Set());
  const list = computed(() => toValue(items) || []);
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

  function set(next) {
    keys.value = new Set(next);
  }

  // Keys whose items have left the list go.
  watch(list, (now) => {
    const present = new Set(now.map(keyOf));
    const kept = [...keys.value].filter((key) => present.has(key));

    if (kept.length !== keys.value.size) {
      set(kept);
    }
  });

  return {
    has(item) {
      return keys.value.has(keyOf(item));
    },

    // Selects the item, or with `on` false unselects it; without `on`, the
    // other of what it is.
    toggle(item, on = !keys.value.has(keyOf(item))) {
      const key = keyOf(item);
      const next = new Set(keys.value);

      if (on) {
        next.add(key);
      } else {
        next.delete(key);
      }

      set(next);
    },

    // Select all: with none or some selected, every item; with all, none.
    toggleAll() {
      set(state.value === 'all' ? [] : list.value.map(keyOf));
    },

    clear() {
      set([]);
    },

    remove(gone) {
      const drop = new Set(gone || []);

      set([...keys.value].filter((key) => !drop.has(key)));
    },

    selected,
    count,
    total,
    state,
  };
}
