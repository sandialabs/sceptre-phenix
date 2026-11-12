// What the capture dialog, the Captures tab and the WebShark page show for an
// experiment's captures: support, budget notices, scopes, counts and names.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import {
  budgetBanner,
  budgetFull,
  budgetNotices,
  captureDrops,
  captureScope,
  captureSupport,
  countText,
  defaultCaptureName,
  filterProblem,
  hostFilters,
  msText,
  runningCount,
  shortReason,
  snaplenProblem,
  statusText,
  statusType,
  unsupportedNotice,
} from '@/components/experiment/experimentCaptures.js';

const OLD = 'requires minimega 3.4.0 or later; the head node runs 3.3.0';

// a budget row of compute1's experiment bridge
const row = (total, experiment, level = 'ok') => ({
  host: 'compute1',
  bridge: 'mega_bridge_exp1',
  total,
  experiment,
  limit: 32,
  level,
});

describe('captureSupport', () => {
  it.each([
    [
      'the list says it is available',
      { support: { available: true, reason: '', version: '3.4.0' } },
      [],
      { available: true, reason: '' },
    ],
    [
      'the list says it is not',
      { support: { available: false, reason: OLD, version: '3.3.0' } },
      ['minimega-captures'],
      { available: false, reason: OLD },
    ],
    [
      'the list gives no reason',
      { support: { available: false, reason: '', version: '' } },
      [],
      { available: false, reason: 'the server did not report capture support' },
    ],
    [
      'no list, with the feature',
      null,
      ['webshark', 'minimega-captures'],
      { available: true, reason: '' },
    ],
    [
      'no list, without the feature',
      null,
      ['webshark'],
      { available: false, reason: 'the server did not report capture support' },
    ],
    [
      'no list and no features',
      null,
      undefined,
      { available: false, reason: 'the server did not report capture support' },
    ],
    [
      'a list from an older server, without support',
      { captures: [] },
      ['minimega-captures'],
      { available: true, reason: '' },
    ],
  ])('when %s', (_, list, features, want) => {
    expect(captureSupport(list, features)).toEqual(want);
  });

  it('shortens a reason to its part before the semicolon', () => {
    expect(shortReason(OLD)).toBe('requires minimega 3.4.0 or later');
    expect(shortReason('')).toBe('the server did not report capture support');
  });

  it('says what to do when not available', () => {
    expect(unsupportedNotice(OLD)).toBe(
      'Experiment captures are not available. Requires minimega 3.4.0 or' +
        ' later; the head node runs 3.3.0. VM interface captures still' +
        ' work, without the new options.',
    );
  });

  it('says what to do with the default reason, ending one full stop', () => {
    const want =
      'Experiment captures are not available. The server did not report' +
      ' capture support. VM interface captures still work, without the new' +
      ' options.';
    expect(unsupportedNotice('')).toBe(want);
    expect(unsupportedNotice(undefined)).toBe(want);
    expect(
      unsupportedNotice(' the server did not report capture support. '),
    ).toBe(want);
  });
});

