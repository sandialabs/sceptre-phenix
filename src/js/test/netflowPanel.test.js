// The running experiment's netflow tab: its lines, the panel that shows them,
// and the capture that streams them.
import { flushPromises } from '@vue/test-utils';
import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h, nextTick, reactive } from 'vue';

const { axios, modules, notify, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
// the room the window has for the table
const room = vi.hoisted(() => vi.fn(() => null));
vi.mock('@/utils/viewportFit.js', () => ({ roomInWindow: room }));

import NetflowCaptures from '@/components/experiment/NetflowCaptures.vue';
import NetflowPanel from '@/components/experiment/NetflowPanel.vue';
import {
  NETFLOW_MAX_LINES,
  NETFLOW_SHOWN_LINES,
  appendNetflowLines,
  matchNetflow,
  netflowLine,
  netflowMarker,
  netflowSummary,
} from '@/components/experiment/netflow.js';
import { useNetflowCapture } from '@/components/experiment/useNetflowCapture.js';
import { httpError } from './helpers/fixtures.js';
import { mountComponent, textOf, toasts } from './helpers/mount.js';

beforeEach(() => {
  resetMocks();
  room.mockReturnValue(null);
});

const flow = (fields = {}) => ({
  src: '10.0.0.1',
  sport: 1234,
  dst: '10.0.0.2',
  dport: 80,
  proto: 6,
  packets: 3,
  bytes: 180,
  ...fields,
});

describe('the lines', () => {
  it('shows a flow by its endpoints and protocol name', () => {
    expect(netflowLine(flow())).toMatchObject({
      src: '10.0.0.1:1234',
      dst: '10.0.0.2:80',
      proto: 'TCP',
    });
    expect(netflowLine(flow({ proto: 132 })).proto).toBe('132');
  });

  it('keeps the newest NETFLOW_MAX_LINES lines, in order', () => {
    fc.assert(
      fc.property(
        fc.array(fc.integer({ min: 0, max: NETFLOW_MAX_LINES }), {
          maxLength: 4,
        }),
        (batches) => {
          const all = [];
          let added = 0;
          for (const count of batches) {
            const ids = Array.from({ length: count }, (_, i) => added + i);
            appendNetflowLines(all, ids);
            added += count;
          }
          const kept = Math.min(added, NETFLOW_MAX_LINES);
          expect(all).toEqual(
            Array.from({ length: kept }, (_, i) => added - kept + i),
          );
        },
      ),
      { numRuns: 20 },
    );
  });

  it('matches a search in any part of a line, ignoring case', () => {
    const lines = [netflowLine(flow()), netflowMarker('Capture started')];
    expect(matchNetflow(lines, ' tcp ')).toEqual([lines[0]]);
    expect(matchNetflow(lines, 'STARTED')).toEqual([lines[1]]);
    expect(matchNetflow(lines, '')).toBe(lines);
  });

  it.each([
    [3, 3, '3 lines, newest first'],
    [2, 3, '2 matching (of 3) lines, newest first'],
    [
      NETFLOW_SHOWN_LINES + 1,
      NETFLOW_SHOWN_LINES + 1,
      `Newest ${NETFLOW_SHOWN_LINES} of ${NETFLOW_SHOWN_LINES + 1} lines, newest first`,
    ],
  ])('sums up %i of %i lines', (matches, total, text) => {
    expect(netflowSummary(matches, total)).toBe(text);
  });
});

describe('the panel', () => {
  const render = (props) =>
    mountComponent(NetflowPanel, { props: { experiment: 'demo', ...props } });
  const rows = (wrapper) =>
    wrapper.findAll('tbody tr').map((tr) => textOf(tr.findAll('td').at(1)));

  it.each([
    [
      false,
      'No netflow captures yet. Start one with the netflow button above.',
    ],
    [true, 'Starting netflow capture…'],
  ])('says so when it has no lines, starting: %s', async (starting, text) => {
    expect(
      textOf((await render({ lines: [], starting })).find('.netflow-empty')),
    ).toBe(text);
  });

  it.each([
    ['no lines', []],
    ['lines', [netflowMarker('Capture started')]],
  ])("offers the experiment's recorded captures with %s", async (_, lines) => {
    const wrapper = await render({ lines });
    const captures = wrapper.findComponent(NetflowCaptures);
    expect(captures.props('experiment')).toBe('demo');
    expect(textOf(captures.find('button'))).toBe('Download captures');
    expect(wrapper.find('.netflow-view').exists()).toBe(lines.length > 0);
  });

  it('shows the lines matching the search, newest first', async () => {
    const lines = [
      netflowMarker('Capture started'),
      netflowLine(flow({ dport: 22 })),
      netflowLine(flow({ dport: 80 })),
    ];
    const wrapper = await render({ lines, search: '10.0.0.2' });
    expect(rows(wrapper)).toEqual(['10.0.0.1:1234', '10.0.0.1:1234']);
    expect(textOf(wrapper.find('.netflow-summary'))).toBe(
      '2 matching (of 3) lines, newest first',
    );
    expect(wrapper.findAll('.netflow-marker')).toHaveLength(0);

    await wrapper.setProps({ search: '' });
    expect(textOf(wrapper.find('.netflow-marker'))).toContain(
      'Capture started',
    );
  });

  it('fits itself to the room in the window as its first line arrives, and as the window resizes', async () => {
    const lines = reactive([]);
    const wrapper = await render({ lines });
    const height = () => wrapper.find('.netflow-view').element.style.maxHeight;
    room.mockReturnValue(500);

    lines.push(netflowMarker('Capture started'));
    await flushPromises();
    expect(height()).toBe('500px');

    // never so short it hides the lines
    room.mockReturnValue(100);
    window.dispatchEvent(new Event('resize'));
    await nextTick();
    expect(height()).toBe('240px');
  });
});

describe('the capture', () => {
  class FakeWebSocket extends EventTarget {
    static instances = [];
    constructor(url) {
      super();
      this.url = url;
      this.close = vi.fn();
      FakeWebSocket.instances.push(this);
    }
  }

  beforeEach(() => {
    FakeWebSocket.instances = [];
    vi.stubGlobal('WebSocket', FakeWebSocket);
  });
  afterEach(() => vi.unstubAllGlobals());

  // a component on experiment demo holding the capture
  const Host = defineComponent({
    setup: () => useNetflowCapture(),
    render: () => h('div'),
  });
  const host = async () =>
    (await mountComponent(Host, { route: '/experiment/demo' })).vm;
  const markers = (capture) =>
    capture.netflow.lines.filter((l) => l.marker).map((l) => l.marker);

  it('starts one on the server, then streams its flows', async () => {
    axios.post.mockResolvedValue({});
    const capture = await host();

    await capture.handleNetflow(true);
    expect(axios.post).toHaveBeenCalledWith('experiments/demo/netflow');
    expect(capture.netflow).toMatchObject({ capturing: true, starting: true });
    const [socket] = FakeWebSocket.instances;
    expect(socket.url).toMatch(
      /\/api\/v1\/experiments\/demo\/netflow\/ws\?token=t$/,
    );

    socket.dispatchEvent(new Event('open'));
    socket.dispatchEvent(
      Object.assign(new Event('message'), {
        data: `${JSON.stringify(flow())}\n${JSON.stringify(flow())}\n`,
      }),
    );
    expect(capture.netflow.starting).toBe(false);
    expect(capture.netflow.lines).toHaveLength(3);
  });

  it('starts nothing when the server refuses', async () => {
    const err = new Error('boom');
    axios.post.mockRejectedValue(err);
    const capture = await host();

    await capture.handleNetflow(true);
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(capture.netflow).toMatchObject({
      capturing: false,
      starting: false,
    });
    expect(FakeWebSocket.instances).toEqual([]);
  });

  it('says so when the server had already stopped it', async () => {
    axios.delete.mockRejectedValue(httpError(404));
    const capture = await host();
    await capture.handleNetflow(true, false);
    const [socket] = FakeWebSocket.instances;

    await capture.handleNetflow(false);
    await flushPromises();
    expect(markers(capture)).toEqual([
      'Capture started',
      'Capture had already stopped on the server',
    ]);
    expect(toasts().map((t) => t.type)).toEqual(['is-info']);
    expect(capture.netflow.capturing).toBe(false);
    expect(socket.close).toHaveBeenCalled();
  });

  it('keeps capturing when the stop fails otherwise', async () => {
    const err = httpError(500);
    axios.delete.mockRejectedValue(err);
    const capture = await host();
    await capture.handleNetflow(true, false);

    await capture.stopNetflow();
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(capture.netflow).toMatchObject({ capturing: true, stopping: false });
  });

  it('closes the stream as its page closes', async () => {
    const wrapper = await mountComponent(Host, { route: '/experiment/demo' });
    await wrapper.vm.handleNetflow(true, false);
    wrapper.unmount();
    expect(FakeWebSocket.instances[0].close).toHaveBeenCalled();
  });
});
