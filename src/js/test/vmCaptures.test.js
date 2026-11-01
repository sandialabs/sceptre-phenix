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
import { useVmCaptures } from '@/components/experiment/useVmCaptures.js';
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
