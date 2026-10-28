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
} from '@/components/experiment/timestamp.js';
import { useVmCaptures } from '@/components/experiment/useVmCaptures.js';
import {
  confirmDialog,
  dialogs,
  mountComponent,
  textOf,
  toasts,
} from './helpers/mount.js';

const url = 'experiments/demo/vms/web/captures';

const web = {
  name: 'web',
  networks: ['DMZ (149)', 'disconnected', 'MGMT (151)'],
  captures: [],
};

// the composable as the demo experiment's page uses it, with spies for the
// page's waiting overlay and its fan-out of requests
async function useCaptures() {
  const setWaiting = vi.fn();
  const fanOut = vi.fn(async (names, _, send) =>
    Promise.allSettled(names.map(send)),
  );
  let captures;
  const Host = defineComponent({
    setup() {
      captures = useVmCaptures({ setWaiting, fanOut });
      return () => h('div');
    },
  });
  await mountComponent(Host, { route: '/experiment/demo' });
  return { captures, setWaiting, fanOut };
}

// the newest dialog's title and confirm button text
const lastDialog = () => {
  const dialog = dialogs().at(-1);
  return {
    title: textOf(dialog.querySelector('.modal-card-title')),
    confirmText: textOf(dialog.querySelector('footer .button:last-child')),
  };
};

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
  it('marks the interface pending until the server answers', async () => {
    const { captures } = await useCaptures();
    const release = listCaptures([]);

    captures.handlePcap(web, 2);
    // a second press while waiting asks nothing more
    captures.handlePcap(web, 2);
    expect(captures.capturePending.value).toEqual({ 'web/2': true });
    expect(captures.pendingCaptureIfaces('web')).toEqual([2]);
    expect(captures.pendingCaptureIfaces('db')).toEqual([]);
    expect(axios.get).toHaveBeenCalledTimes(1);

    release();
    await flushPromises();
    expect(captures.capturePending.value).toEqual({});
    expect(lastDialog()).toEqual({
      title: 'Start a Packet Capture',
      confirmText: 'Start',
    });
  });

  it('starts a capture named for the VM, interface and minute', async () => {
    const { captures, setWaiting } = await useCaptures();
    listCaptures([])();
    axios.post.mockResolvedValue({});

    captures.handlePcap(web, 2);
    await flushPromises();
    await confirmDialog();

    expect(axios.post).toHaveBeenCalledWith(url, {
      interface: 2,
      filename: 'web_2_2024-01-02_0304.pcap',
    });
    expect(setWaiting.mock.calls).toEqual([[true], [false]]);
  });

  it('reports a failed start and clears the waiting overlay', async () => {
    const { captures, setWaiting } = await useCaptures();
    listCaptures([])();
    const err = new Error('no');
    axios.post.mockRejectedValue(err);

    captures.handlePcap(web, 0);
    await flushPromises();
    await confirmDialog();

    expect(notify.error).toHaveBeenCalledWith(err);
    expect(setWaiting.mock.calls).toEqual([[true], [false]]);
  });

  it('refuses a disconnected interface', async () => {
    const { captures } = await useCaptures();
    listCaptures([])();

    captures.handlePcap(web, 1);
    await flushPromises();

    expect(dialogs()).toEqual([]);
    expect(notify.show).toHaveBeenCalledWith(
      'Cannot capture traffic on a disconnected interface.',
    );
    expect(toasts()).toEqual([]);
  });

  it("stops the interface's capture", async () => {
    const { captures } = await useCaptures();
    listCaptures([{ interface: 0 }])();
    axios.delete.mockResolvedValue({});

    captures.handlePcap(web, 0);
    await flushPromises();
    expect(lastDialog()).toEqual({
      title: 'Stop Packet Capture',
      confirmText: 'Stop',
    });
    await confirmDialog();

    expect(axios.delete).toHaveBeenCalledWith(url, { params: { iface: 0 } });
  });

  it.each([
    ['stops every capture', 'last', {}],
    ["stops only the interface's capture", 'first', { iface: 0 }],
  ])(
    'with several captures running, %s from the %s button',
    async (_, which, params) => {
      const { captures } = await useCaptures();
      listCaptures([{ interface: 0 }, { interface: 2 }])();
      axios.delete.mockResolvedValue({});

      captures.handlePcap(web, 0);
      await flushPromises();
      expect(lastDialog()).toEqual({
        title: 'Stop Packet Capture',
        confirmText: 'Stop All Captures',
      });
      dialogs().at(-1).querySelector(`footer .button:${which}-child`).click();
      await flushPromises();

      expect(axios.delete).toHaveBeenCalledWith(url, { params });
    },
  );

  it('reports a failed listing and clears the pending mark', async () => {
    const { captures } = await useCaptures();
    const err = new Error('no');
    axios.get.mockRejectedValue(err);

    captures.handlePcap(web, 0);
    await flushPromises();

    expect(notify.error).toHaveBeenCalledWith(err);
    expect(captures.capturePending.value).toEqual({});
    expect(dialogs()).toEqual([]);
  });
});

describe('every interface', () => {
  it('starts captures on the connected interfaces not yet captured', async () => {
    const { captures, fanOut } = await useCaptures();
    listCaptures([{ interface: 2 }])();
    axios.post.mockResolvedValue({});

    await captures.captureAllInterfaces(web);
    expect(captures.captureAllPending.value).toEqual({});
    expect(lastDialog()).toEqual({
      title: 'Start a Packet Capture',
      confirmText: 'Start',
    });

    const done = Promise.withResolvers();
    fanOut.mockImplementation(async (names, _, send) => {
      await Promise.allSettled(names.map(send));
      await done.promise;
    });
    await confirmDialog();
    expect(captures.captureAllPending.value).toEqual({ web: true });
    // a second press while starting asks nothing more
    await captures.captureAllInterfaces(web);
    expect(axios.get).toHaveBeenCalledTimes(1);

    done.resolve();
    await flushPromises();
    expect(captures.captureAllPending.value).toEqual({});
    expect(fanOut).toHaveBeenCalledWith(
      ['interface 0'],
      'Started packet captures of web on',
      expect.any(Function),
      'interface',
    );
    expect(axios.post).toHaveBeenCalledWith(url, {
      interface: 0,
      filename: 'web_0_2024-01-02_0304.pcap',
    });
  });

  it('says so when every connected interface is captured', async () => {
    const { captures } = await useCaptures();
    listCaptures([{ interface: 0 }, { interface: 2 }])();

    await captures.captureAllInterfaces(web);

    expect(dialogs()).toEqual([]);
    expect(toasts()).toEqual([
      {
        text: 'Every connected interface of the web VM is already being captured.',
        type: 'is-info',
      },
    ]);
  });

  it('stops every capture, pending until the server answers', async () => {
    const { captures } = await useCaptures();
    const vm = { ...web, captures: [{ interface: 0 }, { interface: 2 }] };
    const request = Promise.withResolvers();
    axios.delete.mockReturnValue(request.promise);

    captures.stopAllCaptures(vm);
    await flushPromises();
    expect(lastDialog()).toEqual({
      title: 'Stop Packet Captures',
      confirmText: 'Stop All',
    });
    await confirmDialog();
    expect(captures.captureAllPending.value).toEqual({ web: true });
    expect(axios.delete).toHaveBeenCalledWith(url);

    // a second press while stopping opens no dialog
    captures.stopAllCaptures(vm);
    await flushPromises();
    expect(dialogs()).toEqual([]);

    request.reject(new Error('no'));
    await flushPromises();
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
        },
      ),
    );
  });
});
