// The background jobs store: the server's list, kept in revision order by the
// websocket and after a reconnect, and the start, cancel and dismiss requests.
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/store.js', modules.store);

import { bytesText, isActiveJob, useJobStore } from '@/utils/jobs.js';
import { flush } from './helpers/async.js';
import { wsMsg } from './helpers/fixtures.js';

// a job as the server sends it
const job = (id, fields = {}) => ({
  id,
  kind: 'vms.action',
  owner: 'alice',
  experiment: 'exp',
  title: 'Restart 2 VMs',
  status: 'running',
  cancelable: true,
  progress: { done: 0, total: 2 },
  items: [
    { name: 'a', status: 'pending' },
    { name: 'b', status: 'pending' },
  ],
  created: '2026-10-14T15:00:00Z',
  revision: 1,
  ...fields,
});

let jobs;

beforeEach(() => {
  resetMocks();
  setActivePinia(createPinia());
  jobs = useJobStore();
});

afterEach(() => vi.useRealTimers());

describe('the jobs store', () => {
  it('loads the jobs the server lists, newest first, and forgets the rest', async () => {
    jobs.put(job('gone'));
    axios.get.mockResolvedValue({
      data: {
        jobs: [
          job('old', { created: '2026-10-14T14:00:00Z', status: 'succeeded' }),
          job('new', { created: '2026-10-14T16:00:00Z' }),
        ],
      },
    });

    await jobs.load();

    expect(axios.get).toHaveBeenCalledWith('jobs');
    expect(jobs.loaded).toBe(true);
    expect(jobs.list.map((j) => j.id)).toEqual(['new', 'old']);
    expect(jobs.active.map((j) => j.id)).toEqual(['new']);
    expect(jobs.forExperiment('exp')).toHaveLength(2);
    expect(jobs.forExperiment('other')).toEqual([]);
  });

  it('applies websocket updates in revision order and removals', () => {
    jobs.apply(wsMsg('job', 'j1', 'update', job('j1', { revision: 3 })));
    jobs.apply(
      wsMsg(
        'job',
        'j1',
        'update',
        job('j1', { revision: 2, status: 'failed' }),
      ),
    );
    expect(jobs.jobs.j1.status).toBe('running');

    jobs.apply(
      wsMsg(
        'job',
        'j1',
        'update',
        job('j1', { revision: 4, status: 'partial' }),
      ),
    );
    expect(jobs.jobs.j1.status).toBe('partial');

    // other messages are not jobs
    jobs.apply(wsMsg('experiment', 'exp', 'update', {}));
    expect(Object.keys(jobs.jobs)).toEqual(['j1']);

    jobs.apply(wsMsg('job', 'j1', 'remove', job('j1', { revision: 5 })));
    expect(jobs.jobs.j1).toBeUndefined();
  });

  it('keeps the results a whole copy has over a websocket copy of the same revision', () => {
    const whole = job('j1', {
      status: 'succeeded',
      revision: 7,
      items: [{ name: 'a', status: 'succeeded', result: { name: 'a' } }],
    });
    jobs.put(whole);
    jobs.apply(
      wsMsg('job', 'j1', 'update', {
        ...whole,
        items: [{ name: 'a', status: 'succeeded' }],
      }),
    );
    expect(jobs.jobs.j1.items[0].result).toEqual({ name: 'a' });
  });

  it('starts, cancels and dismisses jobs', async () => {
    axios.post.mockResolvedValueOnce({ data: job('j1') });
    const started = await jobs.start('experiments/exp/vms/actions', {
      action: 'restart',
      vms: ['a', 'b'],
    });
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/exp/vms/actions',
      { action: 'restart', vms: ['a', 'b'] },
      undefined,
    );
    expect(started.id).toBe('j1');

    axios.post.mockResolvedValueOnce({
      data: job('j1', { cancelRequested: true, revision: 2 }),
    });
    await jobs.cancel('j1');
    expect(axios.post).toHaveBeenLastCalledWith('jobs/j1/cancel');
    expect(jobs.jobs.j1.cancelRequested).toBe(true);

    axios.delete.mockResolvedValue({});
    await jobs.dismiss('j1');
    expect(axios.delete).toHaveBeenCalledWith('jobs/j1');
    expect(jobs.jobs.j1).toBeUndefined();
  });

  it('resolves whenDone with the whole job once it finishes', async () => {
    jobs.put(job('j1'));
    const finished = job('j1', {
      status: 'partial',
      revision: 5,
      items: [
        { name: 'a', status: 'succeeded', result: { name: 'a' } },
        { name: 'b', status: 'failed', error: 'locked', retryable: true },
      ],
    });
    axios.get.mockResolvedValue({ data: finished });

    let done = null;
    jobs.whenDone('j1').then((j) => (done = j));
    await flush();
    expect(done).toBe(null);

    jobs.apply(
      wsMsg('job', 'j1', 'update', {
        ...finished,
        items: finished.items.map((item) => ({ ...item, result: undefined })),
      }),
    );
    await flush();

    expect(axios.get).toHaveBeenCalledWith('jobs/j1');
    expect(done.items[0].result).toEqual({ name: 'a' });
  });

  it('asks the server while waiting, in case a websocket message is lost', async () => {
    vi.useFakeTimers();
    jobs.put(job('j1'));
    axios.get.mockResolvedValue({
      data: job('j1', { status: 'failed', revision: 2 }),
    });

    let done = null;
    jobs.whenDone('j1').then((j) => (done = j));
    await vi.advanceTimersByTimeAsync(5000);

    expect(done.status).toBe('failed');
  });

  it('fails whenDone once the server no longer has the job', async () => {
    vi.useFakeTimers();
    jobs.put(job('j1'));
    jobs.put(job('j2'));
    axios.get.mockRejectedValue({ response: { status: 404 } });

    const polled = jobs.whenDone('j1');
    const removed = jobs.whenDone('j2');
    const reloaded = jobs.whenDone('j3');
    const failures = Promise.allSettled([polled, removed, reloaded]);

    // the poll finds the job gone, as after a server restart
    await vi.advanceTimersByTimeAsync(5000);
    // a websocket message says the job is gone
    jobs.apply(wsMsg('job', 'j2', 'remove', {}));
    // a reload does not list it, and the server says it is gone
    axios.get.mockImplementation(async (url) => {
      if (url === 'jobs') return { data: { jobs: [] } };
      throw { response: { status: 404 } };
    });
    await jobs.load();
    await vi.advanceTimersByTimeAsync(0);

    for (const result of await failures) {
      expect(result.status).toBe('rejected');
      expect(result.reason.message).toMatch(/no longer has job j\d/);
    }
    expect(jobs.jobs.j1).toBeUndefined();
  });

  it('fails whenDone, and stops polling, on sign-out', async () => {
    vi.useFakeTimers();
    jobs.put(job('j1'));
    const waiting = jobs.whenDone('j1');
    const failure = waiting.catch((err) => err);

    jobs.clear();

    // canceled, which error reports leave out
    expect(await failure).toMatchObject({
      message: expect.stringMatching(/Signed out/),
      code: 'ERR_CANCELED',
    });
    axios.get.mockClear();
    await vi.advanceTimersByTimeAsync(20000);
    expect(axios.get).not.toHaveBeenCalled();
  });

  it('keeps waiting for a job the server has, though a reload missed it', async () => {
    jobs.put(job('j1'));
    let done = null;
    jobs.whenDone('j1').then((j) => (done = j));
    axios.get.mockImplementation(async (url) =>
      url === 'jobs'
        ? { data: { jobs: [] } }
        : { data: job('j1', { status: 'succeeded', revision: 2 }) },
    );

    await jobs.load();
    await flush();

    expect(axios.get).toHaveBeenCalledWith('jobs/j1');
    expect(done.status).toBe('succeeded');
  });

  it('builds and follows the artifact link with the token, and forgets everything on sign-out', () => {
    expect(jobs.artifactURL({ id: 'j1' })).toBe(
      '/api/v1/jobs/j1/artifact?token=t',
    );

    // downloads through a link, not a window a browser may block
    const link = { click: vi.fn(), remove: vi.fn() };
    const append = vi.fn();
    vi.stubGlobal('document', {
      createElement: () => link,
      body: { appendChild: append },
    });
    jobs.downloadArtifact({ id: 'j1', artifact: { name: 'exp-files.zip' } });
    vi.unstubAllGlobals();
    expect(link).toMatchObject({
      href: '/api/v1/jobs/j1/artifact?token=t',
      download: 'exp-files.zip',
    });
    expect(append).toHaveBeenCalledWith(link);
    expect(link.click).toHaveBeenCalled();
    expect(link.remove).toHaveBeenCalled();

    jobs.put(job('j1'));
    jobs.clear();
    expect(jobs.list).toEqual([]);
    expect(jobs.loaded).toBe(false);
  });

  it('lists every active job and the newest ten finished ones as recent', () => {
    for (let i = 0; i < 12; i++) {
      jobs.put(
        job(`done${i}`, {
          status: 'succeeded',
          created: `2026-10-14T10:${String(i).padStart(2, '0')}:00Z`,
        }),
      );
    }
    jobs.put(job('running', { created: '2026-10-14T09:00:00Z' }));

    const recent = jobs.recent.map((j) => j.id);
    expect(recent[0]).toBe('running');
    expect(recent).toHaveLength(11);
    expect(recent).not.toContain('done0');
  });
});

describe('helpers', () => {
  it.each([
    ['queued', true],
    ['running', true],
    ['succeeded', false],
    ['canceled', false],
  ])('a %s job is active: %s', (status, want) => {
    expect(isActiveJob({ status })).toBe(want);
  });

  it.each([
    [512, '512 B'],
    [1536, '1.5 KiB'],
    [3 * 1024 ** 3, '3.0 GiB'],
  ])('writes %d bytes as %s', (bytes, want) => {
    expect(bytesText(bytes)).toBe(want);
  });
});
