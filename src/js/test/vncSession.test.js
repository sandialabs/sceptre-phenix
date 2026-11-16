// The VNC session of one console (T31): what each experiment/vm action and an
// experiment stop do to it, the retry schedules, and the probe after a
// websocket failure.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises } from '@vue/test-utils';
import { defineComponent, effectScope, h, nextTick, ref } from 'vue';

const { axios, modules, resetMocks, ws } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);

import {
  DENIED,
  EXPERIMENT_STOPPED,
  FAST_RETRY_MS,
  NOT_AVAILABLE,
  NOT_RUNNING,
  SLOW_RETRY_MS,
  STABLE_MS,
  retryDelayMs,
  unreachableMessage,
  useVncSession,
} from '@/vnc/useVncSession.js';
import { UNREACHABLE } from '@/utils/unreachable.js';
import { httpError, wsMsg } from './helpers/fixtures.js';
import { mountComponent } from './helpers/mount.js';

// the session JSON of GET …/vnc (see task 8: VNCSession in openapi.yml)
const SESSION = {
  experiment: 'demo',
  vm: 'web',
  websocket: 'experiments/demo/vms/web/vnc/ws',
  banners: {
    top: {
      lines: ['EXP: demo - VM: web'],
      backgroundColor: 'white',
      textColor: 'black',
    },
    bottom: { lines: [], backgroundColor: 'white', textColor: 'black' },
  },
  display: { vga: 'std', remoteResize: false },
  clipboard: { mode: 'pasteAsKeys' },
};

const UNREACHABLE_BODY = {
  message: 'phenix cannot connect to the VNC port of the VM',
  cause: 'probing VNC server compute-04:43691: connection refused',
  code: 'vnc_unreachable',
  details: { host: 'compute-04', port: 43691 },
};

const answer = () => Promise.resolve({ data: SESSION });
const vmEvent = (action, name = 'demo/web') =>
  wsMsg('experiment/vm', name, action, { name: 'web' });
const send = (msg) => ws.handlers.forEach((handle) => handle(msg));

let scope;

// fake timers leave setImmediate, which flushPromises waits on
const flush = flushPromises;

// a session of demo/web, as a component's setup would start it
function startSession(options = {}) {
  scope = effectScope();
  return scope.run(() =>
    useVncSession({ experiment: 'demo', vm: 'web', ...options }),
  );
}

// the session once its console reports that RFB connected
async function connectedSession(options) {
  axios.get.mockImplementation(answer);
  const session = startSession(options);
  await flush();
  session.connected();
  return session;
}

const probeCalls = () =>
  axios.get.mock.calls.filter(([, config]) => config?.params?.probe);

beforeEach(() => {
  resetMocks();
  vi.useFakeTimers({
    toFake: [
      'setTimeout',
      'clearTimeout',
      'setInterval',
      'clearInterval',
      'Date',
    ],
  });
});

afterEach(() => {
  scope?.stop();
  scope = null;
  vi.useRealTimers();
});

describe('opening the session', () => {
  it('gets the session JSON and gives the console a websocket URL with the token', async () => {
    axios.get.mockImplementation(answer);
    const session = startSession();
    expect(session.state.value).toBe('loading');
    await flush();

    expect(axios.get).toHaveBeenCalledWith('experiments/demo/vms/web/vnc', {});
    expect(session.state.value).toBe('connecting');
    expect(session.info.value).toEqual(SESSION);
    expect(session.connection.value).toEqual({
      url: 'ws://phenix.test/api/v1/experiments/demo/vms/web/vnc/ws?token=t',
      attempt: 1,
    });

    session.connected();
    expect(session.state.value).toBe('connected');
  });

  it('leaves the token out of the websocket URL when there is none', async () => {
    resetMocks({ store: { token: null } });
    axios.get.mockImplementation(answer);
    const session = startSession();
    await flush();
    expect(session.connection.value.url).toBe(
      'ws://phenix.test/api/v1/experiments/demo/vms/web/vnc/ws',
    );
  });

  it.each([
    [409, { message: 'VM is not running' }, 'notRunning', NOT_RUNNING],
    [403, { message: 'forbidden' }, 'denied', DENIED],
    [404, { message: 'VM not found' }, 'notFound', 'VM not found'],
    [
      500,
      {
        message:
          'minimega is too old: vm info has no vga column, update minimega',
      },
      'failed',
      'minimega is too old: vm info has no vga column, update minimega',
    ],
  ])(
    'shows the %i answer and does not retry',
    async (status, data, state, message) => {
      axios.get.mockRejectedValue(httpError(status, data));
      const session = startSession();
      await flush();

      expect(session.state.value).toBe(state);
      if (message) expect(session.message.value).toBe(message);
      expect(session.message.value).not.toBe('');
      expect(session.connection.value).toBe(null);
      await vi.advanceTimersByTimeAsync(10 * 60_000);
      expect(axios.get).toHaveBeenCalledTimes(1);
    },
  );

  it('does not start while it is not enabled, and stops when it is turned off', async () => {
    axios.get.mockImplementation(answer);
    const enabled = ref(false);
    const session = startSession({ enabled });
    await flush();
    expect(session.state.value).toBe('idle');
    expect(axios.get).not.toHaveBeenCalled();

    enabled.value = true;
    await nextTick();
    await flush();
    expect(session.state.value).toBe('connecting');

    enabled.value = false;
    await nextTick();
    expect(session.state.value).toBe('idle');
    expect(session.connection.value).toBe(null);
  });

  it('opens the new VM when the VM it shows changes', async () => {
    axios.get.mockImplementation(answer);
    const vm = ref('web');
    const session = startSession({ vm });
    await flush();
    vm.value = 'db';
    await nextTick();
    await flush();
    expect(axios.get).toHaveBeenLastCalledWith(
      'experiments/demo/vms/db/vnc',
      {},
    );
    expect(session.connection.value.attempt).toBe(2);
  });
});

