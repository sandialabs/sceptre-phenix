// The freshness rules and how responses report it (utils/freshness.js).
import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  FreshnessMeta,
  mergeFreshness,
  readFreshness,
  resourceFor,
  RESOURCES,
  serverResponded,
  serverShuttingDown,
  setWsDown,
  SHUTDOWN_HOLD_MS,
  shutdownWithin,
  staleReason,
  wsCloseCause,
  wsDownReason,
} from '@/utils/freshness.js';
import { isoTimeArb } from './helpers/arbitraries.js';

describe('readFreshness', () => {
  it('keeps the lowest seq of the responses of one load', () => {
    const meta = new FreshnessMeta();
    readFreshness(meta, { 'x-phenix-seq': '30' }, 0, 0);
    readFreshness(meta, { 'x-phenix-seq': '20' }, 0, 0);
    readFreshness(meta, { 'x-phenix-seq': '25' }, 0, 0);
    expect(meta.seq).toBe(20);
  });

  it("measures the data's age by the server's clock", () => {
    // the server read the data 5 s before it answered; the browser's clock
    // is two hours ahead of the server's
    const receivedAt = Date.parse('2026-10-14T12:00:00Z');
    const meta = readFreshness(
      new FreshnessMeta(),
      {
        date: 'Wed, 14 Oct 2026 10:00:05 GMT',
        'x-phenix-observed-at': '2026-10-14T10:00:00Z',
      },
      receivedAt - 100,
      receivedAt,
    );
    expect(new Date(meta.observedAt).toISOString()).toBe(
      '2026-10-14T11:59:55.000Z',
    );
  });

  it('takes data without an observation time as read when it was asked for', () => {
    const meta = readFreshness(new FreshnessMeta(), {}, 1000, 3000);
    expect(meta.observedAt).toBe(1000);
    expect(meta.seq).toBeUndefined();
  });

  it('is never newer than the oldest part, and stale if any part is', () => {
    const meta = new FreshnessMeta();
    readFreshness(meta, { 'x-phenix-stale': 'true' }, 500, 600);
    readFreshness(meta, {}, 900, 1000);
    expect(meta).toMatchObject({ observedAt: 500, stale: true });
  });

  it('reads headers from an axios headers object', () => {
    const headers = new Map([['x-phenix-seq', '7']]);
    expect(readFreshness(new FreshnessMeta(), headers, 0, 0).seq).toBe(7);
  });

  it('folds in the freshness of a load built on another', () => {
    const meta = Object.assign(new FreshnessMeta(), { seq: 9, observedAt: 50 });
    mergeFreshness(meta, { seq: 4, observedAt: 80, stale: true });
    expect(meta).toMatchObject({ seq: 4, observedAt: 50, stale: true });
    expect(mergeFreshness(undefined, meta)).toBeUndefined();
  });

  // one response's freshness headers, each present or not; seq may be junk
  const response = fc.record(
    {
      'x-phenix-seq': fc.oneof(
        fc.integer({ min: -5, max: 1e6 }).map(String),
        fc.constantFrom('', 'abc', 'NaN', 'Infinity'),
      ),
      'x-phenix-stale': fc.constantFrom('true', 'false'),
    },
    { requiredKeys: [] },
  );
  const sentAt = fc.integer({ min: 0, max: 1e12 });

  it('reports the lowest seq, the earliest read and any staleness of the parts, in any order', () => {
    fc.assert(
      fc.property(
        fc.array(fc.tuple(response, sentAt), { minLength: 1, maxLength: 6 }),
        (parts) => {
          const meta = new FreshnessMeta();
          for (const [headers, at] of parts)
            readFreshness(meta, headers, at, at);
          const seqs = parts
            .map(([h]) => Number(h['x-phenix-seq']))
            .filter((seq) => Number.isFinite(seq) && seq > 0);
          expect(meta).toEqual({
            ...new FreshnessMeta(),
            seq: seqs.length ? Math.min(...seqs) : undefined,
            observedAt: Math.min(...parts.map(([, at]) => at)),
            stale: parts.some(([h]) => h['x-phenix-stale'] === 'true'),
          });
        },
      ),
    );
  });

  it('is never newer than the response, whatever the clocks say', () => {
    fc.assert(
      fc.property(
        isoTimeArb,
        isoTimeArb,
        sentAt,
        fc.nat(60_000),
        (observed, { ms: date }, sent, wait) => {
          const headers = {
            date: new Date(date).toUTCString(),
            'x-phenix-observed-at': observed.time,
          };
          const meta = readFreshness(
            new FreshnessMeta(),
            headers,
            sent,
            sent + wait,
          );
          // the Date header keeps whole seconds only
          const age = Math.floor(date / 1000) * 1000 - observed.ms;
          expect(meta.observedAt).toBe(sent + wait - Math.max(0, age));
          expect(meta.observedAt).toBeLessThanOrEqual(sent + wait);
        },
      ),
    );
  });

  const meta = fc
    .record(
      { seq: fc.nat(1000), observedAt: fc.nat(1e6), stale: fc.boolean() },
      { requiredKeys: ['stale'] },
    )
    .map((fields) => Object.assign(new FreshnessMeta(), fields));

  it('folds loads in any order and any number of times to the same result', () => {
    fc.assert(
      fc.property(meta, meta, (a, b) => {
        const ab = mergeFreshness(structuredClone(a), b);
        expect(mergeFreshness(structuredClone(b), a)).toEqual(ab);
        expect(mergeFreshness(structuredClone(ab), b)).toEqual(ab);
        expect(mergeFreshness(structuredClone(a), a)).toEqual(a);
      }),
    );
  });
});