describe('the budget notices of the capture dialog', () => {
  it.each([
    ['a quiet bridge', [row(4, 2)], []],
    [
      'the 17th mirror of the experiment',
      [row(16, 16)],
      [
        {
          type: 'is-warning',
          text:
            'This capture uses mirror 17 of 32 on compute1 / mega_bridge_exp1.' +
            ' exp1 will use more than half of the Open vSwitch mirror limit there.',
        },
      ],
    ],
    [
      'the 28th mirror of the bridge',
      [row(27, 5)],
      [
        {
          type: 'is-warning',
          text:
            'This capture uses mirror 28 of 32 on compute1 / mega_bridge_exp1.' +
            ' 4 are left there after it.',
        },
      ],
    ],
    [
      'the last mirror of the bridge',
      [row(31, 20)],
      [
        {
          type: 'is-warning',
          text:
            'This capture uses mirror 32 of 32 on compute1 / mega_bridge_exp1.' +
            ' exp1 will use more than half of the Open vSwitch mirror limit' +
            ' there. No mirror is left there after it.',
        },
      ],
    ],
    [
      'a full bridge',
      [row(32, 21, 'full')],
      [
        {
          type: 'is-danger',
          text:
            'compute1 / mega_bridge_exp1 has 32 of 32 Open vSwitch mirrors.' +
            ' Stop a capture or a tap mirror on that host to free one. exp1' +
            ' uses 21; 11 are from other experiments or tools.',
        },
      ],
    ],
  ])('for %s', (_, budget, want) => {
    expect(budgetNotices(budget, 'exp1')).toEqual(want);
  });

  it('takes the Open vSwitch limit of 32 for a row that gives none', () => {
    const { limit: _, ...noLimit } = row(27, 5);
    expect(budgetNotices([noLimit], 'exp1')).toEqual([
      {
        type: 'is-warning',
        text:
          'This capture uses mirror 28 of 32 on compute1 / mega_bridge_exp1.' +
          ' 4 are left there after it.',
      },
    ]);
    expect(budgetFull([{ ...noLimit, total: 32 }])).toBe(true);
  });

  it('gives no notice without a budget', () => {
    expect(budgetNotices(undefined, 'exp1')).toEqual([]);
    expect(budgetNotices(null, 'exp1', { someVlans: true })).toEqual([]);
  });

  it('only warns of a full bridge for a capture of some VLANs', () => {
    expect(
      budgetNotices([row(32, 21, 'full')], 'exp1', { someVlans: true }),
    ).toEqual([
      {
        type: 'is-warning',
        text:
          'compute1 / mega_bridge_exp1 has 32 of 32 Open vSwitch mirrors.' +
          ' The start may be refused when the selected VLANs use that bridge.' +
          ' Stop a capture or a tap mirror on that host to free one. exp1' +
          ' uses 21; 11 are from other experiments or tools.',
      },
    ]);
  });

  it('refuses a start only where a bridge is full', () => {
    expect(budgetFull([row(31, 1), row(32, 0)])).toBe(true);
    expect(budgetFull([row(31, 1)])).toBe(false);
    expect(budgetFull(undefined)).toBe(false);
  });

  it('gives a notice for each row past half or near the limit, and none else', () => {
    const rowArb = fc.record({
      host: fc.constantFrom('c1', 'c2'),
      bridge: fc.constant('br'),
      total: fc.integer({ min: 0, max: 40 }),
      experiment: fc.integer({ min: 0, max: 40 }),
      limit: fc.constant(32),
      level: fc.constant('ok'),
    });
    fc.assert(
      fc.property(fc.array(rowArb, { maxLength: 5 }), (budget) => {
        const notices = budgetNotices(budget, 'exp1');
        const flagged = budget.filter(
          (r) => r.total >= 32 || r.total + 1 >= 28 || r.experiment + 1 > 16,
        );
        expect(notices).toHaveLength(flagged.length);
        expect(notices.some((n) => n.type === 'is-danger')).toBe(
          budgetFull(budget),
        );
      }),
      { numRuns: 200 },
    );
  });

  // none (0) < is-warning (1) < is-danger (2)
  const severity = (notices) =>
    Math.max(0, ...notices.map((n) => (n.type === 'is-danger' ? 2 : 1)));

  it('never warns less as a bridge gets more mirrors', () => {
    const counts = fc.integer({ min: 0, max: 40 });
    fc.assert(
      fc.property(
        counts,
        counts,
        counts,
        counts,
        fc.boolean(),
        (t1, t2, e1, e2, someVlans) => {
          const [lowTotal, highTotal] = t1 <= t2 ? [t1, t2] : [t2, t1];
          const [lowExp, highExp] = e1 <= e2 ? [e1, e2] : [e2, e1];
          const level = (total, experiment) =>
            severity(
              budgetNotices([row(total, experiment)], 'exp1', { someVlans }),
            );
          expect(level(lowTotal, lowExp)).toBeLessThanOrEqual(
            level(highTotal, lowExp),
          );
          expect(level(lowTotal, lowExp)).toBeLessThanOrEqual(
            level(lowTotal, highExp),
          );
          expect(level(lowTotal, lowExp)).toBeLessThanOrEqual(
            level(highTotal, highExp),
          );
        },
      ),
      { numRuns: 300 },
    );
  });

  it('never warns more for a capture of some VLANs than for all of them', () => {
    const rowArb = fc
      .tuple(fc.integer({ min: 0, max: 40 }), fc.integer({ min: 0, max: 40 }))
      .map(([total, experiment]) => row(total, experiment));
    fc.assert(
      fc.property(fc.array(rowArb, { maxLength: 4 }), (budget) => {
        const some = budgetNotices(budget, 'exp1', { someVlans: true });
        const all = budgetNotices(budget, 'exp1');
        expect(some).toHaveLength(all.length);
        expect(some.some((n) => n.type === 'is-danger')).toBe(false);
        expect(severity(some)).toBeLessThanOrEqual(severity(all));
      }),
      { numRuns: 200 },
    );
  });
});

