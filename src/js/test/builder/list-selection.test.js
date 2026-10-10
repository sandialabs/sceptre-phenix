// The selection of a list whose items have a checkbox each: by key, so it
// survives the list being read again, and never holding what left the list;
// and the keys that move through such a list and select in it.

import { describe, expect, test, vi } from 'vitest';
import { nextTick, ref } from 'vue';

import {
  columnsOf,
  isTyping,
  listKeyAction,
  onListKeydown,
  shiftPress,
  useListSelection,
} from '@/builder/listSelection.js';

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

describe('ranges, and items that cannot be selected', () => {
  const ids = (selection) => selection.selected.value.map((entry) => entry.id);

  test('a range runs from the item last pressed, forward or backward', () => {
    const { items, selection } = setup(['a', 'b', 'c', 'd', 'e']);
    const [a, b, , d, e] = items.value;

    selection.toggle(b, true);
    selection.extendTo(d);
    expect(ids(selection)).toEqual(['b', 'c', 'd']);

    // A shorter range gives back what the longer one took, and the anchor
    // stays where it was.
    selection.extendTo(items.value[2]);
    expect(ids(selection)).toEqual(['b', 'c']);

    // Backward from the same anchor.
    selection.extendTo(a);
    expect(ids(selection)).toEqual(['a', 'b']);

    // A new press is a new anchor; the range lies over what was selected.
    selection.toggle(e, true);
    selection.extendTo(d);
    expect(ids(selection)).toEqual(['a', 'b', 'd', 'e']);
  });

  test('a range the keys make selects, even after a press that unselected', () => {
    const { items, selection } = setup(['a', 'b', 'c', 'd']);
    const [a, b, c, d] = items.value;

    selection.toggle(a, true);
    selection.toggle(c, true);
    // Space unselects c, then Shift and Down: c and d are selected.
    selection.toggle(c, false);
    expect(ids(selection)).toEqual(['a']);
    selection.extendTo(d, { from: c });
    expect(ids(selection)).toEqual(['a', 'c', 'd']);

    // Back past the anchor, still selecting; b was never selected.
    selection.extendTo(b, { from: d });
    expect(ids(selection)).toEqual(['a', 'b', 'c']);
  });

  test('a checkbox cleared with Shift unselects the range from the anchor', () => {
    const { items, selection } = setup(['a', 'b', 'c', 'd']);
    const [a, b, , d] = items.value;

    selection.selectAll();
    selection.press(b, { checked: false });
    selection.press(d, { checked: false, range: true });
    expect(ids(selection)).toEqual(['a']);
    expect(selection.has(a)).toBe(true);

    // Checked with Shift, the same range is selected again.
    selection.press(d, { checked: true, range: true });
    expect(ids(selection)).toEqual(['a', 'b', 'c', 'd']);
  });

  test('a checkbox pressed with Shift and no anchor (after Select all or clear) changes its item alone, as a plain press does', () => {
    const { items, selection } = setup(['1', '2', '3']);
    const [one, two, three] = items.value;
    const said = () =>
      `${selection.count.value} of ${selection.total.value} selected`;

    // Select all, then Shift and the box of the second item, which clears
    // it: the second alone is unselected, and the box stays cleared.
    selection.toggle(one, true);
    selection.toggleAll();
    selection.press(two, { checked: false, range: true });
    expect(selection.has(two)).toBe(false);
    expect(said()).toBe('2 of 3 selected');

    // It is the anchor now.
    selection.press(three, { checked: false, range: true });
    expect(ids(selection)).toEqual(['1']);

    // The same after the keys' Select all, and after clear.
    selection.selectAll();
    selection.press(three, { checked: false, range: true });
    expect(ids(selection)).toEqual(['1', '2']);
    selection.clear();
    selection.press(two, { checked: true, range: true });
    expect(ids(selection)).toEqual(['2']);
    expect(selection.has(one)).toBe(false);
  });

  test('without an anchor, a range starts at the item focus was on, and selects', () => {
    const { items, selection } = setup(['a', 'b', 'c', 'd']);
    const [, b, c, d] = items.value;

    // Shift and an arrow key from b to c.
    selection.extendTo(c, { from: b });
    expect(ids(selection)).toEqual(['b', 'c']);
    selection.extendTo(d, { from: c });
    expect(ids(selection)).toEqual(['b', 'c', 'd']);

    // After Escape, Shift and Space on one item selects it alone.
    selection.clear();
    selection.extendTo(c);
    expect(ids(selection)).toEqual(['c']);
  });

  test('a checkbox pressed with Shift makes the range as the box now is; without, it is the box', () => {
    const { items, selection } = setup(['a', 'b', 'c']);
    const [a, , c] = items.value;

    selection.press(a, { checked: true });
    selection.press(c, { checked: true, range: true });
    expect(ids(selection)).toEqual(['a', 'b', 'c']);
    selection.press(a, { checked: false });
    expect(ids(selection)).toEqual(['b', 'c']);
  });

  test('items that cannot be selected stay in the list for the keys, and out of the selection', () => {
    const items = ref(['a', 'b', 'c', 'd'].map(item));
    const selection = useListSelection(items, keyOf, {
      selectable: (entry) => entry.id !== 'b',
    });
    const [a, b, c] = items.value;

    expect(selection.total.value).toBe(3);
    selection.toggle(b, true);
    expect(selection.count.value).toBe(0);

    selection.toggle(a, true);
    selection.extendTo(c);
    expect(ids(selection)).toEqual(['a', 'c']);

    selection.selectAll();
    expect(ids(selection)).toEqual(['a', 'c', 'd']);
    expect(selection.state.value).toBe('all');
  });

  test('Select all selects every item; clear unselects them and forgets the anchor', () => {
    const { items, selection } = setup(['a', 'b', 'c']);

    selection.selectAll();
    selection.selectAll();
    expect(selection.state.value).toBe('all');
    selection.clear();
    expect(selection.count.value).toBe(0);

    // With no anchor, a range starts where it is asked from.
    selection.extendTo(items.value[2], { from: items.value[1] });
    expect(ids(selection)).toEqual(['b', 'c']);
  });

  test('an anchor that leaves the list is forgotten', async () => {
    const { items, selection } = setup(['a', 'b', 'c', 'd']);

    selection.toggle(items.value[1], true);
    items.value = ['a', 'c', 'd'].map(item);
    await nextTick();
    expect(selection.count.value).toBe(0);

    // The range starts at the item focus was on, not at the gone anchor.
    selection.extendTo(items.value[2], { from: items.value[1] });
    expect(ids(selection)).toEqual(['c', 'd']);

    // Removing the anchor's key forgets it too.
    selection.toggle(items.value[0], true);
    selection.remove(['alice/a']);
    selection.extendTo(items.value[1]);
    expect(ids(selection)).toEqual(['c', 'd']);
  });

  test('the Tab stop is the item focus was last on, else the first', async () => {
    const { items, selection } = setup(['a', 'b', 'c']);
    const [a, b] = items.value;

    expect(selection.tabStop(a)).toBe(true);
    expect(selection.tabStop(b)).toBe(false);
    selection.focused(b);
    expect(selection.tabStop(a)).toBe(false);
    expect(selection.tabStop(b)).toBe(true);

    // Read again, by key; gone, the first item again.
    items.value = ['a', 'b', 'c'].map(item);
    expect(selection.tabStop(items.value[1])).toBe(true);
    items.value = ['a', 'c'].map(item);
    await nextTick();
    expect(selection.tabStop(items.value[0])).toBe(true);
  });
});

