// The SCORCH page: rows for experiments created while it is open, the run
// button that waits for the server to report the run, and the rows it keeps
// current from websocket messages.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const rbac = vi.hoisted(() => ({ allowed: () => true }));
vi.mock('@/utils/rbac.js', () => ({
  roleAllowed: (...args) => rbac.allowed(...args),
}));

const axios = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  delete: vi.fn(),
}));
vi.mock('@/utils/axios.js', () => ({ default: axios }));

const notify = vi.hoisted(() => ({ error: vi.fn(), show: vi.fn() }));
vi.mock('@/utils/errorNotif', () => ({
  useErrorNotification: notify.error,
  showError: notify.show,
}));

vi.mock('@/utils/websocket', () => ({
  addWsHandler: () => {},
  removeWsHandler: () => {},
}));
vi.mock('@/store.js', () => ({ usePhenixStore: () => ({}) }));
// its terminal needs a browser
vi.mock('@/components/MiniTerminal.vue', () => ({ default: {} }));

import { createLiveRows } from '@/utils/liveRows.js';
import Scorch from '@/views/Scorch.vue';
import { flush } from './helpers/async.js';
import { makeContext } from './helpers/context.js';

// the page, listing these rows
function page(experiments = []) {
  return makeContext(Scorch, {
    experiments,
    liveRows: createLiveRows(),
    $buefy: { toast: { open: vi.fn() } },
  });
}

// a started experiment whose SCORCH pipeline has two named runs
const row = (scorch = {}) => ({
  name: 'exp1',
  running: true,
  status: 'started',
  scorch: { running: false, run: -1, runs: ['first', 'second'], ...scorch },
  terminal: false,
});

const scorchMsg = (action, run = 0, result) => ({
  resource: { type: 'apps/scorch', name: `exp1/${run}`, action },
  result,
});
const expMsg = (action, result) => ({
  resource: { type: 'experiment', name: 'exp1', action },
  result,
});

beforeEach(() => {
  rbac.allowed = () => true;
  for (const fn of [axios.get, axios.post, axios.delete, notify.error]) {
    fn.mockReset();
  }
  notify.show.mockReset();
});

afterEach(() => vi.useRealTimers());

describe('a created experiment', () => {
  const created = { name: 'exp1', apps: ['scorch'], running: false };

  it('is added as stopped, with its run names', async () => {
    axios.get.mockResolvedValue({
      data: { app_running: false, running: -1, pipelines: [{ name: 'first' }] },
    });
    const ctx = page();

    ctx.handle(expMsg('create', created));
    await flush();

    expect(axios.get.mock.calls[0][0]).toBe(
      'experiments/exp1/scorch/pipelines',
    );
    expect(ctx.experiments).toEqual([
      {
        ...created,
        status: 'stopped',
        scorch: { running: false, run: -1, runs: ['first'], pending: false },
      },
    ]);
    expect(ctx.$buefy.toast.open).toHaveBeenCalled();
  });

  it('is left out when the SCORCH list would not show it', async () => {
    const ctx = page();
    ctx.handle(expMsg('create', { name: 'b', apps: ['soh'] }));

    rbac.allowed = (resource) => resource !== 'experiments/apps';
    ctx.handle(expMsg('create', created));
    await flush();

    expect(axios.get).not.toHaveBeenCalled();
    expect(ctx.experiments).toEqual([]);
  });
});

describe('the run button', () => {
  it('spins until the server reports the run started, then stopped', () => {
    axios.post.mockReturnValue(new Promise(() => {}));
    axios.delete.mockReturnValue(new Promise(() => {}));
    const exp = row();
    const ctx = page([exp]);
    expect(ctx.scorchControlLabel(exp)).toBe('Start first');

    ctx.scorchControl(exp);
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/exp1/scorch/pipelines/0',
    );
    expect(exp.scorch.pending).toBe(true);
    // a second press while it spins does nothing
    ctx.scorchControl(exp);
    expect(axios.post).toHaveBeenCalledTimes(1);

    ctx.handle(scorchMsg('start', 1));
    expect(exp.scorch).toMatchObject({ running: true, run: 1, pending: false });
    expect(ctx.scorchControlLabel(exp)).toBe('Stop second');

    ctx.scorchControl(exp);
    expect(axios.delete).toHaveBeenCalledWith(
      'experiments/exp1/scorch/pipelines/1',
    );
    ctx.handle(scorchMsg('success', 1));
    expect(exp.scorch).toMatchObject({ running: false, pending: false });
  });

  it('stops spinning when no report comes', () => {
    vi.useFakeTimers();
    axios.post.mockReturnValue(new Promise(() => {}));
    const exp = row();

    page([exp]).scorchControl(exp);
    vi.runOnlyPendingTimers();
    expect(exp.scorch.pending).toBe(false);
  });

  it('stops spinning and reports a request that fails', async () => {
    const err = new Error('forbidden');
    axios.post.mockRejectedValue(err);
    const exp = row();

    page([exp]).scorchControl(exp);
    await flush();
    expect(exp.scorch.pending).toBe(false);
    expect(notify.error).toHaveBeenCalledWith(err);
  });

  it('reports a failed run', () => {
    const exp = row({ running: true, run: 0 });
    page([exp]).handle(scorchMsg('error', 0, { error: 'component cc failed' }));

    expect(exp.scorch.running).toBe(false);
    expect(notify.show).toHaveBeenCalledWith('component cc failed');
  });
});

describe('experiment messages', () => {
  it('keep the SCORCH state over a start, and end the run on a stop', () => {
    const ctx = page([row({ run: 0 })]);
    ctx.terminals.exp1 = { name: 'cc' };

    ctx.handle(expMsg('start', { name: 'exp1', running: true }));
    expect(ctx.experiments[0]).toMatchObject({
      status: 'started',
      scorch: { runs: ['first', 'second'], run: 0 },
    });

    ctx.experiments[0].scorch.running = true;
    ctx.experiments[0].terminal = true;
    ctx.handle(expMsg('stop', { name: 'exp1', running: false }));
    expect(ctx.experiments[0]).toMatchObject({
      status: 'stopped',
      scorch: { running: false, runs: ['first', 'second'] },
      terminal: false,
    });
    expect(ctx.terminals).toEqual({});
  });

  it('drop the row of a deleted experiment', () => {
    const ctx = page([row()]);
    ctx.handle(expMsg('delete'));
    expect(ctx.experiments).toEqual([]);
  });
});