describe('following experiment/vm events', () => {
  it.each([
    // action, state after, paused after, whether the console connects again
    ['start', 'connected', false, false],
    ['stop', 'connected', true, false],
    ['update', 'connected', false, false],
    ['reset', 'connected', false, false],
    ['errorStarting', 'connected', false, false],
    ['errorStopping', 'connected', false, false],
    ['error', 'connected', false, false],
    ['shutdown', 'notRunning', false, false],
    ['delete', 'notRunning', false, false],
    ['redeploying', 'notRunning', false, false],
    ['redeployed', 'connecting', false, true],
  ])(
    '%s leaves a connected console %s',
    async (action, state, paused, again) => {
      const session = await connectedSession();
      const calls = axios.get.mock.calls.length;
      send(vmEvent(action));
      await flush();

      expect(session.state.value).toBe(state);
      expect(session.paused.value).toBe(paused);
      expect(axios.get.mock.calls.length > calls).toBe(again);
      if (state === 'notRunning') {
        expect(session.message.value).toBe(NOT_RUNNING);
        expect(session.connection.value).toBe(null);
      }
    },
  );

  it('ignores the events of other VMs', async () => {
    const session = await connectedSession();
    send(vmEvent('shutdown', 'demo/db'));
    send(vmEvent('shutdown', 'other/web'));
    send(wsMsg('experiment', 'other', 'stop'));
    expect(session.state.value).toBe('connected');
  });

  it('a paused VM resumes on start and stays connected', async () => {
    const session = await connectedSession();
    send(vmEvent('stop'));
    expect(session.paused.value).toBe(true);
    send(vmEvent('start'));
    expect(session.paused.value).toBe(false);
    expect(session.state.value).toBe('connected');
    expect(axios.get).toHaveBeenCalledTimes(1);
  });

  it('a VM that is not running does not retry, and connects at once on start', async () => {
    const session = await connectedSession();
    send(vmEvent('shutdown'));
    await vi.advanceTimersByTimeAsync(10 * 60_000);
    expect(axios.get).toHaveBeenCalledTimes(1);

    send(vmEvent('start'));
    expect(session.state.value).toBe('loading');
    await flush();
    expect(session.state.value).toBe('connecting');
    expect(axios.get).toHaveBeenCalledTimes(2);
  });

  it('start ends a wait for the next retry', async () => {
    axios.get.mockRejectedValueOnce(httpError(503, {}));
    const session = startSession();
    await flush();
    expect(session.state.value).toBe('waiting');

    axios.get.mockImplementation(answer);
    send(vmEvent('start'));
    await flush();
    expect(session.state.value).toBe('connecting');
  });

  it('checks again after a failed redeploy', async () => {
    const session = await connectedSession();
    send(vmEvent('redeploying'));
    axios.get.mockRejectedValue(
      httpError(409, { message: 'VM is not running' }),
    );
    send(vmEvent('errorRedeploying'));
    await flush();
    expect(axios.get).toHaveBeenCalledTimes(2);
    expect(session.state.value).toBe('notRunning');
  });

  it('a disconnect that the session caused does not start a probe', async () => {
    const session = await connectedSession();
    send(vmEvent('shutdown'));
    session.disconnected({ clean: true });
    await flush();
    expect(probeCalls()).toEqual([]);
    expect(session.state.value).toBe('notRunning');
  });

  it('closes on an experiment stop and shows Experiment stopped', async () => {
    const session = await connectedSession();
    send(wsMsg('experiment', 'demo', 'stop'));

    expect(session.state.value).toBe('experimentStopped');
    expect(session.message.value).toBe(EXPERIMENT_STOPPED);
    expect(session.connection.value).toBe(null);
    await vi.advanceTimersByTimeAsync(10 * 60_000);
    expect(axios.get).toHaveBeenCalledTimes(1);
  });

  it('ignores the other experiment events', async () => {
    const session = await connectedSession();
    send(wsMsg('experiment', 'demo', 'stopping'));
    send(wsMsg('experiment', 'demo', 'start'));
    expect(session.state.value).toBe('connected');
  });
});

