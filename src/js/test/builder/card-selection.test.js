// @vitest-environment happy-dom
// useCardSelection: a card's checkbox, Select all and the keys on a card,
// with each change of the selection said.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { reactive } from 'vue';

import { useCardSelection, useListSelection } from '@/builder/listSelection.js';

afterEach(() => {
  document.body.replaceChildren();
});

// Three cards in a list, the last of which cannot be selected.
function cardList(options = {}) {
  const items = [{ name: 'a' }, { name: 'b' }, { name: 'c' }];
  const selectable = (item) => item.name !== 'c';
  const selection = reactive(
    useListSelection(items, (item) => item.name, { selectable }),
  );
  const said = [];
  const cards = useCardSelection(selection, {
    items,
    announce: (text) => said.push(text),
    selectable,
  });
  const list = document.createElement('ul');

  items.forEach((_, index) => {
    const card = document.createElement('li');

    card.tabIndex = -1;
    card.addEventListener('keydown', (event) =>
      cards.keydown(event, index, options),
    );
    list.append(card);
  });
  document.body.append(list);

  const press = (index, key, more = {}) =>
    list.children[index].dispatchEvent(
      new KeyboardEvent('keydown', { key, bubbles: true, ...more }),
    );

  return { items, selection, cards, said, list, press };
}

describe('useCardSelection', () => {
  test('a checkbox press selects its card, and the box shows the selection', () => {
    const { items, selection, cards, said } = cardList();
    const box = { checked: true };

    cards.select(items[0], { target: box });
    expect(selection.has(items[0])).toBe(true);
    expect(box.checked).toBe(true);

    // A press of an item that cannot be selected changes nothing.
    const other = { checked: true };

    cards.select(items[2], { target: other });
    expect(other.checked).toBe(false);
    expect(said).toEqual(['1 of 2 selected.', '1 of 2 selected.']);
  });

  test('Select all selects every card that can be selected, then none', () => {
    const { selection, cards, said } = cardList();

    cards.selectAll();
    expect(selection.count).toBe(2);
    cards.selectAll();
    expect(selection.count).toBe(0);
    expect(said).toEqual(['2 of 2 selected.', '0 of 2 selected.']);
  });

  test('the keys move focus, select, and are said only when they select', () => {
    const { items, selection, said, list, press } = cardList();

    list.children[0].focus();
    press(0, 'ArrowRight');
    expect(document.activeElement).toBe(list.children[1]);
    expect(selection.tabStop(items[1])).toBe(true);
    expect(said).toEqual([]);

    press(1, ' ');
    expect(selection.has(items[1])).toBe(true);
    expect(said).toEqual(['1 of 2 selected.']);

    // Space on a card that cannot be selected says nothing.
    press(2, ' ');
    expect(said).toEqual(['1 of 2 selected.']);
  });

  test('passes locked, canDelete and onDelete to the keys', () => {
    const onDelete = vi.fn();
    const locked = cardList({ locked: true });

    locked.press(0, ' ');
    expect(locked.selection.count).toBe(0);
    expect(locked.said).toEqual([]);

    const open = cardList({ canDelete: true, onDelete });

    open.press(0, ' ');
    open.press(0, 'Delete');
    expect(onDelete).toHaveBeenCalledTimes(1);
  });
});