describe('staleReason', () => {
  const now = 10 * 60 * 1000;

  it.each([
    [
      'the server served earlier data',
      { stale: true },
      RESOURCES.hosts,
      null,
      'server',
    ],
    [
      'it changed elsewhere',
      { invalidated: true },
      RESOURCES.configs,
      null,
      'invalidated',
    ],
    [
      'live updates stopped',
      { observedAt: now },
      RESOURCES.experiments,
      now - 1,
      'disconnected',
    ],
    [
      'it is older than its bound',
      { observedAt: now - 91000 },
      RESOURCES.hosts,
      null,
      'age',
    ],
    [
      'it is within its bound',
      { observedAt: now - 89000 },
      RESOURCES.hosts,
      null,
      null,
    ],
    [
      'a page that is not live lost the connection',
      { observedAt: now },
      RESOURCES.hosts,
      now - 1,
      null,
    ],
  ])('%s', (_, state, resource, disconnectedSince, reason) => {
    expect(staleReason(state, resource, now, disconnectedSince)).toBe(reason);
  });

  it.each([
    ['shutdown', 'shutdown'],
    ['behind', 'disconnected'],
    ['lost', 'disconnected'],
  ])('calls live updates stopped by a %s close %s', (cause, reason) => {
    const state = { observedAt: now };
    expect(staleReason(state, RESOURCES.experiments, now, now, cause)).toBe(
      reason,
    );
    // a page that is not live is not out of date for it
    expect(staleReason(state, RESOURCES.hosts, now, now, cause)).toBe(null);
  });

  const state = fc.record(
    {
      stale: fc.boolean(),
      invalidated: fc.boolean(),
      observedAt: fc.option(fc.integer({ min: 0, max: now }), {
        nil: undefined,
      }),
    },
    { requiredKeys: [] },
  );
  const resource = fc.constantFrom(undefined, ...Object.values(RESOURCES));
  const disconnectedSince = fc.option(fc.nat(now));
  const cause = fc.constantFrom('shutdown', 'behind', 'lost');

  it('gives the reasons in order: server, invalidated, shutdown or disconnected, age', () => {
    fc.assert(
      fc.property(
        state,
        resource,
        disconnectedSince,
        cause,
        (s, r, down, c) => {
          const reason = staleReason(s, r, now, down, c);
          expect(reason === 'server').toBe(!!s.stale);
          if (!s.stale) expect(reason === 'invalidated').toBe(!!s.invalidated);
          if (!s.stale && !s.invalidated) {
            const cut = !!r?.live && down !== null;
            expect(reason === 'shutdown').toBe(cut && c === 'shutdown');
            expect(reason === 'disconnected').toBe(cut && c !== 'shutdown');
          }
        },
      ),
    );
  });

  it('calls data out of date by age only once it is older than its bound, and from then on', () => {
    const bounded = Object.values(RESOURCES).filter((r) => r.staleAfterMs);
    fc.assert(
      fc.property(
        fc.constantFrom(...bounded),
        fc.integer({ min: -2, max: 2 }),
        fc.nat(now),
        (r, nearBound, older) => {
          const age = r.staleAfterMs + nearBound;
          const reason = (a) =>
            staleReason({ observedAt: now - a }, r, now, null);
          expect(reason(age)).toBe(age > r.staleAfterMs ? 'age' : null);
          if (reason(age)) expect(reason(age + older)).toBe('age');
        },
      ),
    );
  });
});

