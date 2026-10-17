// How a page merges a list it loaded with the rows websocket messages changed,
// ordered by the server's sequence (see freshness.js).
import fc from 'fast-check';
import { expect, it } from 'vitest';
import { createLiveRows } from '@/utils/liveRows.js';

// the server read the list at seq 15: a message with seq 10 came before it,
// one with seq 20 after it
const LIST_SEQ = 15;

// Each case: the changes the page recorded ([touch or remove, row, seq]), the
// rows loaded, the rows on the page, and the rows the merge keeps.
it.each([
  [
    'keeps a row updated after the list was read',
    [['touch', 'demo', 20]],
    [
      { name: 'demo', status: 'started' },
      { name: 'other', status: 'started' },
    ],
    [{ name: 'demo', status: 'stopped' }],
    [
      { name: 'demo', status: 'stopped' },
      { name: 'other', status: 'started' },
    ],
  ],
  [
    'takes the loaded row when the update came before the list was read',
    [['touch', 'demo', 10]],
    [{ name: 'demo', status: 'started' }],
    [{ name: 'demo', status: 'stopped' }],
    [{ name: 'demo', status: 'started' }],
  ],
  [
    'drops a row the list no longer has',
    [['touch', 'gone', 10]],
    [],
    [{ name: 'gone' }],
    [],
  ],
  [
    'keeps a row added after the list was read',
    [['touch', 'new', 20]],
    [{ name: 'old' }],
    [{ name: 'old' }, { name: 'new', status: 'starting' }],
    [{ name: 'old' }, { name: 'new', status: 'starting' }],
  ],
  [
    'leaves out a row deleted after the list was read',
    [['remove', 'gone', 20]],
    [{ name: 'gone' }, { name: 'kept' }],
    [{ name: 'kept' }],
    [{ name: 'kept' }],
  ],
  [
    'keeps a row created again after it was deleted',
    [
      ['remove', 'demo', 20],
      ['touch', 'demo', 21],
    ],
    [{ name: 'demo', status: 'started' }],
    [{ name: 'demo', status: 'stopped' }],
    [{ name: 'demo', status: 'stopped' }],
  ],
  [
    'replaces a row the page changed itself with a list read after it',
    [
      ['touch', 'other', 10],
      ['touch', 'demo', undefined],
    ],
    [{ name: 'demo', status: 'started' }],
    [{ name: 'demo', status: 'pending' }],
    [{ name: 'demo', status: 'started' }],
  ],
])('%s', (_, changes, loaded, current, merged) => {
  const rows = createLiveRows();
  for (const [change, name, seq] of changes) rows[change](name, seq);
  expect(rows.merge(loaded, current, LIST_SEQ)).toEqual(merged);
});

it('a list read after an update replaces the row and forgets the update', () => {
  const rows = createLiveRows();
  rows.touch('demo', 20);

  // the server stopped demo again from the CLI, which sends no message
  const newer = [{ name: 'demo', status: 'stopped' }];
  expect(rows.merge(newer, [{ name: 'demo', status: 'started' }], 30)).toEqual(
    newer,
  );

  // an older list loading late no longer finds a change to keep
  const older = [{ name: 'demo', status: 'started' }];
  expect(rows.merge(older, newer, 25)).toEqual(older);
});

it('a list without a seq loses to every recorded change', () => {
  const rows = createLiveRows();
  rows.touch('demo', 20);
  const current = [{ name: 'demo', status: 'stopped' }];
  expect(rows.merge([{ name: 'demo', status: 'started' }], current)).toEqual(
    current,
  );
});

it('reset forgets updates and deletions', () => {
  const rows = createLiveRows();
  rows.touch('demo', 20);
  rows.remove('gone', 20);
  rows.reset();

  const loaded = [
    { name: 'demo', status: 'started' },
    { name: 'gone', status: 'started' },
  ];
  expect(rows.merge(loaded, [{ name: 'demo', status: 'stopped' }], 5)).toEqual(
    loaded,
  );
});

