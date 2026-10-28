// Busy work (busy.js): one task at a time for each group, shown while it
// runs, and gone when it ends, however it ends.

import { describe, expect, test } from 'vitest';
import { computed } from 'vue';

import { afterPaint, busyTask, runBusy } from '@/builder/busy.js';

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((done, fail) => {
    resolve = done;
    reject = fail;
  });

  return { promise, resolve, reject };
}

describe('runBusy', () => {
  test('runs one task of a group at a time, and shows it while it runs', async () => {
    const work = deferred();
    const shown = computed(() => busyTask('downloads'));
    const first = runBusy('downloads', 'png', () => work.promise);

    // At once, before the work starts.
    expect(shown.value).toBe('png');

    // Another task of the group, or the same again, does not run.
    let ran = false;
    expect(
      await runBusy('downloads', 'svg', () => {
        ran = true;
      }),
    ).toEqual({ ran: false });
    expect(ran).toBe(false);
    expect(shown.value).toBe('png');

    // Another group runs at the same time.
    expect(await runBusy('history', 'restore', () => 'done')).toEqual({
      ran: true,
      value: 'done',
    });

    work.resolve('saved');
    expect(await first).toEqual({ ran: true, value: 'saved' });
    expect(shown.value).toBe('');
  });

  test('ends the task when its work fails, and passes the failure on', async () => {
    const work = deferred();
    const run = runBusy('publish', 'submit', () => work.promise);

    expect(busyTask('publish')).toBe('submit');
    work.reject(new Error('refused'));
    await expect(run).rejects.toThrow('refused');
    expect(busyTask('publish')).toBe('');
    expect(await runBusy('publish', 'submit', () => 1)).toEqual({
      ran: true,
      value: 1,
    });
  });

  test('without a page, waits for no paint', async () => {
    await expect(afterPaint()).resolves.toBeUndefined();
  });
});
