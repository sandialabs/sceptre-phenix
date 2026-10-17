// @vitest-environment happy-dom
// The menu button, mounted and driven from the keyboard and the pointer, as
// the WAI-ARIA APG menu button pattern has it.

import { afterEach, describe, expect, test } from 'vitest';
import { enableAutoUnmount, mount } from '@vue/test-utils';
import fc from 'fast-check';

import BuilderMenuButton from '@/components/builder/BuilderMenuButton.vue';

enableAutoUnmount(afterEach);

afterEach(() => {
  document.body.innerHTML = '';
});

const ITEMS = [
  { id: 'cut', label: 'Cut' },
  { id: 'copy', label: 'Copy', description: 'Copies the selection' },
  { id: 'paste', label: 'Paste', separator: true },
  { id: 'delete', label: 'Delete' },
];

// A menu of radio items, the second one checked.
const CHOICES = [
  { id: 'tree', label: 'Tree', checked: false },
  { id: 'grid', label: 'Grid', checked: true },
  { id: 'circle', label: 'Circle', checked: false },
];

function open(props = {}) {
  // Something else on the page, to take focus and pointer presses.
  document.body.insertAdjacentHTML(
    'beforeend',
    '<button type="button" id="elsewhere">Elsewhere</button>',
  );

  return mount(BuilderMenuButton, {
    attachTo: document.body,
    props: { items: ITEMS, testid: 'menu', ...props },
    slots: { default: 'Edit' },
  });
}

const buttonOf = (wrapper) => wrapper.get('[data-testid="menu"]');
const itemsOf = (wrapper) => wrapper.findAll('[role^="menuitem"]');
const focusedLabel = () =>
  document.activeElement?.querySelector('.builder-menu__text > span')
    ?.textContent;

// Presses a key on whatever has focus, as the browser sends it.
async function press(wrapper, key, mods = {}) {
  const target = wrapper.find(':focus').exists()
    ? wrapper.get(':focus')
    : buttonOf(wrapper);

  await target.trigger('keydown', { key, ...mods });
}

