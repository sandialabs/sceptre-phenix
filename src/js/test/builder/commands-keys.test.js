// @vitest-environment happy-dom
// The key dispatcher, focus scopes and focus after a command, on a real page:
// each press is a KeyboardEvent sent to an element of the Builder's markup.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import fc from 'fast-check';

import {
  READ_ONLY,
  commandKeys,
  defaultKeys,
  deleteSelection,
  dispatchKeydown,
  focusLost,
  focusScope,
  isCustomizable,
  isTextEntry,
  runCommand,
  shortcutConflicts,
  takesLetters,
  ungroupSelection,
  worksInTextFields,
} from '@/builder/commands.js';
import {
  eventToKey,
  keyRefusal,
  loadShortcutSettings,
  setPlatform,
  setShortcut,
  setSingleKeyShortcuts,
  typesCharacter,
} from '@/builder/keymap.js';
import { nextPosition, paletteNode } from '@/components/builder/paletteDnd.js';

import { PLATFORMS, context } from './command-context.js';
import { memoryStorage, sampleDocument } from './fixtures.js';

// The app's header and a modal around the Builder's editor: a toolbar
// button, the canvas, the Inspector's fields and a closed dialog.
const PAGE = `
  <header>
    <a href="#drafts" data-testid="outside-link">Drafts</a>
    <input data-testid="outside-field" />
  </header>
  <div class="modal" role="dialog">
    <button type="button" data-testid="outside-modal">OK</button>
  </div>
  <main class="builder-root builder-root--editing">
    <button type="button" data-testid="button">Undo</button>
    <p data-testid="text">selected words</p>
    <section id="builder-canvas" class="builder-canvas" tabindex="0">
      <div class="vue-flow__node" data-id="n1" tabindex="0">alpha</div>
      <button type="button" data-testid="zoom-in">Zoom in</button>
    </section>
    <aside class="builder-inspector">
      <h2 id="inspector-title" tabindex="-1">Inspector</h2>
      <input data-testid="field" />
      <input type="checkbox" data-testid="checkbox" />
      <input readonly data-testid="read-only" />
    </aside>
    <dialog><button type="button" data-testid="in-dialog">Close</button></dialog>
  </main>`;

const byTestId = (id) => document.querySelector(`[data-testid="${id}"]`);
const root = () => document.querySelector('.builder-root');
const canvas = () => document.getElementById('builder-canvas');

function targets() {
  return {
    body: document.body,
    button: byTestId('button'),
    field: byTestId('field'),
    checkbox: byTestId('checkbox'),
    readOnlyField: byTestId('read-only'),
    canvas: canvas(),
    node: document.querySelector('.vue-flow__node'),
    zoomButton: byTestId('zoom-in'),
    inDialog: byTestId('in-dialog'),
    // Outside the Builder: the app header's link, a field and a modal.
    outside: byTestId('outside-link'),
    outsideField: byTestId('outside-field'),
    outsideModal: byTestId('outside-modal'),
  };
}

// A node of the canvas, as Vue Flow renders one.
function nodeOnCanvas(id) {
  const node = document.createElement('div');

  node.className = 'vue-flow__node';
  node.dataset.id = id;
  node.tabIndex = 0;
  canvas().prepend(node);

  return node;
}

const pressed = new WeakMap();

// A key press on `target`, sent by dispatch.
function keydown(target, key, code, mods = {}) {
  const event = new KeyboardEvent('keydown', {
    key,
    code,
    bubbles: true,
    cancelable: true,
    ...mods,
  });

  pressed.set(event, target);

  return event;
}

// Sends the press to its element, and the window's handler runs the
// dispatcher, as the Builder binds it.
function dispatch(event, ctx) {
  let command = null;
  const handler = (sent) => {
    command = dispatchKeydown(sent, ctx, { root: root() });
  };

  window.addEventListener('keydown', handler);
  pressed.get(event).dispatchEvent(event);
  window.removeEventListener('keydown', handler);

  return command?.id || null;
}

// The platform's command key.
function mod(platform) {
  return platform === 'mac' ? { metaKey: true } : { ctrlKey: true };
}

beforeEach(() => {
  document.body.innerHTML = PAGE;
  loadShortcutSettings(memoryStorage());
});

afterEach(() => {
  setPlatform(null);
  loadShortcutSettings(memoryStorage());
  window.getSelection()?.removeAllRanges();
  document.body.innerHTML = '';
});

