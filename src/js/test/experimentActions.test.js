// The Experiments table's actions: when an experiment's state or the role
// rules one out, for one experiment and for a selection.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import {
  BULK_ACTIONS,
  bulkActionState,
  experimentActionState,
  experimentBlocked,
  partitionExperiments,
} from '@/utils/experimentActions.js';
import { allowAll, denyAll } from './helpers/permissions.js';

const exp = (name, status) => ({
  name,
  status,
  running: status === 'started' || status === 'stopping',
});

describe('one experiment', () => {
  it.each([
    ['start', 'stopped', null],
    ['start', 'started', 'the experiment is already running'],
    ['start', 'starting', 'the experiment is starting'],
    ['stop', 'started', null],
    ['stop', 'stopped', 'the experiment is not running'],
    ['stop', 'stopping', 'the experiment is stopping'],
    ['delete', 'stopped', null],
    ['delete', 'started', 'the experiment is running'],
    ['delete', 'starting', 'the experiment is starting'],
  ])('%s a %s experiment: %s', (action, status, reason) => {
    expect(experimentBlocked(action, exp('x', status))).toBe(reason);
  });

  it.each([
    ['deleting', 'the experiment is being deleted'],
    ['starting', 'the experiment is being started'],
  ])(
    'blocks every action on an experiment whose %s request is under way',
    (pending, reason) => {
      for (const action of BULK_ACTIONS) {
        const row = exp('x', 'stopped');
        expect(experimentBlocked(action, row, { [pending]: true })).toBe(
          reason,
        );
        expect(
          experimentActionState(action, row, {
            can: allowAll,
            [pending]: true,
          }),
        ).toMatchObject({
          disabled: true,
          reason: `Cannot ${action}: ${reason}`,
        });
      }
    },
  );

  it.each([
    [allowAll, { permitted: true, disabled: false, reason: null }],
    [
      denyAll,
      {
        permitted: false,
        disabled: true,
        reason: 'Cannot start: you do not have permission',
      },
    ],
  ])('offers Start by the role', (can, want) => {
    expect(
      experimentActionState('start', exp('x', 'stopped'), { can }),
    ).toMatchObject(want);
  });
});

describe('a selection', () => {
  const status = fc.constantFrom('stopped', 'started', 'starting', 'stopping');
  const table = fc
    .uniqueArray(fc.stringMatching(/^[a-e]$/), { minLength: 1 })
    .chain((names) =>
      fc.tuple(
        fc.tuple(...names.map((n) => status.map((s) => exp(n, s)))),
        fc.subarray([...names, 'gone']),
        fc.subarray(names),
        fc.constantFrom(...BULK_ACTIONS),
        fc.subarray(names),
      ),
    );

  it('acts on each selected experiment it may, and says why it skips the rest', () => {
    fc.assert(
      fc.property(table, ([experiments, selected, permitted, action, sent]) => {
        const can = (...[, , name]) => permitted.includes(name);
        // the experiments whose start request is under way
        const starting = Object.fromEntries(sent.map((name) => [name, true]));
        const opts = { can, starting };
        const { allowed, skipped } = partitionExperiments(
          action,
          selected,
          experiments,
          opts,
        );
        const skippedNames = skipped.flatMap((group) => group.names);
        expect([...allowed, ...skippedNames].toSorted()).toEqual(
          [...selected].toSorted(),
        );
        for (const name of allowed) {
          const row = experiments.find((e) => e.name === name);
          expect(permitted).toContain(name);
          expect(sent).not.toContain(name);
          expect(experimentBlocked(action, row)).toBeNull();
        }
        expect(
          bulkActionState(action, selected, experiments, opts).disabled,
        ).toBe(allowed.length === 0);
      }),
    );
  });
});
