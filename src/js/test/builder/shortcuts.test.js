import { afterEach, beforeEach, describe, expect, test } from 'vitest';

import {
  COMMANDS,
  GROUPS,
  ariaShortcuts,
  canvasHelp,
  commandKeys,
  isCustomizable,
  withShortcut,
} from '@/builder/commands.js';
import {
  SHORTCUTS_STORAGE_KEY,
  eventToKey,
  keymapState,
  loadShortcutSettings,
  setPlatform,
  setShortcut,
  setSingleKeyShortcuts,
  shortcutOverride,
} from '@/builder/keymap.js';
import {
  assignShortcut,
  characterKeyLabels,
  commandName,
  judgeShortcut,
  recorderMessage,
  removeShortcut,
  shortcutGroups,
  shortcutRow,
  singleKeyHint,
  spokenKeys,
  whereText,
} from '@/builder/shortcuts.js';

function noStorage() {
  return { getItem: () => null, setItem: () => {} };
}

function memoryStorage() {
  const items = new Map();

  return {
    getItem: (key) => items.get(key) ?? null,
    setItem: (key, value) => items.set(key, String(value)),
    saved: () => JSON.parse(items.get(SHORTCUTS_STORAGE_KEY) || 'null'),
  };
}

function titles(groups) {
  return groups.flatMap((group) => group.rows.map((row) => row.title));
}

// A key press, as the recorder gets it.
function press(key, { code = '', mod = false, ...rest } = {}) {
  return {
    key,
    code,
    metaKey: false,
    ctrlKey: false,
    altKey: false,
    shiftKey: false,
    ...rest,
    ...(mod ? { metaKey: true } : {}),
  };
}

beforeEach(() => {
  setPlatform('mac');
  loadShortcutSettings(noStorage());
});

afterEach(() => {
  setPlatform(null);
  loadShortcutSettings(noStorage());
});

