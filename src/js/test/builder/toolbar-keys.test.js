// @vitest-environment happy-dom
// The toolbar, mounted: its roving Tab stop and what each button and menu
// runs. Its overflow and widths need layout, so Playwright tests them.

import { reactive, watch } from 'vue';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, mount } from '@vue/test-utils';
import fc from 'fast-check';

import BuilderToolbar from '@/components/builder/BuilderToolbar.vue';
import { loadShortcutSettings, setPlatform } from '@/builder/keymap.js';
import { LAYOUT_ALGORITHMS } from '@/builder/layouts/index.js';

import { memoryStorage } from './fixtures.js';

const held = vi.hoisted(() => ({ store: null }));

vi.mock('@/builder/store.js', () => ({ useBuilderStore: () => held.store }));

enableAutoUnmount(afterEach);

// The store's state and getters the toolbar reads, and its actions as spies.
function fakeStore(overrides = {}) {
  return reactive({
    doc: { nodes: [] },
    selection: { nodes: [], edges: [] },
    readOnly: false,
    canUndo: true,
    canRedo: true,
    canPublish: true,
    canRestoreLayout: false,
    currentLayout: '',
    experimentName: '',
    autoGrouping: false,
    layoutRunning: false,
    mergingConflict: false,
    saveState: { status: 'saved', pending: 0 },
    saveStateText: 'Saved',
    undo: vi.fn(),
    redo: vi.fn(),
    copy: vi.fn(),
    paste: vi.fn(),
    removeSelection: vi.fn(),
    group: vi.fn(),
    ungroup: vi.fn(),
    layout: vi.fn(),
    restoreLayout: vi.fn(),
    autoGroup: vi.fn(),
    retrySave: vi.fn(),
    ...overrides,
  });
}

function open(overrides = {}) {
  held.store = fakeStore(overrides);

  return mount(BuilderToolbar, { attachTo: document.body });
}

beforeEach(() => {
  setPlatform('other');
  loadShortcutSettings(memoryStorage());
});

afterEach(() => {
  setPlatform(null);
  held.store = null;
  document.body.innerHTML = '';
});

const NAMES = [
  'Undo',
  'Redo',
  'Copy',
  'Paste',
  'Delete',
  'Group',
  'Ungroup',
  'Auto-group',
  'Default',
  'Scenarios',
  'Download',
  'Upload',
  'Publish',
  'Minimap',
  'Draft History',
];

const buttonsOf = (wrapper) => wrapper.findAll('[role="toolbar"] button');
const byTestId = (wrapper, id) => wrapper.get(`[data-testid="${id}"]`);
const tabStops = (wrapper) =>
  buttonsOf(wrapper)
    .map((button) => button.element)
    .filter((button) => button.tabIndex === 0);
// A button's name, without the layout names that only hold its width.
const nameOf = (element) =>
  element.querySelector('.builder-button__swap span:not(.is-off)')
    ? element
        .querySelector('.builder-button__swap span:not(.is-off)')
        .textContent.trim()
    : element.textContent.trim();

async function press(key, mods = {}) {
  const event = new KeyboardEvent('keydown', {
    key,
    bubbles: true,
    cancelable: true,
    ...mods,
  });

  document.activeElement.dispatchEvent(event);

  return event;
}

