// The options of an experiment stop and restart (utils/stopOptions.js): the
// request body of each dialog.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import {
  RESTART_DEFAULTS,
  STOP_DEFAULTS,
  restartRequestBody,
  stopRequestBody,
} from '@/utils/stopOptions.js';

const vlanField = fc.oneof(fc.constant(''), fc.integer({ min: 1, max: 4094 }));
const stopArb = fc.record({
  keepInjectionSnapshots: fc.boolean(),
  force: fc.boolean(),
});
const restartArb = fc.record({
  dryRun: fc.boolean(),
  treatMinimegaErrorsAsWarnings: fc.boolean(),
  honorRunPeriodically: fc.boolean(),
  vlanMin: vlanField,
  vlanMax: vlanField,
  keepInjectionSnapshots: fc.boolean(),
  force: fc.boolean(),
});

describe('stopRequestBody', () => {
  it('is empty for the form as it opens', () => {
    expect(stopRequestBody(STOP_DEFAULTS)).toEqual({});
  });

  it('holds each option that is set, as true, and no other', () => {
    fc.assert(
      fc.property(stopArb, (form) => {
        const body = stopRequestBody(form);
        for (const key of Object.keys(STOP_DEFAULTS)) {
          expect(body[key]).toBe(form[key] ? true : undefined);
        }
        expect(Object.keys(body).every((key) => key in STOP_DEFAULTS)).toBe(
          true,
        );
      }),
      { numRuns: 100 },
    );
  });
});

describe('restartRequestBody', () => {
  it('is empty for the form as it opens', () => {
    expect(restartRequestBody(RESTART_DEFAULTS)).toEqual({});
  });

  it('holds what the form asks for, never a dry run: the server reads the defaults for the rest', () => {
    fc.assert(
      fc.property(restartArb, (form) => {
        const body = restartRequestBody(form);
        const server = {
          treatMinimegaErrorsAsWarnings: false,
          honorRunPeriodically: true,
          keepInjectionSnapshots: false,
          force: false,
          ...body,
        };
        expect('dryRun' in body).toBe(false);
        expect(server.treatMinimegaErrorsAsWarnings).toBe(
          form.treatMinimegaErrorsAsWarnings,
        );
        expect(server.honorRunPeriodically).toBe(form.honorRunPeriodically);
        expect(server.keepInjectionSnapshots).toBe(form.keepInjectionSnapshots);
        expect(server.force).toBe(form.force);
        expect(server.vlanMin).toBe(
          form.vlanMin === '' ? undefined : form.vlanMin,
        );
        expect(server.vlanMax).toBe(
          form.vlanMax === '' ? undefined : form.vlanMax,
        );
      }),
      { numRuns: 200 },
    );
  });
});
