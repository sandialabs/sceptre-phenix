// One action on several listed items: the loop that runs it a few at a
// time, what it says it came to, the question a batch delete asks, and the
// share list a batch share saves for each draft.

import { describe, expect, test, vi } from 'vitest';

import {
  BULK_CONCURRENCY,
  BulkError,
  NOT_ATTEMPTED,
  bulkDoneMessage,
  bulkFailureHeading,
  bulkOutcome,
  draftsDeleteMessage,
  mergeShareList,
  runBulk,
  topologiesDeleteMessage,
} from '@/builder/bulk.js';

// Work the test ends by hand: started holds the items under way.
function manualWork() {
  const started = [];
  const settle = new Map();

  return {
    started,
    work: (item) =>
      new Promise((resolve, reject) => {
        started.push(item);
        settle.set(item, { resolve, reject });
      }),
    finish(item, value) {
      settle.get(item).resolve(value);
    },
    fail(item, error) {
      settle.get(item).reject(error);
    },
  };
}

// Lets the promises settled so far run their continuations.
const turn = () => new Promise((resolve) => setTimeout(resolve, 0));

describe('the bulk runner', () => {
  test('has at most four items under way, and starts the next as one ends', async () => {
    const items = ['a', 'b', 'c', 'd', 'e', 'f'];
    const manual = manualWork();
    const progress = [];
    const run = runBulk(items, manual.work, {
      onProgress: (done, total) => progress.push([done, total]),
    });

    await turn();
    expect(BULK_CONCURRENCY).toBe(4);
    expect(manual.started).toEqual(['a', 'b', 'c', 'd']);

    manual.finish('b', 2);
    await turn();
    expect(manual.started).toEqual(['a', 'b', 'c', 'd', 'e']);
    expect(progress).toEqual([[1, 6]]);

    for (const item of ['a', 'c', 'd', 'e']) {
      manual.finish(item, item.toUpperCase());
    }
    await turn();
    expect(manual.started).toEqual(items);
    manual.finish('f', 'F');

    // Every outcome, in the order of the items, whatever order they ended.
    expect(await run).toEqual([
      { item: 'a', ok: true, value: 'A' },
      { item: 'b', ok: true, value: 2 },
      { item: 'c', ok: true, value: 'C' },
      { item: 'd', ok: true, value: 'D' },
      { item: 'e', ok: true, value: 'E' },
      { item: 'f', ok: true, value: 'F' },
    ]);
    expect(progress.at(-1)).toEqual([6, 6]);
    expect(progress).toHaveLength(6);
  });

  test('takes a limit, such as one at a time', async () => {
    const manual = manualWork();
    const run = runBulk(['a', 'b'], manual.work, { limit: 1 });

    await turn();
    expect(manual.started).toEqual(['a']);
    manual.finish('a');
    await turn();
    expect(manual.started).toEqual(['a', 'b']);
    manual.finish('b');
    expect((await run).map((result) => result.ok)).toEqual([true, true]);
  });

  test('never rejects: a failure is an outcome, and the rest are still tried', async () => {
    const boom = new Error('boom');
    const results = await runBulk(['a', 'b', 'c'], async (item) => {
      if (item === 'b') {
        throw boom;
      }

      return item;
    });

    expect(results).toEqual([
      { item: 'a', ok: true, value: 'a' },
      { item: 'b', ok: false, error: boom },
      { item: 'c', ok: true, value: 'c' },
    ]);

    // Work that throws before it returns a promise, and callbacks that
    // throw, do not end the run either.
    const thrown = await runBulk(
      ['a', 'b'],
      () => {
        throw boom;
      },
      {
        onProgress: () => {
          throw new Error('progress');
        },
        stop: () => {
          throw new Error('stop');
        },
      },
    );

    expect(thrown.map((result) => result.error)).toEqual([boom, boom]);
    expect(await runBulk([], vi.fn())).toEqual([]);
    expect(await runBulk(null, vi.fn())).toEqual([]);
  });

  test('starts nothing more once a failure ends the run, and says what it skipped', async () => {
    const ended = Object.assign(new Error('ended'), {
      response: { status: 401 },
    });
    const items = ['a', 'b', 'c', 'd', 'e', 'f'];
    const manual = manualWork();
    const progress = [];
    const run = runBulk(items, manual.work, {
      limit: 2,
      stop: (error) => error === ended,
      onProgress: (done) => progress.push(done),
    });

    await turn();
    manual.finish('a', 1);
    await turn();
    expect(manual.started).toEqual(['a', 'b', 'c']);

    manual.fail('b', ended);
    await turn();
    // The one under way ends; none is started after it.
    manual.finish('c', 3);

    expect(await run).toEqual([
      { item: 'a', ok: true, value: 1 },
      { item: 'b', ok: false, error: ended },
      { item: 'c', ok: true, value: 3 },
      { item: 'd', ok: false, skipped: true },
      { item: 'e', ok: false, skipped: true },
      { item: 'f', ok: false, skipped: true },
    ]);
    expect(manual.started).toEqual(['a', 'b', 'c']);
    // Only what was tried counts as done.
    expect(progress).toEqual([1, 2, 3]);
  });
});

