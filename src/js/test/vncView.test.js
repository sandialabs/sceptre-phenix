// The console page /vnc (T37, M1 and M2a): the panes and layout from the URL,
// bad values included, the banners of each pane, the connection indicator,
// the pane the top bar acts on, and the first-time chord toast.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises } from '@vue/test-utils';
import { nextTick } from 'vue';

const { axios, modules, notify, rbac, resetMocks, store, ws } =
  await vi.hoisted(() => import('./helpers/mocks.js'));
const { FakeRfb, rfbs, resetRfbs } = await vi.hoisted(
  () => import('./helpers/fakeRfb.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/errorNotif', modules.errorNotif);
vi.mock('@/vnc/loadRfb.js', () => ({ loadRfb: async () => FakeRfb }));
// a job that is done as soon as it starts
const jobs = vi.hoisted(() => ({ done: null }));
vi.mock('@/utils/jobs.js', () => ({
  useJobStore: () => ({
    start: async (request) => (await request).data,
    whenDone: async () => jobs.done,
  }),
}));

import { routeTitle } from '@/utils/pageTitle.js';
import VncView from '@/views/VncView.vue';
import { CHORD_HINT } from '@/vnc/chord.js';
import { sharedLiveTilePool } from '@/vnc/liveTilePool.js';
import { CONSOLE_CHANNEL } from '@/vnc/openConsole.js';
import { disk, httpError, vm, wsMsg } from './helpers/fixtures.js';
import {
  buttons,
  confirmDialog,
  dialogs,
  mountPage,
  textOf,
  toasts,
} from './helpers/mount.js';
import { allowOnly } from './helpers/permissions.js';
import { blockedStorage } from './helpers/storage.js';

const session = (exp, name, banners = {}) => ({
  experiment: exp,
  vm: name,
  websocket: `experiments/${exp}/vms/${name}/vnc/ws`,
  banners: {
    top: {
      lines: [`EXP: ${exp} - VM: ${name}`],
      backgroundColor: 'white',
      textColor: 'black',
    },
    bottom: {
      lines: [`EXP: ${exp} - VM: ${name}`],
      backgroundColor: 'white',
      textColor: 'black',
    },
    ...banners,
  },
  display: { vga: 'std', remoteResize: false },
  clipboard: { mode: 'pasteAsKeys' },
});

// what the server answers, by request path
let answers;

function answer(url) {
  const found = answers[url];
  if (found instanceof Error) return Promise.reject(found);
  if (found) return Promise.resolve({ data: found });
  const parts = /^experiments\/([^/]+)\/vms\/([^/]+)(\/vnc)?$/.exec(url);
  if (!parts) return Promise.reject(httpError(404, { message: 'not found' }));
  const [, exp, name, isVnc] = parts.map((p) => p && decodeURIComponent(p));
  return Promise.resolve({
    data: isVnc ? session(exp, name) : vm({ name }),
  });
}

async function openPage(route) {
  const wrapper = await mountPage(VncView, { route, attachTo: document.body });
  await flushPromises();
  return wrapper;
}

const consoleLabels = (wrapper) =>
  wrapper.findAll('.vnc-console').map((c) => c.attributes('aria-label'));

const query = (wrapper) => wrapper.router.currentRoute.value.query;

// a button of the top bar by its name, or else a row of its Actions menu
const control = (wrapper, name) => {
  const found = wrapper.find(`[aria-label="${name}"]`);
  if (found.exists()) return found;
  return wrapper
    .findAll('.vnc-actions-menu .dropdown-item')
    .find((row) => textOf(row.find('.vnc-actions-label')) === name);
};

beforeEach(() => {
  resetMocks();
  resetRfbs();
  sessionStorage.clear();
  answers = {};
  axios.get.mockImplementation((url) => answer(url));
  // browsers let a page set window.opener; happy-dom has only a getter
  Object.defineProperty(window, 'opener', {
    value: null,
    writable: true,
    configurable: true,
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  delete window.opener;
});

describe('the panes and layout from the URL', () => {
  it('shows the VMs in the order of the vm parameters', async () => {
    const wrapper = await openPage(
      '/vnc?vm=demo/kali&vm=demo/web&vm=train/plc-1&layout=main-2',
    );
    expect(consoleLabels(wrapper)).toEqual([
      'Console of VM kali in experiment demo',
      'Console of VM web in experiment demo',
      'Console of VM plc-1 in experiment train',
    ]);
    expect(wrapper.find('.vnc-panes').classes()).toContain(
      'vnc-layout--main-2',
    );
    expect(rfbs.map((rfb) => rfb.url)).toEqual([
      'ws://phenix.test/api/v1/experiments/demo/vms/kali/vnc/ws?token=t',
      'ws://phenix.test/api/v1/experiments/demo/vms/web/vnc/ws?token=t',
      'ws://phenix.test/api/v1/experiments/train/vms/plc-1/vnc/ws?token=t',
    ]);
  });

  it.each([
    [
      'drops a bad VM value and a VM named twice',
      '/vnc?vm=demo&vm=demo/web&vm=a/b/c&vm=&vm=demo/web&vm=demo/db',
      ['web', 'db'],
      'cols',
      { vm: ['demo/web', 'demo/db'] },
    ],
    [
      'uses the default layout for an unknown one',
      '/vnc?vm=demo/web&vm=demo/db&layout=bogus',
      ['web', 'db'],
      'cols',
      { vm: ['demo/web', 'demo/db'] },
    ],
    [
      'uses the default layout for one with too few panes',
      '/vnc?vm=demo/web&vm=demo/db&vm=demo/hmi&layout=rows',
      ['web', 'db', 'hmi'],
      'main-2',
      { vm: ['demo/web', 'demo/db', 'demo/hmi'] },
    ],
    [
      'shows the first four of five VMs',
      '/vnc?vm=d/a&vm=d/b&vm=d/c&vm=d/e&vm=d/f',
      ['a', 'b', 'c', 'e'],
      'grid',
      { vm: ['d/a', 'd/b', 'd/c', 'd/e'] },
    ],
    [
      'keeps a layout with more panes than VMs',
      '/vnc?vm=demo/web&layout=grid',
      ['web'],
      'grid',
      { vm: 'demo/web', layout: 'grid' },
    ],
  ])('%s, and corrects the URL', async (_, route, names, layout, fixed) => {
    const wrapper = await openPage(route);
    expect(consoleLabels(wrapper)).toEqual(
      names.map((n) => expect.stringContaining(`VM ${n} in`)),
    );
    expect(wrapper.find('.vnc-panes').classes()).toContain(
      `vnc-layout--${layout}`,
    );
    expect(query(wrapper)).toEqual(fixed);
  });

  it('offers to add a VM in each empty pane of the layout', async () => {
    const wrapper = await openPage('/vnc?vm=demo/web&layout=grid');
    expect(wrapper.findAll('.vnc-empty-pane')).toHaveLength(3);
  });

  it('says that it has no VM to show without a good vm parameter', async () => {
    const wrapper = await openPage('/vnc?vm=bad');
    expect(consoleLabels(wrapper)).toEqual([]);
    expect(textOf(wrapper.find('.vnc-no-vm'))).toContain('No VM to show');
    expect(axios.get).not.toHaveBeenCalled();
  });

  it('changes the layout and keeps it in the URL', async () => {
    const wrapper = await openPage('/vnc?vm=demo/web&vm=demo/db');
    await wrapper.find('[aria-label="Two rows"]').trigger('click');
    await flushPromises();
    expect(query(wrapper)).toEqual({
      vm: ['demo/web', 'demo/db'],
      layout: 'rows',
    });
    expect(wrapper.find('.vnc-panes').classes()).toContain('vnc-layout--rows');
    // the panes keep their connections
    expect(rfbs).toHaveLength(2);
  });

  it('titles the page with its VMs', async () => {
    await openPage('/vnc?vm=demo/web&vm=demo/db');
    expect(document.title).toBe('demo/web, demo/db - VNC console - phēnix');
  });

  it('keeps the VM names in the title after a layout change', async () => {
    const wrapper = await openPage('/vnc?vm=demo/web&vm=demo/db');
    // as router.js does after each navigation
    wrapper.router.afterEach(() => {
      document.title = routeTitle({ meta: { title: 'VNC console' } });
    });
    await wrapper.find('[aria-label="Two rows"]').trigger('click');
    await flushPromises();
    expect(query(wrapper).layout).toBe('rows');
    expect(document.title).toBe('demo/web, demo/db - VNC console - phēnix');
  });
});

describe('single view and split view', () => {
  it('has no pane header in single view; the top bar names the VM', async () => {
    const wrapper = await openPage('/vnc?vm=demo/web');
    expect(wrapper.find('.vnc-pane-header').exists()).toBe(false);
    expect(textOf(wrapper.find('.vnc-target'))).toBe('demo / web');
    expect(wrapper.find('.vnc-toolbar .tag').exists()).toBe(false);
    expect(wrapper.find('.vnc-details-link').attributes('href')).toBe(
      '/experiment/demo?vm=web',
    );
  });

  it('gives each pane of split view a header with its name, state and number', async () => {
    answers['experiments/demo/vms/plc-1'] = vm({
      name: 'plc-1',
      state: 'PAUSED',
    });
    const wrapper = await openPage('/vnc?vm=demo/web&vm=demo/plc-1');
    const headers = wrapper
      .findAll('.vnc-pane-header')
      .map((header) =>
        ['strong', '.tag', '.vnc-pane-number']
          .map((part) => textOf(header.find(part)))
          .join(' '),
      );
    expect(headers).toEqual(['web Running 1', 'plc-1 Paused 2']);
    expect(wrapper.find('.vnc-toolbar .tag').exists()).toBe(false);
  });

  it('links each pane header to the VM details on the experiment page', async () => {
    const wrapper = await openPage('/vnc?vm=demo/web&vm=train/plc-1');
    const links = wrapper.findAll('.vnc-pane-header .vnc-pane-details');
    expect(
      links.map((link) => [
        link.attributes('href'),
        link.attributes('target'),
        link.attributes('aria-label'),
        link.find('[data-icon]').attributes('data-icon'),
      ]),
    ).toEqual([
      ['/experiment/demo?vm=web', '_blank', 'Details of VM web', 'circle-info'],
      [
        '/experiment/train?vm=plc-1',
        '_blank',
        'Details of VM plc-1',
        'circle-info',
      ],
    ]);
    expect(wrapper.find('.vnc-toolbar .vnc-details-link').exists()).toBe(false);
  });

  it('links to the experiment of the active pane', async () => {
    const wrapper = await openPage('/vnc?vm=demo/web&vm=train/plc-1');
    const link = () => wrapper.find('.vnc-experiment-link');
    expect(link().attributes('href')).toBe('/experiment/demo');
    await wrapper.findAll('.vnc-pane')[1].trigger('pointerdown');
    expect(link().attributes('href')).toBe('/experiment/train');
  });

  it('acts on the pane that a click gives input to', async () => {
    const wrapper = await openPage('/vnc?vm=demo/kali&vm=demo/web');
    expect(textOf(wrapper.find('.vnc-target'))).toBe('acting on demo / kali');
    const panes = wrapper.findAll('.vnc-pane');
    expect(panes[0].classes()).toContain('is-active');
    await panes[1].trigger('pointerdown');
    expect(textOf(wrapper.find('.vnc-target'))).toBe('acting on demo / web');
    expect(panes[1].classes()).toContain('is-active');
    expect(panes[0].classes()).not.toContain('is-active');
  });

  it('moves input to pane 2 with the chord Ctrl+] then 2', async () => {
    const wrapper = await openPage('/vnc?vm=demo/kali&vm=demo/web');
    rfbs[0].focus();
    const pane = wrapper.findAll('.vnc-pane')[0].element;
    for (const init of [
      { code: 'BracketRight', key: ']', ctrlKey: true },
      { code: 'Digit2', key: '2' },
    ]) {
      rfbs[0].canvas.dispatchEvent(
        new KeyboardEvent('keydown', { ...init, bubbles: true }),
      );
    }
    await nextTick();
    expect(pane.contains(document.activeElement)).toBe(false);
    expect(document.activeElement).toBe(rfbs[1].canvas);
    expect(textOf(wrapper.find('.vnc-target'))).toBe('acting on demo / web');
  });

  it('leaves the console for the top bar with the chord Ctrl+] then Tab', async () => {
    const wrapper = await openPage('/vnc?vm=demo/web');
    rfbs[0].focus();
    for (const init of [
      { code: 'BracketRight', key: ']', ctrlKey: true },
      { code: 'Tab', key: 'Tab' },
    ]) {
      rfbs[0].canvas.dispatchEvent(
        new KeyboardEvent('keydown', { ...init, bubbles: true }),
      );
    }
    await nextTick();
    expect(
      wrapper.find('.vnc-toolbar').element.contains(document.activeElement),
    ).toBe(true);
  });
});

describe('the banners', () => {
  it('draws the banners of each pane above and below its screen', async () => {
    const wrapper = await openPage('/vnc?vm=demo/kali&vm=demo/web');
    const panes = wrapper.findAll('.vnc-pane');
    expect(panes.map((p) => p.findAll('.vnc-banner').map(textOf))).toEqual([
      ['EXP: demo - VM: kali', 'EXP: demo - VM: kali'],
      ['EXP: demo - VM: web', 'EXP: demo - VM: web'],
    ]);
  });

  it('draws the server text of a banner as text', async () => {
    const markup = '<img src=x onerror="alert(1)"> <b>TOP SECRET</b>';
    answers['experiments/demo/vms/web/vnc'] = session('demo', 'web', {
      top: { lines: [markup], backgroundColor: 'red', textColor: 'white' },
      bottom: { lines: [], backgroundColor: 'red', textColor: 'white' },
    });
    const wrapper = await openPage('/vnc?vm=demo/web');
    const banners = wrapper.findAll('.vnc-banner');
    expect(banners).toHaveLength(1);
    expect(banners[0].find('.vnc-banner-line').element.textContent).toBe(
      markup,
    );
    expect(wrapper.find('.vnc-pane img').exists()).toBe(false);
    expect(wrapper.find('.vnc-pane b').exists()).toBe(false);
  });
});

describe('the connection indicator', () => {
  const indicator = (wrapper) => textOf(wrapper.find('.vnc-status'));
  const live = (wrapper) => textOf(wrapper.find('[aria-live="polite"]'));

  it('shows Connecting, then Connected, and announces the change', async () => {
    const wrapper = await openPage('/vnc?vm=demo/web');
    expect(indicator(wrapper)).toBe('Connecting');
    rfbs[0].connect();
    await nextTick();
    expect(indicator(wrapper)).toBe('Connected');
    expect(live(wrapper)).toBe('web: Connected');
  });

  it('shows "VM is not running" for a stopped VM, in the bar and the pane', async () => {
    answers['experiments/demo/vms/web/vnc'] = httpError(409, {
      message: 'VM is not running',
    });
    const wrapper = await openPage('/vnc?vm=demo/web');
    expect(indicator(wrapper)).toBe('VM is not running');
    expect(textOf(wrapper.find('.vnc-pane-status'))).toContain(
      'VM is not running',
    );
    expect(rfbs).toHaveLength(0);
  });

  it('follows the pane that the top bar acts on', async () => {
    const wrapper = await openPage('/vnc?vm=demo/kali&vm=demo/web');
    rfbs[1].connect();
    await nextTick();
    expect(indicator(wrapper)).toBe('Connecting');
    await wrapper.findAll('.vnc-pane')[1].trigger('pointerdown');
    expect(indicator(wrapper)).toBe('Connected');
  });

  it('shows a VM pause from the event stream, and stays connected', async () => {
    const wrapper = await openPage('/vnc?vm=demo/web');
    rfbs[0].connect();
    const paused = vm({ state: 'PAUSED' });
    ws.handlers.forEach((h) =>
      h(wsMsg('experiment/vm', 'demo/web', 'stop', paused)),
    );
    await nextTick();
    expect(textOf(wrapper.find('.vnc-target'))).toBe('demo / web');
    expect(indicator(wrapper)).toBe('Connected');
    expect(textOf(wrapper.find('.vnc-paused'))).toContain('Paused');
  });
});

describe('the chord hint', () => {
  it('shows the chord toast the first time a pane takes keyboard input', async () => {
    const wrapper = await openPage('/vnc?vm=demo/kali&vm=demo/web');
    expect(toasts()).toEqual([]);
    rfbs[0].focus();
    await nextTick();
    expect(toasts().map((t) => t.text)).toEqual([CHORD_HINT]);
    rfbs[1].focus();
    rfbs[0].focus();
    await nextTick();
    expect(toasts()).toHaveLength(1);
    expect(wrapper.find('.vnc-pane.is-active .vnc-keyboard-tag').exists()).toBe(
      true,
    );
  });

  it('does not show it again in the same browser session', async () => {
    const first = await openPage('/vnc?vm=demo/web');
    rfbs[0].focus();
    await nextTick();
    first.unmount();
    document.body.innerHTML = '';
    await openPage('/vnc?vm=demo/web');
    rfbs.at(-1).focus();
    await nextTick();
    expect(toasts()).toEqual([]);
  });

  it('shows the KEYBOARD tag in the top bar of single view while the pane has input', async () => {
    const wrapper = await openPage('/vnc?vm=demo/web');
    expect(wrapper.find('.vnc-toolbar .vnc-keyboard-tag').exists()).toBe(false);
    rfbs[0].focus();
    await nextTick();
    expect(wrapper.find('.vnc-toolbar .vnc-keyboard-tag').exists()).toBe(true);
    rfbs[0].blur();
    await nextTick();
    expect(wrapper.find('.vnc-toolbar .vnc-keyboard-tag').exists()).toBe(false);
  });
});

describe('the console tab', () => {
  it('lets go of the window that opened it', async () => {
    window.opener = {};
    await openPage('/vnc?vm=demo/web');
    expect(window.opener).toBeNull();
  });

  it('answers a query for its VMs in any order, and stops when it closes', async () => {
    const channels = [];
    vi.stubGlobal(
      'BroadcastChannel',
      class {
        constructor(name) {
          this.name = name;
          this.sent = [];
          this.closed = false;
          channels.push(this);
        }
        postMessage(data) {
          this.sent.push(data);
        }
        close() {
          this.closed = true;
        }
      },
    );
    const wrapper = await openPage('/vnc?vm=demo/web&vm=demo/db');
    const channel = channels.find((c) => c.name === CONSOLE_CHANNEL);
    channel.onmessage({
      data: { type: 'query', id: 'q1', vms: ['demo/db', 'demo/web'] },
    });
    channel.onmessage({ data: { type: 'query', id: 'q2', vms: ['demo/db'] } });
    expect(channel.sent).toEqual([{ type: 'here', id: 'q1' }]);
    wrapper.unmount();
    expect(channel.closed).toBe(true);
  });
});

describe('what the top bar does', () => {
  const button = (wrapper, text) =>
    wrapper
      .findAll('button')
      .find((b) => textOf(b) === text || b.attributes('aria-label') === text);
  const panel = (wrapper, name) => wrapper.find(`#vnc-panel-${name}`);

  async function connected(route) {
    const wrapper = await openPage(route);
    rfbs.forEach((rfb) => rfb.connect());
    await nextTick();
    return wrapper;
  }

  it('pauses the VM of the active pane in a background job', async () => {
    jobs.done = {
      id: 'j1',
      status: 'succeeded',
      items: [
        {
          name: 'web',
          status: 'succeeded',
          result: vm({ state: 'PAUSED', running: false }),
        },
      ],
    };
    axios.post.mockResolvedValue({ data: { id: 'j1', status: 'running' } });
    const wrapper = await connected('/vnc?vm=demo/kali&vm=demo/web');
    await wrapper.findAll('.vnc-pane')[1].trigger('pointerdown');
    await wrapper.find('[aria-label="Pause"]').trigger('click');
    await flushPromises();
    await confirmDialog();
    expect(axios.post).toHaveBeenCalledWith('experiments/demo/vms/actions', {
      action: 'pause',
      vms: ['web'],
    });
    expect(textOf(wrapper.find('.vnc-target'))).toBe('acting on demo / web');
    expect(wrapper.find('[aria-label="Resume"]').exists()).toBe(true);
  });

  const SNAPSHOT_REQUEST = [
    'experiments/demo/vms/web/snapshots',
    { filename: expect.stringMatching(/^\d{4}-\d{2}-\d{2}_\d{4}$/) },
    { timeout: 0 },
  ];
  const jobRequest = (action) => [
    'experiments/demo/vms/actions',
    { action, vms: ['web'] },
  ];

  it.each([
    [
      'Pause',
      'Pause the web VM',
      'This will pause the web VM. Pause stops a VM and keeps its memory until you resume it.',
      'is-success',
      jobRequest('pause'),
    ],
    [
      'Restart',
      'Restart the web VM',
      'This will restart the web VM. Restart resets a VM at once without a shutdown; unsaved data is lost.',
      'is-success',
      jobRequest('restart'),
    ],
    [
      'Shut down',
      'Shut Down the web VM',
      'This will shut down the web VM. Shut down tells the operating system of a VM to power off; you can start the VM again.',
      'is-danger',
      jobRequest('shutdown'),
    ],
    [
      'Snapshot',
      'Create a Snapshot of the web VM',
      'This will create a snapshot of the web VM. A snapshot saves the disk and memory of a VM, which you can restore later.',
      'is-success',
      SNAPSHOT_REQUEST,
    ],
  ])(
    'asks before %s, sends nothing on Cancel, and sends it once on confirm',
    async (label, title, message, type, request) => {
      jobs.done = { id: 'j1', status: 'succeeded', items: [] };
      axios.post.mockResolvedValue({ data: { id: 'j1', status: 'running' } });
      const wrapper = await connected('/vnc?vm=demo/web');
      await control(wrapper, label).trigger('click');
      await flushPromises();
      expect(dialogs()).toHaveLength(1);
      expect(textOf(dialogs()[0].querySelector('.modal-card-title'))).toBe(
        title,
      );
      expect(textOf(dialogs()[0].querySelector('.media-content'))).toBe(
        message,
      );
      expect(
        dialogs()[0].querySelector('footer .button:last-child').classList,
      ).toContain(type);
      expect(axios.post).not.toHaveBeenCalled();

      dialogs()[0].querySelector('footer .button:first-child').click();
      await flushPromises();
      expect(axios.post).not.toHaveBeenCalled();

      await control(wrapper, label).trigger('click');
      await flushPromises();
      await confirmDialog();
      expect(axios.post).toHaveBeenCalledExactlyOnceWith(...request);
    },
  );

  it.each([
    ['the top bar', (wrapper) => wrapper.find('[aria-label="Resume"]')],
    [
      'the paused pane',
      (wrapper) =>
        wrapper
          .findAll('.vnc-paused button')
          .find((b) => textOf(b) === 'Resume'),
    ],
  ])('resumes a paused VM from %s without asking', async (_, resume) => {
    answers['experiments/demo/vms/web'] = vm({
      name: 'web',
      state: 'PAUSED',
      running: false,
    });
    jobs.done = { id: 'j1', status: 'succeeded', items: [] };
    axios.post.mockResolvedValue({ data: { id: 'j1', status: 'running' } });
    const wrapper = await connected('/vnc?vm=demo/web');
    await resume(wrapper).trigger('click');
    await flushPromises();
    expect(dialogs()).toEqual([]);
    expect(axios.post).toHaveBeenCalledExactlyOnceWith(...jobRequest('start'));
  });

  it('leaves fullscreen before it asks to confirm a snapshot', async () => {
    const wrapper = await connected('/vnc?vm=demo/web');
    const exit = vi.fn(async () => {
      expect(dialogs()).toHaveLength(0);
    });
    Object.defineProperty(document, 'fullscreenElement', {
      value: wrapper.element,
      configurable: true,
    });
    Object.defineProperty(document, 'exitFullscreen', {
      value: exit,
      configurable: true,
    });
    try {
      await control(wrapper, 'Snapshot').trigger('click');
      await flushPromises();
      expect(exit).toHaveBeenCalledOnce();
      expect(dialogs()).toHaveLength(1);
    } finally {
      delete document.fullscreenElement;
      delete document.exitFullscreen;
    }
  });

  it('saves a screenshot of the active pane and names its file in a toast', async () => {
    axios.post.mockResolvedValue({
      data: { path: 'screenshots/web-20261107-153012.png' },
    });
    const wrapper = await connected('/vnc?vm=demo/kali&vm=demo/web');
    await wrapper.findAll('.vnc-pane')[1].trigger('pointerdown');
    await wrapper.find('[aria-label="Screenshot"]').trigger('click');
    await flushPromises();
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/demo/vms/web/screenshots',
    );
    expect(toasts()).toContainEqual({
      text: 'Saved a screenshot of the web VM as screenshots/web-20261107-153012.png',
      type: 'is-success',
    });
  });

  it('shows the error of a screenshot that fails, and spins until then', async () => {
    let fail;
    axios.post.mockReturnValue(
      new Promise((_, reject) => {
        fail = reject;
      }),
    );
    const wrapper = await connected('/vnc?vm=demo/web');
    const shot = () => wrapper.find('[aria-label="Screenshot"]');
    await shot().trigger('click');
    await nextTick();
    expect(shot().attributes('aria-busy')).toBe('true');
    fail(httpError(409, { message: 'VM web is not running' }));
    await flushPromises();
    expect(notify.error).toHaveBeenCalledOnce();
    expect(shot().attributes('aria-busy')).toBeUndefined();
    expect(toasts()).toEqual([]);
  });

  it('creates a port forward from the dialog of the Port forwards panel', async () => {
    answers['experiments/demo/vms/web/forwards'] = { listeners: [] };
    axios.post.mockResolvedValue({ data: {} });
    const wrapper = await connected('/vnc?vm=demo/web');
    await wrapper.find('[aria-label="Port forwards"]').trigger('click');
    await flushPromises();
    await button(panel(wrapper, 'forwards'), 'Add port forward').trigger(
      'click',
    );
    await flushPromises();
    await wrapper.find('#port-forward-src-port').setValue('8080');
    await wrapper.find('#port-forward-dst-port').setValue('80');
    await button(wrapper, 'Create').trigger('click');
    await flushPromises();
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/demo/vms/web/forwards',
      null,
      { params: { src: '8080', host: '127.0.0.1', dst: '80' } },
    );
    expect(toasts().map((t) => t.text)).toContain(
      'Port forward 8080 → 127.0.0.1:80 created for the web VM.',
    );
    // the panel opens again with a fresh list after the add
    expect(panel(wrapper, 'forwards').isVisible()).toBe(true);
    expect(
      axios.get.mock.calls.filter(
        ([url]) => url === 'experiments/demo/vms/web/forwards',
      ),
    ).toHaveLength(2);
  });

  it('sends no memory snapshot when its dialog is cancelled', async () => {
    const wrapper = await connected('/vnc?vm=demo/web');
    await control(wrapper, 'Memory snapshot').trigger('click');
    await flushPromises();
    const modal = wrapper.find('.modal.is-active');
    expect(textOf(modal.find('.modal-card-body p'))).toBe(
      'A memory snapshot saves the memory of a VM to an ELF file in the experiment files.',
    );
    await button(wrapper, 'Cancel').trigger('click');
    await flushPromises();
    expect(axios.post).not.toHaveBeenCalled();
  });

  it('creates a memory snapshot with the name from its dialog', async () => {
    axios.post.mockResolvedValue({ data: {} });
    const wrapper = await connected('/vnc?vm=demo/web');
    await control(wrapper, 'Memory snapshot').trigger('click');
    await flushPromises();
    const name = wrapper.find(
      '[aria-label="Memory snapshot filename for VM web"]',
    );
    expect(name.element.value).toMatch(/^web_\d{8}_\d{6}$/);
    await name.setValue('web-dump');
    await button(wrapper, 'Create').trigger('click');
    await flushPromises();
    expect(axios.post).toHaveBeenCalledExactlyOnceWith(
      'experiments/demo/vms/web/memorySnapshot',
      { filename: 'web-dump.elf' },
      { timeout: 0 },
    );
    expect(toasts().map((t) => t.text)).toContain(
      'Created a memory snapshot of the web VM',
    );
  });

  it('inserts an ISO image picked in the CD-ROM dialog', async () => {
    answers.disks = {
      disks: [disk('/images/tools.iso', 'tools.iso', { kind: 'ISO' })],
    };
    axios.post.mockResolvedValue({ data: {} });
    const wrapper = await connected('/vnc?vm=demo/web');
    await control(wrapper, 'Insert CD-ROM').trigger('click');
    await flushPromises();
    expect(axios.get).toHaveBeenCalledWith('disks', {
      params: { diskType: 'ISO' },
    });
    await wrapper.find('#optical-disc-iso').setValue('/images/tools.iso');
    await wrapper.find('.optical-disc-insert').trigger('click');
    await flushPromises();
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/demo/vms/web/cdrom',
      null,
      { params: { isoPath: '/images/tools.iso' } },
    );
  });

  it('exits full screen from the full screen button while the page is full screen', async () => {
    const wrapper = await connected('/vnc?vm=demo/web');
    const exit = vi.fn(async () => {});
    Object.defineProperty(document, 'fullscreenElement', {
      value: wrapper.element,
      configurable: true,
    });
    Object.defineProperty(document, 'exitFullscreen', {
      value: exit,
      configurable: true,
    });
    try {
      document.dispatchEvent(new Event('fullscreenchange'));
      await nextTick();
      const toggle = wrapper.find('.vnc-fullscreen-button');
      expect(toggle.attributes('aria-label')).toBe('Exit full screen');
      await toggle.trigger('click');
      await flushPromises();
      expect(exit).toHaveBeenCalledOnce();
    } finally {
      delete document.fullscreenElement;
      delete document.exitFullscreen;
    }
    document.dispatchEvent(new Event('fullscreenchange'));
    await nextTick();
    expect(
      wrapper.find('.vnc-fullscreen-button').attributes('aria-label'),
    ).toBe('Full screen');
  });

  it('greys out each VM action that the role may not take on the VM of the active pane', async () => {
    rbac.allowed = allowOnly(['vms/restart', 'update', 'demo/web']);
    const wrapper = await openPage('/vnc?vm=demo/kali&vm=demo/web');
    const enabled = () =>
      wrapper
        .findAll('.vnc-vm-actions button')
        .filter((b) => !b.element.closest('.vnc-panel'))
        .filter((b) => b.attributes('aria-disabled') !== 'true')
        .map((b) => b.attributes('aria-label'));
    expect(enabled()).toEqual([]);
    expect(
      wrapper.find('[aria-label="Restart"]').attributes('aria-disabled'),
    ).toBe('true');
    await wrapper.findAll('.vnc-pane')[1].trigger('pointerdown');
    expect(enabled()).toEqual(['Restart']);
  });

  it('sends the keys of the Keys menu to the console of the active pane', async () => {
    const wrapper = await connected('/vnc?vm=demo/kali&vm=demo/web');
    await wrapper.findAll('.vnc-pane')[1].trigger('pointerdown');
    await button(wrapper, 'Keys').trigger('click');
    await button(panel(wrapper, 'keys'), 'Ctrl+Alt+Del').trigger('click');
    await button(panel(wrapper, 'keys'), 'Esc').trigger('click');
    await panel(wrapper, 'keys')
      .find('[aria-label="Ctrl+Alt+F2"]')
      .trigger('click');
    expect(rfbs[0].ctrlAltDel).toBe(0);
    expect(rfbs[1].ctrlAltDel).toBe(1);
    expect(rfbs[1].keys.map(([, code, down]) => `${code}:${down}`)).toEqual([
      'Escape:true',
      'Escape:false',
      'ControlLeft:true',
      'AltLeft:true',
      'F2:true',
      'F2:false',
      'AltLeft:false',
      'ControlLeft:false',
    ]);
  });

  it('gives focus back to the panel button on Esc when the console is not connected', async () => {
    const wrapper = await openPage('/vnc?vm=demo/web');
    const keys = button(wrapper, 'Keys');
    await keys.trigger('click');
    await panel(wrapper, 'keys').trigger('keydown', { key: 'Escape' });
    await nextTick();
    expect(panel(wrapper, 'keys').isVisible()).toBe(false);
    expect(document.activeElement).toBe(keys.element);
  });

  it('gives focus to the console on Esc in a panel when it is connected', async () => {
    const wrapper = await connected('/vnc?vm=demo/web');
    await button(wrapper, 'Keys').trigger('click');
    await panel(wrapper, 'keys').trigger('keydown', { key: 'Escape' });
    await nextTick();
    expect(
      wrapper.find('.vnc-console').element.contains(document.activeElement),
    ).toBe(true);
  });

  it('holds Ctrl until it is pressed again or the keys are released', async () => {
    const wrapper = await connected('/vnc?vm=demo/web');
    await button(wrapper, 'Keys').trigger('click');
    const ctrl = button(panel(wrapper, 'keys'), 'Ctrl');
    await ctrl.trigger('click');
    expect(ctrl.attributes('aria-pressed')).toBe('true');
    await button(panel(wrapper, 'keys'), 'Release held keys').trigger('click');
    expect(ctrl.attributes('aria-pressed')).toBe('false');
    expect(rfbs[0].keys[0]).toEqual([0xffe3, 'ControlLeft', true]);
    expect(rfbs[0].keys.slice(1).every(([, , down]) => down === false)).toBe(
      true,
    );
    expect(rfbs[0].keys).toHaveLength(9);
  });

  it.each([
    [
      'the connection drops and the user reconnects',
      async (wrapper) => {
        rfbs[0].drop();
        await flushPromises();
        await wrapper.find('.vnc-pane-status button').trigger('click');
      },
    ],
    [
      'the VM is redeployed',
      async () =>
        ws.handlers.forEach((h) =>
          h(wsMsg('experiment/vm', 'demo/web', 'redeployed', vm())),
        ),
    ],
  ])('forgets the held keys when %s', async (_, end) => {
    const wrapper = await connected('/vnc?vm=demo/web');
    await button(wrapper, 'Keys').trigger('click');
    const ctrl = () => button(panel(wrapper, 'keys'), 'Ctrl');
    await ctrl().trigger('click');
    expect(ctrl().attributes('aria-pressed')).toBe('true');
    await end(wrapper);
    await flushPromises();
    expect(ctrl().attributes('aria-pressed')).toBe('false');
    const rfb = rfbs.at(-1);
    rfb.connect();
    await nextTick();
    expect(rfb).not.toBe(rfbs[0]);
    // one click holds Ctrl again on the new connection
    await ctrl().trigger('click');
    expect(ctrl().attributes('aria-pressed')).toBe('true');
    expect(rfb.keys).toEqual([[0xffe3, 'ControlLeft', true]]);
  });

  it('sends no keys while the console is not connected', async () => {
    const wrapper = await openPage('/vnc?vm=demo/web');
    await button(wrapper, 'Keys').trigger('click');
    expect(textOf(panel(wrapper, 'keys'))).toContain(
      'The console is not connected',
    );
    await button(panel(wrapper, 'keys'), 'Esc').trigger('click');
    expect(rfbs[0].keys).toEqual([]);
  });

  it('checks a paste in paste-as-keys mode before it types it', async () => {
    const wrapper = await connected('/vnc?vm=demo/web');
    await button(wrapper, 'Clipboard').trigger('click');
    const clip = panel(wrapper, 'clipboard');
    expect(textOf(clip.find('.vnc-panel-title'))).toBe(
      'Clipboard · paste as keys',
    );
    await clip.find('textarea').setValue('echo “café”');
    await button(clip, 'Send').trigger('click');
    expect(rfbs[0].pasted).toEqual([]);
    expect(clip.findAll('.vnc-panel-warning').map(textOf)).toEqual([
      '2 characters will be typed as ?: “ ”',
      '1 character will be skipped: é',
    ]);
    await button(clip, 'Type anyway · about 1 s').trigger('click');
    expect(rfbs[0].pasted).toEqual(['echo “café”']);
  });

  it('sends a paste at once in bidirectional mode', async () => {
    answers['experiments/demo/vms/web/vnc'] = {
      ...session('demo', 'web'),
      clipboard: { mode: 'bidirectional' },
    };
    const wrapper = await connected('/vnc?vm=demo/web');
    await button(wrapper, 'Clipboard').trigger('click');
    const clip = panel(wrapper, 'clipboard');
    await clip.find('textarea').setValue('café');
    await button(clip, 'Send').trigger('click');
    expect(rfbs[0].pasted).toEqual(['café']);
  });

  it('has Send, Paste from clipboard and Clear on one line', async () => {
    const wrapper = await connected('/vnc?vm=demo/web');
    await button(wrapper, 'Clipboard').trigger('click');
    const clip = panel(wrapper, 'clipboard');
    const row = clip.find('.vnc-panel-row--single');
    expect(row.findAll('button').map(textOf)).toEqual([
      'Send',
      'Paste from clipboard',
      'Clear',
    ]);
    expect(clip.classes()).toContain('vnc-panel--wide');
  });

  it('empties the paste box with Clear, and drops a paste that waits', async () => {
    const wrapper = await connected('/vnc?vm=demo/web');
    await button(wrapper, 'Clipboard').trigger('click');
    const clip = panel(wrapper, 'clipboard');
    await clip.find('textarea').setValue('echo “café”');
    await button(clip, 'Send').trigger('click');
    expect(clip.findAll('.vnc-panel-warning')).toHaveLength(2);
    await button(clip, 'Clear').trigger('click');
    expect(clip.find('textarea').element.value).toBe('');
    expect(clip.findAll('.vnc-panel-warning')).toHaveLength(0);
    expect(button(clip, 'Send').attributes('aria-disabled')).toBe('true');
    expect(rfbs[0].pasted).toEqual([]);
  });

  it('keeps the keys of each Keys row on one line', async () => {
    const wrapper = await connected('/vnc?vm=demo/web');
    await button(wrapper, 'Keys').trigger('click');
    const keys = panel(wrapper, 'keys');
    expect(keys.classes()).toContain('vnc-panel--wide');
    const rows = keys.findAll('.vnc-panel-row--single');
    expect(rows.map((row) => row.findAll('button').length)).toEqual([4, 12]);
  });

  it('explains View only in a tooltip', async () => {
    const wrapper = await connected('/vnc?vm=demo/web');
    await button(wrapper, 'View').trigger('click');
    const view = panel(wrapper, 'view');
    const tip = view
      .findAllComponents({ name: 'BTooltip' })
      .find((t) => t.find('input[type="checkbox"]').exists());
    expect(textOf(tip.vm.$refs.content)).toBe(
      'Show the VM screen, but send no keyboard or mouse input to the VM',
    );
  });

  it('keeps the View settings for the user, and gives them to every pane', async () => {
    const wrapper = await connected('/vnc?vm=demo/kali&vm=demo/web');
    await button(wrapper, 'View').trigger('click');
    const view = panel(wrapper, 'view');
    await button(view, 'Actual').trigger('click');
    await view.findAll('input[type="range"]')[0].setValue('3');
    expect(rfbs.map((rfb) => [rfb.scaleViewport, rfb.qualityLevel])).toEqual([
      [false, 3],
      [false, 3],
    ]);
    expect(
      JSON.parse(localStorage.getItem('phenix.vnc.settings.alice')),
    ).toEqual({ scaling: 'actual', quality: 3, compression: 2 });
    expect(buttons(view)).toContain('Resize VM (disabled)');
    expect(textOf(view)).toContain('Resize VM needs vga virtio.');
  });

  it('turns on View only for the active pane alone', async () => {
    const wrapper = await connected('/vnc?vm=demo/kali&vm=demo/web');
    await button(wrapper, 'View').trigger('click');
    await panel(wrapper, 'view').find('input[type="checkbox"]').setValue(true);
    expect(rfbs.map((rfb) => rfb.viewOnly)).toEqual([true, false]);
  });

  it('adds a running VM in a new pane and keeps it in the URL', async () => {
    answers.vms = {
      vms: [
        { experiment: 'demo', name: 'web', running: true },
        { experiment: 'demo', name: 'db', running: true },
        { experiment: 'demo', name: 'off', running: false },
      ],
    };
    const wrapper = await openPage('/vnc?vm=demo/web');
    await button(wrapper, 'Add VM').trigger('click');
    await flushPromises();
    const add = panel(wrapper, 'add');
    await add.find('input').trigger('focus');
    await flushPromises();
    const items = add.findAll('.dropdown-item');
    expect(items.map(textOf)).toEqual(['demo/db']);
    await items[0].trigger('click');
    await flushPromises();
    expect(query(wrapper)).toEqual({ vm: ['demo/web', 'demo/db'] });
    expect(consoleLabels(wrapper)).toHaveLength(2);
    expect(textOf(wrapper.find('.vnc-target'))).toBe('acting on demo / db');
  });

  it('swaps and closes panes from the pane menu, and keeps the connections', async () => {
    const wrapper = await openPage('/vnc?vm=demo/kali&vm=demo/web&vm=demo/db');
    const menu = (i) => wrapper.findAll('.vnc-pane-menu-button')[i];
    await menu(0).trigger('click');
    await button(wrapper, 'Swap with pane 3').trigger('click');
    await flushPromises();
    expect(query(wrapper).vm).toEqual(['demo/db', 'demo/web', 'demo/kali']);
    await menu(1).trigger('click');
    await button(wrapper, 'Close').trigger('click');
    await flushPromises();
    expect(query(wrapper)).toEqual({
      vm: ['demo/db', 'demo/kali'],
      layout: 'main-2',
    });
    expect(rfbs).toHaveLength(3);
    expect(rfbs.map((rfb) => rfb.disconnected)).toEqual([false, true, false]);
  });

  it.each([
    ['two columns', '/vnc?vm=demo/kali&vm=demo/web'],
    ['the grid', '/vnc?vm=demo/kali&vm=demo/web&layout=grid'],
  ])(
    'shows the last VM left in one pane when a pane of %s closes',
    async (_, route) => {
      const wrapper = await openPage(route);
      await wrapper.findAll('.vnc-pane-menu-button')[0].trigger('click');
      await button(wrapper, 'Close').trigger('click');
      await flushPromises();
      expect(query(wrapper)).toEqual({ vm: ['demo/web'] });
      expect(wrapper.find('.vnc-panes').classes()).toContain(
        'vnc-layout--single',
      );
      expect(wrapper.find('.vnc-empty-pane').exists()).toBe(false);
      // single view: the top bar names the VM, and the pane has no header
      expect(textOf(wrapper.find('.vnc-target'))).toBe('demo / web');
      expect(wrapper.find('.vnc-pane-header').exists()).toBe(false);
    },
  );

  it('keeps the layout when more than one VM is left', async () => {
    const wrapper = await openPage(
      '/vnc?vm=demo/kali&vm=demo/web&vm=demo/db&layout=grid',
    );
    await wrapper.findAll('.vnc-pane-menu-button')[0].trigger('click');
    await button(wrapper, 'Close').trigger('click');
    await flushPromises();
    expect(query(wrapper)).toEqual({
      vm: ['demo/web', 'demo/db'],
      layout: 'grid',
    });
  });
});

