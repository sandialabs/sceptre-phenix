// @vitest-environment happy-dom
// The shortcut sheet, mounted: its key recorder, what it says of a key, and
// how keeping, moving, removing and resetting keys change the keymap.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

import BuilderShortcuts from '@/components/builder/BuilderShortcuts.vue';
import { commandKeys } from '@/builder/commands.js';
import {
  keymapState,
  loadShortcutSettings,
  setPlatform,
} from '@/builder/keymap.js';

import { memoryStorage } from './fixtures.js';

enableAutoUnmount(afterEach);

beforeEach(() => {
  setPlatform('other');
  localStorage.clear();
  loadShortcutSettings(memoryStorage());
});

afterEach(() => {
  setPlatform(null);
  localStorage.clear();
  loadShortcutSettings(memoryStorage());
  document.body.innerHTML = '';
});

function open(props = {}) {
  return mount(BuilderShortcuts, { attachTo: document.body, props });
}

// The sheet in its customization, as Change shortcuts opens it.
async function customizing() {
  const wrapper = open();

  await wrapper.get('[data-testid="shortcuts-customize"]').trigger('click');

  return wrapper;
}

const rowOf = (wrapper, id) => wrapper.get(`[data-testid="shortcut-${id}"]`);
const keysOf = (wrapper, id) =>
  rowOf(wrapper, id)
    .findAll('.builder-shortcuts__combo .builder-visually-hidden')
    .map((key) => key.text());
const status = (wrapper) =>
  wrapper.get('[data-testid="shortcuts-status"]').text();
const recorder = (wrapper) =>
  wrapper.find('[data-testid="shortcut-recorder-input"]');
const recorderSays = (wrapper) =>
  wrapper.get('[data-testid="shortcut-recorder-message"]');

// Selects the row's Change, as the user does, to start recording.
async function change(wrapper, id) {
  await rowOf(wrapper, id)
    .get('[data-testid="shortcut-change"]')
    .trigger('click');
  await flushPromises();
}

async function record(wrapper, key, code, mods = {}) {
  await recorder(wrapper).trigger('keydown', { key, code, ...mods });
}