describe('the retry schedules', () => {
  it('backs off on the fast schedule, then stays at its last step', async () => {
    axios.get.mockRejectedValue(httpError(503, {}));
    const session = startSession();
    await flush();

    for (const delay of [...FAST_RETRY_MS, FAST_RETRY_MS.at(-1)]) {
      const calls = axios.get.mock.calls.length;
      expect(session.state.value).toBe('waiting');
      expect(session.retrySeconds.value).toBe(Math.ceil(delay / 1000));
      await vi.advanceTimersByTimeAsync(delay - 1);
      expect(axios.get).toHaveBeenCalledTimes(calls);
      await vi.advanceTimersByTimeAsync(1);
      expect(axios.get).toHaveBeenCalledTimes(calls + 1);
    }
  });

  it('counts down the seconds to the next retry', async () => {
    axios.get.mockRejectedValue(httpError(503, {}));
    const session = startSession();
    await flush();
    await vi.advanceTimersByTimeAsync(FAST_RETRY_MS[0]);
    expect(session.retrySeconds.value).toBe(FAST_RETRY_MS[1] / 1000);
    await vi.advanceTimersByTimeAsync(1000);
    expect(session.retrySeconds.value).toBe(FAST_RETRY_MS[1] / 1000 - 1);
  });

  it('starts the schedule again after a connection stays up', async () => {
    const session = await connectedSession();
    session.disconnected({ clean: false });
    await flush();
    expect(session.retrySeconds.value).toBe(FAST_RETRY_MS[0] / 1000);
    await vi.advanceTimersByTimeAsync(FAST_RETRY_MS[0]);
    session.connected();
    await vi.advanceTimersByTimeAsync(STABLE_MS);
    session.disconnected({ clean: false });
    await flush();
    expect(session.retrySeconds.value).toBe(FAST_RETRY_MS[0] / 1000);
  });

  it('keeps backing off when each connection drops soon after it opens', async () => {
    const session = await connectedSession();
    for (const delay of FAST_RETRY_MS) {
      await vi.advanceTimersByTimeAsync(STABLE_MS - 1);
      session.disconnected({ clean: false });
      await flush();
      expect(session.state.value).toBe('waiting');
      expect(session.retrySeconds.value).toBe(delay / 1000);
      await vi.advanceTimersByTimeAsync(delay);
      expect(session.state.value).toBe('connecting');
      session.connected();
    }
  });

  it('a Reconnect starts the schedule again at once', async () => {
    const session = await connectedSession();
    session.disconnected({ clean: false });
    await flush();
    await vi.advanceTimersByTimeAsync(FAST_RETRY_MS[0]);
    session.connected();
    session.disconnected({ clean: false });
    await flush();
    expect(session.retrySeconds.value).toBe(FAST_RETRY_MS[1] / 1000);
    session.retryNow();
    await flush();
    session.connected();
    session.disconnected({ clean: false });
    await flush();
    expect(session.retrySeconds.value).toBe(FAST_RETRY_MS[0] / 1000);
  });

  it('retries at once on request', async () => {
    axios.get.mockRejectedValue(httpError(503, {}));
    const session = startSession();
    await flush();
    session.retryNow();
    expect(session.state.value).toBe('loading');
    expect(axios.get).toHaveBeenCalledTimes(2);
  });

  it('gives each schedule its delays by failure count', () => {
    expect(FAST_RETRY_MS[0]).toBeLessThan(SLOW_RETRY_MS[0]);
    expect(retryDelayMs('fast', 1)).toBe(FAST_RETRY_MS[0]);
    expect(retryDelayMs('fast', 99)).toBe(FAST_RETRY_MS.at(-1));
    expect(retryDelayMs('slow', 1)).toBe(SLOW_RETRY_MS[0]);
    expect(retryDelayMs('slow', 99)).toBe(SLOW_RETRY_MS.at(-1));
  });
});

