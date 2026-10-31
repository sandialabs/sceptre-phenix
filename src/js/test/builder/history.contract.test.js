import { describe, expect, test } from 'vitest';
import fc from 'fast-check';

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

  test('a restored history longer than the limit keeps its newest entries', () => {
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

  test('any entries but the current one can be dropped, and undo skips them', () => {
    const history = new History({ n: 0 });

    for (let i = 1; i <= 4; i += 1) {
      history.push({ n: i }, `step ${i}`);
    }

    history.undo();

    expect(history.removeWhere((entry) => entry.snapshot.n % 2 === 1)).toBe(1);
    expect(history.entries.map((entry) => entry.snapshot.n)).toEqual([
      0, 2, 3, 4,
    ]);
    expect(history.current()).toEqual({ n: 3 });
    expect(history.removeWhere((entry) => entry.snapshot.n === 2)).toBe(1);
    expect(history.undo()).toEqual({ n: 0 });
    expect(history.redo()).toEqual({ n: 3 });
    expect(history.removeWhere(() => false)).toBe(0);
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

describe('history, for any edits', () => {
  const step = fc.oneof(
    { weight: 4, arbitrary: fc.constant({ op: 'push' }) },
    { weight: 2, arbitrary: fc.constant({ op: 'undo' }) },
    { weight: 2, arbitrary: fc.constant({ op: 'redo' }) },
    {
      weight: 1,
      arbitrary: fc.record({ op: fc.constant('trim'), n: fc.nat(5) }),
    },
    {
      weight: 1,
      arbitrary: fc.record({
        op: fc.constant('remove'),
        m: fc.integer({ min: 2, max: 4 }),
      }),
    },
  );

  test('behaves as a list and a cursor', () => {
    fc.assert(
      fc.property(
        fc.integer({ min: 1, max: 6 }),
        fc.array(step, { maxLength: 40, size: 'max' }),
        (limit, steps) => {
          let count = 0;
          const first = { n: count };
          const history = new History(first, limit);
          // The model: the snapshots, each a new object, and the cursor.
          let list = [first];
          let at = 0;

          for (const { op, n, m } of steps) {
            if (op === 'push') {
              const snapshot = { n: ++count };

              history.push(snapshot, `step ${count}`, `id-${count}`);
              list = [...list.slice(0, at + 1), snapshot].slice(-limit);
              at = list.length - 1;
            } else if (op === 'undo') {
              expect(history.undo()).toBe(list[Math.max(0, at - 1)]);
              at = Math.max(0, at - 1);
            } else if (op === 'redo') {
              expect(history.redo()).toBe(
                list[Math.min(list.length - 1, at + 1)],
              );
              at = Math.min(list.length - 1, at + 1);
            } else if (op === 'trim') {
              const dropped = Math.min(n, at);

              expect(history.trimOldest(n)).toBe(dropped);
              list = list.slice(dropped);
              at -= dropped;
            } else {
              const current = list[at];
              const kept = list.filter(
                (snapshot) => snapshot === current || snapshot.n % m !== 0,
              );

              expect(
                history.removeWhere((entry) => entry.snapshot.n % m === 0),
              ).toBe(list.length - kept.length);
              list = kept;
              at = kept.indexOf(current);
            }

            expect(history.size).toBe(list.length);
            expect(history.size).toBeLessThanOrEqual(limit);
            expect(history.index).toBe(at);
            expect(history.current()).toBe(list[at]);
            expect(history.canUndo()).toBe(at > 0);
            expect(history.canRedo()).toBe(at < list.length - 1);
            expect(history.redoLabel()).toBe(
              at < list.length - 1 ? `step ${list[at + 1].n}` : '',
            );
          }
        },
      ),
      { numRuns: 200 },
    );
  });

  test('takes as many undos as there were pushes, and as many redos back', () => {
    fc.assert(
      fc.property(fc.integer({ min: 0, max: 20 }), (pushes) => {
        const history = new History({ n: 0 });

        for (let n = 1; n <= pushes; n += 1) {
          history.push({ n }, `step ${n}`, `id-${n}`);
        }
        const top = history.current();
        let undos = 0;

        while (history.canUndo()) {
          history.undo();
          undos += 1;
        }
        expect(undos).toBe(pushes);
        expect(history.current()).toEqual({ n: 0 });
        while (history.canRedo()) {
          history.redo();
        }
        expect(history.current()).toBe(top);
      }),
      { numRuns: 100 },
    );
  });
});
