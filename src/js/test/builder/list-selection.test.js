// The selection of a list whose items have a checkbox each: by key, so it
// survives the list being read again, and never holding what left the list.

import { describe, expect, test } from 'vitest';
import { nextTick, ref } from 'vue';

import { useListSelection } from '@/builder/listSelection.js';

const item = (id) => ({ id, name: `Draft ${id}` });
const keyOf = (entry) => `alice/${entry.id}`;

function setup(ids = ['a', 'b', 'c']) {
  const items = ref(ids.map(item));

  return { items, selection: useListSelection(items, keyOf) };
}

describe('the selection of a list', () => {
  test('starts empty, and counts what can be selected', () => {
    const { items, selection } = setup();

    expect(selection.count.value).toBe(0);
    expect(selection.total.value).toBe(3);
    expect(selection.state.value).toBe('none');
    expect(selection.selected.value).toEqual([]);
    expect(selection.has(items.value[0])).toBe(false);
  });

  test('an item is selected, unselected, or made the other of what it is', () => {
    const { items, selection } = setup();
    const [a, b, c] = items.value;

    selection.toggle(c, true);
    selection.toggle(a, true);
    // In list order, whatever order they were selected in.
    expect(selection.selected.value).toEqual([a, c]);
    expect(selection.count.value).toBe(2);
    expect(selection.state.value).toBe('some');
    expect(selection.has(a)).toBe(true);
    expect(selection.has(b)).toBe(false);

    // Selecting what is selected changes nothing.
    selection.toggle(a, true);
    expect(selection.count.value).toBe(2);

    selection.toggle(a, false);
    expect(selection.selected.value).toEqual([c]);

    selection.toggle(b);
    selection.toggle(c);
    expect(selection.selected.value).toEqual([b]);
  });

  test('Select all selects every item, and with all selected none', () => {
    const { items, selection } = setup();

    // From none.
    selection.toggleAll();
    expect(selection.state.value).toBe('all');
    expect(selection.selected.value).toEqual(items.value);

    // From all.
    selection.toggleAll();
    expect(selection.state.value).toBe('none');

    // From some.
    selection.toggle(items.value[1], true);
    expect(selection.state.value).toBe('some');
    selection.toggleAll();
    expect(selection.state.value).toBe('all');
    expect(selection.count.value).toBe(3);

    selection.clear();
    expect(selection.count.value).toBe(0);
  });

  test('named keys are removed, and the rest stay', () => {
    const { items, selection } = setup();

    selection.toggleAll();
    selection.remove(['alice/a', 'alice/c', 'alice/unknown']);
    expect(selection.selected.value).toEqual([items.value[1]]);
    selection.remove();
    expect(selection.count.value).toBe(1);
  });

  test('a list read again keeps its selection, by key', async () => {
    const { items, selection } = setup();

    selection.toggle(items.value[1], true);
    // New objects for the same items, and one more.
    items.value = ['a', 'b', 'c', 'd'].map(item);
    await nextTick();

    expect(selection.selected.value).toEqual([items.value[1]]);
    expect(selection.has(items.value[1])).toBe(true);
    expect(selection.total.value).toBe(4);
    expect(selection.state.value).toBe('some');
  });

  test('an item that leaves the list leaves the selection, for good', async () => {
    const { items, selection } = setup();

    selection.toggleAll();
    items.value = ['a', 'c'].map(item);
    // At once, before the selection is pruned: only what is listed counts.
    expect(selection.count.value).toBe(2);
    expect(selection.state.value).toBe('all');
    await nextTick();
    expect(selection.count.value).toBe(2);

    // The same item listed again later is not selected.
    items.value = ['a', 'b', 'c'].map(item);
    await nextTick();
    expect(selection.selected.value.map((entry) => entry.id)).toEqual([
      'a',
      'c',
    ]);
    expect(selection.state.value).toBe('some');
  });

  test('takes a getter, and an empty or missing list', async () => {
    const source = ref(null);
    const selection = useListSelection(() => source.value, keyOf);

    expect(selection.total.value).toBe(0);
    expect(selection.state.value).toBe('none');
    selection.toggleAll();
    expect(selection.count.value).toBe(0);

    source.value = ['a'].map(item);
    await nextTick();
    selection.toggleAll();
    expect(selection.state.value).toBe('all');
  });
});
