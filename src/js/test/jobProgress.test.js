// A background job's progress in the jobs menu and pages: its status in words
// and color, a bar, a status line read out as it changes, and its buttons.
import { flushPromises } from '@vue/test-utils';
import fc from 'fast-check';
import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, notify, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);

import JobProgress from '@/components/jobs/JobProgress.vue';
import { useJobStore } from '@/utils/jobs.js';
import { httpError } from './helpers/fixtures.js';
import {
  buttons,
  byLabel,
  confirmDialog,
  dialogs,
  mountComponent,
  textOf,
} from './helpers/mount.js';

const job = (fields = {}) => ({
  id: 'j1',
  kind: 'files.archive',
  experiment: 'exp',
  title: 'Archive 3 files of exp',
  status: 'running',
  cancelable: true,
  progress: { done: 1, total: 3, bytes: 1536, bytesTotal: 3072 },
  items: [
    { name: 'a.log', status: 'succeeded' },
    { name: 'b.log', status: 'running' },
    { name: 'c.log', status: 'pending' },
  ],
  ...fields,
});

const render = (props) => mountComponent(JobProgress, { props });
const liveLine = (wrapper) =>
  textOf(wrapper.find('.job-status-line[aria-live="polite"]'));
const button = (wrapper, text) =>
  wrapper.findAll('button').find((b) => textOf(b) === text);

beforeEach(() => {
  resetMocks();
  setActivePinia(createPinia());
});

