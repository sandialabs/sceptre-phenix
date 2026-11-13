// The progress of an experiment start or stop (utils/experimentProgress.js):
// the label under the bar, the failure text, and how messages change both.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import {
  PHASE_NAMES,
  afterMessage,
  failureLabel,
  listedProgress,
  mergeProgress,
  progressActive,
  progressLabel,
  progressPercent,
  progressPhaseKey,
  sendingProgress,
} from '@/utils/experimentProgress.js';

const PHASES = [...Object.keys(PHASE_NAMES), 'done', 'request'];

// a progress body as a server sends it, or with missing and odd fields
const progressArb = fc.record(
  {
    operation: fc.constantFrom('start', 'stop', '', 'other'),
    percent: fc.oneof(fc.double({ noNaN: false }), fc.constant(undefined)),
    phase: fc.oneof(fc.constantFrom(...PHASES), fc.string({ maxLength: 8 })),
    done: fc.oneof(fc.integer(), fc.double(), fc.string({ maxLength: 8 })),
    total: fc.oneof(fc.integer(), fc.double(), fc.string({ maxLength: 8 })),
    step: fc.oneof(fc.string({ maxLength: 8 }), fc.constant(undefined)),
  },
  { requiredKeys: [] },
);

describe('progressLabel', () => {
  it.each([
    [
      'a phase, its count and step',
      { phase: 'pre-start-apps', done: 3, total: 7, step: 'startup' },
      'Pre-start apps 3/7: startup',
    ],
    [
      'a phase and count with no step',
      { phase: 'launch-vms', done: 12, total: 40, step: '' },
      'Launching VMs 12/40',
    ],
    ['a phase alone', { phase: 'stop-vms', done: 0, total: 0 }, 'Stopping VMs'],
    [
      'a count not yet started',
      { phase: 'post-start-apps', done: 0, total: 2, step: 'scorch' },
      'Post-start apps 0/2: scorch',
    ],
    ['each stop phase', { phase: 'cleanup-apps' }, 'Cleanup apps'],
    ['the stop cleanup', { phase: 'cleanup' }, 'Cleaning up'],
    ['preparing', { phase: 'prepare', total: 1, done: 1 }, 'Preparing 1/1'],
    ['starting VMs', { phase: 'start-vms' }, 'Starting VMs'],
    ['an unknown phase, by its id', { phase: 'warm-up' }, 'warm-up'],
    ['a step with no phase', { step: 'startup' }, 'startup'],
    ['an old server', { percent: 0.4 }, ''],
    [
      "this page's own start request",
      sendingProgress('start'),
      'Sending start request',
    ],
    [
      "this page's own stop request",
      sendingProgress('stop'),
      'Sending stop request',
    ],
    ['nothing', null, ''],
    ['a name from the prototype', { phase: 'toString' }, 'toString'],
  ])('shows %s', (_, progress, want) => {
    expect(progressLabel(progress)).toBe(want);
  });

  it('never throws, and shows no missing value', () => {
    fc.assert(
      fc.property(fc.oneof(progressArb, fc.anything()), (progress) => {
        const label = progressLabel(progress);
        expect(typeof label).toBe('string');
        expect(label).toBe(label.trim());
        expect(label).not.toMatch(/undefined|null|NaN|\[object/);
      }),
      { numRuns: 300 },
    );
  });

  // the phases with display names, and steps with text
  const phaseArb = fc.constantFrom(...Object.keys(PHASE_NAMES));
  const stepArb = fc
    .string({ minLength: 1, maxLength: 8 })
    .filter((step) => step.trim() !== '');

  it('joins the phase, the count and the step', () => {
    fc.assert(
      fc.property(
        phaseArb,
        fc.nat(50),
        fc.integer({ min: 1, max: 50 }),
        stepArb,
        (phase, done, total, step) => {
          expect(progressLabel({ phase, done, total, step })).toBe(
            `${PHASE_NAMES[phase]} ${done}/${total}: ${step.trim()}`,
          );
        },
      ),
      { numRuns: 100 },
    );
  });

  it('leaves out an empty count and step', () => {
    fc.assert(
      fc.property(
        phaseArb,
        fc.constantFrom(0, -3, Number.NaN, undefined, '7'),
        fc.constantFrom('', '  ', undefined, 4),
        (phase, total, step) => {
          expect(progressLabel({ phase, done: 2, total, step })).toBe(
            PHASE_NAMES[phase],
          );
        },
      ),
      { numRuns: 100 },
    );
  });
});

describe('failureLabel', () => {
  it.each([
    [
      'the phase, step and error',
      { phase: 'pre-start-apps', step: 'startup', error: 'exit 1' },
      'Failed at Pre-start apps: startup: exit 1',
    ],
    [
      'no step',
      { phase: 'launch-vms', step: '', error: 'no image' },
      'Failed at Launching VMs: no image',
    ],
    ['no phase', { error: 'boom' }, 'Failed: boom'],
    ['nothing', null, 'Failed'],
  ])('tells %s', (_, failure, want) => {
    expect(failureLabel(failure)).toBe(want);
  });

  it('never throws, and always says it failed', () => {
    fc.assert(
      fc.property(fc.oneof(progressArb, fc.anything()), (failure) => {
        const label = failureLabel(failure);
        expect(label).toMatch(/^Failed/);
        expect(label).not.toMatch(/undefined|null|\[object/);
      }),
      { numRuns: 300 },
    );
  });

  it('leaves out an empty step and error', () => {
    fc.assert(
      fc.property(
        fc.constantFrom(...Object.keys(PHASE_NAMES)),
        fc.constantFrom('', ' ', undefined, 3),
        (phase, empty) => {
          expect(failureLabel({ phase, step: empty, error: empty })).toBe(
            `Failed at ${PHASE_NAMES[phase]}`,
          );
        },
      ),
      { numRuns: 50 },
    );
  });
});

describe('progressPercent', () => {
  it.each([
    [{ percent: 0.456 }, 46],
    [{ percent: 1.5 }, 100],
    [{ percent: -1 }, 0],
    [{ percent: Number.NaN }, 0],
    [{}, 0],
    [null, 0],
  ])('reads %j as %d', (progress, want) => {
    expect(progressPercent(progress)).toBe(want);
  });
});

describe('listedProgress', () => {
  it.each([
    [
      'the progress of a new server',
      { status: 'started', progress: { operation: 'start', phase: 'cleanup' } },
      { operation: 'start', phase: 'cleanup' },
    ],
    [
      "an old server's starting percent",
      { status: 'starting', percent: 0.3 },
      { percent: 0.3 },
    ],
    ['a stopping one', { status: 'stopping' }, { percent: 0 }],
    ['an idle one', { status: 'started', percent: 0 }, undefined],
  ])('reads %s', (_, item, want) => {
    expect(listedProgress(item)).toEqual(want);
  });
});

describe('mergeProgress', () => {
  const start = (percent, phase = 'launch-vms') => ({
    operation: 'start',
    percent,
    phase,
  });

  it('keeps the percent within one operation, and takes the rest', () => {
    expect(mergeProgress(start(0.5), start(0.3, 'start-vms'))).toEqual(
      start(0.5, 'start-vms'),
    );
  });

  it('starts a new operation afresh', () => {
    const stop = { operation: 'stop', percent: 0.1, phase: 'stop-vms' };
    expect(mergeProgress(start(0.9), stop)).toEqual(stop);
  });

  it("keeps the operation for an old server's percent", () => {
    expect(mergeProgress(sendingProgress('start'), { percent: 0.2 })).toEqual({
      operation: 'start',
      percent: 0.2,
    });
  });

  it('ends the bar when done or idle', () => {
    expect(mergeProgress(start(0.9), start(1, 'done'))).toBeUndefined();
    expect(mergeProgress(start(0.9), undefined)).toBeUndefined();
  });

  it('never goes back within an operation', () => {
    fc.assert(
      fc.property(
        fc.constantFrom('start', 'stop'),
        fc.array(fc.double({ min: 0, max: 1, noNaN: true }), { minLength: 1 }),
        (operation, percents) => {
          let shown = 0;
          let progress;
          for (const percent of percents) {
            progress = mergeProgress(progress, {
              operation,
              percent,
              phase: 'stop-vms',
            });
            const now = progressPercent(progress);
            expect(now).toBeGreaterThanOrEqual(shown);
            shown = now;
          }
        },
      ),
      { numRuns: 100 },
    );
  });
});

describe('afterMessage', () => {
  const running = {
    operation: 'start',
    percent: 0.95,
    phase: 'post-start-apps',
  };
  const failure = { operation: 'start', phase: 'launch-vms', error: 'no' };

  it.each([
    [
      'starting keeps the bar of this page request and clears a failure',
      { progress: sendingProgress('start'), failure },
      ['starting'],
      { progress: sendingProgress('start'), failure: null },
    ],
    [
      'stopping starts a stop bar at 0',
      { progress: running, failure: null },
      ['stopping'],
      { progress: { operation: 'stop', percent: 0 }, failure: null },
    ],
    [
      'start keeps the bar of the post-start apps',
      { progress: running, failure: null },
      ['start', { name: 'x' }],
      { progress: running, failure: null },
    ],
    [
      "start ends an old server's bar",
      { progress: { percent: 1 }, failure: null },
      ['start', { name: 'x' }],
      { progress: undefined, failure: null },
    ],
    [
      "stop ends this page's own guess",
      { progress: sendingProgress('stop'), failure: null },
      ['stop', { name: 'x' }],
      { progress: undefined, failure: null },
    ],
    [
      'stop ends the bar of a stop, which sends no done',
      {
        progress: { operation: 'stop', percent: 0.9, phase: 'cleanup' },
        failure: null,
      },
      ['stop', { name: 'x' }],
      { progress: undefined, failure: null },
    ],
    [
      'done ends the bar',
      { progress: running, failure: null },
      ['progress', { operation: 'start', percent: 1, phase: 'done' }],
      { progress: undefined, failure: null },
    ],
    [
      'a failure with a body shows it',
      { progress: running, failure: null },
      ['errorStarting', { phase: 'post-start-apps', step: 'a', error: 'b' }],
      {
        progress: undefined,
        failure: {
          operation: 'start',
          phase: 'post-start-apps',
          step: 'a',
          error: 'b',
        },
      },
    ],
    [
      'a failure without a body (old server) shows none',
      { progress: running, failure: null },
      ['errorStopping', null],
      { progress: undefined, failure: null },
    ],
    [
      'another action changes nothing',
      { progress: running, failure },
      ['schedule', {}],
      { progress: running, failure },
    ],
  ])('%s', (_, state, [action, body], want) => {
    expect(afterMessage(state, action, body)).toEqual(want);
  });

  it('announces a phase change, not a count change', () => {
    const at = (phase, done) =>
      progressPhaseKey({ operation: 'start', phase, done, total: 9 });
    expect(at('launch-vms', 1)).toBe(at('launch-vms', 2));
    expect(at('launch-vms', 1)).not.toBe(at('start-vms', 1));
    expect(progressActive({ phase: 'done' })).toBe(false);
  });
});