describe('the probe after a websocket failure', () => {
  it('probes after a dropped connection and reconnects on the fast schedule', async () => {
    const session = await connectedSession();
    session.disconnected({ clean: false });
    expect(session.state.value).toBe('checking');
    expect(session.connection.value).toBe(null);
    await flush();

    expect(probeCalls()).toEqual([
      ['experiments/demo/vms/web/vnc', { params: { probe: true } }],
    ]);
    expect(session.state.value).toBe('waiting');
    expect(session.retrySeconds.value).toBe(FAST_RETRY_MS[0] / 1000);
    await vi.advanceTimersByTimeAsync(FAST_RETRY_MS[0]);
    expect(session.state.value).toBe('connecting');
    expect(session.connection.value.attempt).toBe(2);
  });

  it('names the host and port after a 502 and retries on the slow schedule', async () => {
    axios.get.mockImplementation(answer);
    const session = startSession();
    await flush();
    axios.get.mockRejectedValue(httpError(502, UNREACHABLE_BODY));
    session.disconnected({ clean: false });
    await flush();

    expect(probeCalls()).toHaveLength(1);
    expect(session.state.value).toBe('waiting');
    expect(session.message.value).toBe(
      'phēnix cannot connect to the VNC port of web on compute-04 (port 43691). Make sure the phēnix host can reach that host.',
    );
    expect(session.message.value).toBe(
      unreachableMessage('web', 'compute-04', 43691),
    );
    expect(session.retrySeconds.value).toBe(SLOW_RETRY_MS[0] / 1000);

    await vi.advanceTimersByTimeAsync(SLOW_RETRY_MS[0] - 1);
    expect(axios.get).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(axios.get).toHaveBeenCalledTimes(3);
  });

  it('retries a phēnix 502 after the probe on the slow schedule', async () => {
    axios.get.mockImplementation(answer);
    const session = startSession();
    await flush();
    axios.get.mockRejectedValue(
      Object.assign(new Error('502'), {
        response: {
          status: 502,
          data: { message: 'bad gateway' },
          headers: { 'x-phenix-seq': '7' },
        },
      }),
    );
    session.disconnected({ clean: false });
    await flush();
    expect(session.message.value).toBe(NOT_AVAILABLE);
    expect(session.retrySeconds.value).toBe(SLOW_RETRY_MS[0] / 1000);
  });

  it('retries a proxy 502 after the probe on the fast schedule', async () => {
    axios.get.mockImplementation(answer);
    const session = startSession();
    await flush();
    axios.get.mockRejectedValue(httpError(502, '<html>Bad Gateway</html>'));
    session.disconnected({ clean: false });
    await flush();
    expect(session.state.value).toBe('waiting');
    expect(session.message.value).toBe(UNREACHABLE);
    expect(session.retrySeconds.value).toBe(FAST_RETRY_MS[0] / 1000);
  });

  it('says the VNC server is not available when a connection never opens but the probe passes', async () => {
    axios.get.mockImplementation(answer);
    const session = startSession();
    await flush();
    session.disconnected({ clean: true });
    await flush();

    expect(session.message.value).toBe(NOT_AVAILABLE);
    expect(session.retrySeconds.value).toBe(SLOW_RETRY_MS[0] / 1000);
  });

  it('a probe that finds the VM stopped shows VM is not running', async () => {
    const session = await connectedSession();
    axios.get.mockRejectedValue(
      httpError(409, { message: 'VM is not running' }),
    );
    session.disconnected({ clean: false });
    await flush();
    expect(session.state.value).toBe('notRunning');
  });

  it('drops the answer of a probe that the session no longer waits for', async () => {
    const session = await connectedSession();
    const probe = Promise.withResolvers();
    axios.get.mockReturnValue(probe.promise);
    session.disconnected({ clean: false });
    send(vmEvent('shutdown'));
    probe.resolve({ data: SESSION });
    await flush();
    expect(session.state.value).toBe('notRunning');
  });

  it('a security failure ends the session with its reason', async () => {
    axios.get.mockImplementation(answer);
    const session = startSession();
    await flush();
    session.securityFailure('no supported security type');
    session.disconnected({ clean: false });
    await flush();
    expect(session.state.value).toBe('failed');
    expect(session.message.value).toContain('no supported security type');
    expect(probeCalls()).toEqual([]);
  });
});

describe('clean-up', () => {
  it('stops following events and timers when its scope ends', async () => {
    axios.get.mockRejectedValue(httpError(503, {}));
    startSession();
    await flush();
    expect(ws.handlers).toHaveLength(1);
    scope.stop();
    expect(ws.handlers).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(10 * 60_000);
    expect(axios.get).toHaveBeenCalledTimes(1);
  });

  it('cleans up before its component unmounts', async () => {
    axios.get.mockImplementation(answer);
    let session;
    const Pane = defineComponent({
      setup() {
        session = useVncSession({ experiment: 'demo', vm: 'web' });
        return () => h('div');
      },
    });
    const wrapper = await mountComponent(Pane);
    await flush();
    expect(session.connection.value).not.toBe(null);
    wrapper.unmount();
    expect(ws.handlers).toHaveLength(0);
    expect(session.connection.value).toBe(null);
  });
});