describe('the budget banner of the Captures tab', () => {
  it.each([
    ['an ok bridge', [row(4, 2)], []],
    [
      'a bridge past half for the experiment',
      [row(18, 17, 'half')],
      [
        {
          type: 'is-warning',
          text: 'exp1 uses 17 of 32 mirrors on compute1 / mega_bridge_exp1.',
        },
      ],
    ],
    [
      'a bridge near the limit',
      [row(29, 18, 'near')],
      [
        {
          type: 'is-warning',
          text:
            'compute1 / mega_bridge_exp1 has 29 of 32 Open vSwitch mirrors;' +
            ' 3 are left. exp1 uses 18 of them, other experiments and tools' +
            ' 11. A capture that needs a 33rd mirror is refused.',
        },
      ],
    ],
    [
      'a full bridge',
      [row(32, 21, 'full')],
      [
        {
          type: 'is-danger',
          text:
            'compute1 / mega_bridge_exp1 has 32 of 32 Open vSwitch mirrors;' +
            ' none are left. exp1 uses 21 of them, other experiments and' +
            ' tools 11. A capture that needs a 33rd mirror is refused.',
        },
      ],
    ],
    [
      'a bridge with one mirror left',
      [row(31, 2, 'near')],
      [
        {
          type: 'is-warning',
          text:
            'compute1 / mega_bridge_exp1 has 31 of 32 Open vSwitch mirrors;' +
            ' 1 is left. exp1 uses 2 of them, other experiments and tools' +
            ' 29. A capture that needs a 33rd mirror is refused.',
        },
      ],
    ],
  ])('for %s', (_, budget, want) => {
    expect(budgetBanner(budget, 'exp1')).toEqual(want);
  });

  it('shows nothing without a budget', () => {
    expect(budgetBanner(undefined, 'exp1')).toEqual([]);
  });

  it.each([
    [10, '11th'],
    [11, '12th'],
    [12, '13th'],
    [20, '21st'],
    [21, '22nd'],
    [22, '23rd'],
    [23, '24th'],
    [100, '101st'],
    [110, '111th'],
    [111, '112th'],
    [112, '113th'],
    [undefined, '33rd'],
  ])('names the mirror past a limit of %s the %s', (limit, nth) => {
    const [line] = budgetBanner([{ ...row(5, 1, 'full'), limit }], 'exp1');
    expect(line.text).toMatch(
      new RegExp(`A capture that needs a ${nth} mirror is refused\\.$`),
    );
  });

  it('never shows a milder banner for a higher budget level', () => {
    const LEVELS = ['ok', 'half', 'near', 'full'];
    const rank = (lines) =>
      lines.length === 0 ? 0 : lines[0].type === 'is-danger' ? 2 : 1;
    fc.assert(
      fc.property(
        fc.integer({ min: 0, max: 40 }),
        fc.integer({ min: 0, max: 40 }),
        fc.integer({ min: 0, max: 3 }),
        fc.integer({ min: 0, max: 3 }),
        (total, experiment, a, b) => {
          const [low, high] = a <= b ? [a, b] : [b, a];
          const at = (i) =>
            budgetBanner([row(total, experiment, LEVELS[i])], 'exp1');
          expect(at(low).length).toBeLessThanOrEqual(1);
          expect(rank(at(low))).toBeLessThanOrEqual(rank(at(high)));
        },
      ),
      { numRuns: 200 },
    );
  });
});

