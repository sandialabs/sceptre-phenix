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
