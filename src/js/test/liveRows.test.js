// How a page merges a list it loaded with the rows websocket messages changed,
// ordered by the server's sequence (see freshness.js).
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