describe('the keys of a list', () => {
  const press = (key, mods = {}) => ({ key, ...mods });
  const at = (index, extra = {}) => ({
    index,
    count: 5,
    platform: 'other',
    ...extra,
  });

  test('Up and Down move in a column, wrapping, and Home and End jump; Left and Right do nothing', () => {
    expect(listKeyAction(press('ArrowDown'), at(1))).toEqual({
      type: 'focus',
      index: 2,
    });
    expect(listKeyAction(press('ArrowUp'), at(0))).toEqual({
      type: 'focus',
      index: 4,
    });
    expect(listKeyAction(press('ArrowDown'), at(4)).index).toBe(0);
    expect(listKeyAction(press('Home'), at(3)).index).toBe(0);
    expect(listKeyAction(press('End'), at(1)).index).toBe(4);
    expect(listKeyAction(press('ArrowLeft'), at(1))).toBeNull();
    expect(listKeyAction(press('ArrowRight'), at(1))).toBeNull();
  });

  test('in a grid, Left and Right move one card and Up and Down one row', () => {
    const grid = (index) => at(index, { count: 7, columns: 3 });

    expect(listKeyAction(press('ArrowRight'), grid(2)).index).toBe(3);
    expect(listKeyAction(press('ArrowLeft'), grid(0)).index).toBe(6);
    expect(listKeyAction(press('ArrowDown'), grid(1)).index).toBe(4);
    // Below a row whose next one is shorter: the last card.
    expect(listKeyAction(press('ArrowDown'), grid(5)).index).toBe(6);
    // On the last row, and above the first, nowhere.
    expect(listKeyAction(press('ArrowDown'), grid(6))).toBeNull();
    expect(listKeyAction(press('ArrowUp'), grid(2))).toBeNull();
    expect(listKeyAction(press('ArrowUp'), grid(4)).index).toBe(1);
  });

  test('Shift and a move extends the range to where focus goes', () => {
    expect(
      listKeyAction(press('ArrowDown', { shiftKey: true }), at(1)),
    ).toEqual({ type: 'extend', index: 2 });
    expect(listKeyAction(press('End', { shiftKey: true }), at(1))).toEqual({
      type: 'extend',
      index: 4,
    });
  });

  test('Space toggles the focused item, with Shift a range; never on a control in it', () => {
    expect(listKeyAction(press(' '), at(1))).toEqual({ type: 'toggle' });
    expect(listKeyAction(press(' ', { shiftKey: true }), at(1))).toEqual({
      type: 'range',
    });
    expect(listKeyAction(press(' '), at(1, { onItem: false }))).toBeNull();
  });

  test('Mod+A selects all: Ctrl elsewhere, ⌘ on macOS', () => {
    expect(listKeyAction(press('a', { ctrlKey: true }), at(0))).toEqual({
      type: 'all',
    });
    expect(listKeyAction(press('a', { metaKey: true }), at(0))).toBeNull();
    expect(
      listKeyAction(press('a', { metaKey: true }), at(0, { platform: 'mac' })),
    ).toEqual({ type: 'all' });
    expect(
      listKeyAction(press('a', { ctrlKey: true }), at(0, { platform: 'mac' })),
    ).toBeNull();
    expect(
      listKeyAction(press('A', { ctrlKey: true, shiftKey: true }), at(0)),
    ).toBeNull();
    // A plain letter is the page's.
    expect(listKeyAction(press('a'), at(0))).toBeNull();
  });

  test('Mod+A on a layout without Latin letters is the key where A is', () => {
    // Russian: ф is typed on the A key, и on the B key.
    expect(
      listKeyAction(press('ф', { ctrlKey: true, code: 'KeyA' }), at(0)),
    ).toEqual({ type: 'all' });
    expect(
      listKeyAction(
        press('ф', { metaKey: true, code: 'KeyA' }),
        at(0, { platform: 'mac' }),
      ),
    ).toEqual({ type: 'all' });
    expect(
      listKeyAction(press('и', { ctrlKey: true, code: 'KeyB' }), at(0)),
    ).toBeNull();
    // A Latin letter decides by what it types, wherever its key is (on
    // AZERTY, A is where QWERTY has Q).
    expect(
      listKeyAction(press('a', { ctrlKey: true, code: 'KeyQ' }), at(0)),
    ).toEqual({ type: 'all' });
    expect(
      listKeyAction(press('q', { ctrlKey: true, code: 'KeyA' }), at(0)),
    ).toBeNull();
  });

  test('Escape clears a selection, and is left alone without one', () => {
    expect(listKeyAction(press('Escape'), at(0, { selected: 2 }))).toEqual({
      type: 'clear',
    });
    expect(listKeyAction(press('Escape'), at(0))).toBeNull();
  });

  test('Delete asks to delete a selection the list can delete; Backspace too on macOS', () => {
    const can = { selected: 2, canDelete: true };

    expect(listKeyAction(press('Delete'), at(0, can))).toEqual({
      type: 'delete',
    });
    expect(listKeyAction(press('Backspace'), at(0, can))).toBeNull();
    expect(
      listKeyAction(press('Backspace'), at(0, { ...can, platform: 'mac' })),
    ).toEqual({ type: 'delete' });
    // Nothing selected, or nothing the list can delete: the key is left.
    expect(
      listKeyAction(press('Delete'), at(0, { canDelete: true })),
    ).toBeNull();
    expect(listKeyAction(press('Delete'), at(0, { selected: 2 }))).toBeNull();
  });

  test('keys with Alt, an empty list, and other keys are left alone', () => {
    expect(listKeyAction(press('ArrowDown', { altKey: true }), at(1))).toBe(
      null,
    );
    expect(listKeyAction(press('ArrowDown', { ctrlKey: true }), at(1))).toBe(
      null,
    );
    expect(listKeyAction(press('ArrowDown'), at(0, { count: 0 }))).toBeNull();
    expect(listKeyAction(press('Enter'), at(0))).toBeNull();
    expect(listKeyAction(press('Tab'), at(0))).toBeNull();
  });
});