describe('a capture in the table', () => {
  it.each([
    ['running', 'is-success'],
    ['starting', 'is-success'],
    ['stalled', 'is-success'],
    ['stopping', 'is-success'],
    ['failed', 'is-danger'],
    ['full', 'is-warning'],
    ['stopped', null],
    ['orphaned', null],
  ])('tags a %s capture %s', (state, want) => {
    expect(statusType({ state })).toBe(want);
  });

  it.each([
    ['running', 'Running'],
    ['stopped', 'Stopped'],
    ['failed', 'Failed'],
    ['', 'Unknown'],
    [undefined, 'Unknown'],
  ])('names a %j capture %s', (state, want) => {
    expect(statusText({ state })).toBe(want);
  });

  it('counts the running captures', () => {
    expect(
      runningCount([
        { state: 'running' },
        { state: 'stopped' },
        { state: 'starting' },
        { state: 'failed' },
      ]),
    ).toBe(2);
    expect(runningCount(undefined)).toBe(0);
  });

  it.each([
    [{ vlans: [], filter: '' }, 'All VLANs'],
    [
      { vlans: ['EXP_1', 'EXP_2'], filter: 'tcp port 80' },
      'EXP_1, EXP_2 · tcp port 80',
    ],
    [{ vlans: ['EXP_2'] }, 'EXP_2'],
  ])('gives the scope of %j as %s', (capture, want) => {
    expect(captureScope(capture)).toBe(want);
  });

  it('adds the kernel, pcap and queue drops, not the lost or late frames', () => {
    expect(
      captureDrops({ kdrops: '4', pdrops: 3, qdrops: 2, lost: 1, late: 99 }),
    ).toBe(9);
    expect(captureDrops(undefined)).toBe(0);
  });

  it.each([
    [80, {}, '80 ms'],
    [0.3, { signed: true }, '+0.3 ms'],
    [-0.6, { signed: true }, '−0.6 ms'],
    [612.4, { signed: true }, '+612 ms'],
    [0, { signed: true }, '0 ms'],
    [undefined, {}, '0.0 ms'],
    [-12.4, {}, '−12 ms'],
    [-0.25, {}, '−0.3 ms'],
  ])('writes %s ms %j as %s', (ms, opts, want) => {
    expect(msText(ms, opts)).toBe(want);
  });

  it.each([
    [1284113, '1,284,113'],
    ['98221', '98,221'],
    [0, '0'],
    [undefined, '0'],
    [null, '0'],
  ])('writes the count %j as %s', (value, want) => {
    expect(countText(value)).toBe(want);
  });
});