describe('the toolbar, from the keyboard', () => {
  test('holds one Tab stop, which the arrows, Home and End move with focus', async () => {
    const wrapper = open();
    const buttons = buttonsOf(wrapper);

    expect(buttons.map((button) => nameOf(button.element))).toEqual(NAMES);
    expect(tabStops(wrapper)).toEqual([buttons[0].element]);

    buttons[0].element.focus();

    const moves = [];

    for (const key of [
      'ArrowLeft',
      'ArrowLeft',
      'Home',
      'ArrowRight',
      'End',
      'ArrowRight',
    ]) {
      const event = await press(key);

      expect(event.defaultPrevented, key).toBe(true);
      moves.push(nameOf(document.activeElement));
      expect(tabStops(wrapper)).toEqual([document.activeElement]);
    }

    expect(moves).toEqual([
      'Draft History',
      'Minimap',
      'Undo',
      'Redo',
      'Draft History',
      'Undo',
    ]);
  });

  test('a button focused another way, or with a modifier held, keeps the Tab stop where focus is', async () => {
    const wrapper = open();
    const publish = byTestId(wrapper, 'toolbar-publish');

    publish.element.focus();
    expect(tabStops(wrapper)).toEqual([publish.element]);

    for (const mods of [
      { altKey: true },
      { ctrlKey: true },
      { metaKey: true },
    ]) {
      const event = await press('ArrowRight', mods);

      expect(event.defaultPrevented).toBe(false);
      expect(document.activeElement).toBe(publish.element);
    }

    // The status text is no stop of the arrows: from Draft History they wrap.
    byTestId(wrapper, 'toolbar-history').element.focus();
    await press('ArrowRight');
    expect(nameOf(document.activeElement)).toBe('Undo');
  });

  test('a button added later, Retry saving, takes no Tab stop of its own', async () => {
    const wrapper = open();

    held.store.saveState = { status: 'offline', pending: 1 };
    await wrapper.vm.$nextTick();

    const retry = byTestId(wrapper, 'toolbar-retry');

    expect(retry.element.tabIndex).toBe(-1);
    expect(tabStops(wrapper)).toHaveLength(1);

    retry.element.focus();
    await retry.trigger('click');
    expect(held.store.retrySave).toHaveBeenCalledExactlyOnceWith({
      announce: true,
    });
    expect(tabStops(wrapper)).toEqual([retry.element]);

    // A save starts: Retry saving goes, and Draft History takes its focus
    // and the Tab stop.
    held.store.saveState = { status: 'saving', pending: 1 };
    await wrapper.vm.$nextTick();
    await wrapper.vm.$nextTick();

    const history = byTestId(wrapper, 'toolbar-history');

    expect(wrapper.find('[data-testid="toolbar-retry"]').exists()).toBe(false);
    expect(document.activeElement).toBe(history.element);
    expect(tabStops(wrapper)).toEqual([history.element]);
  });

  test('when the update that removes Retry saving moves focus elsewhere, focus stays there', async () => {
    const wrapper = open({ saveState: { status: 'offline', pending: 1 } });
    const panel = document.createElement('button');

    document.body.append(panel);
    byTestId(wrapper, 'toolbar-retry').element.focus();

    // As a conflict panel takes focus in the same update.
    const stop = watch(
      () => held.store.saveState,
      () => panel.focus(),
      { flush: 'post' },
    );

    held.store.saveState = { status: 'conflict', pending: 1 };
    await wrapper.vm.$nextTick();
    await wrapper.vm.$nextTick();
    stop();

    expect(document.activeElement).toBe(panel);
    expect(tabStops(wrapper)).toEqual([
      byTestId(wrapper, 'toolbar-history').element,
    ]);
  });
});

