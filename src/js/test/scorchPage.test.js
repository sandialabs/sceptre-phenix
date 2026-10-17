// The SCORCH page: rows for experiments created while open, the run button
// that waits for the server's report, and rows kept current by the websocket.
import { flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, notify, rbac, resetMocks, ws } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/pageLoader.js', modules.pageLoader);
// its terminal needs a browser
vi.mock('@/components/MiniTerminal.vue', modules.stub);

import { nextTick } from 'vue';

import Scorch from '@/views/Scorch.vue';
import { never } from './helpers/async.js';
import { wsMsg } from './helpers/fixtures.js';
import {
  byLabel,
  confirmDialog,
  dialogs,
  mountPage,
  tableRows,
  textOf,
  toasts,
} from './helpers/mount.js';
import { denyOnly } from './helpers/permissions.js';

// the page, listing these rows and holding these terminals
const page = (experiments = [], terminals = {}) =>
  mountPage(Scorch, { route: '/scorch', data: { experiments, terminals } });

// a started experiment whose SCORCH pipeline has two named runs
const row = (scorch = {}, extra = {}) => ({
  name: 'exp1',
  running: true,
  status: 'started',
  scorch: { running: false, run: -1, runs: ['first', 'second'], ...scorch },
  terminal: false,
  ...extra,
});

const scorchMsg = (action, run = 0, result) =>
  wsMsg('apps/scorch', `exp1/${run}`, action, result);
const expMsg = (action, result) => wsMsg('experiment', 'exp1', action, result);

// passes a websocket message to the page, and waits for it to draw
async function handle(msg) {
  for (const handler of ws.handlers) handler(msg);
  await flushPromises();
}

// what each row shows: its name, both statuses and the SCORCH button
const shown = (wrapper) =>
  tableRows(wrapper).map((cells) => {
    const button = cells['Scorch Status'].find('button');
    return {
      name: textOf(cells.Experiment.find('a')),
      status: textOf(cells['Experiment Status'].find('.tag')),
      scorch: textOf(cells['Scorch Status'].find('.tag')),
      button: button.exists() ? button.attributes('aria-label') : null,
      spinning: button.exists() && button.classes('is-loading'),
      terminal: cells.Terminal.find('button').exists(),
    };
  });

beforeEach(() => resetMocks({ store: {} }));

afterEach(() => vi.useRealTimers());

describe('a created experiment', () => {
  const created = { name: 'exp1', apps: ['scorch'], running: false };

  it('is added as stopped, with its run names', async () => {
    axios.get.mockResolvedValue({
      data: { app_running: false, running: -1, pipelines: [{ name: 'first' }] },
    });
    const wrapper = await page();

    await handle(expMsg('create', created));

    expect(axios.get.mock.calls[0][0]).toBe(
      'experiments/exp1/scorch/pipelines',
    );
    expect(shown(wrapper)).toEqual([
      {
        name: 'exp1',
        status: 'stopped',
        scorch: 'stopped',
        button: 'Start first',
        spinning: false,
        terminal: false,
      },
    ]);
    expect(byLabel(wrapper, 'Start experiment exp1').exists()).toBe(true);
    expect(toasts().map((t) => t.text)).toEqual([
      'The exp1 experiment has been created.',
    ]);
  });

  it('is left out when the SCORCH list would not show it', async () => {
    const wrapper = await page();
    await handle(expMsg('create', { name: 'b', apps: ['soh'] }));

    rbac.allowed = denyOnly(['experiments/apps']);
    await handle(expMsg('create', created));

    expect(axios.get).not.toHaveBeenCalled();
    expect(tableRows(wrapper)).toEqual([]);
  });
});