describe('JobProgress', () => {
  it('shows a running job with its bytes, and can cancel it', async () => {
    const progress = await render({ job: job() });
    expect(textOf(progress.find('.job-status'))).toBe('Running');
    expect(
      byLabel(progress, 'Archive 3 files of exp progress').element.tagName,
    ).toBe('PROGRESS');
    expect(liveLine(progress)).toBe('1 of 3 files done (1.5 KiB of 3.0 KiB)');
    expect(buttons(progress)).toEqual(['Cancel']);
    expect(byLabel(progress, 'Cancel Archive 3 files of exp').exists()).toBe(
      true,
    );
  });

  it('says a cancel is under way', async () => {
    const progress = await render({ job: job({ cancelRequested: true }) });
    expect(liveLine(progress)).toMatch(/stopping after the ones in progress$/);
    expect(buttons(progress)).toEqual(['Canceling… (disabled)']);
  });

  it.each([
    ['succeeded', 'Succeeded', 'is-success'],
    ['partial', 'Partly succeeded', 'is-warning'],
    ['failed', 'Failed', 'is-danger'],
    ['canceled', 'Canceled', 'is-dark'],
    ['queued', 'Queued', 'is-light'],
  ])(
    'names a %s job in words, not color alone',
    async (status, label, type) => {
      const tag = (await render({ job: job({ status }) })).find('.tag');
      expect(tag.classes()).toContain(type);
      expect(textOf(tag)).toBe(label);
    },
  );

  it("counts a finished job's items by outcome, and only the outcomes it had", async () => {
    const outcomes = fc.array(
      fc.constantFrom('succeeded', 'failed', 'skipped', 'canceled'),
      { minLength: 2, maxLength: 12 },
    );
    const line =
      /^(\d+) of (\d+) files succeeded((?:, \d+ (?:failed|skipped|canceled))*)$/;
    await fc.assert(
      fc.asyncProperty(outcomes, async (statuses) => {
        const items = statuses.map((status, i) => ({ name: `f${i}`, status }));
        const progress = await render({
          job: job({
            status: 'partial',
            items,
            progress: { done: items.length, total: items.length },
          }),
        });
        const [, ok, total, rest] = liveLine(progress).match(line);
        const count = (status) => statuses.filter((s) => s === status).length;
        expect([Number(ok), Number(total)]).toEqual([
          count('succeeded'),
          items.length,
        ]);
        expect(rest.split(', ').slice(1)).toEqual(
          ['failed', 'skipped', 'canceled']
            .filter((status) => count(status))
            .map((status) => `${count(status)} ${status}`),
        );
        progress.unmount();
      }),
      { numRuns: 20 },
    );
  });

  it('offers a finished archive for download, and dismissal', async () => {
    const progress = await render({
      job: job({
        status: 'succeeded',
        artifact: { name: 'exp-files.zip', size: 10 },
        items: [{ name: 'a.log', status: 'succeeded' }],
        progress: { done: 1, total: 1 },
      }),
    });
    expect(buttons(progress)).toEqual(['Download', 'Dismiss']);
    expect(liveLine(progress)).toBe('1 of 1 file succeeded');
  });

  it("warns before downloading an incomplete archive, and lists the job's outcome", async () => {
    const partial = job({
      status: 'partial',
      artifact: {
        name: 'exp-files-incomplete.zip',
        size: 10,
        incomplete: true,
      },
      items: [
        { name: 'a.log', status: 'succeeded' },
        { name: 'b.log', status: 'failed', error: 'not found' },
      ],
      progress: { done: 2, total: 2 },
    });
    const progress = await render({ job: partial });
    expect(buttons(progress)).toContain('Download incomplete archive');
    expect(liveLine(progress)).toBe('1 of 2 files succeeded, 1 failed');
    expect(textOf(progress)).toContain('b.log: not found');

    const download = vi
      .spyOn(useJobStore(), 'downloadArtifact')
      .mockImplementation(() => {});
    await button(progress, 'Download incomplete archive').trigger('click');
    expect(download).not.toHaveBeenCalled();
    expect(textOf(dialogs()[0])).toContain('MISSING.txt');
    await confirmDialog();
    expect(download).toHaveBeenCalledWith(partial);
  });

  it("gives a lone item's reason for failing", async () => {
    const failed = job({
      kind: 'disk.clone',
      status: 'failed',
      error: 'every item failed',
      items: [{ name: 'a.qc2', status: 'failed', error: 'no space left' }],
      progress: { done: 1, total: 1 },
    });
    const progress = await render({ job: failed });
    expect(liveLine(progress)).toBe(
      '0 of 1 disk image succeeded, 1 failed, every item failed, no space left',
    );
    expect(textOf(progress)).toContain('a.qc2: no space left');

    const menu = await render({ job: failed, compact: true });
    expect(liveLine(menu)).toMatch(/no space left$/);
  });

  it('offers no retry, which a job cannot do, for failures worth retrying', async () => {
    const progress = await render({
      job: job({
        kind: 'vms.action',
        status: 'partial',
        items: [
          { name: 'a', status: 'succeeded' },
          { name: 'b', status: 'failed', error: 'locked', retryable: true },
        ],
        progress: { done: 2, total: 2 },
      }),
    });
    expect(textOf(progress)).toContain('b: locked');
    expect(textOf(progress)).not.toContain('can retry');
    expect(button(progress, 'Retry failed (1)')).toBeUndefined();
  });

  it('counts only done items, and reads the whole job, when a copy leaves items out', async () => {
    const shortened = job({
      kind: 'vms.action',
      status: 'succeeded',
      items: [{ name: 'a', status: 'succeeded' }],
      itemsOmitted: 999,
      progress: { done: 1000, total: 1000 },
      revision: 4,
    });
    axios.get.mockResolvedValue({ data: { ...shortened, itemsOmitted: 0 } });

    const progress = await render({ job: shortened });
    expect(liveLine(progress)).toBe('1000 of 1000 VMs done');
    await flushPromises();
    expect(axios.get).toHaveBeenCalledWith('jobs/j1');
    expect(useJobStore().jobs.j1.itemsOmitted).toBe(0);
  });

  it('cancels and dismisses through the server, and reports a failure', async () => {
    const progress = await render({ job: job() });
    axios.post.mockResolvedValue({
      data: job({ cancelRequested: true, revision: 2 }),
    });
    await button(progress, 'Cancel').trigger('click');
    await flushPromises();
    expect(axios.post).toHaveBeenCalledWith('jobs/j1/cancel');
    expect(useJobStore().jobs.j1.cancelRequested).toBe(true);

    const done = job({ status: 'canceled' });
    await progress.setProps({ job: done });
    axios.delete.mockRejectedValue(
      httpError(409, 'job is still queued or running'),
    );
    await button(progress, 'Dismiss').trigger('click');
    await flushPromises();
    expect(notify.error).toHaveBeenCalled();
    expect(useJobStore().jobs.j1).toBeDefined();

    axios.delete.mockResolvedValue({});
    await button(progress, 'Dismiss').trigger('click');
    await flushPromises();
    expect(axios.delete).toHaveBeenCalledWith('jobs/j1');
    expect(useJobStore().jobs.j1).toBeUndefined();
  });
});