describe('the toolbar’s actions', () => {
  test('each button runs its action, and an unavailable one does nothing', async () => {
    const wrapper = open({ canUndo: false });
    const click = (id) => byTestId(wrapper, `toolbar-${id}`).trigger('click');

    expect(byTestId(wrapper, 'toolbar-undo').attributes('aria-disabled')).toBe(
      'true',
    );
    await click('undo');
    expect(held.store.undo).not.toHaveBeenCalled();

    held.store.canUndo = true;
    await wrapper.vm.$nextTick();
    expect(
      byTestId(wrapper, 'toolbar-undo').attributes('aria-disabled'),
    ).toBeUndefined();
    await click('undo');
    await click('redo');
    await click('copy');
    await click('paste');
    expect(held.store.undo).toHaveBeenCalledOnce();
    expect(held.store.redo).toHaveBeenCalledOnce();
    expect(held.store.copy).toHaveBeenCalledOnce();
    expect(held.store.paste).toHaveBeenCalledOnce();

    // Delete and Group need a selection, and Ungroup a group.
    await click('delete');
    await click('group');
    await click('ungroup');
    expect(held.store.removeSelection).not.toHaveBeenCalled();
    expect(held.store.group).not.toHaveBeenCalled();
    expect(held.store.ungroup).not.toHaveBeenCalled();

    // A device selected: Ungroup still needs a group.
    held.store.doc = { nodes: [{ id: 'd1', kind: 'device' }] };
    held.store.selection = { nodes: ['d1'], edges: [] };
    await wrapper.vm.$nextTick();
    expect(
      byTestId(wrapper, 'toolbar-ungroup').attributes('aria-disabled'),
    ).toBe('true');
    expect(
      byTestId(wrapper, 'toolbar-group').attributes('aria-disabled'),
    ).toBeUndefined();

    held.store.doc = { nodes: [{ id: 'g1', kind: 'group' }] };
    held.store.selection = { nodes: ['g1'], edges: [] };
    await wrapper.vm.$nextTick();
    await click('delete');
    await click('group');
    await click('ungroup');
    expect(held.store.removeSelection).toHaveBeenCalledOnce();
    expect(held.store.group).toHaveBeenCalledOnce();
    expect(held.store.ungroup).toHaveBeenCalledOnce();

    for (const id of [
      'scenario',
      'download',
      'upload',
      'publish',
      'history',
      'minimap',
    ]) {
      await click(id);
    }
    expect(Object.keys(wrapper.emitted())).toEqual(
      expect.arrayContaining([
        'scenario',
        'download',
        'upload',
        'publish',
        'history',
        'toggle-minimap',
      ]),
    );
    expect(
      byTestId(wrapper, 'toolbar-minimap').attributes('aria-pressed'),
    ).toBe('true');
  });

  test('a read-only draft can only be copied, downloaded, looked at, or uploaded as a new draft', async () => {
    const wrapper = open({
      readOnly: true,
      doc: { nodes: [{ id: 'g1', kind: 'group' }] },
      selection: { nodes: ['g1'], edges: [] },
      experimentName: 'plant',
    });
    const click = (id) => byTestId(wrapper, `toolbar-${id}`).trigger('click');

    for (const id of [
      'undo',
      'redo',
      'paste',
      'delete',
      'group',
      'ungroup',
      'scenario',
      'publish',
    ]) {
      await click(id);
    }
    for (const action of [
      'undo',
      'redo',
      'paste',
      'removeSelection',
      'group',
      'ungroup',
    ]) {
      expect(held.store[action], action).not.toHaveBeenCalled();
    }
    expect(byTestId(wrapper, 'toolbar-auto-group').attributes()).toMatchObject({
      'aria-disabled': 'true',
    });
    expect(byTestId(wrapper, 'toolbar-layout').attributes()).toMatchObject({
      'aria-disabled': 'true',
    });

    await click('copy');
    await click('download');
    await click('experiment');
    await click('upload');
    expect(held.store.copy).toHaveBeenCalledOnce();
    for (const event of ['download', 'experiment', 'upload']) {
      expect(wrapper.emitted(event), event).toHaveLength(1);
    }
    expect(wrapper.emitted('scenario')).toBeUndefined();
    expect(wrapper.emitted('publish')).toBeUndefined();
    expect(
      byTestId(wrapper, 'toolbar-experiment').attributes('aria-label'),
    ).toBe('Exp: open experiment plant');
  });

  test('the layout menu lists Network cards first, and opens on it at Default', async () => {
    const wrapper = open({ currentLayout: '' });

    await byTestId(wrapper, 'toolbar-layout').trigger('click');

    const items = wrapper.findAll('[data-testid^="toolbar-layout-item-"]');

    expect(items.map((item) => item.attributes('data-testid'))).toEqual([
      'toolbar-layout-item-cards',
      'toolbar-layout-item-elk',
      'toolbar-layout-item-tiers',
      'toolbar-layout-item-dagre',
      'toolbar-layout-item-standard',
    ]);
    expect(document.activeElement).toBe(items[0].element);
  });

  test('the layout menu runs the chosen layout, or puts the previous one back', async () => {
    const wrapper = open({ currentLayout: 'tiers', canRestoreLayout: true });
    const layout = byTestId(wrapper, 'toolbar-layout');

    expect(layout.attributes('aria-label')).toBe('By layer layout');
    await layout.trigger('click');

    // The draft's layout is checked, and the menu opens on it.
    const checked = wrapper.get('[aria-checked="true"]');

    expect(checked.attributes('data-testid')).toBe('toolbar-layout-item-tiers');
    expect(document.activeElement).toBe(checked.element);
    expect(
      wrapper.findAll('[data-testid^="toolbar-layout-item-"]'),
    ).toHaveLength(LAYOUT_ALGORITHMS.length + 1);

    await byTestId(wrapper, 'toolbar-layout-item-elk').trigger('click');
    expect(held.store.layout).toHaveBeenCalledExactlyOnceWith({
      algorithm: 'elk',
    });
    expect(document.activeElement).toBe(layout.element);

    await layout.trigger('click');
    await byTestId(wrapper, 'toolbar-layout-item-restore').trigger('click');
    expect(held.store.restoreLayout).toHaveBeenCalledOnce();
    expect(held.store.layout).toHaveBeenCalledOnce();
  });

  test('the Auto-group menu groups by its rule, or asks for the name pattern', async () => {
    const wrapper = open();
    const autoGroup = byTestId(wrapper, 'toolbar-auto-group');

    await autoGroup.trigger('click');
    expect(
      wrapper
        .findAll('[data-testid^="toolbar-auto-group-item-"]')
        .map((item) => item.get('.builder-menu__text > span').text()),
    ).toEqual(['By network', 'By name', 'By name pattern…']);

    await byTestId(wrapper, 'toolbar-auto-group-item-name').trigger('click');
    expect(held.store.autoGroup).toHaveBeenCalledExactlyOnceWith('name');
    expect(wrapper.emitted('group-pattern')).toBeUndefined();

    await autoGroup.trigger('click');
    await byTestId(wrapper, 'toolbar-auto-group-item-pattern').trigger('click');
    expect(wrapper.emitted('group-pattern')).toHaveLength(1);
    expect(held.store.autoGroup).toHaveBeenCalledOnce();
  });

  test('a menu’s button shows no tooltip while its menu is open', async () => {
    const wrapper = open();
    const autoGroup = byTestId(wrapper, 'toolbar-auto-group');
    const tooltip = () => wrapper.find('[data-testid="toolbar-tooltip"]');

    // happy-dom has no layout: the button gets a box, so that its tooltip
    // is in view.
    autoGroup.element.getBoundingClientRect = () => new DOMRect(0, 0, 80, 24);

    await autoGroup.trigger('focus');
    expect(tooltip().text()).toBe(
      'Group the ungrouped nodes automatically. Alt+Shift+G groups by network',
    );
    await autoGroup.trigger('blur');
    expect(tooltip().exists()).toBe(false);

    await autoGroup.trigger('click');
    await autoGroup.trigger('focus');
    expect(tooltip().exists()).toBe(false);

    await wrapper.get('[role="menu"]').trigger('keydown', { key: 'Escape' });
    await autoGroup.trigger('focus');
    expect(tooltip().exists()).toBe(true);
  });
});

describe('the toolbar’s Tab stop, for any keys', () => {
  const KEYS = ['ArrowRight', 'ArrowLeft', 'Home', 'End'];

  test('is on the focused button, the one the keys lead to', async () => {
    const wrapper = open();
    const count = NAMES.length;

    await fc.assert(
      fc.asyncProperty(
        fc.nat({ max: count - 1 }),
        fc.array(fc.constantFrom(...KEYS), { maxLength: 20, size: 'max' }),
        async (start, keys) => {
          let index = start;

          buttonsOf(wrapper)[start].element.focus();
          for (const key of keys) {
            await press(key);
            index = {
              ArrowRight: (index + 1) % count,
              ArrowLeft: (index - 1 + count) % count,
              Home: 0,
              End: count - 1,
            }[key];
          }

          const buttons = buttonsOf(wrapper);

          expect(document.activeElement).toBe(buttons[index].element);
          expect(tabStops(wrapper)).toEqual([buttons[index].element]);
        },
      ),
      { numRuns: 60 },
    );
  });
});
