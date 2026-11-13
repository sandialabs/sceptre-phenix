// The running experiment's packet captures: asking the server which run,
// starting and stopping them, and which buttons wait on the server.
import fc from 'fast-check';
import { flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';

const { axios, modules, notify, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);

import {
  minuteStamp,
  timestampParts,
  uniqueStamp,
} from '@/components/experiment/timestamp.js';
import {
  QUIET_MS,
  useVmCaptures,
} from '@/components/experiment/useVmCaptures.js';
import { wsMsg } from './helpers/fixtures.js';
import { dialogs, mountComponent, toasts } from './helpers/mount.js';

const url = 'experiments/demo/vms/web/captures';

const web = {
  name: 'web',
  networks: ['DMZ (149)', 'disconnected', 'MGMT (151)'],
  captures: [],
};

// the composable as the demo experiment's page uses it, with a spy for the
// page's fan-out of requests
async function useCaptures() {
  const fanOut = vi.fn(async (names, _, send) =>
    Promise.allSettled(names.map(send)),
  );
  let captures;
  const Host = defineComponent({
    setup() {
      captures = useVmCaptures({ fanOut });
      return () => h('div');
    },
  });
  await mountComponent(Host, { route: '/experiment/demo' });
  return { captures, fanOut };
}

// answers the captures GET with these running captures, once released
function listCaptures(running) {
  const request = Promise.withResolvers();
  axios.get.mockImplementation(async (got) => {
    expect(got).toBe(url);
    await request.promise;
    return { data: { captures: running } };
  });
  return () => request.resolve();
}

beforeEach(() => {
  resetMocks();
  vi.setSystemTime(new Date(2024, 0, 2, 3, 4, 5));
});

afterEach(() => vi.useRealTimers());

describe('one interface', () => {
  it.each([
    ['pcapng', /^web_2_.*\.pcapng$/],
    ['pcap', /^web_2_.*\.pcap$/],
  ])(
    'starts one in the format %s the format dialog picked',
    async (format, file) => {
      const { captures } = await useCaptures();
      listCaptures([])();
      axios.post.mockResolvedValue({});
      await captures.handlePcap(web, 2, format);
      expect(axios.post.mock.calls).toEqual([
        [url, { interface: 2, filename: expect.stringMatching(file), format }],
      ]);
    },
  );

  it('starts one when the list answer names no captures', async () => {
    const { captures } = await useCaptures();
    axios.get.mockResolvedValue({ data: {} });
    axios.post.mockResolvedValue({});
    await captures.handlePcap(web, 0, 'pcapng');
    expect(axios.get).toHaveBeenCalledWith(url);
    expect(axios.post.mock.calls).toEqual([
      [
        url,
        {
          interface: 0,
          filename: expect.stringMatching(/^web_0_.*\.pcapng$/),
          format: 'pcapng',
        },
      ],
    ]);
    expect(captures.capturePending.value).toEqual({});
  });

  it('stops nothing when a format start finds the interface captured', async () => {
    const { captures } = await useCaptures();
    listCaptures([{ interface: 2 }])();
    await captures.handlePcap(web, 2, 'pcapng');
    expect(axios.delete).not.toHaveBeenCalled();
    expect(axios.post).not.toHaveBeenCalled();
    expect(toasts()).toEqual([
      {
        text: 'phēnix already captures interface 2 of the web VM.',
        type: 'is-info',
      },
    ]);
  });

  it('marks the interface pending until the server answers, asking nothing', async () => {
    const { captures } = await useCaptures();
    const release = listCaptures([]);
    const start = Promise.withResolvers();
    axios.post.mockReturnValue(start.promise);

    captures.handlePcap(web, 2);
    // a second press while waiting sends nothing more
    captures.handlePcap(web, 2);
    expect(captures.capturePending.value).toEqual({ 'web/2': true });
    expect(captures.pendingCaptureIfaces('web')).toEqual([2]);
    expect(captures.pendingCaptureIfaces('db')).toEqual([]);
    expect(axios.get).toHaveBeenCalledTimes(1);

    release();
    await flushPromises();
    // still waiting on the start, which no dialog asked about
    expect(dialogs()).toEqual([]);
    expect(captures.pendingCaptureIfaces('web')).toEqual([2]);

    start.resolve({});
    await flushPromises();
    expect(captures.capturePending.value).toEqual({});
  });

  it('starts a capture named for the VM, interface and second', async () => {
    const { captures } = await useCaptures();
    listCaptures([])();
    axios.post.mockResolvedValue({});

    await captures.handlePcap(web, 2);

    expect(dialogs()).toEqual([]);
    expect(axios.post).toHaveBeenCalledWith(url, {
      interface: 2,
      filename: expect.stringMatching(
        /^web_2_2024-01-02_030405_[0-9a-f]{4}\.pcap$/,
      ),
    });
  });

  // minimega truncates an existing capture file, so a restart in the same
  // second must not reuse the name of the capture it replaces
  it('gives a restarted capture a new file in the same second', async () => {
    let n = 0;
    const random = vi
      .spyOn(crypto, 'getRandomValues')
      .mockImplementation((a) => a.fill(++n));
    try {
      const { captures } = await useCaptures();
      listCaptures([])();
      axios.post.mockResolvedValue({});

      await captures.handlePcap(web, 2);
      await captures.handlePcap(web, 2);

      const [first, second] = axios.post.mock.calls.map(
        ([, body]) => body.filename,
      );
      expect(first).toBe('web_2_2024-01-02_030405_0001.pcap');
      expect(second).toBe('web_2_2024-01-02_030405_0002.pcap');
    } finally {
      random.mockRestore();
    }
  });

  it('reports a failed start and clears the pending mark', async () => {
    const { captures } = await useCaptures();
    listCaptures([])();
    const err = new Error('no');
    axios.post.mockRejectedValue(err);

    await captures.handlePcap(web, 0);

    expect(notify.error).toHaveBeenCalledWith(err);
    expect(captures.capturePending.value).toEqual({});
  });

  it('refuses a disconnected interface', async () => {
    const { captures } = await useCaptures();
    listCaptures([])();

    await captures.handlePcap(web, 1);

    expect(dialogs()).toEqual([]);
    expect(axios.post).not.toHaveBeenCalled();
    expect(notify.show).toHaveBeenCalledWith(
      'Cannot capture traffic on a disconnected interface.',
    );
    expect(toasts()).toEqual([]);
  });

  it.each([
    ['the only capture', [{ interface: 0 }]],
    ['one of several captures', [{ interface: 0 }, { interface: 2 }]],
  ])(
    "stops only the interface's capture when it is %s, asking nothing",
    async (_, running) => {
      const { captures } = await useCaptures();
      listCaptures(running)();
      axios.delete.mockResolvedValue({});

      await captures.handlePcap(web, 0);

      expect(dialogs()).toEqual([]);
      expect(axios.delete.mock.calls).toEqual([
        [url, { params: { iface: 0 } }],
      ]);
      expect(captures.capturePending.value).toEqual({});
    },
  );

  it('reports a failed listing and clears the pending mark', async () => {
    const { captures } = await useCaptures();
    const err = new Error('no');
    axios.get.mockRejectedValue(err);

    await captures.handlePcap(web, 0);

    expect(notify.error).toHaveBeenCalledWith(err);
    expect(captures.capturePending.value).toEqual({});
    expect(axios.post).not.toHaveBeenCalled();
    expect(axios.delete).not.toHaveBeenCalled();
  });
});

describe('every interface', () => {
  it('starts captures on the connected interfaces not yet captured, asking nothing', async () => {
    const { captures, fanOut } = await useCaptures();
    listCaptures([{ interface: 2 }])();
    axios.post.mockResolvedValue({});
    const done = Promise.withResolvers();
    fanOut.mockImplementation(async (names, _, send) => {
      await Promise.allSettled(names.map(send));
      await done.promise;
    });

    const started = captures.captureAllInterfaces(web);
    await flushPromises();
    expect(dialogs()).toEqual([]);
    expect(captures.captureAllPending.value).toEqual({ web: true });
    // a second press while starting sends nothing more
    await captures.captureAllInterfaces(web);
    expect(axios.get).toHaveBeenCalledTimes(1);

    done.resolve();
    await started;
    expect(captures.captureAllPending.value).toEqual({});
    expect(fanOut).toHaveBeenCalledWith(
      ['interface 0'],
      'Started packet captures of web on',
      expect.any(Function),
      'interface',
    );
    expect(axios.post).toHaveBeenCalledWith(url, {
      interface: 0,
      filename: expect.stringMatching(
        /^web_0_2024-01-02_030405_[0-9a-f]{4}\.pcap$/,
      ),
    });
  });

  it('says so when every connected interface is captured', async () => {
    const { captures, fanOut } = await useCaptures();
    listCaptures([{ interface: 0 }, { interface: 2 }])();

    await captures.captureAllInterfaces(web);

    expect(dialogs()).toEqual([]);
    expect(fanOut).not.toHaveBeenCalled();
    expect(captures.captureAllPending.value).toEqual({});
    expect(toasts()).toEqual([
      {
        text: 'phēnix already captures every connected interface of the web VM.',
        type: 'is-info',
      },
    ]);
  });

  it('stops every capture, pending until the server answers, asking nothing', async () => {
    const { captures } = await useCaptures();
    const vm = { ...web, captures: [{ interface: 0 }, { interface: 2 }] };
    const request = Promise.withResolvers();
    axios.delete.mockReturnValue(request.promise);

    const stopped = captures.stopAllCaptures(vm);
    await flushPromises();
    expect(dialogs()).toEqual([]);
    expect(captures.captureAllPending.value).toEqual({ web: true });
    expect(axios.delete).toHaveBeenCalledWith(url);

    // a second press while stopping sends nothing more
    await captures.stopAllCaptures(vm);
    expect(axios.delete).toHaveBeenCalledTimes(1);

    request.reject(new Error('no'));
    await stopped;
    expect(notify.error).toHaveBeenCalled();
    expect(captures.captureAllPending.value).toEqual({});
  });
});

describe('every interface on a VLAN', () => {
  it.each([
    [
      'starts',
      true,
      'experiments/demo/captureVlan',
      { data: { captures: [{ vm: 'web' }, { vm: 'db' }] } },
      {
        text: 'Started 2 packet captures on the DMZ VLAN.',
        type: 'is-success',
      },
    ],
    [
      'says so when it starts none',
      true,
      'experiments/demo/captureVlan',
      { data: { captures: [] } },
      { text: 'Started 0 packet captures on the DMZ VLAN.', type: 'is-info' },
    ],
    [
      'stops',
      false,
      'experiments/demo/stopCaptureVlan',
      { data: { vms: ['web'] } },
      {
        text: 'Stopped the packet captures on the DMZ VLAN.',
        type: 'is-success',
      },
    ],
  ])(
    '%s the captures, pending until the server answers',
    async (_, start, path, answer, toast) => {
      const { captures } = await useCaptures();
      const request = Promise.withResolvers();
      axios.post.mockReturnValue(request.promise);

      const done = captures.captureVlan('DMZ', start);
      expect(captures.vlanCapturePending.value).toEqual({ DMZ: true });
      // a second press while waiting sends nothing more
      await captures.captureVlan('DMZ', start);
      expect(axios.post.mock.calls).toEqual([[path, { vlan: 'DMZ' }]]);

      request.resolve(answer);
      await done;
      expect(captures.vlanCapturePending.value).toEqual({});
      expect(toasts()).toEqual([toast]);
    },
  );

  it('reports a refused request and clears the pending mark', async () => {
    const { captures } = await useCaptures();
    const err = new Error('forbidden');
    axios.post.mockRejectedValue(err);

    await captures.captureVlan('DMZ', true);

    expect(notify.error).toHaveBeenCalledWith(err);
    expect(captures.vlanCapturePending.value).toEqual({});
    expect(toasts()).toEqual([]);
  });
});

describe('the events of its own bulk requests', () => {
  const db = { name: 'db', networks: ['MGMT (151)', 'dmz (149)'] };
  const rows = [web, db];
  const event = (action, vm, iface) =>
    wsMsg('experiment/vm/capture', `demo/${vm}`, action, { interface: iface });
  // which of these events the page shows no toast for
  const quietOf = (captures, action, events) =>
    events.filter(([vm, iface]) =>
      captures.quietCaptureEvent(event(action, vm, iface), vm, rows),
    );

  it.each([
    [
      'starts',
      true,
      'start',
      { data: { captures: [{ vm: 'web', interface: 0 }] } },
      [['web', 0]],
    ],
    ['stops', false, 'stop', { data: { vms: ['db'] } }, [['db', 1]]],
  ])(
    'while it %s the captures on a VLAN, and a moment after, hushes those of the VLAN alone',
    async (_, start, action, answer, after) => {
      const { captures } = await useCaptures();
      const request = Promise.withResolvers();
      axios.post.mockReturnValue(request.promise);
      const events = [
        ['web', 0],
        ['web', 2],
        ['db', 1],
        ['db', 0],
        ['other-page', 0],
      ];

      const done = captures.captureVlan('DMZ', start);
      expect(quietOf(captures, action, events)).toEqual([
        ['web', 0],
        ['db', 1],
        ['other-page', 0],
      ]);
      // the other action still toasts
      const other = start ? 'stop' : 'start';
      expect(quietOf(captures, other, events)).toEqual([]);

      request.resolve(answer);
      await done;
      expect(quietOf(captures, action, events)).toEqual(after);

      vi.setSystemTime(Date.now() + QUIET_MS);
      expect(quietOf(captures, action, events)).toEqual([]);
    },
  );

  it('hushes nothing once a VLAN request fails', async () => {
    const { captures } = await useCaptures();
    axios.post.mockRejectedValue(new Error('no'));
    await captures.captureVlan('DMZ', true);
    expect(quietOf(captures, 'start', [['web', 0]])).toEqual([]);
  });

  it('hushes the starts of the interfaces it captures on a VM, and a moment after', async () => {
    const { captures, fanOut } = await useCaptures();
    listCaptures([{ interface: 2 }])();
    axios.post.mockResolvedValue({});
    const done = Promise.withResolvers();
    fanOut.mockImplementation(async (names, _, send) => {
      await Promise.allSettled(names.map(send));
      await done.promise;
    });
    const events = [
      ['web', 0],
      ['web', 2],
      ['db', 0],
    ];

    const started = captures.captureAllInterfaces(web);
    await flushPromises();
    expect(quietOf(captures, 'start', events)).toEqual([['web', 0]]);

    done.resolve();
    await started;
    expect(quietOf(captures, 'start', events)).toEqual([['web', 0]]);
    vi.setSystemTime(Date.now() + QUIET_MS);
    expect(quietOf(captures, 'start', events)).toEqual([]);
  });
});

describe('timestamps', () => {
  it('gives the local time zero-padded, to the second', () => {
    fc.assert(
      fc.property(
        fc.date({
          min: new Date(1000, 0, 1),
          max: new Date(9999, 11, 31),
          noInvalidDate: true,
        }),
        (date) => {
          vi.setSystemTime(date);
          const t = timestampParts();
          expect(
            [t.month, t.day, t.hours, t.minutes, t.seconds].map(Number),
          ).toEqual([
            date.getMonth() + 1,
            date.getDate(),
            date.getHours(),
            date.getMinutes(),
            date.getSeconds(),
          ]);
          expect(t.year).toBe(date.getFullYear());
          for (const part of [t.month, t.day, t.hours, t.minutes, t.seconds]) {
            expect(part).toMatch(/^\d\d$/);
          }
          expect(minuteStamp()).toMatch(/^\d{4}-\d\d-\d\d_\d{4}$/);
          const second = `${t.year}-${t.month}-${t.day}_${t.hours}${t.minutes}${t.seconds}`;
          expect(uniqueStamp()).toMatch(new RegExp(`^${second}_[0-9a-f]{4}$`));
        },
      ),
    );
  });
});