describe('what a run came to', () => {
  test('the items changed, and each one that was not, with why', () => {
    const failed = new Error('refused');
    const outcome = bulkOutcome(
      [
        { item: 'a', ok: true },
        { item: 'b', ok: false, error: failed },
        { item: 'c', ok: false, error: new BulkError('Too many people.') },
        { item: 'd', ok: false, skipped: true },
      ],
      (error, item) => `${item}: ${error.message}`,
    );

    expect(outcome).toEqual({
      done: ['a'],
      failures: [
        { item: 'b', reason: 'b: refused' },
        // A reason already in words is used as it is.
        { item: 'c', reason: 'Too many people.' },
        { item: 'd', reason: 'Not attempted.' },
      ],
    });
    expect(NOT_ATTEMPTED).toBe('Not attempted.');
    expect(new BulkError('x')).toBeInstanceOf(Error);
  });

  test('the summary heading counts what failed and what did not', () => {
    expect(bulkFailureHeading(2, 5, 'draft', 'drafts', 'deleted')).toBe(
      '2 of 5 drafts could not be deleted. The other 3 were deleted.',
    );
    expect(bulkFailureHeading(1, 2, 'draft', 'drafts', 'shared')).toBe(
      '1 of 2 drafts could not be shared. The other 1 was shared.',
    );
    // Nothing else changed: only the first sentence.
    expect(bulkFailureHeading(3, 3, 'topology', 'topologies', 'deleted')).toBe(
      '3 of 3 topologies could not be deleted.',
    );
    // The noun is singular when the run was for one item.
    expect(bulkFailureHeading(1, 1, 'topology', 'topologies', 'deleted')).toBe(
      '1 of 1 topology could not be deleted.',
    );
  });

  test('the page says how many were changed', () => {
    expect(bulkDoneMessage(5, 'draft', 'drafts', 'deleted')).toBe(
      'Deleted 5 drafts.',
    );
    expect(bulkDoneMessage(1, 'draft', 'drafts', 'deleted')).toBe(
      'Deleted 1 draft.',
    );
    expect(bulkDoneMessage(5, 'topology', 'topologies', 'deleted')).toBe(
      'Deleted 5 topologies.',
    );
    expect(bulkDoneMessage(1, 'topology', 'topologies', 'deleted')).toBe(
      'Deleted 1 topology.',
    );
  });
});

