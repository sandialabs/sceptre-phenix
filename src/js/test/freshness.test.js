// The freshness rules and how responses report it (utils/freshness.js).
import { describe, expect, it } from 'vitest';
import {
  FreshnessMeta,
  mergeFreshness,
  readFreshness,
  resourceFor,
  RESOURCES,
  staleReason,
} from '@/utils/freshness.js';

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
});
