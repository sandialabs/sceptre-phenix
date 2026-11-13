// The options of an experiment start (utils/startOptions.js): the checks of
// the start dialog's form, the request body it sends, and the answer.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import {
  START_DEFAULTS,
  startOptionErrors,
  startRequestBody,
  startResult,
} from '@/utils/startOptions.js';

const form = (fields = {}) => ({ ...START_DEFAULTS, ...fields });
const noErrors = { vlanMin: '', vlanMax: '' };

// a VLAN field as the dialog holds it: empty, or a number from the input
const vlanField = fc.oneof(fc.constant(''), fc.integer({ min: 1, max: 4094 }));
const formArb = fc.record({
  dryRun: fc.boolean(),
  treatMinimegaErrorsAsWarnings: fc.boolean(),
  honorRunPeriodically: fc.boolean(),
  vlanMin: vlanField,
  vlanMax: vlanField,
});
const BODY_KEYS = [
  'dryRun',
  'treatMinimegaErrorsAsWarnings',
  'honorRunPeriodically',
  'vlanMin',
  'vlanMax',
];

describe('startOptionErrors', () => {
  it.each([
    ['empty fields', {}, noErrors],
    ['a minimum alone', { vlanMin: 100 }, noErrors],
    ['a maximum alone', { vlanMax: 100 }, noErrors],
    ['a range', { vlanMin: 1, vlanMax: 4094 }, noErrors],
    [
      'zero',
      { vlanMin: 0 },
      { vlanMin: 'Type an ID from 1 to 4094.', vlanMax: '' },
    ],
    [
      'an ID past the last',
      { vlanMax: 4095 },
      { vlanMin: '', vlanMax: 'Type an ID from 1 to 4094.' },
    ],
    [
      'a fraction',
      { vlanMin: 1.5 },
      { vlanMin: 'Type an ID from 1 to 4094.', vlanMax: '' },
    ],
    [
      'a minimum more than the maximum',
      { vlanMin: 200, vlanMax: 100 },
      {
        vlanMin: 'The minimum must not be more than the maximum.',
        vlanMax: '',
      },
    ],
  ])('%s', (_, fields, want) => {
    expect(startOptionErrors(form(fields))).toEqual(want);
  });

  it('refuses a range only when its minimum is more than its maximum', () => {
    fc.assert(
      fc.property(
        fc.integer({ min: 1, max: 4094 }),
        fc.integer({ min: 1, max: 4094 }),
        (min, max) => {
          const errors = startOptionErrors(
            form({ vlanMin: min, vlanMax: max }),
          );
          expect(errors.vlanMax).toBe('');
          expect(errors.vlanMin !== '').toBe(min > max);
        },
      ),
      { numRuns: 200 },
    );
  });

  it('refuses every integer outside 1 to 4094', () => {
    fc.assert(
      fc.property(
        fc.oneof(
          fc.integer({ max: 0 }),
          fc.integer({ min: 4095, max: 1_000_000 }),
        ),
        fc.constantFrom('vlanMin', 'vlanMax'),
        (id, key) => {
          expect(startOptionErrors(form({ [key]: id }))[key]).not.toBe('');
        },
      ),
      { numRuns: 200 },
    );
  });
});

describe('startRequestBody', () => {
  it.each([
    ['the defaults', {}, {}],
    ['spaces in the VLAN fields', { vlanMin: ' ', vlanMax: '' }, {}],
    ['a dry run', { dryRun: true }, { dryRun: true }],
    [
      'a dry run, the schedule unchecked',
      { dryRun: true, honorRunPeriodically: false },
      { dryRun: true },
    ],
    [
      'minimega errors as warnings',
      { treatMinimegaErrorsAsWarnings: true },
      { treatMinimegaErrorsAsWarnings: true },
    ],
    [
      'no scheduled runs',
      { honorRunPeriodically: false },
      { honorRunPeriodically: false },
    ],
    [
      'a VLAN range',
      { vlanMin: 100, vlanMax: '200' },
      { vlanMin: 100, vlanMax: 200 },
    ],
    [
      'every option',
      {
        dryRun: true,
        treatMinimegaErrorsAsWarnings: true,
        vlanMin: 1,
        vlanMax: 4094,
      },
      {
        dryRun: true,
        treatMinimegaErrorsAsWarnings: true,
        vlanMin: 1,
        vlanMax: 4094,
      },
    ],
  ])('%s', (_, fields, want) => {
    expect(startRequestBody(form(fields))).toEqual(want);
  });

  it('sends only options that differ from the defaults, and never honorRunPeriodically: true', () => {
    fc.assert(
      fc.property(formArb, (f) => {
        const body = startRequestBody(f);
        for (const [key, value] of Object.entries(body)) {
          expect(BODY_KEYS).toContain(key);
          expect(value).not.toEqual(START_DEFAULTS[key]);
        }
        expect(body.honorRunPeriodically).not.toBe(true);
        expect(body.dryRun && 'honorRunPeriodically' in body).toBeFalsy();
      }),
      { numRuns: 200 },
    );
  });

  it('holds what the form asks for: the server reads the defaults for the rest', () => {
    fc.assert(
      fc.property(formArb, (f) => {
        const server = {
          dryRun: false,
          treatMinimegaErrorsAsWarnings: false,
          honorRunPeriodically: true,
          ...startRequestBody(f),
        };
        expect(server.dryRun).toBe(f.dryRun);
        expect(server.treatMinimegaErrorsAsWarnings).toBe(
          f.treatMinimegaErrorsAsWarnings,
        );
        // a dry run starts nothing, so it has no scheduled runs to skip
        if (!f.dryRun) {
          expect(server.honorRunPeriodically).toBe(f.honorRunPeriodically);
        }
        expect(server.vlanMin).toBe(f.vlanMin === '' ? undefined : f.vlanMin);
        expect(server.vlanMax).toBe(f.vlanMax === '' ? undefined : f.vlanMax);
      }),
      { numRuns: 200 },
    );
  });
});

describe('startResult', () => {
  const exp = { name: 'exp', running: false, status: 'stopped' };

  it.each([
    [
      'an Experiment',
      exp,
      { experiment: exp, dryRun: false, warnings: [], mmScript: '' },
    ],
    [
      'no body',
      undefined,
      { experiment: null, dryRun: false, warnings: [], mmScript: '' },
    ],
    [
      'a dry run',
      {
        experiment: exp,
        dryRun: true,
        warnings: ['no host for vm1'],
        mmScript: '/phenix/experiments/exp/mm_files/exp.mm',
      },
      {
        experiment: exp,
        dryRun: true,
        warnings: ['no host for vm1'],
        mmScript: '/phenix/experiments/exp/mm_files/exp.mm',
      },
    ],
    [
      'a start with warnings',
      { experiment: exp, warnings: [] },
      { experiment: exp, dryRun: false, warnings: [], mmScript: '' },
    ],
  ])('reads %s', (_, data, want) => {
    expect(startResult(data)).toEqual(want);
  });

  it('reads any Experiment as itself, and any StartResult as its parts', () => {
    const expArb = fc.record({
      name: fc.string({ minLength: 1 }),
      running: fc.boolean(),
    });
    fc.assert(
      fc.property(
        expArb,
        fc.boolean(),
        fc.array(fc.string()),
        fc.string(),
        (experiment, dryRun, warnings, mmScript) => {
          expect(startResult(experiment).experiment).toBe(experiment);
          expect(
            startResult({ experiment, dryRun, warnings, mmScript }),
          ).toEqual({ experiment, dryRun, warnings, mmScript });
        },
      ),
      { numRuns: 200 },
    );
  });
});
