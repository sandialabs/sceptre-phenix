import { describe, expect, test } from 'vitest';

import { DEFAULT_HISTORY_LIMIT, History } from '@/builder/history.js';

describe('history', () => {
  test('keeps the same number of snapshots the server retains', () => {
    expect(DEFAULT_HISTORY_LIMIT).toBe(50);

    const history = new History({ n: 0 });

    for (let i = 1; i <= 150; i += 1) {
      history.push({ n: i }, `step ${i}`);
    }

    expect(history.size).toBe(50);
    expect(history.current()).toEqual({ n: 150 });
    expect(history.entries[0].snapshot).toEqual({ n: 101 });
  });

  test('a longer history kept before the limit was lowered is trimmed on restore', () => {
    const history = new History({ n: 0 });
    const entries = Array.from({ length: 100 }, (_, i) => ({
      id: `c${i}`,
      label: `step ${i}`,
      snapshot: { n: i },
    }));

    history.restore(entries, 79);

    expect(history.size).toBe(DEFAULT_HISTORY_LIMIT);
    expect(history.entries[0].id).toBe('c50');
    expect(history.current()).toEqual({ n: 79 });
  });

  test('the oldest entries can be dropped, never the current one', () => {
    const history = new History({ n: 0 });

    for (let i = 1; i <= 4; i += 1) {
      history.push({ n: i }, `step ${i}`);
    }

    history.undo();

    expect(history.trimOldest(2)).toBe(2);
    expect(history.entries.map((entry) => entry.snapshot.n)).toEqual([2, 3, 4]);
    expect(history.current()).toEqual({ n: 3 });
    expect(history.trimOldest(5)).toBe(1);
    expect(history.canUndo()).toBe(false);
    expect(history.current()).toEqual({ n: 3 });
  });

  test('every commit gets its own id and label', () => {
    const history = new History({ n: 0 });
    const first = history.push({ n: 1 }, 'added a device');
    const second = history.push({ n: 2 }, 'added a switch');

    expect(first.id).not.toBe(second.id);
    expect(history.currentEntry().label).toBe('added a switch');
    expect(history.undoLabel()).toBe('added a switch');
  });

  test('undo and redo walk the stack', () => {
    const history = new History({ n: 0 });

    history.push({ n: 1 }, 'one');
    history.push({ n: 2 }, 'two');

    expect(history.undo()).toEqual({ n: 1 });
    expect(history.canRedo()).toBe(true);
    expect(history.redo()).toEqual({ n: 2 });
    expect(history.canRedo()).toBe(false);
  });

  test('a commit after undo drops the redo branch', () => {
    const history = new History({ n: 0 });

    history.push({ n: 1 }, 'one');
    history.push({ n: 2 }, 'two');
    history.undo();
    history.push({ n: 3 }, 'three');

    expect(history.canRedo()).toBe(false);
    expect(history.entries.map((entry) => entry.snapshot)).toEqual([
      { n: 0 },
      { n: 1 },
      { n: 3 },
    ]);
  });

  test('a recovered ordered log restores entries and cursor', () => {
    const history = new History({ n: 0 });
    const entries = [
      { id: 'a', label: 'one', snapshot: { n: 1 } },
      { id: 'b', label: 'two', snapshot: { n: 2 } },
      { id: 'c', label: 'three', snapshot: { n: 3 } },
    ];

    history.restore(entries, 1);

    expect(history.size).toBe(3);
    expect(history.current()).toEqual({ n: 2 });
    expect(history.canRedo()).toBe(true);
  });

  test('reset starts a fresh log', () => {
    const history = new History({ n: 0 });

    history.push({ n: 1 }, 'one');
    history.reset({ n: 9 }, 'loaded');

    expect(history.size).toBe(1);
    expect(history.canUndo()).toBe(false);
    expect(history.current()).toEqual({ n: 9 });
  });
});
