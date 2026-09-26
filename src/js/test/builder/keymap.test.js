import { afterEach, describe, expect, test } from 'vitest';

import {
  SHORTCUTS_STORAGE_KEY,
  ariaKey,
  currentPlatform,
  detectPlatform,
  eventToKey,
  findConflicts,
  isCharacterKey,
  keyCaps,
  keyLabel,
  keyRefusal,
  keyText,
  keymapState,
  loadShortcutSettings,
  matchesKey,
  normalizeKey,
  parseKey,
  reservedReason,
  resetAllShortcuts,
  resetShortcut,
  sameKey,
  setPlatform,
  setShortcut,
  setSingleKeyShortcuts,
  shortcutOverride,
  spokenKey,
  typesCharacter,
  unbindShortcut,
} from '@/builder/keymap.js';

function fakeStorage(initial = {}) {
  const data = { ...initial };

  return {
    data,
    getItem: (key) => (key in data ? data[key] : null),
    setItem: (key, value) => {
      data[key] = String(value);
    },
  };
}

const brokenStorage = {
  getItem: () => {
    throw new Error('SecurityError');
  },
  setItem: () => {
    throw new Error('QuotaExceededError');
  },
};

// A KeyboardEvent as the dispatcher reads it.
function press(key, code, mods = {}) {
  return {
    key,
    code,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    shiftKey: false,
    getModifierState: (name) => Boolean(mods.altGraph && name === 'AltGraph'),
    ...mods,
  };
}

afterEach(() => {
  setPlatform(null);
  loadShortcutSettings(fakeStorage());
});

describe('key specs', () => {
  test('parse into a key and its modifiers, written one way', () => {
    expect(parseKey('Mod+K')).toMatchObject({
      spec: 'Mod+K',
      key: 'K',
      kind: 'letter',
      mod: true,
      shift: false,
    });
    expect(normalizeKey('Shift+Mod+g')).toBe('Mod+Shift+G');
    expect(normalizeKey('shift+cmd+z')).toBe('Meta+Shift+Z');
    expect(parseKey('1')).toMatchObject({ key: '1', kind: 'digit' });
    expect(parseKey('Shift+1')).toMatchObject({ kind: 'digit', shift: true });
    expect(parseKey('?')).toMatchObject({ key: '?', kind: 'char' });
    expect(parseKey('+')).toMatchObject({ key: '+', kind: 'char' });
    expect(parseKey('Mod++')).toMatchObject({ key: '+', mod: true });
    expect(normalizeKey('esc')).toBe('Escape');
    expect(normalizeKey('Delete')).toBe('Delete');
    expect(normalizeKey('f2')).toBe('F2');
    // Shift is what types a character.
    expect(normalizeKey('Shift+?')).toBe('?');
  });

  test('refuse what is not one key with modifiers', () => {
    for (const spec of [
      '',
      'Mod',
      'Mod+',
      'K+J',
      'Mod+Mod+K',
      'Hyper+K',
      'Mod+Ctrl+K',
      'Mod+Meta+K',
      'Mod+NoSuchKey',
      'Mod+é',
      null,
    ]) {
      expect(parseKey(spec), String(spec)).toBeNull();
    }
  });

  test('know which keys are one character (WCAG 2.1.4)', () => {
    for (const spec of ['?', '=', '-', '+', 'Shift+1', '1']) {
      expect(isCharacterKey(spec), spec).toBe(true);
    }
    for (const spec of ['Mod+K', 'Alt+1', 'Delete', 'F2', 'Escape']) {
      expect(isCharacterKey(spec), spec).toBe(false);
    }
  });

  test('know which keys type a character in a text field', () => {
    // Character keys, on every platform.
    for (const platform of ['mac', 'other']) {
      for (const spec of ['?', '/', 'Shift+1', '1']) {
        expect(typesCharacter(spec, platform), `${spec} ${platform}`).toBe(
          true,
        );
      }
    }

    // On macOS, Option with or without Shift, but without ⌘ or ⌃: ⌥E is the
    // acute accent's dead key, ⌥/ types ÷ and ⌥⇧K the Apple logo.
    for (const spec of ['Alt+E', 'Alt+/', 'Alt+Shift+K', 'Alt+1']) {
      expect(typesCharacter(spec, 'mac'), spec).toBe(true);
      // Alt types nothing on Windows and Linux.
      expect(typesCharacter(spec, 'other'), spec).toBe(false);
    }
    for (const spec of [
      'Mod+Alt+E',
      'Ctrl+Alt+E',
      'Mod+K',
      'Ctrl+K',
      'Alt+F2',
      'Alt+ArrowUp',
      'F2',
      'nonsense',
    ]) {
      expect(typesCharacter(spec, 'mac'), spec).toBe(false);
    }
  });

  test('compare as the platform resolves Mod', () => {
    expect(sameKey('Mod+K', 'Meta+K', 'mac')).toBe(true);
    expect(sameKey('Mod+K', 'Ctrl+K', 'mac')).toBe(false);
    expect(sameKey('Mod+K', 'Ctrl+K', 'other')).toBe(true);
    expect(sameKey('Mod+Shift+Z', 'Shift+Mod+Z', 'other')).toBe(true);
  });
});