describe('the new VM actions of the top bar', () => {
  const menu = (wrapper, name) => control(wrapper, name);
  const buttonIn = (node, text) =>
    node.findAll('button').find((b) => textOf(b) === text);

  async function connected(route) {
    const wrapper = await openPage(route);
    rfbs.forEach((rfb) => rfb.connect());
    await nextTick();
    return wrapper;
  }

  // a job that ends when the test calls the function it returns
  function pendingJob(items) {
    let finish;
    jobs.done = new Promise((resolve) => {
      finish = () => resolve({ id: 'j1', status: 'succeeded', items });
    });
    axios.post.mockResolvedValue({ data: { id: 'j1', status: 'running' } });
    return () => finish();
  }

  it('kills the VM of the active pane after it asks, and says so in the pane with a link to the experiment', async () => {
    jobs.done = {
      id: 'j1',
      status: 'succeeded',
      items: [{ name: 'web', status: 'succeeded' }],
    };
    axios.post.mockResolvedValue({ data: { id: 'j1', status: 'running' } });
    const wrapper = await connected('/vnc?vm=demo/kali&vm=demo/web');
    await wrapper.findAll('.vnc-pane')[1].trigger('pointerdown');
    await menu(wrapper, 'Kill').trigger('click');
    await flushPromises();
    expect(textOf(dialogs()[0].querySelector('.modal-card-title'))).toBe(
      'Kill the web VM',
    );
    await confirmDialog();
    expect(axios.post).toHaveBeenCalledExactlyOnceWith(
      'experiments/demo/vms/actions',
      { action: 'kill', vms: ['web'] },
    );
    const panes = wrapper.findAll('.vnc-pane');
    expect(panes[0].find('.vnc-pane-killed').exists()).toBe(false);
    const killed = panes[1].find('.vnc-pane-killed');
    expect(textOf(killed.find('.vnc-pane-status-label'))).toBe('VM killed');
    expect(killed.find('a').attributes('href')).toBe('/experiment/demo');
    expect(wrapper.router.currentRoute.value.name).toBe('vnc');
  });

  it('shows that Reset disk restarts the VM, then connects again', async () => {
    const finish = pendingJob([
      { name: 'web', status: 'succeeded', result: vm() },
    ]);
    const wrapper = await connected('/vnc?vm=demo/web');
    await menu(wrapper, 'Reset disk').trigger('click');
    await flushPromises();
    await confirmDialog();
    expect(axios.post).toHaveBeenCalledExactlyOnceWith(
      'experiments/demo/vms/actions',
      { action: 'resetDisk', vms: ['web'] },
    );
    // the VM restarts: the console drops and waits to connect again
    rfbs[0].drop();
    await flushPromises();
    const restarting = wrapper.find('.vnc-pane-restarting');
    expect(textOf(restarting.find('.vnc-pane-status-label'))).toBe(
      'Restarting the VM',
    );
    expect(textOf(restarting)).toContain('Reset disk restarts the VM.');

    finish();
    await flushPromises();
    expect(rfbs).toHaveLength(2);
    rfbs[1].connect();
    await nextTick();
    expect(wrapper.find('.vnc-pane-restarting').exists()).toBe(false);
    expect(textOf(wrapper.find('.vnc-status'))).toBe('Connected');
  });

  it('redeploys the VM with the settings of its dialog, shows that it restarts, then connects again', async () => {
    let answer;
    axios.post.mockImplementation((url) =>
      url.endsWith('/redeploy')
        ? new Promise((resolve) => (answer = resolve))
        : Promise.resolve({ data: {} }),
    );
    const wrapper = await connected('/vnc?vm=demo/web');
    await menu(wrapper, 'Redeploy').trigger('click');
    await flushPromises();
    const modal = wrapper.find('.modal.is-active');
    expect(textOf(modal.find('.modal-card-title'))).toBe('Redeploy the VM');
    await buttonIn(modal, 'Redeploy').trigger('click');
    await flushPromises();
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/demo/vms/web/redeploy',
      { cpus: 1, ram: 2048 },
    );
    ws.handlers.forEach((h) =>
      h(wsMsg('experiment/vm', 'demo/web', 'redeploying', null)),
    );
    await nextTick();
    expect(wrapper.find('.vnc-pane-restarting').exists()).toBe(true);
    expect(rfbs[0].disconnected).toBe(true);

    answer({ data: vm() });
    ws.handlers.forEach((h) =>
      h(wsMsg('experiment/vm', 'demo/web', 'redeployed', vm())),
    );
    await flushPromises();
    rfbs.at(-1).connect();
    await nextTick();
    expect(wrapper.find('.vnc-pane-restarting').exists()).toBe(false);
    expect(toasts().map((t) => t.text)).toContain('Redeployed the web VM');
  });

  it('opens the mount browser for the VM', async () => {
    store.features = ['vm-mount'];
    axios.post.mockResolvedValue({ data: {} });
    const wrapper = await connected('/vnc?vm=demo/web');
    await wrapper.find('[aria-label="Mount disk"]').trigger('click');
    await flushPromises();
    expect(
      [...document.querySelectorAll('.modal-card-title')].map(textOf),
    ).toContain('web Mount');
    expect(axios.post).toHaveBeenCalledWith('experiments/demo/vms/web/mount');
  });

  it('opens the label editor for the VM', async () => {
    answers['experiments/demo/vms/web'] = vm({ tags: { role: 'server' } });
    const wrapper = await connected('/vnc?vm=demo/web');
    await menu(wrapper, 'Edit labels').trigger('click');
    await flushPromises();
    expect(
      [...document.querySelectorAll('.modal-card-title')].map(textOf),
    ).toContain('web Labels');
  });

  it('edits the annotations of the VM in a dialog and saves them', async () => {
    answers['experiments/demo/vms/web'] = vm({
      annotations: { 'custom/key': 'one' },
    });
    axios.patch.mockResolvedValue({ data: vm() });
    const wrapper = await connected('/vnc?vm=demo/web');
    await menu(wrapper, 'Edit annotations').trigger('click');
    await flushPromises();
    const card = document.querySelector('.vm-annotations-modal');
    expect(textOf(card.querySelector('.modal-card-title'))).toBe(
      'Annotations of the web VM',
    );
    card.querySelector('.vm-annotations-save').click();
    await flushPromises();
    expect(axios.patch).toHaveBeenCalledExactlyOnceWith(
      'experiments/demo/vms/web',
      { annotations: { 'custom/key': 'one' } },
    );
    expect(document.querySelector('.vm-annotations-modal')).toBeNull();
  });

  it('starts a capture on interface 0 in the format picked, then reads the captures again', async () => {
    store.features = ['minimega-captures'];
    answers['experiments/demo/vms/web/captures'] = { captures: [] };
    axios.post.mockResolvedValue({ data: {} });
    const wrapper = await connected('/vnc?vm=demo/web');
    const reads = () =>
      axios.get.mock.calls.filter(
        ([url]) => url === 'experiments/demo/vms/web/captures',
      ).length;
    const before = reads();
    await wrapper.find('[aria-label="Capture interface 0"]').trigger('click');
    await flushPromises();
    const modal = wrapper.find('.capture-format-card');
    expect(textOf(modal.find('.modal-card-title'))).toBe(
      'Start capture on interface 0 of web',
    );
    await modal.trigger('submit');
    await flushPromises();
    expect(axios.post).toHaveBeenCalledExactlyOnceWith(
      'experiments/demo/vms/web/captures',
      {
        interface: 0,
        filename: expect.stringMatching(/^web_0_.*\.pcapng$/),
        format: 'pcapng',
      },
    );
    expect(reads()).toBeGreaterThan(before + 1);
  });

  it('stops every capture of the VM while one runs, and shows the capture button again', async () => {
    answers['experiments/demo/vms/web'] = vm({ captures: [{ interface: 1 }] });
    axios.delete.mockResolvedValue({ data: {} });
    const wrapper = await connected('/vnc?vm=demo/web');
    // GET captures, after the stop, finds none
    answers['experiments/demo/vms/web/captures'] = { captures: [] };
    await wrapper.find('[aria-label="Stop captures"]').trigger('click');
    await flushPromises();
    expect(axios.delete).toHaveBeenCalledExactlyOnceWith(
      'experiments/demo/vms/web/captures',
    );
    expect(wrapper.find('[aria-label="Capture interface 0"]').exists()).toBe(
      true,
    );
  });

  it('reads the captures again when one starts or stops elsewhere', async () => {
    const wrapper = await connected('/vnc?vm=demo/web');
    answers['experiments/demo/vms/web/captures'] = {
      captures: [{ interface: 0 }],
    };
    ws.handlers.forEach((h) =>
      h(wsMsg('experiment/vm/capture', 'demo/web', 'start', {})),
    );
    await flushPromises();
    expect(wrapper.find('[aria-label="Stop captures"]').exists()).toBe(true);
  });

  it('acts on the VM of the active pane in split view', async () => {
    store.features = ['vm-mount'];
    axios.post.mockResolvedValue({ data: {} });
    const wrapper = await connected('/vnc?vm=demo/kali&vm=demo/web');
    await wrapper.findAll('.vnc-pane')[1].trigger('pointerdown');
    await wrapper.find('[aria-label="Mount disk"]').trigger('click');
    await flushPromises();
    expect(axios.post).toHaveBeenCalledWith('experiments/demo/vms/web/mount');
  });

  it('lists the keyboard shortcuts in the View panel', async () => {
    const wrapper = await connected('/vnc?vm=demo/web');
    const view = wrapper.find('#vnc-panel-view');
    expect(
      view
        .findAll('.vnc-shortcuts dt')
        .map((dt, i) => [
          textOf(dt),
          textOf(view.findAll('.vnc-shortcuts dd')[i]),
        ]),
    ).toEqual([
      ['1–4', 'Select that pane'],
      ['F', 'Exit full screen'],
      ['T', 'Show or hide the toolbar'],
      ['Tab', 'Leave the console'],
      ['Esc', 'Cancel the shortcut'],
    ]);
    expect(textOf(view)).toContain('Press Ctrl+], then:');
  });
});

