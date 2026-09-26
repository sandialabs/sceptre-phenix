import { beforeEach, describe, expect, test } from 'vitest';

import {
  RECENT_LIMIT,
  RECENT_STORAGE_KEY,
  clearRecent,
  readRecent,
  rememberCommand,
} from '@/builder/recent.js';

function memoryStorage(initial = {}) {
  const items = { ...initial };

  return {
    items,
    getItem: (key) => (key in items ? items[key] : null),
    setItem: (key, value) => {
      items[key] = String(value);
    },
    removeItem: (key) => {
      delete items[key];
    },
  };
}

function throwing() {
  const fail = () => {
    throw new Error('SecurityError');
  };

  return { getItem: fail, setItem: fail, removeItem: fail };
}

describe('recent commands', () => {
  beforeEach(() => clearRecent(memoryStorage()));

  test('are kept most recent first, with their choices, per browser', () => {
    const storage = memoryStorage();

    expect(readRecent(storage)).toEqual([]);
    rememberCommand('structure.layout', [], storage);
    rememberCommand('add.device', ['router'], storage);

    expect(readRecent(storage)).toEqual([
      { id: 'add.device', choices: ['router'] },
      { id: 'structure.layout', choices: [] },
    ]);
    expect(JSON.parse(storage.items[RECENT_STORAGE_KEY])).toEqual(
      readRecent(storage),
    );
    expect(RECENT_STORAGE_KEY).toBe('phenix.builder.recentCommands');
  });

  test('a repeat moves to the front; other choices are another entry', () => {
    const storage = memoryStorage();

    rememberCommand('add.device', ['router'], storage);
    rememberCommand('add.device', ['server'], storage);
    rememberCommand('structure.layout', [], storage);
    rememberCommand('add.device', ['router'], storage);

    expect(readRecent(storage)).toEqual([
      { id: 'add.device', choices: ['router'] },
      { id: 'structure.layout', choices: [] },
      { id: 'add.device', choices: ['server'] },
    ]);
  });

  test(`at most ${RECENT_LIMIT} are kept`, () => {
    const storage = memoryStorage();

    for (let i = 0; i < RECENT_LIMIT + 2; i += 1) {
      rememberCommand(`command.${i}`, [], storage);
    }

    const ids = readRecent(storage).map((entry) => entry.id);
    expect(ids).toHaveLength(RECENT_LIMIT);
    expect(ids[0]).toBe(`command.${RECENT_LIMIT + 1}`);
  });

  test('a damaged or foreign entry is ignored', () => {
    expect(
      readRecent(memoryStorage({ [RECENT_STORAGE_KEY]: '{not json' })),
    ).toEqual([]);
    expect(
      readRecent(
        memoryStorage({
          [RECENT_STORAGE_KEY]: JSON.stringify([
            { id: 'edit.undo', choices: ['a', 3] },
            { id: 7 },
            null,
            'draft.save',
          ]),
        }),
      ),
    ).toEqual([{ id: 'edit.undo', choices: ['a'] }]);
  });

  test('without storage, or when it refuses, the list lasts as long as the page', () => {
    rememberCommand('structure.layout', [], null);
    expect(readRecent(null)).toEqual([{ id: 'structure.layout', choices: [] }]);

    const blocked = throwing();
    expect(() => rememberCommand('edit.undo', [], blocked)).not.toThrow();
    expect(readRecent(blocked)[0]).toEqual({ id: 'edit.undo', choices: [] });

    // Storage that can be read but not written would hand back its older
    // list; the page's own is used instead.
    const full = memoryStorage({
      [RECENT_STORAGE_KEY]: JSON.stringify([{ id: 'old', choices: [] }]),
    });
    full.setItem = () => {
      throw new Error('QuotaExceededError');
    };
    rememberCommand('draft.save', [], full);
    expect(readRecent(full)[0].id).toBe('draft.save');
    expect(() => clearRecent(blocked)).not.toThrow();
  });
});