describe('the run button', () => {
  // the SCORCH button of the only row, found again after each change
  const scorchButton = (wrapper) =>
    tableRows(wrapper)[0]['Scorch Status'].find('button');

  it('spins until the server reports the run started, then stopped', async () => {
    axios.post.mockReturnValue(never());
    axios.delete.mockReturnValue(never());
    const wrapper = await page([row()]);
    expect(scorchButton(wrapper).attributes('aria-label')).toBe('Start first');

    await scorchButton(wrapper).trigger('click');
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/exp1/scorch/pipelines/0',
    );
    expect(shown(wrapper)[0].spinning).toBe(true);
    // a second press while it spins does nothing, even past the disabled button
    const spinning = scorchButton(wrapper);
    expect(spinning.attributes('disabled')).toBeDefined();
    spinning.element.disabled = false;
    await spinning.trigger('click');
    expect(axios.post).toHaveBeenCalledTimes(1);

    await handle(scorchMsg('start', 1));
    expect(shown(wrapper)[0]).toMatchObject({
      scorch: 'running',
      button: 'Stop second',
      spinning: false,
    });

    await scorchButton(wrapper).trigger('click');
    expect(axios.delete).toHaveBeenCalledWith(
      'experiments/exp1/scorch/pipelines/1',
    );
    await handle(scorchMsg('success', 1));
    expect(shown(wrapper)[0]).toMatchObject({
      scorch: 'stopped',
      spinning: false,
    });
  });

  it('stops spinning when no report comes', async () => {
    axios.post.mockReturnValue(never());
    const wrapper = await page([row()]);
    vi.useFakeTimers();

    await scorchButton(wrapper).trigger('click');
    expect(shown(wrapper)[0].spinning).toBe(true);
    vi.runOnlyPendingTimers();
    await nextTick();
    expect(shown(wrapper)[0].spinning).toBe(false);
  });

  it('stops spinning and reports a request that fails', async () => {
    const err = new Error('forbidden');
    axios.post.mockRejectedValue(err);
    const wrapper = await page([row()]);

    await scorchButton(wrapper).trigger('click');
    await flushPromises();
    expect(shown(wrapper)[0].spinning).toBe(false);
    expect(notify.error).toHaveBeenCalledWith(err);
  });

  it('reports a failed run', async () => {
    const wrapper = await page([row({ running: true, run: 0 })]);
    await handle(scorchMsg('error', 0, { error: 'component cc failed' }));

    expect(shown(wrapper)[0].scorch).toBe('stopped');
    expect(notify.show).toHaveBeenCalledWith('component cc failed');
  });
});

describe('experiment messages', () => {
  it('keep the SCORCH state over a start, and end the run on a stop', async () => {
    const wrapper = await page(
      [row({ running: true, run: 0 }, { terminal: true })],
      { exp1: { name: 'cc' } },
    );

    await handle(expMsg('start', { name: 'exp1', running: true }));
    expect(shown(wrapper)).toEqual([
      {
        name: 'exp1',
        status: 'started',
        scorch: 'running',
        button: 'Stop first',
        spinning: false,
        terminal: true,
      },
    ]);

    await handle(expMsg('stop', { name: 'exp1', running: false }));
    expect(shown(wrapper)).toEqual([
      {
        name: 'exp1',
        status: 'stopped',
        scorch: 'stopped',
        button: 'Start first',
        spinning: false,
        terminal: false,
      },
    ]);
    expect(wrapper.vm.terminals).toEqual({});
  });

  it('drop the row of a deleted experiment', async () => {
    const wrapper = await page([row()]);
    await handle(expMsg('delete'));
    expect(tableRows(wrapper)).toEqual([]);
  });
});

describe('the experiment button', () => {
  it.each([
    [true, 'Stop experiment a<b>', 'Stop the Experiment', 'Stop', 'stop'],
    [false, 'Start experiment a<b>', 'Start the Experiment', 'Start', 'start'],
  ])(
    'asks once, naming the experiment escaped, then sends the request (running: %s)',
    async (running, label, title, confirmText, path) => {
      axios.post.mockResolvedValue({});
      const wrapper = await page([row({}, { name: 'a<b>', running })]);

      await byLabel(wrapper, label).trigger('click');
      const [dialog] = dialogs();
      expect(textOf(dialog.querySelector('.modal-card-title'))).toBe(title);
      expect(textOf(dialog.querySelector('footer .button:last-child'))).toBe(
        confirmText,
      );
      expect(dialog.querySelector('.media-content div').innerHTML).toBe(
        `<p>This will ${path} the a&lt;b&gt; experiment.</p>`,
      );
      await confirmDialog();
      expect(axios.post).toHaveBeenCalledWith(`experiments/a<b>/${path}`);
    },
  );

  it('sends nothing when canceled, and reports a refused request', async () => {
    const wrapper = await page([row()]);
    const button = byLabel(wrapper, 'Stop experiment exp1');

    await button.trigger('click');
    dialogs().at(-1).querySelector('footer .button:first-child').click();
    await flushPromises();
    expect(axios.post).not.toHaveBeenCalled();

    const err = new Error('forbidden');
    axios.post.mockRejectedValue(err);
    await button.trigger('click');
    await confirmDialog();
    expect(notify.error).toHaveBeenCalledWith(err);
  });
});