describe('the shortcut sheet', () => {
  test('opens with focus in its filter, and the customization opens and closes in place', async () => {
    const wrapper = open();
    const dialog = wrapper.get('dialog');

    expect(document.activeElement).toBe(
      wrapper.get('[data-testid="shortcuts-filter"]').element,
    );
    expect(wrapper.get('h2').text()).toBe('Keyboard shortcuts');
    expect(wrapper.get('.builder-shortcuts__intro').text()).toBe(
      'Keys for Windows and Linux, and where each one works.',
    );
    expect(wrapper.find('[data-testid="shortcut-change"]').exists()).toBe(
      false,
    );

    await wrapper.get('[data-testid="shortcuts-customize"]').trigger('click');
    await flushPromises();
    expect(wrapper.get('h2').text()).toBe('Change keyboard shortcuts');
    expect(document.activeElement).toBe(dialog.element);
    expect(
      wrapper.findAll('[data-testid="shortcut-change"]').length,
    ).toBeGreaterThan(0);
    expect(
      wrapper
        .get('[data-testid="shortcuts-reset-all"]')
        .attributes('aria-disabled'),
    ).toBe('true');

    await wrapper.get('[data-testid="shortcuts-done"]').trigger('click');
    await flushPromises();
    expect(wrapper.get('h2').text()).toBe('Keyboard shortcuts');
    expect(document.activeElement).toBe(
      wrapper.get('[data-testid="shortcuts-customize"]').element,
    );
    expect(wrapper.emitted('close')).toBeUndefined();
  });

  test('opened from Settings on the customization, Done closes it', async () => {
    const wrapper = open({ customize: true });

    expect(wrapper.get('h2').text()).toBe('Change keyboard shortcuts');
    await wrapper.get('[data-testid="shortcuts-done"]').trigger('click');
    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  test('the filter narrows the rows, and their count is said once typing pauses', async () => {
    const wrapper = open();
    const filter = wrapper.get('[data-testid="shortcuts-filter"]');

    await filter.setValue('undo');
    expect(
      wrapper.findAll('.builder-shortcuts__title').map((title) => title.text()),
    ).toEqual(['Undo']);
    await vi.waitFor(() => expect(status(wrapper)).toBe('1 shortcut'));

    await filter.setValue('qqzz');
    expect(wrapper.get('[data-testid="shortcuts-empty"]').text()).toBe(
      'Nothing matches “qqzz”.',
    );
    await vi.waitFor(() => expect(status(wrapper)).toBe('Nothing matches.'));
  });

  test('the single-key switch turns the one-key shortcuts off and on', async () => {
    const wrapper = open();
    const toggle = wrapper.get('[data-testid="shortcuts-single-keys"]');

    expect(toggle.attributes('aria-checked')).toBe('true');
    await toggle.trigger('click');
    expect(keymapState.singleKeys).toBe(false);
    expect(toggle.attributes('aria-checked')).toBe('false');
    await toggle.trigger('click');
    expect(keymapState.singleKeys).toBe(true);
  });
});

describe('the key recorder', () => {
  test('records a free key, and Enter keeps it with focus back on Change', async () => {
    const wrapper = await customizing();

    expect(keysOf(wrapper, 'view.snapToGrid')).toEqual([]);
    await change(wrapper, 'view.snapToGrid');
    expect(document.activeElement).toBe(recorder(wrapper).element);
    expect(
      rowOf(wrapper, 'view.snapToGrid')
        .find('[data-testid="shortcut-change"]')
        .exists(),
    ).toBe(false);

    await record(wrapper, 'J', 'KeyJ', { altKey: true, shiftKey: true });
    expect(recorder(wrapper).element.value).toBe('Alt+Shift+J');
    expect(recorder(wrapper).attributes('aria-invalid')).toBeUndefined();
    expect(recorderSays(wrapper).attributes('data-tone')).toBe('info');
    expect(recorderSays(wrapper).text()).toContain(
      'is free. Press Enter to keep it, or Escape to cancel.',
    );
    expect(wrapper.find('[data-testid="shortcut-keep"]').exists()).toBe(true);

    await record(wrapper, 'Enter', 'Enter');
    await flushPromises();

    expect(status(wrapper)).toBe('Snap to grid now uses Alt+Shift+J.');
    expect(commandKeys('view.snapToGrid')).toEqual(['Alt+Shift+J']);
    expect(keysOf(wrapper, 'view.snapToGrid')).toEqual(['Alt+Shift+J']);
    expect(recorder(wrapper).exists()).toBe(false);
    expect(document.activeElement).toBe(
      rowOf(wrapper, 'view.snapToGrid').get('[data-testid="shortcut-change"]')
        .element,
    );
  });

  test('a key another command has is said as a conflict, and Use for moves it', async () => {
    const wrapper = await customizing();

    await change(wrapper, 'view.snapToGrid');
    await record(wrapper, 'g', 'KeyG', { ctrlKey: true });

    expect(recorderSays(wrapper).attributes('data-tone')).toBe('warning');
    expect(recorderSays(wrapper).text()).toContain(
      'already runs Group selection. Select Use for Snap to grid to move it, or press another shortcut.',
    );
    expect(wrapper.find('[data-testid="shortcut-keep"]').exists()).toBe(false);

    // Enter does not take the key from another command.
    await record(wrapper, 'Enter', 'Enter');
    expect(commandKeys('structure.group')).toEqual(['Mod+G']);
    expect(recorder(wrapper).exists()).toBe(true);

    await wrapper.get('[data-testid="shortcut-take"]').trigger('click');
    await flushPromises();

    expect(status(wrapper)).toBe(
      'Snap to grid now uses Ctrl+G. Group selection has no shortcut now.',
    );
    expect(commandKeys('view.snapToGrid')).toEqual(['Mod+G']);
    expect(commandKeys('structure.group')).toEqual([]);
    expect(keysOf(wrapper, 'structure.group')).toEqual([]);
    expect(
      rowOf(wrapper, 'structure.group')
        .get('[data-testid="shortcut-keys"]')
        .text(),
    ).toBe('No shortcut');
  });

  test('a key the browser keeps is refused, and nothing can keep it', async () => {
    const wrapper = await customizing();

    await change(wrapper, 'view.snapToGrid');
    await record(wrapper, 't', 'KeyT', { ctrlKey: true });

    expect(recorder(wrapper).attributes('aria-invalid')).toBe('true');
    expect(recorderSays(wrapper).attributes('data-tone')).toBe('error');
    expect(recorderSays(wrapper).text()).toContain('Press another shortcut.');
    expect(wrapper.find('[data-testid="shortcut-keep"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="shortcut-take"]').exists()).toBe(false);

    await record(wrapper, 'Enter', 'Enter');
    expect(commandKeys('view.snapToGrid')).toEqual([]);
  });

  test('Enter before any key, Backspace and typed text leave nothing recorded', async () => {
    const wrapper = await customizing();

    await change(wrapper, 'view.snapToGrid');
    await record(wrapper, 'Enter', 'Enter');
    expect(status(wrapper)).toBe(
      'Press the new shortcut for Snap to grid first, or Escape to cancel.',
    );

    await record(wrapper, 'J', 'KeyJ', { altKey: true, shiftKey: true });
    await record(wrapper, 'Backspace', 'Backspace');
    expect(status(wrapper)).toBe('Cleared. Press the new shortcut.');
    expect(recorder(wrapper).element.value).toBe('');

    // Text that got in anyway, as composition can put it there.
    await record(wrapper, 'J', 'KeyJ', { altKey: true, shiftKey: true });
    recorder(wrapper).element.value = 'é';
    await recorder(wrapper).trigger('input');
    expect(recorder(wrapper).element.value).toBe('Alt+Shift+J');

    // Tab leaves the recorder as it leaves any field.
    const tab = new KeyboardEvent('keydown', {
      key: 'Tab',
      bubbles: true,
      cancelable: true,
    });

    recorder(wrapper).element.dispatchEvent(tab);
    expect(tab.defaultPrevented).toBe(false);
    expect(commandKeys('view.snapToGrid')).toEqual([]);
  });

  test('Escape, in the recorder or on its buttons, and Cancel stop recording with focus back on Change', async () => {
    const wrapper = await customizing();
    const changeButton = () =>
      rowOf(wrapper, 'view.snapToGrid').get('[data-testid="shortcut-change"]')
        .element;

    for (const stop of ['recorder', 'button', 'cancel']) {
      await change(wrapper, 'view.snapToGrid');
      await record(wrapper, 'J', 'KeyJ', { altKey: true, shiftKey: true });

      if (stop === 'recorder') {
        await record(wrapper, 'Escape', 'Escape');
      } else if (stop === 'button') {
        await wrapper
          .get('[data-testid="shortcut-keep"]')
          .trigger('keydown', { key: 'Escape' });
      } else {
        await wrapper.get('[data-testid="shortcut-cancel"]').trigger('click');
      }
      await flushPromises();

      expect(recorder(wrapper).exists(), stop).toBe(false);
      expect(status(wrapper), stop).toBe(
        'Canceled. Snap to grid keeps no shortcut.',
      );
      expect(document.activeElement, stop).toBe(changeButton());
    }
    expect(commandKeys('view.snapToGrid')).toEqual([]);
    // Escape in the row went to the recorder, not to the dialog.
    expect(wrapper.emitted('close')).toBeUndefined();
  });

  test('Change and Remove keep their columns: an empty place holds a missing Remove, and a missing Reset once a shortcut changed', async () => {
    const wrapper = await customizing();
    const spacers = (id) =>
      rowOf(wrapper, id)
        .findAll('.builder-shortcuts__spacer')
        .map((spacer) => [spacer.text(), spacer.attributes('aria-hidden')]);

    expect(spacers('structure.group')).toEqual([]);
    expect(spacers('edit.undo')).toEqual([]);

    await rowOf(wrapper, 'structure.group')
      .get('[data-testid="shortcut-remove"]')
      .trigger('click');
    await flushPromises();

    // Group selection has Reset now and no Remove. Undo has no Reset.
    expect(spacers('structure.group')).toEqual([['Remove', 'true']]);
    expect(spacers('edit.undo')).toEqual([['Reset', 'true']]);
  });

  test('Remove and Reset change the keys, and Reset all puts every one back', async () => {
    const wrapper = await customizing();
    const row = () => rowOf(wrapper, 'structure.group');
    const changeButton = () =>
      row().get('[data-testid="shortcut-change"]').element;

    expect(keysOf(wrapper, 'structure.group')).toEqual(['Ctrl+G']);
    expect(row().find('[data-testid="shortcut-reset"]').exists()).toBe(false);

    await row().get('[data-testid="shortcut-remove"]').trigger('click');
    await flushPromises();
    expect(status(wrapper)).toBe('Group selection has no shortcut now.');
    expect(commandKeys('structure.group')).toEqual([]);
    expect(keysOf(wrapper, 'structure.group')).toEqual([]);
    expect(row().text()).toContain('Default: Ctrl+G');
    expect(row().find('[data-testid="shortcut-remove"]').exists()).toBe(false);
    expect(document.activeElement).toBe(changeButton());

    await row().get('[data-testid="shortcut-reset"]').trigger('click');
    await flushPromises();
    expect(status(wrapper)).toBe('Group selection uses Ctrl+G again.');
    expect(commandKeys('structure.group')).toEqual(['Mod+G']);
    expect(keysOf(wrapper, 'structure.group')).toEqual(['Ctrl+G']);
    expect(document.activeElement).toBe(changeButton());

    const resetAll = wrapper.get('[data-testid="shortcuts-reset-all"]');

    await resetAll.trigger('click');
    expect(status(wrapper)).toBe('Every shortcut is at its default already.');

    await row().get('[data-testid="shortcut-remove"]').trigger('click');
    await change(wrapper, 'view.snapToGrid');
    await record(wrapper, 'J', 'KeyJ', { altKey: true, shiftKey: true });
    await record(wrapper, 'Enter', 'Enter');
    await wrapper.get('[data-testid="shortcuts-single-keys"]').trigger('click');
    expect(resetAll.attributes('aria-disabled')).toBeUndefined();

    await resetAll.trigger('click');
    expect(status(wrapper)).toBe(
      'Every shortcut is back to its default. Single-key shortcuts are still off.',
    );
    expect(commandKeys('structure.group')).toEqual(['Mod+G']);
    expect(commandKeys('view.snapToGrid')).toEqual([]);
    expect(keymapState.overrides).toEqual({});
    expect(resetAll.attributes('aria-disabled')).toBe('true');
  });
});