describe('the sheet', () => {
  test('says where each scope and view works', () => {
    expect(whereText('palette.open')).toBe(
      'Editor and drafts list, text fields too',
    );
    expect(whereText('shortcuts.open')).toBe(
      'Editor and drafts list, not in text fields',
    );
    expect(whereText('draft.save')).toBe('Editor, text fields too');
    expect(whereText('edit.undo')).toBe('Editor, not in text fields');
    expect(whereText('drafts.blank')).toBe('Drafts list, not in text fields');
    expect(whereText('view.fit')).toBe('Canvas');
    expect(whereText('outline.move')).toBe('Outline rows');
    expect(whereText('edit.delete')).toBe('Canvas and outline rows');
  });

  test('lists the commands with keys, by group, in the registry order', () => {
    const groups = shortcutGroups();
    const order = groups.map((group) => group.group);

    expect(order).toEqual(GROUPS.filter((group) => order.includes(group)));
    expect(titles(groups)).toEqual(
      COMMANDS.filter(
        (command) => commandKeys(command, { all: true }).length,
      ).map((command) => command.title),
    );
    expect(titles(groups)).toContain('Delete selection');
    expect(titles(groups)).not.toContain('Auto layout');
  });

  test('lists every command whose keys can change when customizing', () => {
    const rows = shortcutGroups({ customize: true }).flatMap(
      (group) => group.rows,
    );

    expect(rows.map((row) => row.id)).toEqual(
      COMMANDS.filter(isCustomizable).map((command) => command.id),
    );
    expect(rows.every((row) => row.customizable)).toBe(true);
    expect(rows.map((row) => row.id)).not.toContain('edit.rename');
  });

  test('shows the platform keys, as caps and words', () => {
    const redo = shortcutRow('edit.redo');

    expect(redo.keys.map((key) => key.caps)).toEqual([['⇧', '⌘', 'Z']]);
    expect(redo.keys.map((key) => key.spoken)).toEqual(['Shift+Command+Z']);

    setPlatform('other');
    expect(
      shortcutRow('edit.redo').keys.map((key) => [key.label, key.spoken]),
    ).toEqual([
      ['Ctrl+Shift+Z', 'Ctrl+Shift+Z'],
      ['Ctrl+Y', 'Ctrl+Y'],
    ]);
  });

  test('filters by a key for one character, by words otherwise', () => {
    expect(titles(shortcutGroups({ query: 'G' }))).toEqual([
      'Group selection',
      'Ungroup',
    ]);
    expect(titles(shortcutGroups({ query: '?' }))).toEqual([
      'Keyboard shortcuts',
    ]);
    expect(titles(shortcutGroups({ query: '-' }))).toEqual(['Zoom out']);
    expect(titles(shortcutGroups({ query: 'undo' }))).toEqual(['Undo']);
    // Where it works, and the modifiers in words.
    expect(titles(shortcutGroups({ query: 'outline rows f2' }))).toEqual([
      'Rename',
    ]);
    expect(titles(shortcutGroups({ query: 'cmd shift' }))).toEqual([
      'Redo',
      'Ungroup',
      'Go to node',
      'Focus mode',
    ]);
    expect(shortcutGroups({ query: 'no such thing' })).toEqual([]);
    // The command being recorded stays, whatever the filter.
    expect(
      titles(
        shortcutGroups({
          query: 'duplicate',
          customize: true,
          keep: 'structure.layout',
        }),
      ),
    ).toEqual(['Duplicate', 'Auto layout']);
  });

  test('marks the one-character keys the switch has turned off', () => {
    expect(characterKeyLabels()).toEqual(['?', '=', '+', '−', '⇧1']);
    expect(singleKeyHint()).toMatch(
      /^\?, =, \+, − and ⇧1 work alone, without ⌘\./,
    );
    setPlatform('other');
    expect(characterKeyLabels()).toEqual(['?', '=', '+', '−', 'Shift+1']);
    expect(singleKeyHint()).toMatch(/without Ctrl\. Turn them off if speech/);

    setSingleKeyShortcuts(false, null);
    const zoom = shortcutRow('view.zoomIn');

    expect(zoom.keys.map((key) => [key.label, key.off])).toEqual([
      ['=', true],
      ['+', true],
    ]);
    expect(shortcutRow('edit.undo').keys[0].off).toBe(false);
  });

  test('names commands without the dialog ellipsis', () => {
    expect(commandName('draft.export')).toBe('Export');
    expect(shortcutRow('draft.export')).toMatchObject({
      title: 'Export…',
      name: 'Export',
    });
  });
});

