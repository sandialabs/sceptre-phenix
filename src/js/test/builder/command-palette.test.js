// @vitest-environment happy-dom
// The command palette, opened as Builder opens it and driven from the
// keyboard: filtering, the highlight, steps, running, and focus after it.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

import BuilderCommandPalette from '@/components/builder/BuilderCommandPalette.vue';
import {
  loadShortcutSettings,
  setPlatform,
  setShortcut,
} from '@/builder/keymap.js';
import { RECENT_STORAGE_KEY } from '@/builder/recent.js';

import { context } from './command-context.js';
import { memoryStorage, sampleDocument } from './fixtures.js';

enableAutoUnmount(afterEach);

beforeEach(() => {
  setPlatform('other');
  loadShortcutSettings(memoryStorage());
  localStorage.clear();
});

afterEach(() => {
  setPlatform(null);
  loadShortcutSettings(memoryStorage());
  localStorage.clear();
  document.body.innerHTML = '';
});

// Opens the palette as Builder does: from a focused control, on the
// command context, with what to show first.
function open({ store = {}, view = {}, request, onClose } = {}) {
  const sample = sampleDocument();
  const ctx = context({ store: { doc: sample.doc, ...store }, view });
  const opener = document.createElement('button');

  opener.textContent = 'Commands';
  document.body.append(opener);
  opener.focus();

  const wrapper = mount(BuilderCommandPalette, {
    attachTo: document.body,
    props: { context: ctx, ...(request ? { request } : {}), onClose },
  });

  return { wrapper, ctx, opener, sample, field: wrapper.get('input') };
}

const titles = (wrapper) =>
  wrapper
    .findAll('[role="option"]')
    .map((option) => option.get('.builder-command-palette__title').text());

// The highlighted option's title, after checking that it is the field's
// active descendant and the only selected option.
function highlighted(wrapper) {
  const selected = wrapper.findAll('[role="option"][aria-selected="true"]');

  expect(selected).toHaveLength(1);
  expect(wrapper.get('input').attributes('aria-activedescendant')).toBe(
    selected[0].attributes('id'),
  );

  return selected[0].get('.builder-command-palette__title').text();
}

const said = (wrapper) =>
  wrapper.get('[data-testid="command-palette-message"]').text();

async function press(field, key, mods = {}) {
  await field.trigger('keydown', { key, ...mods });
}

