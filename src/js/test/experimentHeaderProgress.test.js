// The start and stop bar of the experiment page's header: the page keeps it
// (headerProgress.js) from the click and the websocket, and the header draws it.
import { flushPromises } from '@vue/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { h } from 'vue';

const { modules, notify, resetMocks, ws } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/utils/websocket', modules.websocket);

import ExperimentHeader from '@/components/experiment/ExperimentHeader.vue';
import { provideExperimentProgress } from '@/components/experiment/headerProgress.js';
import { wsMsg } from './helpers/fixtures.js';
import { mountComponent, textOf } from './helpers/mount.js';

beforeEach(() => resetMocks());

// the page of experiment exp1 with its header; `tracker` is what it provides
let tracker;
const Page = {
  setup() {
    tracker = provideExperimentProgress('exp1');
    return () =>
      h(ExperimentHeader, { name: 'exp1', experiment: { name: 'exp1' } });
  },
};
const open = () => mountComponent(Page, { route: '/experiment/exp1' });

const publish = async (action, result, name = 'exp1') => {
  for (const handler of ws.handlers) {
    handler(wsMsg('experiment', name, action, result));
  }
  await flushPromises();
};

// what the header shows under the summary: the bar and its label, or the
// failure; null when nothing
const shown = (page) => {
  const area = page.find('.experiment-header-progress');
  if (!area.exists()) return null;
  const failure = area.find('.exp-progress-failure');
  if (failure.exists()) return `failed: ${textOf(failure)}`;
  const label = area.find('.exp-progress-label');
  return [
    textOf(area.find('.progress-value')),
    ...(label.exists() ? [textOf(label)] : []),
  ].join(' ');
};

const progress = (operation, percent, phase, done = 0, total = 0, step) => ({
  operation,
  percent,
  phase,
  done,
  total,
  step,
});

describe("the experiment page header's bar", () => {
  it('shows nothing while the experiment is idle', async () => {
    const page = await open();
    expect(shown(page)).toBe(null);
  });

  it('follows a start from the click to the end of its post-start apps', async () => {
    const page = await open();

    const sending = tracker.begin('start');
    await flushPromises();
    expect(shown(page)).toBe('0% Sending start request');

    await publish('starting');
    expect(shown(page)).toBe('0% Sending start request');
    // another experiment's news is not this one's
    await publish('progress', progress('start', 0.5, 'launch-vms'), 'exp2');
    expect(shown(page)).toBe('0% Sending start request');

    await publish('progress', progress('start', 0.3, 'launch-vms', 3, 10));
    expect(shown(page)).toBe('30% Launching VMs 3/10');

    await publish('start', { name: 'exp1' });
    sending.done();
    await publish(
      'progress',
      progress('start', 0.9, 'post-start-apps', 1, 2, 'scorch'),
    );
    expect(shown(page)).toBe('90% Post-start apps 1/2: scorch');

    await publish('progress', progress('start', 1, 'done'));
    expect(shown(page)).toBe(null);
    expect(page.find('progress').exists()).toBe(false);
  });

  it('ends a stop at its stop message, which no done follows', async () => {
    const page = await open();

    const sending = tracker.begin('stop');
    await publish('stopping');
    sending.done();
    await publish('progress', progress('stop', 0.9, 'cleanup', 0, 0));
    expect(shown(page)).toBe('90% Cleaning up');

    await publish('stop', { name: 'exp1', running: false });
    expect(shown(page)).toBe(null);
    expect(page.find('progress').exists()).toBe(false);
  });

  it('puts back what it showed when the request fails before any news', async () => {
    const page = await open();
    await publish('progress', progress('start', 0.9, 'post-start-apps'));

    tracker.begin('stop').undo();
    await flushPromises();
    expect(shown(page)).toBe('90% Post-start apps');
  });

  it('keeps the news a failed request had', async () => {
    const page = await open();

    const sending = tracker.begin('stop');
    await publish('stopping');
    await publish('progress', progress('stop', 0.4, 'stop-vms', 2, 5));
    sending.undo();
    await flushPromises();
    expect(shown(page)).toBe('40% Stopping VMs 2/5');
  });

  it('shows where a start failed until the next action, and tells it once', async () => {
    const page = await open();

    await publish('errorStarting', {
      operation: 'start',
      phase: 'pre-start-apps',
      step: 'startup',
      error: 'exit 1',
    });
    const reason = 'Failed at Pre-start apps: startup: exit 1';
    expect(shown(page)).toBe(`failed: ${reason}`);
    expect(page.find('.notification').classes()).toContain('is-danger');
    expect(notify.show.mock.calls).toEqual([
      ['The exp1 experiment did not start', reason],
    ]);

    tracker.begin('start');
    await flushPromises();
    expect(shown(page)).toBe('0% Sending start request');
  });

  it("leaves a failure of this page's own request to the request to tell", async () => {
    const page = await open();

    tracker.begin('stop');
    await publish('errorStopping', {
      phase: 'stop-vms',
      error: 'VMs survived',
    });

    expect(shown(page)).toBe('failed: Failed at Stopping VMs: VMs survived');
    expect(notify.show).not.toHaveBeenCalled();
  });

  it('tells a failure of an old server with no reason, and draws none', async () => {
    const page = await open();
    await publish('stopping');

    await publish('errorStopping', null);

    expect(shown(page)).toBe(null);
    expect(notify.show.mock.calls).toEqual([
      ['The exp1 experiment did not stop', ''],
    ]);
  });

  it('stops listening when the page closes', async () => {
    const page = await open();
    expect(ws.handlers).toHaveLength(1);
    page.unmount();
    expect(ws.handlers).toHaveLength(0);
  });
});
