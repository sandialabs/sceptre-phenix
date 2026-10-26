// The background jobs store: the server's list, kept in revision order by the
// websocket and after a reconnect, and the start, cancel and dismiss requests.
import fc from 'fast-check';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/store.js', modules.store);

import {
  BACK_SPREAD_MS,
  forgetServer,
  serverUnanswered,
  setWsDown,
} from '@/utils/freshness.js';
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

afterEach(() => {
  forgetServer();
  vi.useRealTimers();
});

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
    expect(jobs.list.map((j) => j.id)).toEqual(['new', 'old']);
    expect(jobs.active.map((j) => j.id)).toEqual(['new']);
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

  it('holds the last copy of the highest revision, whatever order they come in', () => {
    fc.assert(
      fc.property(
        fc.array(fc.nat(5), { minLength: 1, maxLength: 8 }),
        (revisions) => {
          setActivePinia(createPinia());
          const store = useJobStore();
          revisions.forEach((revision, i) =>
            store.apply(
              wsMsg('job', 'j1', 'update', job('j1', { revision, title: i })),
            ),
          );
          const newest = Math.max(...revisions);
          expect(store.jobs.j1.title).toBe(revisions.lastIndexOf(newest));
        },
      ),
    );
  });

  it('lists jobs newest first, and as recent every active one and the newest ten finished', () => {
    const listed = fc.uniqueArray(
      fc.record({
        id: fc.stringMatching(/^j[a-z0-9]{1,3}$/),
        status: fc.constantFrom(
          'queued',
          'running',
          'succeeded',
          'partial',
          'failed',
          'canceled',
        ),
        ms: fc.integer({
          min: Date.UTC(2026, 0, 1),
          max: Date.UTC(2027, 0, 1),
        }),
      }),
      { selector: (j) => j.id, maxLength: 25, size: 'max' },
    );
    fc.assert(
      fc.property(listed, (given) => {
        setActivePinia(createPinia());
        const store = useJobStore();
        for (const { id, status, ms } of given) {
          store.put(job(id, { status, created: new Date(ms).toISOString() }));
        }
        const at = (j) => Date.parse(j.created);
        const ids = (list) => list.map((j) => j.id).sort();
        const newestFirst = (list) =>
          list.slice(1).every((j, i) => at(j) <= at(list[i]));

        expect(ids(store.list)).toEqual(ids(given));
        expect(newestFirst(store.list)).toBe(true);

        const active = store.list.filter(isActiveJob);
        const finished = store.list.filter((j) => !isActiveJob(j));
        const recent = store.recent;
        expect(recent.slice(0, active.length)).toEqual(active);
        const shown = recent.slice(active.length);
        expect(shown).toHaveLength(Math.min(finished.length, 10));
        expect(newestFirst(shown)).toBe(true);
        for (const left of finished.filter((j) => !shown.includes(j))) {
          expect(at(left)).toBeLessThanOrEqual(at(shown.at(-1)));
        }
      }),
    );
  });

  it('lists a job created later in the same second first', () => {
    jobs.put(job('early', { created: '2026-10-14T15:00:00.12Z' }));
    jobs.put(job('late', { created: '2026-10-14T15:00:00.123Z' }));
    expect(jobs.list.map((j) => j.id)).toEqual(['late', 'early']);
  });

  it('lists jobs by when they were created, as Go writes the time in any zone', () => {
    // as Go's RFC3339Nano writes ns nanoseconds past the epoch second s, at
    // an offset of off minutes: trailing zeros trimmed
    const goTime = ([s, ns, off]) => {
      const local = new Date((s + off * 60) * 1000).toISOString().slice(0, 19);
      const frac = String(ns).padStart(9, '0').replace(/0+$/, '');
      const zone = off
        ? `${off < 0 ? '-' : '+'}${new Date(Math.abs(off) * 60000).toISOString().slice(11, 16)}`
        : 'Z';
      return `${local}${frac && `.${frac}`}${zone}`;
    };
    const second = Date.UTC(2026, 9, 14) / 1000;
    const instant = fc.tuple(
      fc.integer({ min: second, max: second + 2 }),
      fc.oneof(
        fc.integer({ min: 0, max: 999_999_999 }),
        fc.constantFrom(0, 120_000_000, 123_000_000, 123_400_000, 123_456_789),
      ),
      fc.constantFrom(0, -360, 330, 600),
    );
    fc.assert(
      fc.property(
        fc.uniqueArray(instant, {
          selector: ([s, ns]) => `${s}.${ns}`,
          maxLength: 8,
        }),
        (given) => {
          setActivePinia(createPinia());
          const store = useJobStore();
          given.forEach((at, i) =>
            store.put(job(`j${i}`, { created: goTime(at) })),
          );
          const want = given
            .map((at, i) => [at, `j${i}`])
            .sort(([[as, an]], [[bs, bn]]) => bs - as || bn - an)
            .map(([, id]) => id);
          expect(store.list.map((j) => j.id)).toEqual(want);
        },
      ),
    );
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

  // the times jobs/j1 was asked for, in ms from now
  function checks() {
    const start = Date.now();
    const at = [];
    axios.get.mockImplementation(async (url) => {
      if (url === 'jobs/j1') at.push(Date.now() - start);
      return { data: job('j1') };
    });
    return at;
  }

  it('stops asking while the server shuts down, asks once when it is back, then every 5 s', async () => {
    vi.useFakeTimers();
    jobs.put(job('j1'));
    const at = checks();
    jobs.whenDone('j1').catch(() => {});

    await vi.advanceTimersByTimeAsync(5000);
    setWsDown(true, 'shutdown');
    await vi.advanceTimersByTimeAsync(40_000);
    expect(at).toEqual([5000]);

    setWsDown(false);
    await vi.advanceTimersByTimeAsync(BACK_SPREAD_MS);
    expect(at).toHaveLength(2);
    const back = at[1];
    await vi.advanceTimersByTimeAsync(10_000);
    expect(at).toEqual([5000, back, back + 5000, back + 10_000]);
    jobs.clear();
  });

  it('asks less often while the server does not answer', async () => {
    vi.useFakeTimers();
    jobs.put(job('j1'));
    const at = [];
    axios.get.mockImplementation(async (url) => {
      if (url === 'jobs/j1') at.push(Date.now());
      // as the axios client notes a request with no response
      serverUnanswered();
      throw { code: 'ERR_NETWORK', request: {} };
    });
    const start = Date.now();
    jobs.whenDone('j1').catch(() => {});

    await vi.advanceTimersByTimeAsync(80_000);
    expect(at.map((t) => t - start)).toEqual([5000, 15_000, 35_000, 75_000]);
    jobs.clear();
  });

  it('asks one check at a time, though the server is slow', async () => {
    vi.useFakeTimers();
    jobs.put(job('j1'));
    axios.get.mockImplementation(() => new Promise(() => {}));
    jobs.whenDone('j1').catch(() => {});

    await vi.advanceTimersByTimeAsync(30_000);
    expect(axios.get).toHaveBeenCalledTimes(1);
    jobs.clear();
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

    // downloads through a link on the page, not a window a browser may block
    let link;
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, 'click')
      .mockImplementation(function () {
        link = {
          href: this.href,
          download: this.download,
          onPage: this.isConnected,
        };
      });
    jobs.downloadArtifact({ id: 'j1', artifact: { name: 'exp-files.zip' } });
    click.mockRestore();
    expect(link).toEqual({
      href: 'http://phenix.test/api/v1/jobs/j1/artifact?token=t',
      download: 'exp-files.zip',
      onPage: true,
    });
    expect(document.querySelector('a')).toBe(null);

    jobs.put(job('j1'));
    jobs.clear();
    expect(jobs.list).toEqual([]);
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

  it('writes any byte count in the largest unit it fills, to a tenth', () => {
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
    // counts near each unit's start, where the unit changes
    const near = fc
      .tuple(fc.integer({ min: 1, max: 4 }), fc.integer({ min: -9, max: 9 }))
      .map(([unit, off]) => 1024 ** unit + off);
    fc.assert(
      fc.property(fc.oneof(fc.maxSafeNat(), near), (bytes) => {
        const [value, unitName] = bytesText(bytes).split(' ');
        const unit = units.indexOf(unitName);
        expect(unit).toBeGreaterThanOrEqual(0);
        // shown as at least 1 of its unit, and below 1024 unless the largest
        if (unit > 0) expect(Number(value)).toBeGreaterThanOrEqual(1);
        if (unit < units.length - 1) expect(Number(value)).toBeLessThan(1024);
        expect(
          Math.abs(Number(value) * 1024 ** unit - bytes),
        ).toBeLessThanOrEqual(0.05 * 1024 ** unit);
      }),
    );
  });

  it('writes 1 MiB less a byte as 1.0 MiB', () => {
    expect(bytesText(1024 ** 2 - 1)).toBe('1.0 MiB');
  });
});