describe('the menu button', () => {
  test('a click opens the menu on its first item, and another closes it', async () => {
    const wrapper = open();
    const button = buttonOf(wrapper);

    expect(button.attributes('aria-haspopup')).toBe('menu');
    expect(button.attributes('aria-expanded')).toBe('false');
    expect(button.attributes('aria-controls')).toBeUndefined();
    expect(wrapper.find('[role="menu"]').exists()).toBe(false);

    await button.trigger('click');

    const menu = wrapper.get('[role="menu"]');

    expect(button.attributes('aria-expanded')).toBe('true');
    expect(button.attributes('aria-controls')).toBe(menu.attributes('id'));
    expect(menu.attributes('aria-labelledby')).toBe(button.attributes('id'));
    expect(itemsOf(wrapper)).toHaveLength(4);
    expect(wrapper.findAll('[role="separator"]')).toHaveLength(1);
    expect(document.activeElement).toBe(itemsOf(wrapper)[0].element);
    expect(wrapper.emitted('toggle')).toEqual([[true]]);

    await button.trigger('click');

    expect(button.attributes('aria-expanded')).toBe('false');
    expect(wrapper.find('[role="menu"]').exists()).toBe(false);
    expect(document.activeElement).toBe(button.element);
    expect(wrapper.emitted('toggle')).toEqual([[true], [false]]);
  });

  test('Down Arrow opens it on the first item, and Up Arrow on the last', async () => {
    const wrapper = open();
    const button = buttonOf(wrapper);

    button.element.focus();
    await button.trigger('keydown', { key: 'ArrowDown' });
    expect(focusedLabel()).toBe('Cut');

    await press(wrapper, 'Escape');
    await button.trigger('keydown', { key: 'ArrowUp' });
    expect(focusedLabel()).toBe('Delete');

    // With a modifier, the arrows are someone else's keys.
    await press(wrapper, 'Escape');
    await button.trigger('keydown', { key: 'ArrowDown', altKey: true });
    expect(button.attributes('aria-expanded')).toBe('false');
  });

  test('a menu of choices opens on the checked one', async () => {
    const wrapper = open({ items: CHOICES });

    await buttonOf(wrapper).trigger('click');

    const items = itemsOf(wrapper);

    expect(items.map((item) => item.attributes('role'))).toEqual([
      'menuitemradio',
      'menuitemradio',
      'menuitemradio',
    ]);
    expect(items.map((item) => item.attributes('aria-checked'))).toEqual([
      'false',
      'true',
      'false',
    ]);
    expect(focusedLabel()).toBe('Grid');
  });

  test('the arrows wrap, Home and End go to the ends, and a letter to its item', async () => {
    const wrapper = open();

    await buttonOf(wrapper).trigger('click');

    const moves = [];

    for (const key of [
      'ArrowDown',
      'ArrowDown',
      'ArrowDown',
      'ArrowDown',
      'ArrowUp',
      'Home',
      'End',
      'c',
      'c',
      'p',
    ]) {
      await press(wrapper, key);
      moves.push(focusedLabel());
    }

    expect(moves).toEqual([
      'Copy',
      'Paste',
      'Delete',
      'Cut',
      'Delete',
      'Cut',
      'Delete',
      'Cut',
      'Copy',
      'Paste',
    ]);
    // The focused item holds the menu's one Tab stop.
    expect(itemsOf(wrapper).map((item) => item.attributes('tabindex'))).toEqual(
      ['-1', '-1', '0', '-1'],
    );
  });

  test('Enter, Space or a click chooses the item, and focus goes back to the button', async () => {
    for (const choose of ['Enter', ' ', 'click']) {
      const wrapper = open();
      const button = buttonOf(wrapper);

      await button.trigger('click');
      await press(wrapper, 'ArrowDown');

      if (choose === 'click') {
        await itemsOf(wrapper)[1].trigger('click');
      } else {
        await press(wrapper, choose);
      }

      expect(wrapper.emitted('select'), choose).toEqual([[ITEMS[1]]]);
      expect(button.attributes('aria-expanded'), choose).toBe('false');
      expect(document.activeElement, choose).toBe(button.element);
      wrapper.unmount();
    }
  });

  test('Escape closes it, with focus on the button, and the Builder never sees the key', async () => {
    const wrapper = open();
    const button = buttonOf(wrapper);
    const reached = [];
    const listener = (event) => reached.push(event.key);

    window.addEventListener('keydown', listener);
    await button.trigger('click');
    await press(wrapper, 'ArrowDown');
    await press(wrapper, 'Escape');
    window.removeEventListener('keydown', listener);

    expect(button.attributes('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(button.element);
    expect(reached).toEqual([]);
    expect(wrapper.emitted('select')).toBeUndefined();
  });

  test('Tab closes it from the button, as though the menu were not there', async () => {
    const wrapper = open();
    const button = buttonOf(wrapper);

    await button.trigger('click');
    await press(wrapper, 'Tab');

    expect(button.attributes('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(button.element);
  });

  test('focus that leaves for the page closes it where focus went', async () => {
    const wrapper = open();
    const elsewhere = document.getElementById('elsewhere');

    await buttonOf(wrapper).trigger('click');
    // Focus inside the menu keeps it open.
    itemsOf(wrapper)[2].element.focus();
    await wrapper.vm.$nextTick();
    expect(buttonOf(wrapper).attributes('aria-expanded')).toBe('true');

    elsewhere.focus();
    await wrapper.vm.$nextTick();

    expect(buttonOf(wrapper).attributes('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(elsewhere);
    expect(wrapper.emitted('toggle')).toEqual([[true], [false]]);
  });

  test('a press outside closes it with focus on the button, and a press inside does not', async () => {
    const wrapper = open();
    const button = buttonOf(wrapper);
    const pointerdown = (target) =>
      target.dispatchEvent(new Event('pointerdown', { bubbles: true }));

    await button.trigger('click');
    pointerdown(itemsOf(wrapper)[1].element);
    await wrapper.vm.$nextTick();
    expect(button.attributes('aria-expanded')).toBe('true');

    pointerdown(document.getElementById('elsewhere'));
    await wrapper.vm.$nextTick();
    expect(button.attributes('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(button.element);
    expect(wrapper.emitted('toggle')).toEqual([[true], [false]]);
  });

  test('an unavailable button keeps focus and does not open, and closes its menu', async () => {
    const wrapper = open({ disabled: true });
    const button = buttonOf(wrapper);

    expect(button.attributes('aria-disabled')).toBe('true');
    await button.trigger('click');
    await button.trigger('keydown', { key: 'ArrowDown' });
    expect(wrapper.find('[role="menu"]').exists()).toBe(false);
    expect(wrapper.emitted('toggle')).toBeUndefined();

    await wrapper.setProps({ disabled: false });
    await button.trigger('click');
    expect(button.attributes('aria-expanded')).toBe('true');

    await wrapper.setProps({ disabled: true });
    expect(button.attributes('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(button.element);
  });

  test('when the focused item goes away, the item now in its place takes focus', async () => {
    const wrapper = open();

    await buttonOf(wrapper).trigger('click');
    await press(wrapper, 'End');
    expect(focusedLabel()).toBe('Delete');

    await wrapper.setProps({ items: ITEMS.slice(0, 3) });
    await wrapper.vm.$nextTick();

    expect(focusedLabel()).toBe('Paste');
  });
});

describe('the menu’s focus, for any keys', () => {
  const KEYS = ['ArrowDown', 'ArrowUp', 'Home', 'End', 'c', 'd', 'p', 'x'];

  // The item each key moves to, worked out apart from the component.
  function expected(index, key, labels) {
    const count = labels.length;

    switch (key) {
      case 'ArrowDown':
        return (index + 1) % count;
      case 'ArrowUp':
        return (index - 1 + count) % count;
      case 'Home':
        return 0;
      case 'End':
        return count - 1;
      default: {
        for (let step = 1; step <= count; step += 1) {
          const at = (index + step) % count;

          if (labels[at].toLowerCase().startsWith(key)) {
            return at;
          }
        }

        return index;
      }
    }
  }

  test('stays on one item, which holds the only Tab stop, where the keys say', async () => {
    await fc.assert(
      fc.asyncProperty(
        fc.array(fc.constantFrom(...KEYS), { maxLength: 12 }),
        async (keys) => {
          const labels = ITEMS.map((item) => item.label);
          const wrapper = open();
          let index = 0;

          try {
            await buttonOf(wrapper).trigger('click');
            for (const key of keys) {
              await press(wrapper, key);
              index = expected(index, key, labels);

              const items = itemsOf(wrapper);

              expect(focusedLabel()).toBe(labels[index]);
              expect(
                items.filter((item) => item.attributes('tabindex') === '0'),
              ).toEqual([items[index]]);
            }
          } finally {
            wrapper.unmount();
            document.body.innerHTML = '';
          }
        },
      ),
      { numRuns: 40 },
    );
  });
});