describe('the recorder', () => {
  test('records a key press as a spec', () => {
    const spec = eventToKey(
      press('L', { code: 'KeyL', mod: true, shiftKey: true }),
    );

    expect(spec).toBe('Mod+Shift+L');
    expect(judgeShortcut('structure.layout', spec)).toMatchObject({
      status: 'free',
      label: '⇧⌘L',
      spoken: 'Shift+Command+L',
      others: 0,
    });
  });

  test('refuses reserved keys, letters alone and Ctrl+Alt, with the reason', () => {
    expect(judgeShortcut('structure.layout', 'Mod+T')).toMatchObject({
      status: 'refused',
      reason:
        'Browsers keep ⌘T to open a new tab and never pass it to the page.',
    });
    expect(judgeShortcut('structure.layout', 'Mod+0').reason).toMatch(
      /^Browsers use ⌘0 to zoom/,
    );
    expect(judgeShortcut('structure.layout', 'Q').reason).toMatch(
      /^Letters without ⌘ are for typing/,
    );
    expect(judgeShortcut('structure.layout', 'Enter').status).toBe('refused');

    setPlatform('other');
    expect(judgeShortcut('structure.layout', 'Ctrl+Alt+L').reason).toMatch(
      /Ctrl\+Alt types characters/,
    );
    expect(judgeShortcut('structure.layout', 'Ctrl+T').reason).toMatch(
      /^Browsers keep Ctrl\+T/,
    );
    expect(judgeShortcut('structure.layout', 'Meta+Shift+L').reason).toBe(
      'Shift+Meta+L is not used: Windows and Linux keep most shortcuts with the Windows key for themselves.',
    );
  });

  test('names the commands a key would clash with, where both work', () => {
    const verdict = judgeShortcut('draft.export', 'Mod+D');

    expect(verdict.status).toBe('conflict');
    expect(verdict.conflicts.map((command) => command.id)).toEqual([
      'edit.duplicate',
    ]);

    // Mod+K works in text fields and on the landing too.
    expect(
      judgeShortcut('drafts.blank', 'Mod+K').conflicts.map(
        (command) => command.id,
      ),
    ).toEqual(['palette.open']);

    // The zoom keys work on the canvas only; the outline's own does not
    // clash with them.
    expect(judgeShortcut('view.fit', 'Shift+2').status).toBe('free');
  });

  test('refuses a key that types a character for a command that works in text fields', () => {
    expect(judgeShortcut('palette.open', '/')).toMatchObject({
      status: 'refused',
      reason:
        '/ types a character in text fields, where Command palette works too, so its shortcut needs ⌘ or ⌃.',
    });
    expect(judgeShortcut('draft.save', 'Shift+1').status).toBe('refused');
    // With ⌘ or ⌃ it types nothing.
    expect(judgeShortcut('palette.open', 'Mod+/').status).toBe('free');
    expect(judgeShortcut('palette.open', 'Ctrl+/').status).toBe('free');
    // Commands that stay out of text fields may have one.
    expect(judgeShortcut('structure.layout', '/').status).toBe('free');

    setPlatform('other');
    expect(judgeShortcut('palette.open', '.').reason).toBe(
      '. types a character in text fields, where Command palette works too, so its shortcut needs Ctrl or Alt.',
    );
    expect(
      assignShortcut('palette.open', '/', { storage: noStorage() }).ok,
    ).toBe(false);
    expect(commandKeys('palette.open')).toEqual(['Mod+K']);
  });

  test('on macOS, refuses ⌥ without ⌘ or ⌃ for a command that works in text fields', () => {
    // Option types characters there: ⌥E is the acute accent's dead key,
    // ⌥/ types ÷ and ⌥⇧K the Apple logo.
    expect(judgeShortcut('palette.open', 'Alt+E')).toMatchObject({
      status: 'refused',
      reason:
        '⌥E types a character in text fields, where Command palette works too, so its shortcut needs ⌘ or ⌃.',
    });
    expect(judgeShortcut('draft.save', 'Alt+/').status).toBe('refused');
    expect(judgeShortcut('palette.open', 'Alt+Shift+K').reason).toMatch(
      /^⌥⇧K types a character in text fields/,
    );
    expect(
      recorderMessage(judgeShortcut('palette.open', 'Alt+E'), 'Command palette')
        .spoken,
    ).toBe(
      'Option+E types a character in text fields, where Command palette works too, so its shortcut needs ⌘ or ⌃. Choose another.',
    );
    expect(
      assignShortcut('palette.open', 'Alt+E', { storage: noStorage() }).ok,
    ).toBe(false);
    expect(commandKeys('palette.open')).toEqual(['Mod+K']);

    // With ⌘ or ⌃ too it types nothing, and commands that stay out of text
    // fields may have it.
    expect(judgeShortcut('palette.open', 'Mod+Alt+E').status).toBe('free');
    expect(judgeShortcut('palette.open', 'Ctrl+Alt+E').status).toBe('free');
    expect(judgeShortcut('structure.layout', 'Alt+E').status).toBe('free');

    // Alt types nothing on Windows and Linux; AltGr, their Ctrl+Alt, does,
    // and is refused for every command.
    setPlatform('other');
    expect(judgeShortcut('palette.open', 'Alt+E').status).toBe('free');
    expect(judgeShortcut('draft.save', 'Alt+/').status).toBe('free');
    expect(judgeShortcut('palette.open', 'Ctrl+Alt+E').reason).toMatch(
      /Ctrl\+Alt types characters/,
    );
  });

  test('refuses the keys the controls use, with any modifiers', () => {
    expect(judgeShortcut('structure.layout', 'Mod+Backspace').status).toBe(
      'refused',
    );
    expect(judgeShortcut('structure.layout', 'Alt+ArrowUp').status).toBe(
      'refused',
    );
    setPlatform('other');
    expect(judgeShortcut('structure.layout', 'Mod+Enter').status).toBe(
      'refused',
    );
  });

  test('knows a key the command has already', () => {
    expect(judgeShortcut('edit.duplicate', 'Mod+D')).toMatchObject({
      status: 'same',
      others: 0,
    });

    setPlatform('other');
    expect(judgeShortcut('edit.redo', 'Mod+Y')).toMatchObject({
      status: 'same',
      others: 1,
    });
    expect(judgeShortcut('edit.redo', 'Mod+J').others).toBe(2);
  });

  test('keeps a single key, and says it waits for the switch', () => {
    setSingleKeyShortcuts(false, null);

    const verdict = judgeShortcut('structure.layout', '1');

    expect(verdict).toMatchObject({ status: 'free', inactive: true });
    expect(recorderMessage(verdict, 'Auto layout').spoken).toBe(
      '1 is free. Press Enter to keep it, or Escape to cancel. ' +
        'Single-key shortcuts are off, so it works only once they are on.',
    );
  });

  test('says what it found, with the key as caps and as words', () => {
    const refused = recorderMessage(
      judgeShortcut('structure.layout', 'Mod+T'),
      'Auto layout',
    );

    expect(refused.tone).toBe('error');
    expect(refused.parts).toEqual([
      { text: 'Browsers keep ' },
      { key: true },
      { text: ' to open a new tab and never pass it to the page.' },
      { text: ' Choose another.' },
    ]);
    expect(refused.spoken).toBe(
      'Browsers keep Command+T to open a new tab and never pass it to the page. Choose another.',
    );

    // A reason that does not name the key follows it.
    expect(
      recorderMessage(judgeShortcut('structure.layout', 'Q'), 'Auto layout')
        .spoken,
    ).toMatch(/^Q: Letters without ⌘/);
    // The key is named once, as a word of its own: L is not the L of
    // "Letters", and '.' not the stop that ends the sentence.
    expect(
      recorderMessage(judgeShortcut('structure.layout', 'L'), 'Auto layout')
        .parts[0],
    ).toEqual({ key: true });
    expect(
      recorderMessage(judgeShortcut('structure.layout', 'L'), 'Auto layout')
        .spoken,
    ).toMatch(/^L: Letters without ⌘/);
    const dot = recorderMessage(
      judgeShortcut('palette.open', '.'),
      'Command palette',
    );
    expect(dot.parts.filter((part) => part.key)).toHaveLength(1);
    expect(dot.spoken).toBe(
      '. types a character in text fields, where Command palette works too, so its shortcut needs ⌘ or ⌃. Choose another.',
    );

    const conflict = recorderMessage(
      judgeShortcut('draft.export', 'Mod+D'),
      'Export',
    );
    expect(conflict.tone).toBe('warning');
    expect(conflict.spoken).toBe(
      'Command+D already runs Duplicate. Choose Use for Export to move it, or press another shortcut.',
    );

    setPlatform('other');
    expect(
      recorderMessage(judgeShortcut('edit.redo', 'Mod+J'), 'Redo').spoken,
    ).toBe(
      'Ctrl+J is free. Press Enter to use it instead, or Escape to cancel.',
    );
    expect(
      recorderMessage(judgeShortcut('edit.redo', 'Mod+Y'), 'Redo').spoken,
    ).toBe(
      'Redo already uses Ctrl+Y. Press Enter to make it the only one, or press another shortcut.',
    );
  });
});