describe('isTextEntry', () => {
  const input = (type, extra = {}) =>
    Object.assign(document.createElement('input'), { type, ...extra });

  test('text inputs and text areas hold typed text', () => {
    for (const type of ['text', 'number', 'search', 'email', 'url']) {
      expect(isTextEntry(input(type)), type).toBe(true);
    }
    expect(isTextEntry(document.createElement('textarea'))).toBe(true);
  });

  test('choices, buttons, files and colors do not, nor read-only fields', () => {
    for (const type of [
      'checkbox',
      'radio',
      'button',
      'submit',
      'reset',
      'file',
      'color',
    ]) {
      expect(isTextEntry(input(type)), type).toBe(false);
    }
    expect(isTextEntry(input('text', { readOnly: true }))).toBe(false);
    expect(isTextEntry(targets().readOnlyField)).toBe(false);
    expect(isTextEntry(document.createElement('select'))).toBe(false);
    expect(isTextEntry(document.createElement('button'))).toBe(false);
    expect(isTextEntry(null)).toBe(false);
  });
});

describe('focus scopes', () => {
  test('name where a key was pressed', () => {
    const t = targets();
    const scope = (target, editing = true) =>
      focusScope(target, { root: root(), editing });

    // The rest of the page, such as the header link that led here, but not
    // its text fields or dialogs.
    expect(scope(t.outside)).toBe('page');
    expect(scope(t.outsideField)).toBe('outside');
    expect(scope(t.outsideModal)).toBe('outside');
    expect(scope(null)).toBe('outside');
    expect(scope(t.inDialog)).toBe('dialog');
    expect(scope(t.field)).toBe('field');
    expect(scope(t.readOnlyField)).toBe('field');
    expect(scope(t.canvas)).toBe('canvas');
    expect(scope(t.node)).toBe('canvas');
    // The canvas's own controls are not the canvas.
    expect(scope(t.zoomButton)).toBe('editor');
    expect(scope(t.button)).toBe('editor');
    // Focus that fell to the page.
    expect(scope(t.body)).toBe('editor');
    expect(scope(t.button, false)).toBe('landing');
    expect(scope(t.field, false)).toBe('field');
    // A checkbox of the drafts landing selects a card and takes no typed
    // text. One in the editor is a field of the Inspector's form.
    expect(scope(t.checkbox, false)).toBe('landing');
    expect(scope(t.checkbox)).toBe('field');
  });

  test('an open dialog keeps every key', () => {
    const t = targets();

    document.querySelector('dialog').setAttribute('open', '');

    expect(focusScope(t.body, { root: root(), editing: true })).toBe('dialog');
    expect(focusScope(t.button, { root: root(), editing: true })).toBe(
      'dialog',
    );
    // The page behind it too, which is inert.
    expect(focusScope(t.outside, { root: root(), editing: true })).toBe(
      'outside',
    );
  });
});

// The Settings dialog's Snap to grid, from its keys.
describe('the Snap to grid command', () => {
  test.each(PLATFORMS)(
    'has no default keys, and runs on keys the user gives it (%s)',
    (platform) => {
      setPlatform(platform);

      expect(defaultKeys('view.snapToGrid', platform)).toEqual([]);
      expect(commandKeys('view.snapToGrid', { platform })).toEqual([]);
      expect(isCustomizable('view.snapToGrid')).toBe(true);
      expect(
        shortcutConflicts('view.snapToGrid', 'Alt+Shift+J', { platform }),
      ).toEqual([]);

      setShortcut('view.snapToGrid', ['Alt+Shift+J'], memoryStorage());
      expect(commandKeys('view.snapToGrid', { platform })).toEqual([
        'Alt+Shift+J',
      ]);

      const t = targets();
      const ctx = context();
      const event = keydown(t.button, 'J', 'KeyJ', {
        altKey: true,
        shiftKey: true,
      });

      expect(dispatch(event, ctx)).toBe('view.snapToGrid');
      expect(event.defaultPrevented).toBe(true);
      expect(ctx.view.toggleSnapToGrid).toHaveBeenCalledOnce();
    },
  );
});

// Show or hide connections, from its default keys.
describe('the Show or hide connections command', () => {
  test.each(PLATFORMS)(
    'answers to Alt+Shift+C outside text fields, a free key (%s)',
    (platform) => {
      setPlatform(platform);

      const ID = 'view.connections';

      expect(defaultKeys(ID, platform)).toEqual(['Alt+Shift+C']);
      expect(commandKeys(ID, { platform })).toEqual(['Alt+Shift+C']);
      expect(isCustomizable(ID)).toBe(true);
      expect(keyRefusal('Alt+Shift+C', platform)).toBe('');
      expect(shortcutConflicts(ID, 'Alt+Shift+C', { platform })).toEqual([]);

      const t = targets();
      const ctx = context();
      const press = (target) =>
        keydown(target, 'C', 'KeyC', { altKey: true, shiftKey: true });

      for (const target of [t.button, t.canvas, t.node]) {
        const event = press(target);

        expect(dispatch(event, ctx), target.dataset.testid).toBe(ID);
        expect(event.defaultPrevented).toBe(true);
      }
      expect(ctx.view.toggleConnections).toHaveBeenCalledTimes(3);
      // Not in a text field, where ⌥⇧C types a character on a Mac.
      expect(dispatch(press(t.field), ctx)).toBe(null);
    },
  );
});