describe('platform', () => {
  test('is macOS on a Mac, an iPad or an iPhone, and other elsewhere', () => {
    expect(detectPlatform({ platform: 'MacIntel' })).toBe('mac');
    expect(detectPlatform({ platform: 'iPad' })).toBe('mac');
    expect(detectPlatform({ platform: 'Win32' })).toBe('other');
    expect(detectPlatform({ platform: 'Linux x86_64' })).toBe('other');
    expect(
      detectPlatform({ platform: '', userAgentData: { platform: 'macOS' } }),
    ).toBe('mac');
    expect(
      detectPlatform({ userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17)' }),
    ).toBe('mac');
    expect(detectPlatform({})).toBe('other');
    expect(detectPlatform(null)).toBe('other');
  });

  // An emulated user agent (Playwright's device profiles) must not move the
  // labels away from the keys the machine sends.
  test('prefers navigator.platform to an emulated userAgentData', () => {
    expect(
      detectPlatform({
        platform: 'MacIntel',
        userAgentData: { platform: 'Windows' },
        userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64)',
      }),
    ).toBe('mac');
  });

  test('can be set, and detected again', () => {
    setPlatform('mac');
    expect(currentPlatform()).toBe('mac');
    setPlatform('other');
    expect(currentPlatform()).toBe('other');
    setPlatform(null);
    expect(['mac', 'other']).toContain(currentPlatform());
  });
});

describe('matching key presses', () => {
  test('Mod is ⌘ on macOS and Ctrl elsewhere, never both', () => {
    const cmdK = press('k', 'KeyK', { metaKey: true });
    const ctrlK = press('k', 'KeyK', { ctrlKey: true });

    expect(matchesKey(cmdK, 'Mod+K', 'mac')).toBe(true);
    expect(matchesKey(ctrlK, 'Mod+K', 'mac')).toBe(false);
    expect(matchesKey(ctrlK, 'Mod+K', 'other')).toBe(true);
    expect(matchesKey(cmdK, 'Mod+K', 'other')).toBe(false);
  });

  test('modifiers must be exactly the spec’s', () => {
    const redo = press('Z', 'KeyZ', { metaKey: true, shiftKey: true });

    expect(matchesKey(redo, 'Mod+Shift+Z', 'mac')).toBe(true);
    expect(matchesKey(redo, 'Mod+Z', 'mac')).toBe(false);
    expect(
      matchesKey(press('z', 'KeyZ', { metaKey: true }), 'Mod+Shift+Z', 'mac'),
    ).toBe(false);
    expect(
      matchesKey(
        press('k', 'KeyK', { ctrlKey: true, altKey: true }),
        'Mod+K',
        'other',
      ),
    ).toBe(false);
  });

  test('letters follow the layout, or the physical key when the layout types none', () => {
    // Dvorak: the key labelled K sends 'k' from another position.
    expect(
      matchesKey(press('k', 'KeyV', { ctrlKey: true }), 'Mod+K', 'other'),
    ).toBe(true);
    // Russian: the K position types л.
    expect(
      matchesKey(press('л', 'KeyK', { ctrlKey: true }), 'Mod+K', 'other'),
    ).toBe(true);
    // Option changes the character on macOS: ⌥G types ©.
    expect(
      matchesKey(press('©', 'KeyG', { altKey: true }), 'Alt+G', 'mac'),
    ).toBe(true);
  });

  test('digits match the physical key, on any layout', () => {
    // US: Shift+1 types !; AZERTY: Shift+& types 1.
    expect(
      matchesKey(press('!', 'Digit1', { shiftKey: true }), 'Shift+1', 'mac'),
    ).toBe(true);
    expect(
      matchesKey(press('1', 'Digit1', { shiftKey: true }), 'Shift+1', 'other'),
    ).toBe(true);
    expect(matchesKey(press('1', 'Digit1'), 'Shift+1', 'other')).toBe(false);
    expect(matchesKey(press('1', 'Numpad1'), '1', 'other')).toBe(true);
    expect(
      matchesKey(press('1', 'Numpad1', { shiftKey: true }), 'Shift+1', 'other'),
    ).toBe(false);
  });

  test('characters match what was typed, whatever Shift or AltGr it took', () => {
    expect(matchesKey(press('?', 'Slash', { shiftKey: true }), '?')).toBe(true);
    expect(matchesKey(press('?', 'Minus', { shiftKey: true }), '?')).toBe(true);
    expect(matchesKey(press('=', 'Equal'), '=')).toBe(true);
    expect(matchesKey(press('+', 'NumpadAdd'), '+')).toBe(true);
    expect(matchesKey(press('-', 'Minus'), '-')).toBe(true);
    // AltGr, reported as Ctrl+Alt on Windows.
    expect(
      matchesKey(
        press('?', 'Minus', { ctrlKey: true, altKey: true, altGraph: true }),
        '?',
        'other',
      ),
    ).toBe(true);
    expect(
      matchesKey(press('?', 'Slash', { ctrlKey: true, shiftKey: true }), '?'),
    ).toBe(false);
  });

  test('named keys match by name', () => {
    expect(matchesKey(press(' ', 'Space'), 'Space')).toBe(true);
    expect(matchesKey(press('Delete', 'Delete'), 'Delete')).toBe(true);
    expect(matchesKey(press('Backspace', 'Backspace'), 'Delete')).toBe(false);
    expect(matchesKey(press('F2', 'F2'), 'F2')).toBe(true);
    expect(
      matchesKey(press('Enter', 'Enter', { shiftKey: true }), 'Enter'),
    ).toBe(false);
  });
});