describe('wsCloseCause', () => {
  it.each([
    ['the server shutting down', 1001, 'server shutting down', 'shutdown'],
    ['the server shutting down a console', 1001, '', 'shutdown'],
    ['a client too slow to keep up', 1013, 'client too slow', 'behind'],
    ['a socket that dropped without a close frame', 1006, '', 'lost'],
    ['a normal close', 1000, 'server shutting down', 'lost'],
  ])('names %s', (_, code, reason, cause) => {
    expect(wsCloseCause({ code, reason })).toBe(cause);
  });

  it('calls a close lost without an event', () => {
    expect(wsCloseCause(undefined)).toBe('lost');
  });

  it('calls every 1001 a shutdown, whatever its reason, and nothing else', () => {
    fc.assert(
      fc.property(
        fc.integer({ min: 1000, max: 4999 }),
        fc.string(),
        (code, reason) => {
          expect(wsCloseCause({ code, reason }) === 'shutdown').toBe(
            code === 1001,
          );
        },
      ),
    );
  });
});

describe('the server shutting down', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    setWsDown(false);
    vi.useRealTimers();
  });

  it('holds through the failed reconnects that follow, until the socket is back up', () => {
    setWsDown(true, 'shutdown');
    setWsDown(true, 'lost');
    setWsDown(true, 'behind');
    expect(wsDownReason()).toBe('shutdown');
    expect(serverShuttingDown()).toBe(true);

    setWsDown(false);
    expect(wsDownReason()).toBe(null);
    expect(serverShuttingDown()).toBe(false);
  });

  it('reports the latest cause of a drop that was not a shutdown', () => {
    setWsDown(true, 'behind');
    expect(wsDownReason()).toBe('behind');
    setWsDown(true);
    expect(wsDownReason()).toBe('lost');
    expect(serverShuttingDown()).toBe(false);
  });

  it('is believed only for a while without the server answering', () => {
    setWsDown(true, 'shutdown');
    vi.advanceTimersByTime(SHUTDOWN_HOLD_MS - 1);
    expect(serverShuttingDown()).toBe(true);

    vi.advanceTimersByTime(1);
    expect(serverShuttingDown()).toBe(false);
    expect(wsDownReason()).toBe('lost');
    expect(staleReason({}, RESOURCES.experiments)).toBe('disconnected');
    // a later failed reconnect is a lost connection
    setWsDown(true, 'lost');
    expect(wsDownReason()).toBe('lost');
  });

  it('ends once the server answers a request sent after it said so', () => {
    const before = Date.now();
    vi.advanceTimersByTime(10);
    setWsDown(true, 'shutdown');

    // a request in flight as it shut down is finished, not a sign it is back
    serverResponded(before);
    expect(serverShuttingDown()).toBe(true);

    vi.advanceTimersByTime(10);
    serverResponded(Date.now());
    expect(serverShuttingDown()).toBe(false);
    expect(wsDownReason()).toBe('lost');
  });

  it('says the server is shutting down only after a shutdown, before it answers or the socket reopens', () => {
    const step = fc.oneof(
      fc.constantFrom('shutdown', 'behind', 'lost').map((c) => ['down', c]),
      fc.constantFrom(['up'], ['answered']),
    );
    fc.assert(
      fc.property(fc.array(step, { maxLength: 12 }), (steps) => {
        setWsDown(false);
        let shuttingDown = false;
        for (const [event, cause] of steps) {
          vi.advanceTimersByTime(1);
          if (event === 'down') setWsDown(true, cause);
          else if (event === 'up') setWsDown(false);
          else serverResponded(Date.now());
          if (event === 'down') shuttingDown ||= cause === 'shutdown';
          else shuttingDown = false;
        }
        expect(serverShuttingDown()).toBe(shuttingDown);
      }),
    );
  });

  it('is waited for briefly, as a dropped socket may come before the reason', async () => {
    const waited = shutdownWithin(1500);
    vi.advanceTimersByTime(1000);
    setWsDown(true, 'shutdown');
    await vi.advanceTimersByTimeAsync(500);
    expect(await waited).toBe(true);

    setWsDown(false);
    const never = shutdownWithin(1500);
    await vi.advanceTimersByTimeAsync(1500);
    expect(await never).toBe(false);
  });
});