describe('the dispatcher', () => {
  test.each(PLATFORMS)(
    'runs the platform’s keys where their scope reaches (%s)',
    (platform) => {
      setPlatform(platform);

      const t = targets();
      const ctx = context({ store: { canUndo: true } });
      const run = (target, key, code, mods = {}) => {
        const event = keydown(target, key, code, mods);

        return [dispatch(event, ctx), event.defaultPrevented];
      };

      // Mod+K and Mod+S work in text fields. The editing keys do not.
      expect(run(t.field, 'k', 'KeyK', mod(platform))).toEqual([
        'palette.open',
        true,
      ]);
      expect(ctx.view.openPalette).toHaveBeenLastCalledWith({});
      expect(run(t.field, 's', 'KeyS', mod(platform))).toEqual([
        'draft.save',
        true,
      ]);
      expect(run(t.field, 'z', 'KeyZ', mod(platform))).toEqual([null, false]);
      expect(run(t.field, '?', 'Slash', { shiftKey: true })).toEqual([
        null,
        false,
      ]);

      // The editing keys work anywhere else in the editor.
      expect(run(t.button, 'z', 'KeyZ', mod(platform))).toEqual([
        'edit.undo',
        true,
      ]);
      expect(run(t.node, 'z', 'KeyZ', mod(platform))).toEqual([
        'edit.undo',
        true,
      ]);
      expect(ctx.store.undo).toHaveBeenCalledTimes(2);
      expect(run(t.button, '?', 'Slash', { shiftKey: true })).toEqual([
        'shortcuts.open',
        true,
      ]);

      // Zoom keys only on the canvas, not its zoom buttons.
      expect(run(t.canvas, '=', 'Equal')).toEqual(['view.zoomIn', true]);
      expect(run(t.node, '-', 'Minus')).toEqual(['view.zoomOut', true]);
      expect(run(t.node, '!', 'Digit1', { shiftKey: true })).toEqual([
        'view.fit',
        true,
      ]);
      expect(run(t.zoomButton, '=', 'Equal')).toEqual([null, false]);
      expect(ctx.view.zoomIn).toHaveBeenCalledOnce();
      expect(ctx.view.fitView).toHaveBeenCalledOnce();

      // Go to node opens the palette at its prefix.
      expect(
        run(t.button, 'O', 'KeyO', { ...mod(platform), shiftKey: true }),
      ).toEqual(['goto.node', true]);
      expect(ctx.view.openPalette).toHaveBeenLastCalledWith({ query: '@' });

      // The other platform's modifier does nothing.
      const other = platform === 'mac' ? { ctrlKey: true } : { metaKey: true };
      expect(run(t.button, 'z', 'KeyZ', other)).toEqual([null, false]);
    },
  );

  test('N on the canvas adds the plain Device in view, without asking, and focuses it', () => {
    const t = targets();
    const { doc } = sampleDocument();
    const ctx = context({ store: { doc } });
    const press = (target, mods = {}) => {
      const event = keydown(target, 'n', 'KeyN', mods);

      return [dispatch(event, ctx), event.defaultPrevented];
    };

    ctx.store.addNode = vi.fn(() => ({ id: 'added' }));

    expect(press(t.canvas)).toEqual(['add.device', true]);
    expect(ctx.view.openPalette).not.toHaveBeenCalled();
    expect(ctx.store.addNode).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({
        kind: 'device',
        position: nextPosition(doc, { kind: 'device' }),
      }),
    );
    // The plain Device, as the palette's Add Device button adds it.
    expect(ctx.store.addNode.mock.calls[0][0]).toEqual(
      expect.objectContaining(paletteNode(ctx.store, 'device', '')),
    );
    expect(ctx.view.revealNode).toHaveBeenCalledWith('added');
    expect(ctx.view.showNode).toHaveBeenCalledWith('added');

    // On a focused node too: the canvas's scope.
    expect(press(t.node)).toEqual(['add.device', true]);

    // Not in a text field (the Inspector), nor elsewhere in the editor,
    // nor with Shift.
    expect(press(t.field)).toEqual([null, false]);
    expect(press(t.button)).toEqual([null, false]);
    expect(press(t.canvas, { shiftKey: true })).toEqual([null, false]);
    expect(ctx.store.addNode).toHaveBeenCalledTimes(2);

    // The palette's Add device still asks, and leaves focus where it is.
    ctx.view.showNode.mockClear();
    runCommand('add.device', { ...ctx, source: 'palette' });
    expect(ctx.view.openPalette).toHaveBeenLastCalledWith({
      command: 'add.device',
    });
    runCommand('add.device', { ...ctx, source: 'palette' }, { value: '' });
    expect(ctx.view.showNode).not.toHaveBeenCalled();

    // A read-only draft adds nothing, and says why.
    const readOnly = context({ store: { readOnly: true } });

    expect(dispatch(keydown(t.canvas, 'n', 'KeyN'), readOnly)).toBe(
      'add.device',
    );
    expect(readOnly.store.addNode).not.toHaveBeenCalled();
    expect(readOnly.store.announce).toHaveBeenCalledWith(READ_ONLY);

    // A letter alone is for the canvas only. The single-key switch turns
    // it off.
    expect(takesLetters('add.device')).toBe(true);
    expect(takesLetters('view.zoomIn')).toBe(true);
    expect(takesLetters('edit.rename')).toBe(true);
    expect(takesLetters('view.reset')).toBe(false);
    expect(takesLetters('edit.undo')).toBe(false);
    setSingleKeyShortcuts(false, null);
    expect(press(t.canvas)).toEqual([null, false]);
    setSingleKeyShortcuts(true, null);
  });

  test('redo is ⇧⌘Z on macOS, and also Ctrl+Y elsewhere', () => {
    const t = targets();
    const ctx = context({ store: { canRedo: true } });

    setPlatform('mac');
    expect(
      dispatch(keydown(t.button, 'y', 'KeyY', { metaKey: true }), ctx),
    ).toBe(null);
    expect(
      dispatch(
        keydown(t.button, 'z', 'KeyZ', { metaKey: true, shiftKey: true }),
        ctx,
      ),
    ).toBe('edit.redo');

    setPlatform('other');
    expect(
      dispatch(keydown(t.button, 'y', 'KeyY', { ctrlKey: true }), ctx),
    ).toBe('edit.redo');
    expect(ctx.store.redo).toHaveBeenCalledTimes(2);
  });

  test('leaves alone what others handle', () => {
    setPlatform('other');

    const t = targets();
    const ctx = context({ store: { canUndo: true } });
    const handled = keydown(t.node, 'z', 'KeyZ', { ctrlKey: true });

    // The node's own handler takes the press first.
    t.node.addEventListener('keydown', (event) => event.preventDefault(), {
      once: true,
    });
    expect(dispatch(handled, ctx)).toBe(null);
    expect(
      dispatch(keydown(t.inDialog, 'k', 'KeyK', { ctrlKey: true }), ctx),
    ).toBe(null);
    expect(
      dispatch(keydown(t.outsideField, 'k', 'KeyK', { ctrlKey: true }), ctx),
    ).toBe(null);
    expect(
      dispatch(keydown(t.outsideModal, 'k', 'KeyK', { ctrlKey: true }), ctx),
    ).toBe(null);
    expect(
      dispatch(
        keydown(t.field, 'k', 'KeyK', { ctrlKey: true, isComposing: true }),
        ctx,
      ),
    ).toBe(null);
    // Delete and Enter are the canvas's own.
    expect(dispatch(keydown(t.node, 'Delete', 'Delete'), ctx)).toBe(null);
    expect(dispatch(keydown(t.node, 'Enter', 'Enter'), ctx)).toBe(null);
    expect(ctx.store.undo).not.toHaveBeenCalled();
  });

  test.each(PLATFORMS)(
    'on the rest of the page, only the palette’s and the sheet’s keys answer (%s)',
    (platform) => {
      setPlatform(platform);

      const t = targets();
      const run = (ctx, key, code, mods = {}) => {
        const event = keydown(t.outside, key, code, mods);

        return [dispatch(event, ctx), event.defaultPrevented];
      };

      for (const editing of [true, false]) {
        const ctx = context({ store: { canUndo: true }, view: { editing } });

        expect(run(ctx, 'k', 'KeyK', mod(platform))).toEqual([
          'palette.open',
          true,
        ]);
        expect(run(ctx, '?', 'Slash', { shiftKey: true })).toEqual([
          'shortcuts.open',
          true,
        ]);
        expect(run(ctx, 'z', 'KeyZ', mod(platform))).toEqual([null, false]);
        expect(run(ctx, 's', 'KeyS', mod(platform))).toEqual([null, false]);
        expect(ctx.store.undo).not.toHaveBeenCalled();
      }
    },
  );

  test('a text field keeps every key that types a character', () => {
    setPlatform('other');

    const t = targets();
    const ctx = context();

    // As if the key had been given before the recorder refused it.
    setShortcut('palette.open', ['/'], memoryStorage());
    setShortcut('draft.save', ['Shift+1'], memoryStorage());

    const slash = keydown(t.field, '/', 'Slash');
    expect(dispatch(slash, ctx)).toBe(null);
    expect(slash.defaultPrevented).toBe(false);
    expect(
      dispatch(keydown(t.field, '!', 'Digit1', { shiftKey: true }), ctx),
    ).toBe(null);
    expect(ctx.view.openPalette).not.toHaveBeenCalled();
    expect(ctx.store.saveNow).not.toHaveBeenCalled();

    // Elsewhere, the key is the palette's.
    expect(dispatch(keydown(t.button, '/', 'Slash'), ctx)).toBe('palette.open');
    expect(worksInTextFields('palette.open')).toBe(true);
    expect(worksInTextFields('draft.save')).toBe(true);
    expect(worksInTextFields('edit.undo')).toBe(false);
    expect(worksInTextFields('view.zoomIn')).toBe(false);
  });

  test('on macOS, a text field keeps ⌥ keys without ⌘ or ⌃: they type', () => {
    setPlatform('mac');

    const t = targets();
    const ctx = context();

    // As if the keys had been given before the recorder refused them: ⌥E
    // is the acute accent's dead key, and ⌥⇧/ types ¿.
    setShortcut('palette.open', ['Alt+E'], memoryStorage());
    setShortcut('draft.save', ['Alt+Shift+/'], memoryStorage());

    const acute = keydown(t.field, 'Dead', 'KeyE', { altKey: true });
    expect(dispatch(acute, ctx)).toBe(null);
    expect(acute.defaultPrevented).toBe(false);
    expect(
      dispatch(
        keydown(t.field, '¿', 'Slash', { altKey: true, shiftKey: true }),
        ctx,
      ),
    ).toBe(null);
    expect(ctx.view.openPalette).not.toHaveBeenCalled();
    expect(ctx.store.saveNow).not.toHaveBeenCalled();

    // Elsewhere, the key is the palette's. With ⌘ or ⌃ too, it types
    // nothing in the field either.
    expect(
      dispatch(keydown(t.button, 'Dead', 'KeyE', { altKey: true }), ctx),
    ).toBe('palette.open');
    setShortcut('palette.open', ['Ctrl+Alt+E'], memoryStorage());
    const ctrl = keydown(t.field, 'Dead', 'KeyE', {
      altKey: true,
      ctrlKey: true,
    });
    expect(dispatch(ctrl, ctx)).toBe('palette.open');
    expect(ctrl.defaultPrevented).toBe(true);
  });

  test('on Windows and Linux, Alt keys work in a text field: Alt types nothing', () => {
    setPlatform('other');

    const t = targets();
    const ctx = context();

    setShortcut('palette.open', ['Alt+E'], memoryStorage());

    const alt = keydown(t.field, 'e', 'KeyE', { altKey: true });
    expect(dispatch(alt, ctx)).toBe('palette.open');
    expect(alt.defaultPrevented).toBe(true);
  });

  test('takes a matched key even when its command cannot run, and says why', () => {
    setPlatform('other');

    const t = targets();
    const ctx = context({ store: { readOnly: true } });
    const paste = keydown(t.node, 'v', 'KeyV', { ctrlKey: true });

    expect(dispatch(paste, ctx)).toBe('edit.paste');
    expect(paste.defaultPrevented).toBe(true);
    expect(ctx.store.paste).not.toHaveBeenCalled();
    expect(ctx.store.announce).toHaveBeenCalledWith(READ_ONLY);

    // F2 on a node of a read-only diagram, which renames nothing.
    dispatch(keydown(t.node, 'F2', 'F2'), ctx);
    expect(ctx.store.announce).toHaveBeenLastCalledWith(READ_ONLY);
  });

  test('leaves selected text to the browser’s copy', () => {
    setPlatform('other');

    const t = targets();
    const range = document.createRange();

    range.selectNodeContents(byTestId('text'));
    window.getSelection().addRange(range);
    const { doc, alpha } = sampleDocument();
    const ctx = context({
      store: { doc, selection: { nodes: [alpha.id], edges: [] } },
    });
    const copy = keydown(t.button, 'c', 'KeyC', { ctrlKey: true });

    expect(dispatch(copy, ctx)).toBe(null);
    expect(copy.defaultPrevented).toBe(false);
    expect(ctx.store.copy).not.toHaveBeenCalled();
  });

  test('F2 on a canvas node renames it in the Inspector', () => {
    setPlatform('mac');

    const { doc, alpha } = sampleDocument();
    const node = nodeOnCanvas(alpha.id);
    const ctx = context({ store: { doc } });

    expect(dispatch(keydown(node, 'F2', 'F2'), ctx)).toBe('edit.rename');
    expect(ctx.store.selection).toEqual({ nodes: [alpha.id], edges: [] });
    expect(ctx.view.focusInspector).toHaveBeenCalledWith({ field: true });
  });

  test('Alt+Shift+E on a canvas node opens the Node Editor for it', () => {
    for (const platform of PLATFORMS) {
      setPlatform(platform);

      const { doc, alpha, bravo } = sampleDocument();
      const t = targets();
      const node = nodeOnCanvas(alpha.id);
      // Another node is selected: the focused one is edited, and selected.
      const ctx = context({
        store: { doc, selection: { nodes: [bravo.id], edges: [] } },
      });
      const press = (target) =>
        keydown(target, 'E', 'KeyE', { altKey: true, shiftKey: true });

      expect(dispatch(press(node), ctx), platform).toBe('node.edit');
      expect(ctx.store.selection).toEqual({ nodes: [alpha.id], edges: [] });
      expect(ctx.view.openDialog).toHaveBeenCalledExactlyOnceWith(
        'node-editor',
        { id: alpha.id },
      );
      // Not in a text field, where ⌥⇧E types a character on a Mac.
      expect(dispatch(press(t.field), ctx), platform).toBe(null);
    }
  });

  test('on the landing, only the landing’s commands answer', () => {
    setPlatform('mac');

    const t = targets();
    const ctx = context({ store: { canUndo: true }, view: { editing: false } });

    expect(
      dispatch(keydown(t.button, 'k', 'KeyK', { metaKey: true }), ctx),
    ).toBe('palette.open');
    expect(dispatch(keydown(t.button, '?', 'Slash'), ctx)).toBe(
      'shortcuts.open',
    );
    // Focus mode is the drafts' too, so it can be left there.
    expect(
      dispatch(
        keydown(t.button, 'F', 'KeyF', { metaKey: true, shiftKey: true }),
        ctx,
      ),
    ).toBe('view.focusMode');
    expect(ctx.view.toggleFocusMode).toHaveBeenCalledOnce();
    expect(
      dispatch(keydown(t.button, 'z', 'KeyZ', { metaKey: true }), ctx),
    ).toBe(null);
    expect(ctx.store.undo).not.toHaveBeenCalled();
  });

  // Option with a letter types another character on a Mac ('Í' for ⌥⇧S),
  // so these keys match the physical key, and a text field keeps them.
  test.each(PLATFORMS)(
    'Alt+Shift with S, L and G open Settings, lay out and auto-group (%s)',
    (platform) => {
      setPlatform(platform);

      const t = targets();
      const ctx = context();
      const typed = platform === 'mac' ? { S: 'Í', L: 'Ò', G: '˝' } : {};
      const run = (target, letter, on = ctx) => {
        const event = keydown(target, typed[letter] || letter, `Key${letter}`, {
          altKey: true,
          shiftKey: true,
        });

        return [dispatch(event, on), event.defaultPrevented];
      };

      expect(run(t.button, 'S')).toEqual(['settings.open', true]);
      expect(run(t.canvas, 'S')).toEqual(['settings.open', true]);
      expect(run(t.zoomButton, 'L')).toEqual(['structure.layout', true]);
      expect(run(t.node, 'G')).toEqual(['structure.autoGroup.network', true]);
      expect(ctx.view.openSettings).toHaveBeenCalledTimes(2);
      expect(ctx.store.layout).toHaveBeenCalledExactlyOnceWith();
      expect(ctx.store.autoGroup).toHaveBeenCalledExactlyOnceWith('network');

      // Not in a text field, nor in a dialog, on either platform.
      for (const letter of ['S', 'L', 'G']) {
        expect(run(t.field, letter), letter).toEqual([null, false]);
        expect(run(t.inDialog, letter), letter).toEqual([null, false]);
      }

      // Without Shift, or with the command key too, they are other keys.
      expect(
        dispatch(keydown(t.button, 's', 'KeyS', { altKey: true }), ctx),
      ).toBe(null);
      expect(
        dispatch(
          keydown(t.button, 'S', 'KeyS', {
            ...mod(platform),
            altKey: true,
            shiftKey: true,
          }),
          ctx,
        ),
      ).toBe(null);

      // Settings opens from the drafts page and from the app header too.
      // The layout and Auto-group are the editor's alone.
      const landing = context({ view: { editing: false } });
      expect(run(t.button, 'S', landing)).toEqual(['settings.open', true]);
      expect(run(t.outside, 'S', landing)).toEqual(['settings.open', true]);
      expect(run(t.outsideField, 'S', landing)).toEqual([null, false]);
      expect(run(t.button, 'L', landing)).toEqual([null, false]);
      expect(run(t.button, 'G', landing)).toEqual([null, false]);
      expect(run(t.outside, 'L')).toEqual([null, false]);
      expect(landing.view.openSettings).toHaveBeenCalledTimes(2);
      expect(landing.store.layout).not.toHaveBeenCalled();

      // From a checkbox that selects a card of the drafts page as well: it
      // is no text field. The Inspector's checkboxes keep the key.
      expect(run(t.checkbox, 'S', landing)).toEqual(['settings.open', true]);
      expect(landing.view.openSettings).toHaveBeenCalledTimes(3);
      expect(run(t.checkbox, 'S')).toEqual([null, false]);
      expect(run(t.checkbox, 'L')).toEqual([null, false]);

      // A read-only draft: the key is taken, and the reason said.
      const viewer = context({ store: { readOnly: true } });
      expect(run(t.button, 'L', viewer)).toEqual(['structure.layout', true]);
      expect(run(t.button, 'G', viewer)).toEqual([
        'structure.autoGroup.network',
        true,
      ]);
      expect(viewer.store.layout).not.toHaveBeenCalled();
      expect(viewer.store.autoGroup).not.toHaveBeenCalled();
      expect(viewer.store.announce).toHaveBeenCalledTimes(2);
      expect(viewer.store.announce).toHaveBeenLastCalledWith(READ_ONLY);
      expect(run(t.button, 'S', viewer)).toEqual(['settings.open', true]);
    },
  );

  test('follows the user’s keys and the single-key switch', () => {
    setPlatform('other');

    const t = targets();
    const ctx = context();

    setShortcut('structure.layout', ['Mod+Shift+L'], memoryStorage());
    expect(
      dispatch(
        keydown(t.button, 'L', 'KeyL', { ctrlKey: true, shiftKey: true }),
        ctx,
      ),
    ).toBe('structure.layout');
    expect(ctx.store.layout).toHaveBeenCalledOnce();

    setSingleKeyShortcuts(false, memoryStorage());
    const question = keydown(t.button, '?', 'Slash', { shiftKey: true });

    expect(dispatch(question, ctx)).toBe(null);
    expect(question.defaultPrevented).toBe(false);
  });
});