describe('labels', () => {
  test('macOS writes symbols in its own order, others words joined by +', () => {
    expect(keyLabel('Mod+K', 'mac')).toBe('⌘K');
    expect(keyLabel('Mod+K', 'other')).toBe('Ctrl+K');
    expect(keyLabel('Shift+Mod+G', 'mac')).toBe('⇧⌘G');
    expect(keyLabel('Shift+Mod+G', 'other')).toBe('Ctrl+Shift+G');
    expect(keyLabel('Ctrl+Alt+Shift+Meta+K', 'mac')).toBe('⌃⌥⇧⌘K');
    expect(keyLabel('Shift+1', 'mac')).toBe('⇧1');
    expect(keyLabel('Shift+1', 'other')).toBe('Shift+1');
    expect(keyLabel('-', 'other')).toBe('−');
    expect(keyLabel('Backspace', 'mac')).toBe('⌫');
    expect(keyLabel('Delete', 'mac')).toBe('⌦');
    expect(keyLabel('Escape', 'other')).toBe('Esc');
    expect(keyCaps('Mod+Shift+Z', 'mac')).toEqual(['⇧', '⌘', 'Z']);
    expect(keyCaps('Mod+Shift+Z', 'other')).toEqual(['Ctrl', 'Shift', 'Z']);
    expect(keyCaps('nonsense+K')).toEqual([]);
  });

  test('running text names the named keys as the keyboard does', () => {
    expect(keyText('Backspace', 'mac')).toBe('Delete');
    expect(keyText('Delete', 'mac')).toBe('Forward Delete');
    expect(keyText('Enter', 'mac')).toBe('Return');
    expect(keyText('Enter', 'other')).toBe('Enter');
    expect(keyText('Mod+A', 'mac')).toBe('⌘A');
    expect(spokenKey('Mod+Shift+G', 'mac')).toBe('Shift+Command+G');
    expect(spokenKey('Mod+Shift+G', 'other')).toBe('Ctrl+Shift+G');
  });

  test('aria-keyshortcuts names the keys pressed on this platform', () => {
    expect(ariaKey('Mod+K', 'mac')).toBe('Meta+K');
    expect(ariaKey('Mod+K', 'other')).toBe('Control+K');
    expect(ariaKey('Mod+Shift+Z', 'mac')).toBe('Shift+Meta+Z');
    expect(ariaKey('Mod+Y', 'other')).toBe('Control+Y');
    // The keys that type a shifted character, not the character (WAI-ARIA
    // 1.2 gives "Shift+5" for "%", MDN "Shift+2" for "@").
    expect(ariaKey('?', 'other')).toBe('Shift+/');
    expect(ariaKey('?', 'mac')).toBe('Shift+/');
    expect(ariaKey('%', 'mac')).toBe('Shift+5');
    expect(ariaKey('@', 'other')).toBe('Shift+2');
    expect(ariaKey('"', 'other')).toBe("Shift+'");
    expect(ariaKey('+', 'other')).toBe('Shift+=');
    expect(ariaKey('Mod++', 'mac')).toBe('Shift+Meta+=');
    expect(ariaKey('=', 'mac')).toBe('=');
    expect(ariaKey('Shift+1', 'mac')).toBe('Shift+1');
    expect(ariaKey('Space', 'mac')).toBe('Space');
    expect(ariaKey('F2', 'other')).toBe('F2');
    expect(ariaKey('nope+nope')).toBe('');
  });
});