describe('a key pressed in a list', () => {
  function keydown(key, mods = {}, { onItem = true, target } = {}) {
    const item = { tagName: 'LI' };
    const event = {
      key,
      target: target || (onItem ? item : { tagName: 'BUTTON' }),
      currentTarget: item,
      defaultPrevented: false,
      preventDefault: vi.fn(function prevent() {
        this.defaultPrevented = true;
      }),
      stopPropagation: vi.fn(),
      ...mods,
    };

    return event;
  }

  function list(ids = ['a', 'b', 'c', 'd']) {
    const { items, selection } = setup(ids);
    const focus = vi.fn();
    const onDelete = vi.fn();
    const run = (event, index, extra = {}) =>
      onListKeydown(event, {
        selection,
        items: items.value,
        index,
        focus,
        onDelete,
        canDelete: true,
        platform: 'other',
        ...extra,
      });

    return { items, selection, focus, onDelete, run };
  }

  test('arrow keys move focus, and are taken from the page', () => {
    const { focus, run } = list();
    const event = keydown('ArrowDown');

    expect(run(event, 0)).toEqual({ type: 'focus', index: 1 });
    expect(focus).toHaveBeenCalledWith(1);
    expect(event.preventDefault).toHaveBeenCalled();
  });

  test('keyboard-only: Space, then Shift and Down twice, select three items', () => {
    const { items, selection, focus, run } = list();

    run(keydown(' '), 0);
    run(keydown('ArrowDown', { shiftKey: true }), 0);
    run(keydown('ArrowDown', { shiftKey: true }), 1);
    expect(focus).toHaveBeenLastCalledWith(2);
    expect(selection.selected.value).toEqual(items.value.slice(0, 3));

    // Shift and Up takes the last back.
    run(keydown('ArrowUp', { shiftKey: true }), 2);
    expect(selection.count.value).toBe(2);
  });

  test('Shift and an arrow key with nothing pressed yet starts at the focused item', () => {
    const { items, selection, run } = list();

    run(keydown('ArrowDown', { shiftKey: true }), 1);
    expect(selection.selected.value).toEqual(items.value.slice(1, 3));
  });

  test('Mod+A, Escape and Delete; Escape is kept from a dialog', () => {
    const { selection, onDelete, run } = list();

    run(keydown('a', { ctrlKey: true }), 0);
    expect(selection.state.value).toBe('all');

    run(keydown('Delete'), 0);
    expect(onDelete).toHaveBeenCalledOnce();
    // A list that cannot delete leaves the key.
    expect(run(keydown('Delete'), 0, { canDelete: false })).toBeNull();
    expect(run(keydown('Delete'), 0, { onDelete: undefined })).toBeNull();

    const escape = keydown('Escape');

    run(escape, 0);
    expect(selection.count.value).toBe(0);
    expect(escape.stopPropagation).toHaveBeenCalled();

    // Without a selection, Escape goes on (to close the dialog).
    const again = keydown('Escape');

    expect(run(again, 0)).toBeNull();
    expect(again.preventDefault).not.toHaveBeenCalled();
  });

  test('Space on a control in the item is the control’s', () => {
    const { selection, run } = list();
    const event = keydown(' ', {}, { onItem: false });

    expect(run(event, 0)).toBeNull();
    expect(event.preventDefault).not.toHaveBeenCalled();
    expect(selection.count.value).toBe(0);
  });

  test('typing in a field, a key already handled, and a locked selection', () => {
    const { selection, focus, run } = list();
    const field = keydown(
      'a',
      { ctrlKey: true },
      {
        target: { tagName: 'INPUT', type: 'text' },
      },
    );

    expect(run(field, 0)).toBeNull();
    expect(selection.count.value).toBe(0);

    const handled = keydown('ArrowDown', { defaultPrevented: true });

    expect(run(handled, 0)).toBeNull();
    expect(focus).not.toHaveBeenCalled();

    // While an action runs on the selection, the keys only move.
    expect(run(keydown(' '), 0, { locked: true })).toBeNull();
    expect(run(keydown('ArrowDown'), 0, { locked: true })).toEqual({
      type: 'focus',
      index: 1,
    });
    expect(selection.count.value).toBe(0);
  });

  test('what counts as typing', () => {
    expect(isTyping({ tagName: 'INPUT', type: 'text' })).toBe(true);
    expect(isTyping({ tagName: 'INPUT', type: 'search' })).toBe(true);
    expect(isTyping({ tagName: 'INPUT' })).toBe(true);
    expect(isTyping({ tagName: 'INPUT', type: 'checkbox' })).toBe(false);
    expect(isTyping({ tagName: 'TEXTAREA' })).toBe(true);
    expect(isTyping({ tagName: 'SELECT' })).toBe(true);
    expect(isTyping({ tagName: 'DIV', isContentEditable: true })).toBe(true);
    expect(isTyping({ tagName: 'LI' })).toBe(false);
    expect(isTyping(null)).toBe(false);
  });
});

describe('a checkbox pressed with Shift', () => {
  test('by a click, or by Space on the box, is told once', () => {
    const shift = shiftPress();

    shift.pointerdown();
    shift.click({ shiftKey: true });
    expect(shift.take()).toBe(true);
    expect(shift.take()).toBe(false);

    // Space with Shift, whose click carries no Shift.
    shift.keydown({ key: ' ', shiftKey: true });
    shift.click({ shiftKey: false });
    expect(shift.take()).toBe(true);

    // A click after it, without Shift, is a plain press.
    shift.keydown({ key: ' ', shiftKey: true });
    shift.pointerdown();
    shift.click({ shiftKey: false });
    expect(shift.take()).toBe(false);
  });
});

describe('the columns of a laid-out list', () => {
  test('are the items on the first row', () => {
    const row = (...tops) => ({
      children: tops.map((offsetTop) => ({ offsetTop })),
    });

    expect(columnsOf(row(0, 0, 0, 120, 120))).toBe(3);
    expect(columnsOf(row(0, 40, 80))).toBe(1);
    expect(columnsOf(row())).toBe(1);
    expect(columnsOf(null)).toBe(1);
  });
});