describe('the capture dialog fields', () => {
  it.each([
    ['', ''],
    ['tcp port 80', ''],
    ['host 10.0.0.1 and (udp or icmp) && !arp', ''],
    ['vlan 101 and ether[12:2] = 0x0800', ''],
    ['host "a"', 'Use only letters'],
    ["port '80'", 'Use only letters'],
    ['a\\b', 'Use only letters'],
    ['tcp # comment', 'Use only letters'],
    ['x'.repeat(1025), 'Use 1024 characters or fewer.'],
    [undefined, ''],
    [null, ''],
    ['x'.repeat(1024), ''],
    ['tcp\n', 'Use only letters'],
  ])('checks the filter %j', (filter, want) => {
    const problem = filterProblem(filter);
    if (want) expect(problem).toContain(want);
    else expect(problem).toBe('');
  });

  it('accepts no filter with a quote, backslash or #', () => {
    fc.assert(
      fc.property(
        fc.string({ maxLength: 40 }),
        fc.constantFrom('"', "'", '\\', '#'),
        fc.string({ maxLength: 40 }),
        (a, bad, b) => {
          expect(filterProblem(`${a}${bad}${b}`)).not.toBe('');
        },
      ),
      { numRuns: 200 },
    );
  });

  it.each([
    ['1600', ''],
    ['14', ''],
    ['65535', ''],
    ['13', 'Use a whole number from 14 to 65535.'],
    ['65536', 'Use a whole number from 14 to 65535.'],
    ['16.5', 'Use a whole number from 14 to 65535.'],
    ['', 'Use a whole number from 14 to 65535.'],
    ['1e3', 'Use a whole number from 14 to 65535.'],
    [' 9000 ', ''],
    [9000, ''],
    [undefined, 'Use a whole number from 14 to 65535.'],
    [null, 'Use a whole number from 14 to 65535.'],
  ])('checks the snaplen %j', (value, want) => {
    expect(snaplenProblem(value)).toBe(want);
  });

  // ValidateCaptureFilter in src/go/util/mm/captures.go: at most 1024 bytes,
  // each one of these characters
  const GO_FILTER_CHARS = new Set(
    'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789 .:/[]()&|!<>=+-*,',
  );
  const goAccepts = (filter) =>
    new TextEncoder().encode(filter).length <= 1024 &&
    [...filter].every((c) => GO_FILTER_CHARS.has(c));

  it('accepts a filter exactly when the server does', () => {
    const allowed = fc.constantFrom(...GO_FILTER_CHARS);
    const any = fc.oneof(
      { weight: 8, arbitrary: allowed },
      { weight: 1, arbitrary: fc.string({ unit: 'grapheme', maxLength: 1 }) },
      {
        weight: 1,
        arbitrary: fc.constantFrom('"', "'", '\\', '#', '\n', '\t'),
      },
    );
    const filter = fc.oneof(
      fc.array(any, { maxLength: 40 }).map((cs) => cs.join('')),
      fc
        .tuple(fc.integer({ min: 1015, max: 1035 }), any)
        .map(([n, c]) => 'a'.repeat(n) + c),
    );
    fc.assert(
      fc.property(filter, (f) => {
        const problem = filterProblem(f);
        expect(problem === '').toBe(goAccepts(f));
        if (f.length > 1024) {
          expect(problem).toBe('Use 1024 characters or fewer.');
        }
      }),
      { numRuns: 300 },
    );
  });

  it('accepts a snaplen exactly when it is a whole number from 14 to 65535', () => {
    fc.assert(
      fc.property(
        fc.integer({ min: 0, max: 200000 }),
        fc.constantFrom('', ' ', '  '),
        fc.constantFrom('', ' ', '\t'),
        (n, before, after) => {
          const ok = n >= 14 && n <= 65535;
          expect(snaplenProblem(`${before}${n}${after}`) === '').toBe(ok);
          expect(snaplenProblem(n) === '').toBe(ok);
        },
      ),
      { numRuns: 300 },
    );
  });

  it('refuses a snaplen with a sign, a point or an exponent', () => {
    fc.assert(
      fc.property(
        fc.integer({ min: 14, max: 65535 }),
        fc.constantFrom('+', '-', '.0', 'e0', '0x', ' x'),
        fc.boolean(),
        (n, extra, first) => {
          const text = first ? `${extra}${n}` : `${n}${extra}`;
          expect(snaplenProblem(text)).toBe(
            'Use a whole number from 14 to 65535.',
          );
        },
      ),
      { numRuns: 200 },
    );
  });
});