describe('keys a page must not take', () => {
  test('browser and OS keys are reserved, with the reason', () => {
    expect(reservedReason('Mod+T', 'mac')).toBe(
      'Browsers keep ⌘T to open a new tab and never pass it to the page.',
    );
    expect(reservedReason('Mod+Shift+P', 'other')).toMatch(/private window/);
    expect(reservedReason('Mod+W', 'other')).toMatch(/close the tab/);
    expect(reservedReason('Mod+3', 'other')).toMatch(/switch tabs/);
    expect(reservedReason('Mod+=', 'other')).toMatch(/zoom/);
    expect(reservedReason('Mod+-', 'mac')).toMatch(/zoom/);
    expect(reservedReason('F5', 'other')).toMatch(/reload/);
    expect(reservedReason('Meta+Space', 'mac')).toMatch(/^macOS uses ⌘Space/);
    expect(reservedReason('Meta+Space', 'other')).toBe('');
    expect(reservedReason('Mod+Q', 'mac')).toMatch(/quit/);
    expect(reservedReason('Mod+K', 'mac')).toBe('');
    expect(reservedReason('Mod+K', 'other')).toBe('');
  });

  test('refusals also cover AltGr, lone letters and the keys controls use', () => {
    expect(keyRefusal('Mod+Shift+L', 'other')).toBe('');
    expect(keyRefusal('Mod+T', 'other')).toMatch(/new tab/);
    expect(keyRefusal('Ctrl+Alt+K', 'other')).toMatch(/Ctrl\+Alt/);
    expect(keyRefusal('Ctrl+Alt+K', 'mac')).toBe('');
    expect(keyRefusal('K', 'mac')).toMatch(/^Letters without ⌘/);
    expect(keyRefusal('Shift+K', 'other')).toMatch(/^Letters without Ctrl/);
    expect(keyRefusal('Tab', 'other')).toBe(
      "Tab already moves around or operates the Builder's controls.",
    );
    expect(keyRefusal('Shift+ArrowUp', 'mac')).toMatch(/operates/);
    // The controls take these keys with any modifiers, before the view's
    // shortcuts see them: ⌘⌫ deletes, ⌥↑ moves nodes, Ctrl+Enter selects.
    expect(keyRefusal('Mod+Backspace', 'mac')).toBe(
      "⌘⌫ already moves around or operates the Builder's controls, which take Delete with any modifiers.",
    );
    expect(keyRefusal('Alt+ArrowUp', 'mac')).toMatch(/operates/);
    expect(keyRefusal('Mod+Enter', 'other')).toMatch(
      /^Ctrl\+Enter already .* take Enter with any modifiers\.$/,
    );
    for (const spec of [
      'Mod+Shift+Delete',
      'Alt+Tab',
      'Ctrl+Escape',
      'Mod+Space',
      'Alt+Home',
      'Mod+End',
      'Shift+PageDown',
      'Mod+F2',
    ]) {
      expect(keyRefusal(spec, 'other'), spec).not.toBe('');
    }
    // Other named keys stay free.
    expect(keyRefusal('Mod+F3', 'other')).toBe('');
    expect(keyRefusal('?', 'mac')).toBe('');
    expect(keyRefusal('what', 'mac')).toMatch(/not a key combination/);
  });
});

