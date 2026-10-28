// The snapshots that the next change of a draft removes (newerHistory.js).

import fc from 'fast-check';
import { describe, expect, test } from 'vitest';

import {
  historyAfter,
  newerNotice,
  newerSnapshots,
  restoreNote,
} from '@/builder/newerHistory.js';

const LIST = [
  { id: 's1', createdBy: 'alice', createdAt: '2026-10-26T09:00:00Z' },
  { id: 's2', createdBy: 'alice', createdAt: '2026-10-26T09:05:00Z' },
  {
    id: 's3',
    createdBy: 'bob',
    createdAt: '2026-10-26T09:10:00Z',
    current: true,
  },
];

describe('newerSnapshots', () => {
  test('counts the snapshots after the cursor, with the newest', () => {
    expect(
      newerSnapshots({ snapshotId: 's1', cursor: 0, snapshots: 3 }, LIST, 's1'),
    ).toEqual({ count: 2, newest: LIST[2] });
    expect(
      newerSnapshots({ snapshotId: 's2', cursor: 1, snapshots: 3 }, LIST, 's2'),
    ).toEqual({ count: 1, newest: LIST[2] });
  });

  test('is null when the cursor is on the newest snapshot', () => {
    expect(
      newerSnapshots({ snapshotId: 's3', cursor: 2, snapshots: 3 }, LIST, 's3'),
    ).toBeNull();
  });

  test('is null while the editor does not show the snapshot at the cursor', () => {
    const head = { snapshotId: 's1', cursor: 0, snapshots: 3 };

    // A change not saved yet, and a cursor move not confirmed yet.
    expect(newerSnapshots(head, LIST, undefined)).toBeNull();
    expect(newerSnapshots(head, LIST, '')).toBeNull();
    expect(newerSnapshots(head, LIST, 's2')).toBeNull();
    expect(newerSnapshots(null, LIST, 's1')).toBeNull();
  });

  test('does not name the newest from a list that is not the server’s', () => {
    const head = { snapshotId: 's1', cursor: 0, snapshots: 4 };

    expect(newerSnapshots(head, LIST, 's1')).toEqual({
      count: 3,
      newest: null,
    });
    expect(
      newerSnapshots({ snapshotId: 'x', cursor: 0, snapshots: 3 }, LIST, 'x'),
    ).toEqual({ count: 2, newest: null });
    expect(
      newerSnapshots({ snapshotId: 's1', cursor: 0, snapshots: 3 }, null, 's1'),
    ).toEqual({ count: 2, newest: null });
  });
});

describe('historyAfter', () => {
  test('a save after the cursor takes the place of the snapshots after it', () => {
    const before = [{ id: 's1' }, { id: 's2', current: true }, { id: 's3' }];
    const saved = {
      id: 's4',
      createdBy: 'alice',
      createdAt: 't',
      summary: 'x',
    };

    expect(
      historyAfter(
        before,
        { snapshotId: 's4', cursor: 2, snapshots: 3 },
        saved,
      ),
    ).toEqual([
      { id: 's1', current: false },
      { id: 's2', current: false },
      { ...saved, current: true },
    ]);
  });

  test('the oldest go as the server drops them', () => {
    const before = [{ id: 's1' }, { id: 's2', current: true }];
    const saved = { id: 's3' };

    expect(
      historyAfter(
        before,
        { snapshotId: 's3', cursor: 1, snapshots: 2 },
        saved,
      ),
    ).toEqual([
      { id: 's2', current: false },
      { id: 's3', current: true },
    ]);
  });

  test('a cursor move marks the current snapshot', () => {
    expect(
      historyAfter(LIST, { snapshotId: 's1', cursor: 0, snapshots: 3 }),
    ).toEqual([
      { ...LIST[0], current: true },
      { ...LIST[1], current: false },
      { ...LIST[2], current: false },
    ]);
  });

  test('an answer that changes nothing keeps the list itself', () => {
    expect(
      historyAfter(LIST, { snapshotId: 's3', cursor: 2, snapshots: 3 }),
    ).toBe(LIST);
    expect(historyAfter(LIST, null)).toBe(LIST);
    // A save the list holds already, as after a read.
    expect(
      historyAfter(
        LIST,
        { snapshotId: 's3', cursor: 2, snapshots: 3 },
        { id: 's3' },
      ),
    ).toBe(LIST);
  });

  test('is never longer than the server’s list, and marks one current snapshot when it holds it', () => {
    const id = fc.constantFrom('a', 'b', 'c', 'd', 'e');
    const entry = fc.record({ id, current: fc.boolean() });

    fc.assert(
      fc.property(
        fc.uniqueArray(entry, { selector: (item) => item.id, maxLength: 5 }),
        id,
        fc.integer({ min: 1, max: 6 }),
        fc.boolean(),
        (list, snapshotId, snapshots, save) => {
          const head = { snapshotId, cursor: 0, snapshots };
          const after = historyAfter(
            list,
            head,
            save ? { id: snapshotId } : null,
          );

          expect(after.length).toBeLessThanOrEqual(snapshots);
          expect(after.filter((item) => item.current)).toEqual(
            after.filter((item) => item.id === snapshotId),
          );
          // A second answer of the same head changes nothing more.
          expect(historyAfter(after, head)).toBe(after);
        },
      ),
      { numRuns: 200 },
    );
  });
});

describe('newerNotice', () => {
  const time = (value) => `at ${value}`;

  test('says how many, and who saved the newest and when', () => {
    expect(
      newerNotice({ count: 2, newest: LIST[2] }, { me: 'alice', time }),
    ).toBe(
      'Your next change removes 2 newer snapshots (latest by bob, at 2026-10-26T09:10:00Z).',
    );
    expect(
      newerNotice({ count: 1, newest: LIST[1] }, { me: 'alice', time }),
    ).toBe(
      'Your next change removes 1 newer snapshot (latest by you, at 2026-10-26T09:05:00Z).',
    );
  });

  test('says only the count when the newest is not known', () => {
    expect(newerNotice({ count: 3, newest: null })).toBe(
      'Your next change removes 3 newer snapshots.',
    );
    expect(newerNotice({ count: 1, newest: { id: 's9' } })).toBe(
      'Your next change removes 1 newer snapshot.',
    );
  });

  test('names the draft the newest was saved as', () => {
    expect(
      newerNotice(
        { count: 1, newest: null, saved: 'Pump station (copy)' },
        { time },
      ),
    ).toBe(
      'Your next change removes 1 newer snapshot. The latest is saved as draft Pump station (copy).',
    );
  });
});

describe('restoreNote', () => {
  test('counts the snapshots after the one restored', () => {
    expect(restoreNote(0, 3)).toBe(
      'Your next change after this restore removes 2 newer snapshots',
    );
    expect(restoreNote(1, 3)).toBe(
      'Your next change after this restore removes 1 newer snapshot',
    );
    expect(restoreNote(2, 3)).toBe('');
  });
});