describe('the dispatcher, for any key the user gives the palette', () => {
  // Physical keys as a US layout types them, without and with Shift.
  const KEYS = {
    ...Object.fromEntries(
      [...'ABCDEFGHIJKLMNOPQRSTUVWXYZ'].map((letter) => [
        `Key${letter}`,
        [letter.toLowerCase(), letter],
      ]),
    ),
    Digit1: ['1', '!'],
    Digit7: ['7', '&'],
    Slash: ['/', '?'],
    Equal: ['=', '+'],
    Minus: ['-', '_'],
    Comma: [',', '<'],
    Semicolon: [';', ':'],
  };

  test('a text field keeps it when it types a character, and elsewhere it opens the palette', () => {
    fc.assert(
      fc.property(
        fc.constantFrom(...PLATFORMS),
        fc.constantFrom(...Object.keys(KEYS)),
        fc.record({
          shiftKey: fc.boolean(),
          altKey: fc.boolean(),
          ctrlKey: fc.boolean(),
          metaKey: fc.boolean(),
        }),
        (platform, code, mods) => {
          setPlatform(platform);
          loadShortcutSettings(memoryStorage());

          // On a Mac, Option with a key types another character.
          const typed =
            platform === 'mac' && mods.altKey
              ? 'ø'
              : KEYS[code][mods.shiftKey ? 1 : 0];
          const press = (target) => keydown(target, typed, code, mods);
          const spec = eventToKey(press(document.body), platform);

          // No shortcut has both Ctrl and ⌘ (or Windows), so none is recorded.
          fc.pre(spec !== '');
          setShortcut('palette.open', [spec], memoryStorage());

          const t = targets();
          const ctx = context();
          const inField = press(t.field);
          const onButton = press(t.button);
          const types = typesCharacter(spec, platform);

          expect(dispatch(inField, ctx), spec).toBe(
            types ? null : 'palette.open',
          );
          expect(inField.defaultPrevented, spec).toBe(!types);
          expect(dispatch(onButton, ctx), spec).toBe('palette.open');
          expect(onButton.defaultPrevented, spec).toBe(true);
          expect(ctx.view.openPalette).toHaveBeenCalledTimes(types ? 1 : 2);
        },
      ),
      { numRuns: 300 },
    );
  });
});