describe('defaultCaptureName', () => {
  const at = new Date(2026, 10, 5, 14, 22, 33);

  it.each([
    [[], 'pcapng', 'exp1-20261105-142233.pcapng'],
    [['EXP_1'], 'pcapng', 'exp1-EXP_1-20261105-142233.pcapng'],
    [['EXP_1', 'EXP_2'], 'pcap', 'exp1-20261105-142233.pcap'],
  ])('names a capture of %j in %s %s', (vlans, format, want) => {
    expect(defaultCaptureName('exp1', vlans, format, at)).toBe(want);
  });

  // as api/capture fileName does
  it.each([
    ['.hidden', [], 'pcapng', 'hidden-20261105-142233.pcapng'],
    ['..', ['.x'], 'pcap', '-.x-20261105-142233.pcap'],
    ['a b/ü😀', [], 'pcapng', 'a_b___-20261105-142233.pcapng'],
    ['e'.repeat(250), [], 'pcap', `${'e'.repeat(200)}.pcap`],
  ])('makes %j a file name as the server does', (exp, vlans, format, want) => {
    expect(defaultCaptureName(exp, vlans, format, at)).toBe(want);
  });

  it("gives every name only the server's characters and the format's extension", () => {
    fc.assert(
      fc.property(
        fc.string({ maxLength: 20 }),
        fc.array(fc.string({ maxLength: 10 }), { maxLength: 3 }),
        fc.constantFrom('pcapng', 'pcap'),
        fc.date({
          min: new Date(2000, 0, 1),
          max: new Date(2099, 11, 31),
          noInvalidDate: true,
        }),
        (exp, vlans, format, time) => {
          const name = defaultCaptureName(exp, vlans, format, time);
          expect(name).toMatch(/^[A-Za-z0-9_-][A-Za-z0-9._-]*$/);
          expect(name.endsWith(`.${format}`)).toBe(true);
          expect(name).toMatch(/-\d{8}-\d{6}\.pcap(ng)?$/);
        },
      ),
      { numRuns: 200 },
    );
  });

  it('keeps a name to 200 characters before its extension, with no path or leading dot', () => {
    fc.assert(
      fc.property(
        fc.string({ unit: 'grapheme', maxLength: 260 }),
        fc.array(fc.string({ maxLength: 10 }), { maxLength: 3 }),
        fc.constantFrom('pcapng', 'pcap'),
        (exp, vlans, format) => {
          const name = defaultCaptureName(exp, vlans, format, at);
          const stem = name.slice(0, -(format.length + 1));
          expect(stem.length).toBeGreaterThan(0);
          expect(stem.length).toBeLessThanOrEqual(200);
          expect(stem.startsWith('.')).toBe(false);
          expect(name).not.toMatch(/[/\\]/);
          expect(name.split('.').pop()).toBe(format);
        },
      ),
      { numRuns: 200 },
    );
  });

  it('names the VLAN of a capture of exactly one, and no VLAN else', () => {
    const alias = fc.stringMatching(/^[A-Z][A-Z0-9_]{0,7}$/);
    fc.assert(
      fc.property(
        fc.uniqueArray(alias, { maxLength: 3 }),
        fc.constantFrom('pcapng', 'pcap'),
        (vlans, format) => {
          const name = defaultCaptureName('exp1', vlans, format, at);
          const want =
            vlans.length === 1
              ? `exp1-${vlans[0]}-20261105-142233.${format}`
              : `exp1-20261105-142233.${format}`;
          expect(name).toBe(want);
        },
      ),
      { numRuns: 100 },
    );
  });
});

describe('hostFilters', () => {
  const stream = (host, bridge) => ({ host, bridge });

  it('gives one display filter for each host of a pcapng capture', () => {
    expect(
      hostFilters({
        format: 'pcapng',
        streams: [
          stream('compute1', 'mega_bridge_exp1'),
          stream('compute2', 'mega_bridge_exp1'),
        ],
      }),
    ).toEqual([
      {
        label: 'compute1 only',
        filter: 'frame.interface_name == "compute1/mega_bridge_exp1"',
      },
      {
        label: 'compute2 only',
        filter: 'frame.interface_name == "compute2/mega_bridge_exp1"',
      },
    ]);
  });

  it('names the bridge of a host with several', () => {
    const labels = hostFilters({
      format: 'pcapng',
      streams: [stream('c1', 'a'), stream('c1', 'b')],
    }).map((f) => f.label);
    expect(labels).toEqual(['c1 / a only', 'c1 / b only']);
  });

  it('gives none for a pcapng capture without streams, or a stream without a bridge', () => {
    expect(hostFilters({ format: 'pcapng' })).toEqual([]);
    expect(hostFilters(undefined)).toEqual([]);
    expect(
      hostFilters({ format: 'pcapng', streams: [{ host: 'c1', bridge: '' }] }),
    ).toEqual([]);
  });

  it('gives none for classic pcap, or for a name a filter cannot quote', () => {
    expect(
      hostFilters({ format: 'pcap', streams: [stream('c1', 'a')] }),
    ).toEqual([]);
    expect(
      hostFilters({ format: 'pcapng', streams: [stream('c"1', 'a')] }),
    ).toEqual([]);
  });
});