describe('the pane menu', () => {
  const menuButton = (wrapper) => wrapper.find('.vnc-pane-menu-button');
  const menuList = (wrapper) => wrapper.find('.vnc-pane-menu-list');

  it('closes on Esc and gives focus back to its button', async () => {
    const wrapper = await openPage('/vnc?vm=demo/kali&vm=demo/web');
    await menuButton(wrapper).trigger('click');
    expect(menuList(wrapper).exists()).toBe(true);
    await menuList(wrapper).find('button').trigger('keydown', {
      key: 'Escape',
    });
    expect(menuList(wrapper).exists()).toBe(false);
    expect(document.activeElement).toBe(menuButton(wrapper).element);
  });

  it('closes on a click outside it, and not on a click inside it', async () => {
    const wrapper = await openPage('/vnc?vm=demo/kali&vm=demo/web');
    await menuButton(wrapper).trigger('click');
    menuList(wrapper)
      .element.querySelector('button')
      .dispatchEvent(new Event('pointerdown', { bubbles: true }));
    await nextTick();
    expect(menuList(wrapper).exists()).toBe(true);
    document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    await nextTick();
    expect(menuList(wrapper).exists()).toBe(false);
  });

  it('closes when focus moves out of it', async () => {
    const wrapper = await openPage('/vnc?vm=demo/kali&vm=demo/web');
    await menuButton(wrapper).trigger('click');
    const item = menuList(wrapper).find('button');
    await item.trigger('focusout', {
      relatedTarget: menuButton(wrapper).element,
    });
    expect(menuList(wrapper).exists()).toBe(true);
    await item.trigger('focusout', {
      relatedTarget: wrapper.find('.vnc-panel-button').element,
    });
    expect(menuList(wrapper).exists()).toBe(false);
  });
});

