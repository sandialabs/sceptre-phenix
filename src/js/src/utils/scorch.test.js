import { describe, expect, it } from 'vitest';
import { executionActive, mergeScorchRuns, terminalKey } from './scorch.js';

describe('Scorch execution snapshots', () => {
  it('keeps a setup breakpoint active while another run finishes', () => {
    const snapshot = {
      pipelines: [
        { name: 'setup', pipeline: [] },
        { name: 'work', pipeline: [] },
      ],
      executions: {
        0: { executionID: 'setup', state: 'waiting', wait: 'breakpoint' },
        1: { executionID: 'work', state: 'succeeded' },
      },
    };
    const runs = mergeScorchRuns(snapshot);
    expect(runs.map((run) => run.running)).toEqual([true, false]);
    expect(executionActive({ state: 'canceling' })).toBe(true);
    expect(executionActive({ state: 'finalizing' })).toBe(true);
  });
  it('retains loop history for one execution and resets it for a rerun', () => {
    const snapshot = {
      pipelines: [{ pipeline: ['new'] }],
      executions: { 0: { executionID: 'new', state: 'starting' } },
    };
    const old = [
      { execution: { executionID: 'old' }, loop: 2, nodes: ['old'] },
    ];
    expect(mergeScorchRuns(snapshot, old)[0]).toMatchObject({
      loop: 0,
      nodes: ['new'],
    });
    old[0].execution.executionID = 'new';
    expect(mergeScorchRuns(snapshot, old)[0]).toMatchObject({
      loop: 2,
      nodes: ['old'],
    });
  });
  it('identifies terminals by run and invocation', () => {
    const terminal = {
      exp: 'example',
      run: 0,
      executionID: 'a',
      loop: 0,
      stage: 'start',
      name: 'break',
    };
    expect(terminalKey(terminal)).not.toBe(
      terminalKey({ ...terminal, run: 1 }),
    );
    expect(terminalKey(terminal)).not.toBe(
      terminalKey({ ...terminal, executionID: 'b' }),
    );
  });
});