// Lets keepFocus and focusSoon finish: two ticks, then up to ten frames.
async function settle() {
  for (let turn = 0; turn < 20; turn += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
}

// What the command's store action does to the page: re-renders an element
// in place, as Vue does after an undo, or removes it.
function rerender(element) {
  const copy = element.cloneNode(true);

  element.replaceWith(copy);

  return copy;
}

describe('focus after a command', () => {
  test('is lost when nothing, the page itself, or a removed element has it', () => {
    const t = targets();

    expect(document.activeElement).toBe(document.body);
    expect(focusLost()).toBe(true);

    t.button.focus();
    expect(focusLost()).toBe(false);

    t.button.remove();
    expect(focusLost()).toBe(true);
  });

  test('goes back to the same item when the command re-renders it: a node, an id or a test id', async () => {
    const t = targets();
    const ctx = context({ store: { canUndo: true } });
    let copy = null;

    ctx.store.undo.mockImplementation(() => {
      copy = rerender(document.activeElement);
    });

    t.button.id = 'toolbar-undo';
    for (const element of [t.node, t.button, byTestId('zoom-in')]) {
      element.focus();
      expect(runCommand('edit.undo', ctx)).toBe(true);
      expect(focusLost()).toBe(true);

      await vi.waitFor(() => expect(document.activeElement).toBe(copy));
      expect(copy).not.toBe(element);
    }
    expect(ctx.store.undo).toHaveBeenCalledTimes(3);
  });

  test('goes to the region of a removed item: the Inspector’s title, or else the canvas', async () => {
    const t = targets();
    const ctx = context({ store: { canUndo: true } });

    ctx.store.undo.mockImplementation(() => document.activeElement.remove());

    for (const [element, region] of [
      [t.field, document.getElementById('inspector-title')],
      [t.node, t.canvas],
      [t.zoomButton, t.canvas],
      [t.button, t.canvas],
    ]) {
      element.focus();
      runCommand('edit.undo', ctx);

      await vi.waitFor(() => expect(document.activeElement).toBe(region));
    }
  });

  test('goes to the shown tab on the drafts landing', async () => {
    const t = targets();
    const ctx = context({ view: { editing: false } });

    root().classList.remove('builder-root--editing');
    root().insertAdjacentHTML(
      'afterbegin',
      `<div role="tablist">
        <button class="builder-tab" role="tab" aria-selected="false">Drafts</button>
        <button class="builder-tab" role="tab" aria-selected="true">Templates</button>
      </div>`,
    );
    ctx.view.startBlank.mockImplementation(() => t.button.remove());

    t.button.focus();
    expect(runCommand('drafts.blank', ctx)).toBe(true);

    await vi.waitFor(() =>
      expect(document.activeElement.textContent).toBe('Templates'),
    );
  });

  test('stays where the command put it, and is not recorded outside the Builder', async () => {
    const t = targets();
    const ctx = context({ store: { canUndo: true } });

    // The command moves focus itself, then removes what had it.
    ctx.store.undo.mockImplementationOnce(() => {
      t.field.focus();
      t.button.remove();
    });
    t.button.focus();
    runCommand('edit.undo', ctx);
    await settle();
    expect(document.activeElement).toBe(t.field);

    // Focus on the app's header is not the Builder's to put back, even
    // when its link is re-rendered.
    ctx.store.undo.mockImplementationOnce(() => rerender(t.outside));
    t.outside.focus();
    runCommand('edit.undo', ctx);
    await settle();
    expect(document.activeElement).toBe(document.body);
  });

  test('tries again on the next frames until a re-rendered node can take it', async () => {
    const t = targets();
    const ctx = context({ store: { canUndo: true } });
    let copy = null;
    let tries = 0;

    // Vue Flow shows the node only after it measures it, frames later.
    ctx.store.undo.mockImplementation(() => {
      copy = rerender(t.node);
      copy.focus = function focus(options) {
        tries += 1;
        if (tries >= 3) {
          HTMLElement.prototype.focus.call(this, options);
        }
      };
    });

    t.node.focus();
    runCommand('edit.undo', ctx);

    await vi.waitFor(() => expect(document.activeElement).toBe(copy));
    expect(tries).toBe(3);
  });

  test('stops trying as soon as anything else takes focus', async () => {
    const t = targets();
    const ctx = context({ store: { canUndo: true } });
    let tries = 0;

    // The node never shows, and the user moves on to the Inspector.
    ctx.store.undo.mockImplementation(() => {
      rerender(t.node).focus = () => {
        tries += 1;
        requestAnimationFrame(() => t.field.focus());
      };
    });

    t.node.focus();
    runCommand('edit.undo', ctx);
    await settle();

    expect(document.activeElement).toBe(t.field);
    expect(tries).toBe(1);
  });

  test('Delete focuses the canvas once the selection is gone', async () => {
    const t = targets();
    const { store } = context({
      store: { selection: { nodes: ['n1'], edges: [] } },
    });

    store.removeSelection.mockImplementation(() => t.node.remove());
    t.node.focus();

    await deleteSelection(store);

    expect(store.removeSelection).toHaveBeenCalledOnce();
    expect(document.activeElement).toBe(t.canvas);
  });

  test('Ungroup focuses the node of a member, or the canvas when it has none', async () => {
    const t = targets();
    const group = { id: 'g1', kind: 'group' };
    const member = { id: 'm1', kind: 'device', parentId: 'g1' };
    const selection = { nodes: ['g1'], edges: [] };
    const withMember = context({
      store: { doc: { nodes: [group, member] }, selection },
    }).store;

    withMember.ungroup.mockImplementation(() => nodeOnCanvas('m1'));
    t.button.focus();
    await ungroupSelection(withMember);

    expect(withMember.ungroup).toHaveBeenCalledOnce();
    expect(document.activeElement.dataset.id).toBe('m1');

    const empty = context({
      store: { doc: { nodes: [group] }, selection },
    }).store;

    t.button.focus();
    await ungroupSelection(empty);
    expect(document.activeElement).toBe(t.canvas);
  });

  test('Group focuses the node of the new group', async () => {
    const t = targets();
    const ctx = context({ store: { selection: { nodes: ['n1'], edges: [] } } });

    ctx.store.group.mockImplementation(() => {
      nodeOnCanvas('g2');

      return { id: 'g2' };
    });
    t.node.focus();
    expect(runCommand('structure.group', ctx)).toBe(true);

    await vi.waitFor(() =>
      expect(document.activeElement.dataset?.id).toBe('g2'),
    );
  });
});