describe('one connection for each VM in a tab', () => {
  it('takes the tile of a VM in this tab off while a pane shows the VM', async () => {
    // a new pool with no observer counts each tile as on screen
    vi.stubGlobal('IntersectionObserver', undefined);
    sharedLiveTilePool().dispose();
    const pool = sharedLiveTilePool();
    const web = pool.register('demo/web');
    const db = pool.register('demo/db');
    const wrapper = await openPage('/vnc?vm=demo/web');
    expect([web.live.value, db.live.value]).toEqual([false, true]);
    wrapper.unmount();
    expect(web.live.value).toBe(true);
    pool.dispose();
  });
});

describe('hiding the top bar', () => {
  const HIDDEN_KEY = 'phenix.vnc.toolbarHidden';
  const bar = (wrapper) => wrapper.find('.vnc-toolbar');
  const barShown = (wrapper) => bar(wrapper).element.style.display !== 'none';
  const showButton = (wrapper) => wrapper.find('[aria-label="Show toolbar"]');
  const hideButton = (wrapper) => wrapper.find('[aria-label="Hide toolbar"]');
  const chord = async (code, key) => {
    for (const init of [
      { code: 'BracketRight', key: ']', ctrlKey: true },
      { code, key },
    ]) {
      rfbs[0].canvas.dispatchEvent(
        new KeyboardEvent('keydown', { ...init, bubbles: true }),
      );
    }
    await nextTick();
    await nextTick();
  };

  it('hides the bar from Hide toolbar, keeps the choice, and gives focus to Show toolbar', async () => {
    const wrapper = await openPage('/vnc?vm=demo/kali&vm=demo/web');
    expect(barShown(wrapper)).toBe(true);
    expect(showButton(wrapper).exists()).toBe(false);

    await hideButton(wrapper).trigger('click');
    await nextTick();
    expect(barShown(wrapper)).toBe(false);
    expect(localStorage.getItem(HIDDEN_KEY)).toBe('true');
    expect(document.activeElement).toBe(showButton(wrapper).element);
    const tip = wrapper
      .findAllComponents({ name: 'BTooltip' })
      .find((t) => t.find('[aria-label="Show toolbar"]').exists());
    expect(textOf(tip.vm.$refs.content)).toBe(
      'Show the toolbar (Ctrl+] then T)',
    );
    expect(tip.props('position')).toBe('is-right');
    // the panes get the space, and keep their connections
    expect(wrapper.classes()).toContain('is-toolbar-hidden');
    expect(wrapper.findAll('.vnc-pane')).toHaveLength(2);
    expect(rfbs).toHaveLength(2);
    expect(rfbs.some((rfb) => rfb.disconnected)).toBe(false);
  });

  it('shows the bar again from Show toolbar, and gives focus to Hide toolbar', async () => {
    localStorage.setItem(HIDDEN_KEY, 'true');
    const wrapper = await openPage('/vnc?vm=demo/web');
    expect(barShown(wrapper)).toBe(false);
    // a native button that Tab reaches, before the panes
    const show = showButton(wrapper);
    expect(show.element.tagName).toBe('BUTTON');
    expect(show.attributes('tabindex')).toBeUndefined();

    await show.trigger('click');
    await nextTick();
    expect(barShown(wrapper)).toBe(true);
    expect(showButton(wrapper).exists()).toBe(false);
    expect(localStorage.getItem(HIDDEN_KEY)).toBe('false');
    expect(document.activeElement).toBe(hideButton(wrapper).element);
  });

  it('hides and shows the bar with the chord Ctrl+] then T, and the VM gets no key', async () => {
    const wrapper = await openPage('/vnc?vm=demo/web');
    rfbs[0].connect();
    rfbs[0].focus();
    await chord('KeyT', 't');
    expect(barShown(wrapper)).toBe(false);
    expect(localStorage.getItem(HIDDEN_KEY)).toBe('true');
    expect(document.activeElement).toBe(rfbs[0].canvas);

    await chord('KeyT', 't');
    expect(barShown(wrapper)).toBe(true);
    expect(localStorage.getItem(HIDDEN_KEY)).toBe('false');
    expect(rfbs[0].keys).toEqual([]);
  });

  it('leaves the console for Show toolbar with the chord Ctrl+] then Tab while the bar is hidden', async () => {
    localStorage.setItem(HIDDEN_KEY, 'true');
    const wrapper = await openPage('/vnc?vm=demo/web');
    rfbs[0].focus();
    await chord('Tab', 'Tab');
    expect(document.activeElement).toBe(showButton(wrapper).element);
  });

  it('shows the bar and its Add VM panel from Add VM in an empty pane', async () => {
    localStorage.setItem(HIDDEN_KEY, 'true');
    const wrapper = await openPage('/vnc?vm=demo/web&layout=cols');
    await wrapper.find('.vnc-empty-pane button').trigger('click');
    await flushPromises();
    expect(barShown(wrapper)).toBe(true);
    expect(wrapper.find('#vnc-panel-add').isVisible()).toBe(true);
  });

  it('hides the bar for the visit when the browser blocks storage', async () => {
    vi.stubGlobal('localStorage', blockedStorage());
    const wrapper = await openPage('/vnc?vm=demo/web');
    expect(barShown(wrapper)).toBe(true);
    await hideButton(wrapper).trigger('click');
    await nextTick();
    expect(barShown(wrapper)).toBe(false);
  });
});
