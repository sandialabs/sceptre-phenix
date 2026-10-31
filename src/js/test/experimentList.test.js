// The Experiments table's VLAN and start time sorts, and the totals above it.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import {
  experimentTotals,
  sortByStartTime,
  sortByVlans,
} from '@/utils/experimentList.js';
import { isoTimeArb } from './helpers/arbitraries.js';

// sorts rows with a Buefy custom-sort in the direction asked
const sorted = (rows, sort, isAsc) =>
  rows.toSorted((a, b) => sort(a, b, isAsc));

const namesArb = (row) =>
  fc.uniqueArray(
    fc.record({ name: fc.stringMatching(/^[a-c]{1,3}$/), value: row }),
    { selector: (r) => r.name, maxLength: 8 },
  );

describe('the VLAN sort', () => {
  it('orders by VLAN count, ties by name from A to Z both ways', () => {
    const rows = [
      { name: 'b', vlan_count: 2 },
      { name: 'a', vlan_count: 2 },
      { name: 'c' },
      { name: 'd', vlan_count: 1 },
    ];
    const names = (isAsc) =>
      sorted(rows, sortByVlans, isAsc).map((r) => r.name);
    expect(names(true)).toEqual(['c', 'd', 'a', 'b']);
    expect(names(false)).toEqual(['a', 'b', 'd', 'c']);
  });

  it('keeps every row, counts in order and equal counts by name', () => {
    fc.assert(
      fc.property(
        namesArb(fc.option(fc.nat(4), { nil: undefined })),
        fc.boolean(),
        (entries, isAsc) => {
          const rows = entries.map(({ name, value }) => ({
            name,
            vlan_count: value,
          }));
          const out = sorted(rows, sortByVlans, isAsc);
          expect(out.toSorted((a, b) => a.name.localeCompare(b.name))).toEqual(
            rows.toSorted((a, b) => a.name.localeCompare(b.name)),
          );
          for (let i = 1; i < out.length; i++) {
            const [prev, next] = [out[i - 1], out[i]];
            const diff = (next.vlan_count ?? 0) - (prev.vlan_count ?? 0);
            expect(isAsc ? diff >= 0 : diff <= 0).toBe(true);
            if (diff === 0) {
              expect(prev.name.localeCompare(next.name)).toBeLessThan(0);
            }
          }
        },
      ),
    );
  });
});

describe('the start time sort', () => {
  it('orders by the moment, not the text, unstarted ones first', () => {
    const rows = [
      // 05:00 UTC, though its text sorts after b's
      { name: 'a', start_time: '2026-01-01T10:00:00+05:00' },
      { name: 'b', start_time: '2026-01-01T06:00:00Z' },
      { name: 'c', start_time: '' },
      { name: 'd', start_time: 'garbled' },
    ];
    expect(sorted(rows, sortByStartTime, true).map((r) => r.name)).toEqual([
      'c',
      'd',
      'a',
      'b',
    ]);
  });

  it('puts the rows in the order of their moments', () => {
    fc.assert(
      fc.property(namesArb(isoTimeArb), fc.boolean(), (entries, isAsc) => {
        const rows = entries.map(({ name, value }) => ({
          name,
          start_time: value.time,
          ms: value.ms,
        }));
        const out = sorted(rows, sortByStartTime, isAsc);
        for (let i = 1; i < out.length; i++) {
          const diff = out[i].ms - out[i - 1].ms;
          expect(isAsc ? diff >= 0 : diff <= 0).toBe(true);
        }
      }),
    );
  });
});

describe('the totals', () => {
  it('count experiments, those running, their VMs and every VLAN', () => {
    expect(
      experimentTotals([
        { name: 'a', running: true, vm_count: 3, vlan_count: 2 },
        { name: 'b', running: false, vm_count: 5, vlan_count: 4 },
        { name: 'c', running: true, vm_count: 1 },
        // known only from a websocket message
        { name: 'd', status: 'starting', partial: true },
      ]),
    ).toEqual({ experiments: 4, running: 2, runningVms: 4, vlans: 6 });
  });

  const expArb = fc.record(
    {
      name: fc.string(),
      running: fc.boolean(),
      vm_count: fc.nat(50),
      vlan_count: fc.nat(20),
    },
    { requiredKeys: ['name'] },
  );

  it('add up across any split of the list, with running within the total', () => {
    fc.assert(
      fc.property(fc.array(expArb), fc.array(expArb), (a, b) => {
        const [ta, tb, all] = [a, b, [...a, ...b]].map(experimentTotals);
        for (const key of Object.keys(all)) {
          expect(all[key]).toBe(ta[key] + tb[key]);
        }
        expect(all.running).toBeLessThanOrEqual(all.experiments);
        expect(all.experiments).toBe(a.length + b.length);
      }),
    );
  });
});
