// The running experiment's count of VMs that wait for a delayed start: the
// VMs a VM list names, and how the VM events since change it.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import { createDelayedVms } from '@/components/experiment/delayedVms.js';

const vm = (name, state, delayed_start = '') => ({
  name,
  state,
  delayed_start,
});

describe('the delayed count', () => {
  it('is unknown until a list names the waiting VMs', () => {
    const delayed = createDelayedVms();
    expect(delayed.changed('a', 'start', vm('a', 'RUNNING'))).toBeNull();
    expect(delayed.listed(undefined, [])).toBeNull();
    expect(delayed.listed(['a', 'b'])).toBe(2);
    expect(delayed.listed(undefined, [])).toBeNull();
  });

  it('leaves out the listed VMs whose rows started since the list', () => {
    const delayed = createDelayedVms();
    const rows = [
      vm('a', 'RUNNING'),
      vm('b', 'BUILDING', 'user'),
      // minimega did not answer for it
      vm('c', ''),
    ];
    expect(delayed.listed(['a', 'b', 'c', 'd'], rows)).toBe(3);
  });

  it.each([
    ['a timer VM started', 'a', 'start', vm('a', 'RUNNING'), 1],
    ['a user VM started by hand', 'b', 'start', vm('b', 'RUNNING', 'user'), 1],
    ['a waiting VM killed', 'a', 'delete', null, 1],
    ['a waiting VM updated', 'a', 'update', vm('a', 'BUILDING'), 2],
    ['a running VM stopped', 'x', 'stop', vm('x', 'PAUSED'), 2],
    [
      'a VM redeployed to wait',
      'x',
      'redeployed',
      vm('x', 'BUILDING', 'user'),
      3,
    ],
    [
      'a VM redeployed without a delay',
      'x',
      'redeployed',
      vm('x', 'BUILDING'),
      2,
    ],
    ['an event without a VM', 'a', 'cdrom-inserted', undefined, 2],
  ])('counts %s', (_, name, action, result, want) => {
    const delayed = createDelayedVms();
    delayed.listed(['a', 'b'], []);
    expect(delayed.changed(name, action, result)).toBe(want);
  });

  // Once a VM leaves minimega's BUILDING state, it no longer waits.
  it('counts the VMs that wait through any run of events', () => {
    const names = ['a', 'b', 'c', 'd', 'e'];
    const event = fc.record({
      name: fc.constantFrom(...names),
      action: fc.constantFrom('start', 'stop', 'delete', 'update'),
      state: fc.constantFrom('BUILDING', 'RUNNING', 'PAUSED', 'QUIT'),
    });
    fc.assert(
      fc.property(
        fc.subarray(names),
        fc.array(event, { maxLength: 20 }),
        (start, events) => {
          const model = new Set(start);
          const delayed = createDelayedVms();
          expect(delayed.listed(start, [])).toBe(model.size);

          for (const { name, action, state } of events) {
            if (action === 'delete' || state !== 'BUILDING') {
              model.delete(name);
            }
            const result = action === 'delete' ? null : vm(name, state);
            expect(delayed.changed(name, action, result)).toBe(model.size);
          }
        },
      ),
      { numRuns: 100 },
    );
  });
});

describe('the due times of the timer VMs', () => {
  const due = {
    a: '2026-01-01T10:05:00Z',
    b: '2026-01-01T10:01:00Z',
    // not named as waiting, so it started already
    z: '2026-01-01T10:02:00Z',
  };

  it('keeps those of the listed VMs that still wait', () => {
    const delayed = createDelayedVms();
    expect(delayed.dueTimes()).toEqual({});
    delayed.listed(['a', 'b', 'c'], [vm('b', 'RUNNING')], due);
    expect(delayed.dueTimes()).toEqual({ a: '2026-01-01T10:05:00Z' });
    delayed.listed(undefined, []);
    expect(delayed.dueTimes()).toEqual({});
  });

  it.each([
    ['a VM that starts', 'start', vm('a', 'RUNNING'), { b: due.b }],
    ['a VM that is killed', 'delete', null, { b: due.b }],
    [
      'a VM redeployed to wait',
      'redeployed',
      vm('a', 'BUILDING', 'user'),
      {
        b: due.b,
      },
    ],
    [
      'a VM that is updated',
      'update',
      vm('a', 'BUILDING', 'timer:5m'),
      {
        a: due.a,
        b: due.b,
      },
    ],
  ])(
    'drops the due time of %s only when it no longer waits for it',
    (_, action, result, want) => {
      const delayed = createDelayedVms();
      delayed.listed(['a', 'b'], [], due);
      delayed.changed('a', action, result);
      expect(delayed.dueTimes()).toEqual(want);
    },
  );

  it('gives a copy that a change does not alter', () => {
    const delayed = createDelayedVms();
    delayed.listed(['a'], [], due);
    const copy = delayed.dueTimes();
    delayed.changed('a', 'start', vm('a', 'RUNNING'));
    expect(copy).toEqual({ a: due.a });
  });
});