describe('the question a batch delete asks', () => {
  test('names up to three drafts, and says what goes with them', () => {
    expect(draftsDeleteMessage(['Lab', 'Core'])).toBe(
      'Lab and Core. The drafts and their whole histories are removed from the server. This cannot be undone.',
    );
    expect(draftsDeleteMessage(['Lab', 'Core', 'Edge', 'Site', 'Plant'])).toBe(
      'Lab, Core and 3 others. The drafts and their whole histories are removed from the server. This cannot be undone.',
    );
  });

  test('says how many of the user’s own drafts are shared', () => {
    expect(draftsDeleteMessage(['Lab', 'Core', 'Edge'], { shared: 1 })).toBe(
      'Lab, Core and Edge. The drafts and their whole histories are removed from the server. 1 of them is shared: the people with access lose it too. This cannot be undone.',
    );
    expect(draftsDeleteMessage(['Lab', 'Core'], { shared: 2 })).toBe(
      'Lab and Core. The drafts and their whole histories are removed from the server. 2 of them are shared: the people with access lose it too. This cannot be undone.',
    );
  });

  test('names the owners of other users’ drafts, each once', () => {
    expect(
      draftsDeleteMessage(['Lab', 'Core', 'Edge'], {
        owners: ['bob', 'carol', 'bob'],
      }),
    ).toBe(
      'Lab, Core and Edge. They belong to bob and carol. The drafts and their whole histories are removed from the server. This cannot be undone.',
    );
  });

  test('names the topologies, and what is left as it was', () => {
    expect(topologiesDeleteMessage(['lab', 'core'])).toBe(
      'lab and core. The topologies are deleted from phēnix. Drafts and experiments made from them are not changed.',
    );
    expect(topologiesDeleteMessage(['a', 'b', 'c', 'd'])).toBe(
      'a, b and 2 others. The topologies are deleted from phēnix. Drafts and experiments made from them are not changed.',
    );
  });
});

describe('the share list a batch share saves', () => {
  const current = [
    { user: 'bob', access: 'edit' },
    { user: 'carol', access: 'view' },
  ];

  test('adds the people, and keeps everyone else with their access', () => {
    expect(mergeShareList(current, ['dave', 'erin'], 'view')).toEqual({
      shares: [
        { user: 'bob', access: 'edit' },
        { user: 'carol', access: 'view' },
        { user: 'dave', access: 'view' },
        { user: 'erin', access: 'view' },
      ],
      changed: true,
    });
    expect(mergeShareList([], ['dave'], 'edit')).toEqual({
      shares: [{ user: 'dave', access: 'edit' }],
      changed: true,
    });
    expect(mergeShareList(undefined, ['dave'], 'edit').shares).toHaveLength(1);
  });

  test('gives someone listed already the access chosen, up or down', () => {
    expect(mergeShareList(current, ['carol'], 'edit')).toEqual({
      shares: [
        { user: 'bob', access: 'edit' },
        { user: 'carol', access: 'edit' },
      ],
      changed: true,
    });
    expect(mergeShareList(current, ['bob', 'dave'], 'view').shares).toEqual([
      { user: 'bob', access: 'view' },
      { user: 'carol', access: 'view' },
      { user: 'dave', access: 'view' },
    ]);
    // An access the server does not know is the least one.
    expect(mergeShareList([], ['dave'], 'admin').shares).toEqual([
      { user: 'dave', access: 'view' },
    ]);
  });

  test('says when nothing would change, so nothing is sent', () => {
    expect(mergeShareList(current, ['bob'], 'edit')).toEqual({
      shares: current,
      changed: false,
    });
    expect(mergeShareList(current, [], 'edit').changed).toBe(false);
    // The list read is left as it was.
    expect(current).toEqual([
      { user: 'bob', access: 'edit' },
      { user: 'carol', access: 'view' },
    ]);
  });

  test('leaves out a share whose account was removed, which the server would refuse', () => {
    const stale = [
      { user: 'bob', access: 'edit' },
      { user: 'gone', access: 'edit', stale: true },
    ];

    expect(mergeShareList(stale, ['dave'], 'view')).toEqual({
      shares: [
        { user: 'bob', access: 'edit' },
        { user: 'dave', access: 'view' },
      ],
      changed: true,
    });
    // Dropping it is a change, even when no one new is added.
    expect(mergeShareList(stale, ['bob'], 'edit')).toEqual({
      shares: [{ user: 'bob', access: 'edit' }],
      changed: true,
    });
    // Someone added whose old share is stale is sent as new; the server
    // then says their account was removed.
    expect(mergeShareList(stale, ['gone'], 'view').shares).toEqual([
      { user: 'bob', access: 'edit' },
      { user: 'gone', access: 'view' },
    ]);
  });
});