describe('resourceFor', () => {
  it('finds the rules of a page and of each experiment page', () => {
    expect(resourceFor('hosts')).toBe(RESOURCES.hosts);
    expect(resourceFor('experiment/exp1').live).toBe(true);
    expect(resourceFor('experiment/exp1/stopped').live).toBe(true);
    expect(resourceFor('scorchruns/exp1').live).toBe(true);
    expect(resourceFor('nothing')).toBeUndefined();
    expect(resourceFor(null)).toBeUndefined();
  });

  it("invalidates an experiment's page only for messages about it", () => {
    const [matches] = resourceFor('experiment/exp1').invalidatedBy;
    const msg = (type, name) => ({ resource: { type, name } });
    expect(matches(msg('experiment', 'exp1'), 'experiment/exp1')).toBe(true);
    expect(matches(msg('experiment/vm', 'exp1/vm1'), 'experiment/exp1')).toBe(
      true,
    );
    expect(matches(msg('config', 'Experiment/exp1'), 'experiment/exp1')).toBe(
      true,
    );
    expect(matches(msg('experiment', 'exp10'), 'experiment/exp1')).toBe(false);
    expect(matches(msg('experiment/vm', 'exp10/vm1'), 'experiment/exp1')).toBe(
      false,
    );
  });

  it("invalidates an experiment's page for its own messages and no other experiment's", () => {
    const name = fc.stringMatching(/^[a-z0-9][a-z0-9_.-]{0,6}$/);
    const about = fc.constantFrom(
      (exp) => ({ type: 'experiment', name: exp }),
      (exp) => ({ type: 'experiment/vm', name: `${exp}/vm1`, action: 'stop' }),
      (exp) => ({ type: 'config', name: `Experiment/${exp}` }),
    );
    fc.assert(
      fc.property(
        name,
        name,
        about,
        fc.boolean(),
        (page, exp, msg, stopped) => {
          const key = stopped
            ? `experiment/${page}/stopped`
            : `experiment/${page}`;
          const [matches] = resourceFor(key).invalidatedBy;
          expect(matches({ resource: msg(exp) }, key)).toBe(page === exp);
        },
      ),
    );
  });

  it("ignores messages that change none of an experiment's state", () => {
    const [matches] = resourceFor('experiment/exp1').invalidatedBy;
    const msg = (type, action) => ({
      resource: { type, name: 'exp1/vm1', action },
    });
    const key = 'experiment/exp1';
    expect(matches(msg('experiment/vm/screenshot', 'update'), key)).toBe(false);
    expect(matches(msg('experiment/vm/commit', 'progress'), key)).toBe(false);
    expect(matches(msg('experiment/vm', 'stop'), key)).toBe(true);
    expect(matches(msg('experiment/vm/snapshot', 'create'), key)).toBe(true);
  });
});
