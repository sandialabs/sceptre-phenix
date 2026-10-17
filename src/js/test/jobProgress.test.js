// A background job's progress in the jobs menu and pages: its status in words
// and color, a bar, a status line read out as it changes, and its buttons.
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
import { flush } from './helpers/async.js';
import { lastConfirm } from './helpers/buefy.js';
import { httpError } from './helpers/fixtures.js';
import { pageContext } from './helpers/page.js';
import { buttons, renderSSR, textOf } from './helpers/render.js';

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

const render = (props) => renderSSR(JobProgress, props);
const liveLine = (html) =>
  textOf(
    html.match(
      /<p class="job-status-line" aria-live="polite"[^>]*>([\s\S]*?)<\/p>/,
    )[1],
  );

beforeEach(() => {
  resetMocks();
  setActivePinia(createPinia());
});

describe('JobProgress', () => {
  it('shows a running job with its bytes, and can cancel it', async () => {
    const html = await render({ job: job() });
    expect(html).toContain('Running');
    expect(html).toMatch(
      /<progress[^>]*aria-label="Archive 3 files of exp progress"/,
    );
    expect(liveLine(html)).toBe('1 of 3 files done (1.5 KiB of 3.0 KiB)');
    expect(buttons(html)).toEqual(['Cancel']);
    expect(html).toContain('aria-label="Cancel Archive 3 files of exp"');
  });

  it('says a cancel is under way', async () => {
    const html = await render({ job: job({ cancelRequested: true }) });
    expect(liveLine(html)).toMatch(/stopping after the ones in progress$/);
    expect(buttons(html)).toEqual(['Canceling… (disabled)']);
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
      const html = await render({ job: job({ status }) });
      expect(html).toMatch(
        new RegExp(`<span class="tag ${type}[^"]*"[^>]*>[\\s\\S]*?${label}`),
      );
    },
  );

  it('offers a finished archive for download, and dismissal', async () => {
    const html = await render({
      job: job({
        status: 'succeeded',
        artifact: { name: 'exp-files.zip', size: 10 },
        items: [{ name: 'a.log', status: 'succeeded' }],
        progress: { done: 1, total: 1 },
      }),
    });
    expect(buttons(html)).toEqual(['Download', 'Dismiss']);
    expect(liveLine(html)).toBe('1 of 1 file succeeded');
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
    const html = await render({ job: partial });
    expect(buttons(html)).toContain('Download incomplete archive');
    expect(liveLine(html)).toBe('1 of 2 files succeeded, 1 failed');
    expect(textOf(html)).toContain('b.log : not found');

    const download = vi
      .spyOn(useJobStore(), 'downloadArtifact')
      .mockImplementation(() => {});
    const ctx = pageContext(JobProgress, { fields: { job: partial } });
    ctx.download();
    expect(download).not.toHaveBeenCalled();
    expect(lastConfirm(ctx).message).toContain('MISSING.txt');
    lastConfirm(ctx).onConfirm();
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
    const html = await render({ job: failed });
    expect(liveLine(html)).toBe(
      '0 of 1 disk image succeeded, 1 failed, every item failed, no space left',
    );
    expect(textOf(html)).toContain('a.qc2 : no space left');

    const menu = await render({ job: failed, compact: true });
    expect(liveLine(menu)).toMatch(/no space left$/);
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
    expect(liveLine(await render({ job: shortened }))).toBe(
      '1000 of 1000 VMs done',
    );

    axios.get.mockResolvedValue({ data: { ...shortened, itemsOmitted: 0 } });
    const ctx = pageContext(JobProgress, { fields: { job: shortened } });
    JobProgress.mounted.call(ctx);
    await flush();
    expect(axios.get).toHaveBeenCalledWith('jobs/j1');
    expect(useJobStore().jobs.j1.itemsOmitted).toBe(0);
  });

  it('cancels and dismisses through the server, and reports a failure', async () => {
    const ctx = pageContext(JobProgress, { fields: { job: job() } });
    axios.post.mockResolvedValue({
      data: job({ cancelRequested: true, revision: 2 }),
    });
    await ctx.cancel();
    expect(axios.post).toHaveBeenCalledWith('jobs/j1/cancel');
    expect(useJobStore().jobs.j1.cancelRequested).toBe(true);

    axios.delete.mockRejectedValue(
      httpError(409, 'job is still queued or running'),
    );
    await ctx.dismiss();
    expect(notify.error).toHaveBeenCalled();
    expect(ctx.$emit).not.toHaveBeenCalled();

    axios.delete.mockResolvedValue({});
    await ctx.dismiss();
    expect(ctx.$emit).toHaveBeenCalledWith('dismissed', ctx.job);
  });
});