describe('recording', () => {
  test('a press becomes a spec, with the platform’s command key as Mod', () => {
    expect(
      eventToKey(press('L', 'KeyL', { metaKey: true, shiftKey: true }), 'mac'),
    ).toBe('Mod+Shift+L');
    expect(eventToKey(press('l', 'KeyL', { ctrlKey: true }), 'other')).toBe(
      'Mod+L',
    );
    expect(eventToKey(press('l', 'KeyL', { ctrlKey: true }), 'mac')).toBe(
      'Ctrl+L',
    );
    expect(eventToKey(press('©', 'KeyG', { altKey: true }), 'mac')).toBe(
      'Alt+G',
    );
    expect(eventToKey(press('–', 'Minus', { altKey: true }), 'mac')).toBe(
      'Alt+-',
    );
    expect(eventToKey(press('?', 'Slash', { shiftKey: true }), 'mac')).toBe(
      '?',
    );
    expect(eventToKey(press('!', 'Digit1', { shiftKey: true }), 'mac')).toBe(
      'Shift+1',
    );
    expect(eventToKey(press('F2', 'F2'), 'other')).toBe('F2');
    expect(eventToKey(press('Shift', 'ShiftLeft', { shiftKey: true }))).toBe(
      '',
    );
    expect(eventToKey(press('Dead', 'Quote'))).toBe('');
  });
});

describe('customization', () => {
  test('reads overrides and the single-key switch, dropping what it cannot use', () => {
    loadShortcutSettings(
      fakeStorage({
        [SHORTCUTS_STORAGE_KEY]: JSON.stringify({
          keys: {
            'structure.layout': ['Shift+Mod+L', 'nonsense+'],
            'edit.undo': [],
            broken: 'Mod+K',
          },
          singleKeys: false,
        }),
      }),
    );

    expect(shortcutOverride('structure.layout')).toEqual(['Mod+Shift+L']);
    expect(shortcutOverride('edit.undo')).toEqual([]);
    expect(shortcutOverride('broken')).toBeUndefined();
    expect(keymapState.singleKeys).toBe(false);
  });

  test('falls back to the defaults when storage is unreadable or blocked', () => {
    loadShortcutSettings(fakeStorage({ [SHORTCUTS_STORAGE_KEY]: '{oops' }));
    expect(keymapState.overrides).toEqual({});
    expect(keymapState.singleKeys).toBe(true);

    loadShortcutSettings(brokenStorage);
    expect(keymapState.overrides).toEqual({});
    expect(keymapState.singleKeys).toBe(true);

    loadShortcutSettings(null);
    expect(keymapState.singleKeys).toBe(true);
  });

  test('stores changes, resets one or all, and keeps the switch apart', () => {
    const storage = fakeStorage();

    setShortcut('structure.layout', ['shift+mod+l'], storage);
    unbindShortcut('edit.undo', storage);
    setSingleKeyShortcuts(false, storage);

    expect(JSON.parse(storage.data[SHORTCUTS_STORAGE_KEY])).toEqual({
      keys: { 'structure.layout': ['Mod+Shift+L'], 'edit.undo': [] },
      singleKeys: false,
    });

    resetShortcut('edit.undo', storage);
    expect(shortcutOverride('edit.undo')).toBeUndefined();
    expect(shortcutOverride('structure.layout')).toEqual(['Mod+Shift+L']);

    resetAllShortcuts(storage);
    expect(keymapState.overrides).toEqual({});
    expect(keymapState.singleKeys).toBe(false);
    expect(JSON.parse(storage.data[SHORTCUTS_STORAGE_KEY])).toEqual({
      keys: {},
      singleKeys: false,
    });
  });

  test('still applies when storage refuses to save', () => {
    setShortcut('structure.layout', ['Mod+Shift+L'], brokenStorage);
    setSingleKeyShortcuts(false, brokenStorage);

    expect(shortcutOverride('structure.layout')).toEqual(['Mod+Shift+L']);
    expect(keymapState.singleKeys).toBe(false);
  });

  test('finds the bindings a key clashes with on the platform', () => {
    const bindings = [
      { id: 'a', keys: ['Mod+D'] },
      { id: 'b', keys: ['Meta+D'] },
      { id: 'c', keys: ['Ctrl+D'] },
    ];

    expect(findConflicts('Mod+D', bindings, 'mac')).toEqual(['a', 'b']);
    expect(findConflicts('Mod+D', bindings, 'other')).toEqual(['a', 'c']);
  });
});