describe('the command palette', () => {
  test('opens with focus in its search field, on the first command, by group', async () => {
    const { wrapper, field } = open({ store: { canUndo: true } });

    expect(document.activeElement).toBe(field.element);
    expect(field.attributes()).toMatchObject({
      role: 'combobox',
      'aria-expanded': 'true',
      'aria-controls': 'command-palette-list',
    });
    expect(wrapper.get('[role="listbox"]').attributes('aria-label')).toBe(
      'Commands',
    );
    expect(
      wrapper
        .findAll('.builder-command-palette__group')
        .map((group) => group.text()),
    ).toEqual([
      'General',
      'Edit',
      'Selection',
      'Structure',
      'Add',
      'Go to',
      'View',
      'Draft',
    ]);
    expect(highlighted(wrapper)).toBe('Keyboard shortcuts');

    // The count waits for typing to pause.
    await vi.waitFor(() =>
      expect(
        wrapper.get('[data-testid="command-palette-count"]').text(),
      ).toMatch(/^\d+ results$/),
    );
  });

  test('typing filters the options, and the highlight goes back to the first', async () => {
    const { wrapper, field } = open();

    await field.setValue('zoom');
    expect(titles(wrapper).slice(0, 2)).toEqual(['Zoom in', 'Zoom out']);
    await press(field, 'ArrowDown');
    expect(highlighted(wrapper)).toBe('Zoom out');

    // Zoom out still matches, and the highlight leaves it all the same.
    await field.setValue('zoo');
    expect(titles(wrapper).slice(0, 2)).toEqual(['Zoom in', 'Zoom out']);
    expect(highlighted(wrapper)).toBe('Zoom in');

    await field.setValue('@qqzzxx');
    expect(titles(wrapper)).toEqual([]);
    expect(field.attributes('aria-expanded')).toBe('false');
    expect(field.attributes('aria-activedescendant')).toBeUndefined();
    expect(
      wrapper.get('[data-testid="command-palette-list"]').attributes('hidden'),
    ).toBeDefined();
    expect(wrapper.find('[data-testid="command-palette-empty"]').exists()).toBe(
      true,
    );
    await vi.waitFor(() =>
      expect(wrapper.get('[data-testid="command-palette-count"]').text()).toBe(
        'No results',
      ),
    );
  });

  test('Up and Down move the highlight and wrap, and Page Up and Page Down move between groups', async () => {
    const { wrapper, field } = open({ store: { canUndo: true } });
    const all = titles(wrapper);
    const moves = [];

    for (const key of [
      'ArrowDown',
      'ArrowUp',
      'ArrowUp',
      'ArrowDown',
      'PageDown',
      'PageDown',
      'PageUp',
      'PageUp',
      'PageUp',
    ]) {
      await press(field, key);
      moves.push(highlighted(wrapper));
    }

    expect(moves).toEqual([
      'Settings…',
      'Keyboard shortcuts',
      all.at(-1),
      'Keyboard shortcuts',
      'Undo',
      'Select all',
      'Undo',
      'Keyboard shortcuts',
      'Save now',
    ]);

    // Home and End move the caret in the field, and modifiers leave the
    // arrows to others.
    for (const [key, mods] of [
      ['Home', {}],
      ['End', {}],
      ['ArrowDown', { altKey: true }],
      ['ArrowDown', { ctrlKey: true }],
    ]) {
      await press(field, key, mods);
      expect(highlighted(wrapper), key).toBe('Save now');
    }
  });

  test('Enter closes the palette, then runs the highlighted command and remembers it', async () => {
    const ranBeforeClose = [];
    const { wrapper, ctx, field, opener } = open({
      store: { canUndo: true },
      onClose: () => ranBeforeClose.push(ctx.store.undo.mock.calls.length),
    });

    await field.setValue('undo');
    expect(highlighted(wrapper)).toBe('Undo');
    await press(field, 'Enter');
    await flushPromises();

    expect(wrapper.emitted('close')).toHaveLength(1);
    expect(ranBeforeClose).toEqual([0]);
    expect(ctx.store.undo).toHaveBeenCalledOnce();
    expect(JSON.parse(localStorage.getItem(RECENT_STORAGE_KEY))).toEqual([
      { id: 'edit.undo', choices: [] },
    ]);

    // Builder removes it, and the dialog gives focus back.
    wrapper.unmount();
    expect(document.activeElement).toBe(opener);
    expect(ctx.view.focusCanvas).not.toHaveBeenCalled();
  });

  test('a click on an option runs it too', async () => {
    const { wrapper, ctx, field } = open();

    await field.setValue('zoom in');
    await wrapper.get('[role="option"]').trigger('click');
    await flushPromises();

    expect(wrapper.emitted('close')).toHaveLength(1);
    expect(ctx.view.zoomIn).toHaveBeenCalledOnce();
  });

  test('an unavailable command stays in the palette, which says why', async () => {
    const { wrapper, ctx, field } = open();

    await field.setValue('undo');
    await press(field, 'Enter');

    expect(said(wrapper)).toBe('Undo is unavailable. Nothing to undo.');
    expect(wrapper.get('[role="option"]').attributes('aria-disabled')).toBe(
      'true',
    );
    expect(wrapper.emitted('close')).toBeUndefined();
    await flushPromises();
    expect(ctx.store.undo).not.toHaveBeenCalled();

    // Typing again clears the message.
    await field.setValue('undo ');
    expect(said(wrapper)).toBe('');
  });

  test('a command with steps asks for each choice, and Backspace in the empty field goes back', async () => {
    const { wrapper, ctx, field, sample } = open();
    const chip = () => wrapper.find('[data-testid="command-palette-step"]');
    const label = () => wrapper.get('label[for="command-palette-input"]');

    await field.setValue('connect');
    await press(field, 'Enter');

    expect(chip().text()).toBe('Connect device to a switch ›');
    expect(label().text()).toBe('Connect device to a switch: choose a device');
    expect(field.element.value).toBe('');
    expect(field.attributes('placeholder')).toBe('Device');
    expect(wrapper.get('[role="listbox"]').attributes('aria-label')).toBe(
      'Device choices',
    );
    expect(titles(wrapper)).toEqual(['alpha', 'bravo']);

    await field.setValue('bra');
    await press(field, 'Enter');
    expect(chip().text()).toBe('Connect device to a switch › bravo ›');
    expect(label().text()).toBe(
      'Connect device to a switch › bravo: choose a switch',
    );
    expect(titles(wrapper)).toEqual(['EXP']);

    // Backspace goes back a step with what was typed before it.
    await press(field, 'Backspace');
    expect(chip().text()).toBe('Connect device to a switch ›');
    expect(field.element.value).toBe('bra');

    // In a field with text, Backspace is the field's.
    await press(field, 'Backspace');
    expect(chip().text()).toBe('Connect device to a switch ›');

    await field.setValue('');
    await press(field, 'Backspace');
    expect(chip().exists()).toBe(false);
    expect(field.element.value).toBe('connect');
    expect(highlighted(wrapper)).toBe('Connect device to a switch');

    // All the way through: the command runs with both choices.
    await press(field, 'Enter');
    await press(field, 'Enter');
    await press(field, 'Enter');
    await flushPromises();
    expect(wrapper.emitted('close')).toHaveLength(1);
    expect(ctx.store.connect).toHaveBeenCalledExactlyOnceWith({
      sourceNodeId: sample.alpha.id,
      sourceHandleId: null,
      targetNodeId: sample.sw.id,
    });
  });

  test('opened on a command it starts at its choices, and on @ it lists the nodes', async () => {
    const step = open({ request: { command: 'structure.connect' } });

    await flushPromises();
    expect(
      step.wrapper.get('[data-testid="command-palette-step"]').text(),
    ).toBe('Connect device to a switch ›');
    expect(titles(step.wrapper)).toEqual(['alpha', 'bravo']);
    // Backspace leaves the step for the commands.
    await press(step.field, 'Backspace');
    expect(step.wrapper.get('[role="listbox"]').attributes('aria-label')).toBe(
      'Commands',
    );
    step.wrapper.unmount();

    const nodes = open({ request: { query: '@' } });

    await flushPromises();
    expect(nodes.field.element.value).toBe('@');
    expect(nodes.wrapper.get('[role="listbox"]').attributes('aria-label')).toBe(
      'Nodes',
    );
    expect(titles(nodes.wrapper)).toEqual(['EXP', 'alpha', 'bravo']);
  });

  test('Shift+Enter adds a node to the selection and stays open, and the selection is summed up as it closes', async () => {
    const { wrapper, ctx, field, sample } = open({
      request: { query: '@' },
    });

    await press(field, 'ArrowDown');
    await press(field, 'Enter', { shiftKey: true });

    expect(ctx.store.select).toHaveBeenCalledExactlyOnceWith({
      nodes: [sample.alpha.id],
      edges: [],
    });
    expect(said(wrapper)).toBe(
      'Added alpha to the selection, 1 item selected.',
    );
    expect(wrapper.emitted('close')).toBeUndefined();
    expect(ctx.store.announce).not.toHaveBeenCalled();

    await press(field, 'ArrowDown');
    await press(field, 'Enter', { shiftKey: true });
    expect(ctx.store.selection.nodes).toEqual([
      sample.alpha.id,
      sample.bravo.id,
    ]);
    expect(said(wrapper)).toBe(
      'Added bravo to the selection, 2 items selected.',
    );

    await press(field, 'Escape');
    expect(wrapper.emitted('close')).toHaveLength(1);
    wrapper.unmount();
    expect(ctx.store.announce).toHaveBeenCalledExactlyOnceWith(
      'Selected alpha and bravo.',
    );
  });

  test('Escape closes it, and focus goes back to the opener, or to the canvas when the opener is gone', async () => {
    const first = open();

    await press(first.field, 'Escape');
    expect(first.wrapper.emitted('close')).toHaveLength(1);
    first.wrapper.unmount();
    expect(document.activeElement).toBe(first.opener);

    const second = open();

    second.opener.remove();
    await press(second.field, 'Escape');
    second.wrapper.unmount();
    expect(second.ctx.view.focusCanvas).toHaveBeenCalledOnce();
  });

  test('on the drafts landing, focus goes to the shown tab when the opener is gone', async () => {
    document.body.innerHTML = `
      <button class="builder-tab" aria-selected="false">Drafts</button>
      <button class="builder-tab" aria-selected="true">Templates</button>`;

    const { wrapper, field, opener, ctx } = open({ view: { editing: false } });

    expect(field.attributes('placeholder')).toBe(
      'Type a command or a draft name',
    );
    opener.remove();
    await press(field, 'Escape');
    wrapper.unmount();

    expect(document.activeElement.textContent).toBe('Templates');
    expect(ctx.view.focusCanvas).not.toHaveBeenCalled();
  });

  test('the palette’s own key closes it from the field, but not a key that types there', async () => {
    const { wrapper, field } = open();
    const keydown = (key, mods = {}) => {
      const event = new KeyboardEvent('keydown', {
        key,
        bubbles: true,
        cancelable: true,
        ...mods,
      });

      field.element.dispatchEvent(event);

      return event;
    };

    // As if the key had been given before the recorder refused it.
    setShortcut('palette.open', ['Mod+K', '/'], memoryStorage());

    expect(keydown('/').defaultPrevented).toBe(false);
    expect(
      keydown('k', { ctrlKey: true, isComposing: true }).defaultPrevented,
    ).toBe(false);
    expect(wrapper.emitted('close')).toBeUndefined();

    expect(keydown('k', { ctrlKey: true }).defaultPrevented).toBe(true);
    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  test('Enter during composition chooses nothing', async () => {
    const { wrapper, ctx, field } = open({ store: { canUndo: true } });

    await field.setValue('undo');
    await press(field, 'Enter', { isComposing: true });
    await flushPromises();

    expect(wrapper.emitted('close')).toBeUndefined();
    expect(ctx.store.undo).not.toHaveBeenCalled();
  });
});