describe('keeping a key', () => {
  test('makes it the command’s only key, stored per browser', () => {
    const storage = memoryStorage();
    const result = assignShortcut('structure.layout', 'Mod+Shift+L', {
      storage,
    });

    expect(result).toMatchObject({ ok: true, taken: [] });
    expect(commandKeys('structure.layout')).toEqual(['Mod+Shift+L']);
    expect(storage.saved()).toEqual({
      keys: { 'structure.layout': ['Mod+Shift+L'] },
      singleKeys: true,
    });

    // Everything written from the registry follows at once.
    expect(withShortcut('Auto layout', 'structure.layout')).toBe(
      'Auto layout (⇧⌘L)',
    );
    expect(ariaShortcuts('structure.layout')).toBe('Shift+Meta+L');
  });

  test('takes a key from another command only when told to', () => {
    const storage = memoryStorage();

    expect(assignShortcut('draft.export', 'Mod+D', { storage }).ok).toBe(false);
    expect(commandKeys('edit.duplicate')).toEqual(['Mod+D']);
    expect(storage.saved()).toBe(null);

    const result = assignShortcut('draft.export', 'Mod+D', {
      take: true,
      storage,
    });

    expect(result.ok).toBe(true);
    expect(result.taken).toEqual([
      { id: 'edit.duplicate', name: 'Duplicate', keys: [] },
    ]);
    expect(commandKeys('draft.export')).toEqual(['Mod+D']);
    expect(commandKeys('edit.duplicate')).toEqual([]);
    expect(storage.saved().keys).toEqual({
      'edit.duplicate': [],
      'draft.export': ['Mod+D'],
    });
  });

  test('leaves the other keys of the command it takes one from', () => {
    setPlatform('other');

    const result = assignShortcut('structure.layout', 'Mod+Y', {
      take: true,
      storage: null,
    });

    expect(result.taken).toEqual([
      { id: 'edit.redo', name: 'Redo', keys: ['Mod+Shift+Z'] },
    ]);
    expect(commandKeys('edit.redo')).toEqual(['Mod+Shift+Z']);
    expect(spokenKeys(commandKeys('edit.redo'))).toBe('Ctrl+Shift+Z');
  });

  test('never keeps a refused key, nor one for a fixed command', () => {
    expect(assignShortcut('structure.layout', 'Mod+T', { take: true }).ok).toBe(
      false,
    );
    expect(assignShortcut('edit.rename', 'Mod+Shift+F', {}).ok).toBe(false);
    expect(keymapState.overrides).toEqual({});
  });

  test('resets a command given its default keys back', () => {
    setShortcut('edit.duplicate', ['Mod+Shift+D'], null);
    expect(shortcutOverride('edit.duplicate')).toEqual(['Mod+Shift+D']);

    expect(
      assignShortcut('edit.duplicate', 'Mod+D', { storage: null }).ok,
    ).toBe(true);
    expect(shortcutOverride('edit.duplicate')).toBeUndefined();
    expect(shortcutRow('edit.duplicate').changed).toBe(false);
  });

  test('removes a command’s keys, or resets one that has none by default', () => {
    removeShortcut('edit.duplicate', { storage: null });
    expect(commandKeys('edit.duplicate')).toEqual([]);
    expect(shortcutRow('edit.duplicate')).toMatchObject({
      changed: true,
      defaults: [expect.objectContaining({ label: '⌘D' })],
    });

    setShortcut('structure.layout', ['Mod+Shift+L'], null);
    removeShortcut('structure.layout', { storage: null });
    expect(shortcutOverride('structure.layout')).toBeUndefined();
  });

  test('turning single keys off updates the Keyboard help', () => {
    expect(canvasHelp({ readOnly: false }).join(' ')).toContain('? lists');

    setSingleKeyShortcuts(false, null);
    expect(canvasHelp({ readOnly: false }).join(' ')).not.toContain('? lists');
    expect(ariaShortcuts('view.zoomIn')).toBeUndefined();
  });
});