// a few names, so changes, loaded rows and rows on the page share them
const name = fc.constantFrom('a', 'b', 'c', 'd', 'e');
const rowsOf = (tag) =>
  fc
    .uniqueArray(name, { maxLength: 5 })
    .map((names) => names.map((n) => ({ name: n, tag })));
const change = fc.tuple(
  fc.constantFrom('touch', 'remove'),
  name,
  fc.option(fc.nat(30), { nil: undefined }),
);
const changes = fc.array(change, { maxLength: 8 });
const listSeq = fc.option(fc.nat(30), { nil: undefined });

it('merges to loaded rows in their order, then rows on the page, each name once', () => {
  fc.assert(
    fc.property(
      changes,
      rowsOf('loaded'),
      rowsOf('current'),
      listSeq,
      (cs, loaded, current, seq) => {
        const rows = createLiveRows();
        for (const [op, n, s] of cs) rows[op](n, s);
        const merged = rows.merge(loaded, current, seq);

        // model: each name's last change, with a missing seq ordered after
        // the highest seq seen so far (or after every list if none)
        const last = new Map();
        let latest = -Infinity;
        for (const [op, n, s] of cs) {
          if (s !== undefined) latest = Math.max(latest, s);
          const at = s ?? (Number.isFinite(latest) ? latest + 1 : Infinity);
          last.set(n, { op, after: at > (seq ?? -Infinity) });
        }
        const rowNamed = (list, n) => list.find((row) => row.name === n);
        for (const n of ['a', 'b', 'c', 'd', 'e']) {
          const { op, after } = last.get(n) ?? {};
          const got = rowNamed(merged, n);
          if (after && op === 'remove') expect(got).toBeUndefined();
          else if (after && rowNamed(current, n))
            expect(got).toBe(rowNamed(current, n));
          else expect(got).toBe(rowNamed(loaded, n));
        }

        const names = merged.map((row) => row.name);
        expect(new Set(names).size).toBe(names.length);
        for (const row of merged) {
          expect(loaded.includes(row) || current.includes(row)).toBe(true);
        }
        // rows the page has not changed are the loaded ones, untouched
        const changed = new Set(cs.map(([, n]) => n));
        const kept = (row) => !changed.has(row.name);
        expect(merged.filter(kept)).toEqual(loaded.filter(kept));
        // loaded names keep their order ahead of rows only the page has
        const loadedNames = loaded
          .map((row) => row.name)
          .filter((n) => names.includes(n));
        expect(names.slice(0, loadedNames.length)).toEqual(loadedNames);
      },
    ),
  );
});

it('takes a list read after every change as it is, and forgets those changes', () => {
  fc.assert(
    fc.property(
      changes,
      rowsOf('loaded'),
      rowsOf('current'),
      rowsOf('later'),
      fc.nat(5),
      (cs, loaded, current, later, ahead) => {
        const rows = createLiveRows();
        rows.merge([], [], 0); // the page's own changes then order after seq 0
        for (const [op, n, s] of cs) rows[op](n, s);
        const seq = Math.max(0, ...cs.map(([, , s]) => s ?? 0)) + 1 + ahead;
        expect(rows.merge(loaded, current, seq)).toEqual(loaded);
        expect(rows.merge(later, loaded, seq)).toEqual(later);
      },
    ),
  );
});

// a change made before any seq is ordered after every list, so no list forgets it
it.fails(
  "forgets the page's own change before the first list once a newer list loads",
  () => {
    const rows = createLiveRows();
    rows.touch('demo');
    const loaded = [{ name: 'demo', status: 'started' }];
    const pending = [{ name: 'demo', status: 'pending' }];
    rows.merge(loaded, pending, 30);
    expect(rows.merge(loaded, pending, 31)).toEqual(loaded);
  },
);

it('merges as if nothing changed after a reset', () => {
  fc.assert(
    fc.property(
      changes,
      rowsOf('loaded'),
      rowsOf('current'),
      listSeq,
      (cs, loaded, current, seq) => {
        const rows = createLiveRows();
        for (const [op, n, s] of cs) rows[op](n, s);
        rows.reset();
        expect(rows.merge(loaded, current, seq)).toEqual(loaded);
      },
    ),
  );
});
