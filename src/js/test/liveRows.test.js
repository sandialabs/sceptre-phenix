// How a page merges a list it loaded with the rows websocket messages changed
// while the list was loading.
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createLiveRows } from '@/utils/liveRows.js';

// the list is requested at 1500: a change at 1000 came before the request,
// one at 2000 after it
const REQUESTED_AT = 1500;

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

// Each case: the changes the page recorded ([touch or remove, row, when]),
// the rows loaded, the rows on the page, and the rows the merge keeps.
it.each([
  [
    'keeps a row updated after the list was requested',
    [['touch', 'demo', 2000]],
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
    'takes the loaded row when the update came before the request',
    [['touch', 'demo', 1000]],
    [{ name: 'demo', status: 'started' }],
    [{ name: 'demo', status: 'stopped' }],
    [{ name: 'demo', status: 'started' }],
  ],
  [
    'drops a row the list no longer has',
    [['touch', 'gone', 1000]],
    [],
    [{ name: 'gone' }],
    [],
  ],
  [
    'keeps a row added after the list was requested',
    [['touch', 'new', 2000]],
    [{ name: 'old' }],
    [{ name: 'old' }, { name: 'new', status: 'starting' }],
    [{ name: 'old' }, { name: 'new', status: 'starting' }],
  ],
  [
    'leaves out a row deleted after the list was requested',
    [['remove', 'gone', 2000]],
    [{ name: 'gone' }, { name: 'kept' }],
    [{ name: 'kept' }],
    [{ name: 'kept' }],
  ],
  [
    'keeps a row created again after it was deleted',
    [
      ['remove', 'demo', 2000],
      ['touch', 'demo', 2100],
    ],
    [{ name: 'demo', status: 'started' }],
    [{ name: 'demo', status: 'stopped' }],
    [{ name: 'demo', status: 'stopped' }],
  ],
])('%s', (_, changes, loaded, current, merged) => {
  const rows = createLiveRows();
  for (const [change, name, at] of changes) {
    vi.setSystemTime(at);
    rows[change](name);
  }
  expect(rows.merge(loaded, current, REQUESTED_AT)).toEqual(merged);
});
