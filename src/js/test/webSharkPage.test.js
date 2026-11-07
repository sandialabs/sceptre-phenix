// The WebShark page: pickers, server requests, frame, empty captures and URL.
import fc from 'fast-check';
import { flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, notify, rbac, resetMocks, store } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
// reactive, as the features can arrive after the page opens
vi.mock('@/store.js', async () => {
  const [{ reactive }, mocks] = await Promise.all([
    import('vue'),
    import('./helpers/mocks.js'),
  ]);
  return { usePhenixStore: () => reactive(mocks.store) };
});

import { reactive } from 'vue';

import { cachePage, clearPageCache } from '@/utils/pageCache.js';
import { experimentKey } from '@/utils/pageData.js';
import WebShark from '@/views/WebShark.vue';
import {
  byLabel,
  dialogs,
  mountPage,
  textOf,
  toasts,
} from './helpers/mount.js';
import { allowAll, denyOnly } from './helpers/permissions.js';

// what the mocked server answers each GET with, by URL
const SERVER = {
  'experiments?vms=false': {
    experiments: [
      { name: 'exp2', running: false },
      { name: 'exp1', running: true },
    ],
  },
  'experiments/exp1/captures': {
    captures: [
      {
        vm: 'web',
        interface: 1,
        filepath: '/phenix/images/exp1/files/w1.pcap',
      },
      { vm: 'db', interface: 0, filepath: '/phenix/images/exp1/files/d0.pcap' },
    ],
  },
  'experiments/exp1/files': {
    files: [
      { name: 'a.pcap', path: 'caps/a.pcap', size: 2048 },
      { name: 'notes.txt', path: 'notes.txt', size: 10 },
      { name: 'B.PCAPNG.gz', path: 'B.PCAPNG.gz', size: 10 },
      { name: 'run.pcap', path: 'run.pcap', isDir: true },
      { name: 'w1.pcap', path: 'w1.pcap', size: 100 },
    ],
    total: 5,
  },
};

// the server's answer for a stream token
const token = {
  id: '3f9a0c51d2e47b86',
  token: 'pcs_abc',
  urls: { stream: 'webshark/streams/pcs_abc' },
};

// /webshark/watch: each one the page opens, which tells it a capture's size
class FakeEventSource {
  static sources = [];
  // the size the next watch reports when it opens; undefined reports nothing
  static size = 2048;

  constructor(url) {
    this.url = url;
    this.closed = false;
    this.listeners = {};
    FakeEventSource.sources.push(this);
    const size = FakeEventSource.size;
    if (size !== undefined) {
      queueMicrotask(() =>
        this.emit('capture-changed', { size, kind: 'init' }),
      );
    }
  }

  addEventListener(type, fn) {
    (this.listeners[type] ??= []).push(fn);
  }

  emit(type, data) {
    if (this.closed) return;
    const event = {
      data: data === undefined ? undefined : JSON.stringify(data),
    };
    (this.listeners[type] ?? []).forEach((fn) => fn(event));
  }

  close() {
    this.closed = true;
  }
}

const watches = () => FakeEventSource.sources;

beforeEach(() => {
  FakeEventSource.sources = [];
  FakeEventSource.size = 2048;
  vi.stubGlobal('EventSource', FakeEventSource);
  resetMocks({
    store: {
      features: ['webshark'],
      featuresLoaded: true,
      token: 'jwt.token',
    },
  });
  axios.get.mockImplementation(async (url) => {
    if (!(url in SERVER)) throw new Error(`unexpected GET ${url}`);
    return { data: SERVER[url] };
  });
  axios.post.mockImplementation(async (_, body) => ({
    data: {
      capture: body.path
        ? `exp1/${body.path}`
        : `exp1/${body.vm}-${body.interface}`,
      live: !body.path,
      title: 'a capture',
    },
  }));
});

afterEach(() => {
  clearPageCache();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

// the page at the URL with this query
const opened = (query = {}) =>
  mountPage(WebShark, { route: { name: 'webshark', query } });

const experimentPicker = (page) => byLabel(page, 'Experiment');
const capturePicker = (page) => byLabel(page, 'Capture');

// the options a picker offers, by group, leaving out its placeholder
const options = (picker, group) =>
  picker
    .findAll(group ? `optgroup[label="${group}"] option` : 'option')
    .filter((o) => !o.element.hidden)
    .map((o) => ({ value: o.element.value, text: textOf(o) }));
const values = (picker, group) => options(picker, group).map((o) => o.value);
const texts = (picker, group) => options(picker, group).map((o) => o.text);

const LIVE = 'Live captures';
const FILES = 'Saved files';

const query = (page) => page.router.currentRoute.value.query;
const frame = (page) => page.find('iframe');
const emptyText = (page) => textOf(page.find('.webshark-empty'));
const copyButton = (page) => page.find('.webshark-copy');

// picks this capture, and waits for the page to follow its URL
async function pick(page, key) {
  await capturePicker(page).setValue(key);
  await flushPromises();
}

// The VM and interface the Wireshark command streams: copies it, and reads
// the stream token request.
async function streamed(page) {
  vi.stubGlobal('navigator', {});
  axios.post.mockResolvedValue({ data: token });
  await copyButton(page).trigger('click');
  await flushPromises();
  const [url, { vm, interface: iface }] = axios.post.mock.lastCall;
  expect(url).toBe('experiments/exp1/captureTokens');
  return { vm, iface };
}

describe('the pickers', () => {
  it('list the experiments by name, then their captures', async () => {
    const page = await opened({ exp: 'exp1' });
    expect(texts(experimentPicker(page))).toEqual(['exp1 (running)', 'exp2']);
    expect(texts(capturePicker(page), LIVE)).toEqual(['db · IF0', 'web · IF1']);
    expect(values(capturePicker(page), FILES)).toEqual([
      'file:caps/a.pcap',
      'file:B.PCAPNG.gz',
      'file:w1.pcap',
    ]);
    expect(texts(capturePicker(page), FILES)).toEqual([
      'caps/a.pcap (2.0 KiB)',
      'B.PCAPNG.gz (10 B)',
      'w1.pcap (100 B)',
    ]);
    expect(axios.get).toHaveBeenCalledWith('experiments/exp1/files', {
      params: { sortCol: 'date', sortDir: 'desc' },
    });
    expect(
      byLabel(page, 'Reload the captures').attributes(),
    ).not.toHaveProperty('disabled');
    expect(axios.post).not.toHaveBeenCalled();
  });

  it("name live captures after the interface's network when known", async () => {
    cachePage(experimentKey('exp1'), {
      vms: [{ name: 'web', networks: ['DMZ (149)', 'MGMT (151)'] }],
    });
    const page = await opened({ exp: 'exp1' });
    expect(texts(capturePicker(page), LIVE)).toEqual([
      'db · IF0',
      'web · IF1 (mgmt)',
    ]);
  });

  it('leave out what the role may not open', async () => {
    rbac.allowed = denyOnly(
      ['experiments/files', 'get'],
      ['vms/captures', '*', 'exp1/db'],
    );
    const page = await opened({ exp: 'exp1' });
    expect(texts(capturePicker(page), LIVE)).toEqual(['web · IF1']);
    expect(options(capturePicker(page), FILES)).toEqual([]);
    expect(axios.get).not.toHaveBeenCalledWith(
      'experiments/exp1/files',
      expect.anything(),
    );
  });

  it('report a list that fails to load', async () => {
    const err = new Error('boom');
    axios.get.mockImplementation(async (url) => {
      if (url === 'experiments/exp1/captures') throw err;
      return { data: SERVER[url] };
    });
    const page = await opened({ exp: 'exp1' });
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(options(capturePicker(page), LIVE)).toEqual([]);
    expect(options(capturePicker(page), FILES)).toHaveLength(3);
  });

  // exp1's running captures, each VM interface once, and capture file paths
  const name = fc.string({ unit: 'grapheme', minLength: 1, maxLength: 8 });
  const liveCaptures = fc.uniqueArray(
    fc.record({ vm: name, interface: fc.nat(9) }),
    { selector: (c) => `${c.interface}:${c.vm}`, maxLength: 6 },
  );
  const filePaths = fc.uniqueArray(name, { maxLength: 4 });
  const liveKey = (c) => `live:${c.interface}:${c.vm}`;

  // the page on exp1, whose server lists these captures and files
  function openedWith(captures, paths) {
    const files = paths.map((path) => ({ name: 'f.pcap', path, size: 1 }));
    axios.get.mockImplementation(async (url) => ({
      data: {
        ...SERVER,
        'experiments/exp1/captures': { captures },
        'experiments/exp1/files': { files },
      }[url],
    }));
    return opened({ exp: 'exp1' });
  }

  it('list every live capture once, by VM and then interface', async () => {
    await fc.assert(
      fc.asyncProperty(liveCaptures, async (captures) => {
        const page = await openedWith(captures, []);
        const listed = values(capturePicker(page), LIVE);
        expect([...listed].sort()).toEqual(captures.map(liveKey).sort());
        const read = listed.map((key) => key.match(/^live:(\d+):(.*)$/su));
        read.slice(1).forEach(([, iface, vm], i) => {
          const [, before, beforeVm] = read[i];
          const order = beforeVm.localeCompare(vm) || before - iface;
          expect(order).toBeLessThanOrEqual(0);
        });
        page.unmount();
      }),
      { numRuns: 20 },
    );
  });

  it('put any picked capture in the URL, and open it', async () => {
    await fc.assert(
      fc.asyncProperty(
        liveCaptures,
        filePaths,
        fc.nat(),
        async (live, paths, n) => {
          fc.pre(live.length + paths.length > 0);
          const page = await openedWith(live, paths);
          const picks = [
            ...live.map((c) => [
              liveKey(c),
              { vm: c.vm, iface: String(c.interface) },
              { vm: c.vm, interface: c.interface },
            ]),
            ...paths.map((path) => [`file:${path}`, { file: path }, { path }]),
          ];
          const [key, inQuery, body] = picks[n % picks.length];
          await pick(page, key);
          expect(query(page)).toEqual({ exp: 'exp1', ...inQuery });
          expect(capturePicker(page).element.value).toBe(key);
          expect(axios.post).toHaveBeenLastCalledWith(
            'experiments/exp1/webshark',
            body,
          );
          page.unmount();
        },
      ),
      { numRuns: 20 },
    );
  });

  it('put the picked experiment in the URL and list its captures', async () => {
    const page = await opened({});
    // no experiment is picked, so there is nothing to pick a capture of
    expect(capturePicker(page).attributes()).toHaveProperty('disabled');

    await experimentPicker(page).setValue('exp1');
    await flushPromises();
    expect(query(page)).toEqual({ exp: 'exp1' });
    expect(experimentPicker(page).element.value).toBe('exp1');
    expect(capturePicker(page).attributes()).not.toHaveProperty('disabled');
    expect(options(capturePicker(page), LIVE)).toHaveLength(2);
  });
});

describe('opening a capture', () => {
  it('posts a saved file by path and frames it from /webshark/', async () => {
    const page = await opened({ exp: 'exp1' });
    const answer = axios.post.getMockImplementation();
    const held = Promise.withResolvers();
    axios.post.mockImplementationOnce(async (...args) => {
      await held.promise;
      return answer(...args);
    });

    await pick(page, 'file:caps/a.pcap');
    expect(query(page)).toEqual({ exp: 'exp1', file: 'caps/a.pcap' });
    expect(emptyText(page)).toBe('Opening caps/a.pcap…');
    held.resolve();
    await flushPromises();

    expect(axios.post).toHaveBeenCalledTimes(1);
    expect(axios.post).toHaveBeenCalledWith('experiments/exp1/webshark', {
      path: 'caps/a.pcap',
    });
    expect(page.find('.webshark-empty').exists()).toBe(false);
    expect(frame(page).attributes('src')).toBe(
      '/webshark/embed?capture=exp1%2Fcaps%2Fa.pcap&embed=1',
    );
    expect(page.find('.webshark-new-tab').attributes('href')).toBe(
      '/webshark/embed?capture=exp1%2Fcaps%2Fa.pcap',
    );
    // a finished file has nothing to stream
    expect(copyButton(page).exists()).toBe(false);
  });

  it('posts a live capture by VM and interface', async () => {
    const page = await opened({ exp: 'exp1' });
    await pick(page, 'live:1:web');
    expect(query(page)).toEqual({ exp: 'exp1', vm: 'web', iface: '1' });

    expect(axios.post).toHaveBeenCalledWith('experiments/exp1/webshark', {
      vm: 'web',
      interface: 1,
    });
    expect(textOf(page.find('.tag.is-danger'))).toBe('Live');
    expect(frame(page).attributes('src')).toBe(
      '/webshark/embed?capture=exp1%2Fweb-1&embed=1',
    );
    expect(await streamed(page)).toEqual({ vm: 'web', iface: 1 });
  });

  it('streams a saved file that is still being captured', async () => {
    axios.post.mockResolvedValue({
      data: { capture: 'w1', live: true, title: 'w1.pcap' },
    });
    const page = await opened({ exp: 'exp1', file: 'w1.pcap' });
    expect(await streamed(page)).toEqual({ vm: 'web', iface: 1 });
  });

  it('shows why a capture could not be opened', async () => {
    const err = Object.assign(new Error('404'), {
      response: { status: 404, data: 'capture not found' },
    });
    axios.post.mockRejectedValue(err);
    const page = await opened({ exp: 'exp1', vm: 'web', iface: '3' });
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(frame(page).exists()).toBe(false);
    expect(emptyText(page)).toBe(
      'Could not open web · IF3. The server did not find it, or its capture stopped.',
    );
  });

  it('refuses an answer that names no capture', async () => {
    axios.post.mockResolvedValue({ data: {} });
    const page = await opened({ exp: 'exp1', file: 'caps/a.pcap' });
    expect(notify.error).toHaveBeenCalled();
    expect(frame(page).exists()).toBe(false);
  });

  it('keeps only the answer for the last pick', async () => {
    const answers = [];
    axios.post.mockImplementation(
      (_, body) => new Promise((resolve) => answers.push({ body, resolve })),
    );
    const page = await opened({ exp: 'exp1' });
    await pick(page, 'file:caps/a.pcap');
    await pick(page, 'live:0:db');
    answers[1].resolve({ data: { capture: 'db', live: true } });
    answers[0].resolve({ data: { capture: 'a', live: false } });
    await flushPromises();
    expect(frame(page).attributes('src')).toBe(
      '/webshark/embed?capture=db&embed=1',
    );
  });
});

describe('deep links', () => {
  it('open the saved file the URL names', async () => {
    const page = await opened({ exp: 'exp1', file: 'caps/a.pcap' });
    expect(experimentPicker(page).element.value).toBe('exp1');
    expect(capturePicker(page).element.value).toBe('file:caps/a.pcap');
    expect(axios.post).toHaveBeenCalledTimes(1);
    expect(axios.post).toHaveBeenCalledWith('experiments/exp1/webshark', {
      path: 'caps/a.pcap',
    });
    expect(frame(page).attributes('src')).toBe(
      '/webshark/embed?capture=exp1%2Fcaps%2Fa.pcap&embed=1',
    );
  });

  it('open the running capture the URL names', async () => {
    const page = await opened({ exp: 'exp1', vm: 'web', iface: '1' });
    expect(capturePicker(page).element.value).toBe('live:1:web');
    expect(axios.post).toHaveBeenCalledWith('experiments/exp1/webshark', {
      vm: 'web',
      interface: 1,
    });
    // the listed entry, not a second one for the URL
    expect(values(capturePicker(page), LIVE)).toEqual([
      'live:0:db',
      'live:1:web',
    ]);
  });

  it('keep a file the list does not have pickable', async () => {
    const page = await opened({ exp: 'exp1', file: 'gone.pcap' });
    expect(values(capturePicker(page), FILES)).toContain('file:gone.pcap');
  });

  it('wait for the features before asking the server anything', async () => {
    store.features = [];
    store.featuresLoaded = false;
    await opened({ exp: 'exp1', file: 'caps/a.pcap' });
    expect(axios.get).not.toHaveBeenCalled();

    reactive(store).features = ['webshark'];
    await flushPromises();
    expect(axios.post).toHaveBeenCalledWith('experiments/exp1/webshark', {
      path: 'caps/a.pcap',
    });
  });
});

describe('Copy Wireshark command', () => {
  // the page on a running capture, its next requests answered with a token
  async function live() {
    vi.stubEnv('VITE_AUTH', 'enabled');
    const page = await opened({ exp: 'exp1', vm: 'web', iface: '1' });
    axios.post.mockReset();
    axios.post.mockResolvedValue({ data: token });
    return page;
  }

  async function copy(page) {
    await copyButton(page).trigger('click');
    await flushPromises();
  }

  // the stream token is the command's only credential
  const command =
    `curl -fsSN --max-redirs 0 -L '${location.origin}/webshark/streams/pcs_abc'` +
    ' | wireshark -k -i -';

  const minted = () =>
    expect(axios.post).toHaveBeenCalledWith('experiments/exp1/captureTokens', {
      vm: 'web',
      interface: 1,
      operations: ['stream'],
    });

  // the title and message of the dialog the page shows
  const alert = () => {
    const [dialog] = dialogs();
    return {
      title: textOf(dialog.querySelector('.modal-card-title')),
      message: dialog.querySelector('.media-content').innerHTML,
    };
  };

  it('copies a command with a stream token, as the clipboard item it awaits', async () => {
    const items = [];
    vi.stubGlobal(
      'ClipboardItem',
      class {
        constructor(data) {
          this.data = data;
        }
      },
    );
    const write = vi.fn(async (written) => items.push(...written));
    const writeText = vi.fn(async () => {});
    vi.stubGlobal('navigator', { clipboard: { write, writeText } });
    const page = await live();
    await copy(page);
    minted();
    expect(writeText).not.toHaveBeenCalled();
    expect(await (await items[0].data['text/plain']).text()).toBe(command);
    const [{ text, type }] = toasts();
    expect(type).toBe('is-success');
    expect(text).toContain('5 minutes');
    expect(text).toContain('sign out');
  });

  it('writes the command once it has it without clipboard items', async () => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    const page = await live();
    await copy(page);
    minted();
    expect(writeText).toHaveBeenCalledWith(command);
    expect(toasts()).toHaveLength(1);
    expect(dialogs()).toHaveLength(0);
  });

  it('shows the command when there is no clipboard', async () => {
    vi.stubGlobal('navigator', {});
    const page = await live();
    await copy(page);
    const { title, message } = alert();
    expect(title).toBe('Wireshark command');
    expect(message).toContain(command);
    expect(message).toContain('5 minutes');
    expect(message).not.toContain('jwt.token');
  });

  it('copies nothing and says why when the server gives no token', async () => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    const page = await live();
    const err = Object.assign(new Error('forbidden'), {
      response: { status: 403 },
    });
    axios.post.mockRejectedValue(err);
    await copy(page);
    expect(writeText).not.toHaveBeenCalled();
    // one error that stays until dismissed, saying why in plain words
    expect(notify.show).toHaveBeenCalledWith(
      'Could not copy the Wireshark command',
      'You do not have permission to open it.',
    );
    expect(notify.error).not.toHaveBeenCalled();
    expect(toasts()).toEqual([]);
    expect(dialogs()).toHaveLength(0);
  });

  it('explains a sign-in proxy, which the command must get past', async () => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    const page = await live();
    vi.stubEnv('VITE_AUTH', 'proxy');
    await copy(page);
    expect(writeText).toHaveBeenCalledWith(command);
    expect(toasts()).toEqual([]);
    const { title, message } = alert();
    expect(title).toBe('Copied the Wireshark command');
    expect(message).toContain('sign-in proxy');
    expect(message).toContain("-b 'NAME=VALUE'");
    expect(message).toContain(
      "phenix vm capture stream 'exp1' 'web' 1 | wireshark -k -i -",
    );
  });
});

describe('rendering', () => {
  it('says when WebShark is not installed', async () => {
    store.features = ['tunneler-download'];
    const page = await opened();
    expect(textOf(page)).toContain(
      'WebShark is not installed on this phēnix server.',
    );
    expect(page.find('.webshark-missing a').attributes('href')).toBe(
      'https://phenix.sceptre.dev/latest/webshark/',
    );
    expect(page.find('.webshark-bar').exists()).toBe(false);
    expect(axios.get).not.toHaveBeenCalled();
  });

  it('waits for the features before saying so', async () => {
    store.features = [];
    store.featuresLoaded = false;
    const page = await opened();
    expect(textOf(page)).not.toContain('not installed');
    expect(textOf(page)).toContain('Loading…');
  });

  it('groups live captures and saved files in the capture picker', async () => {
    const page = await opened({ exp: 'exp1' });
    expect(options(capturePicker(page), LIVE)).toContainEqual({
      value: 'live:1:web',
      text: 'web · IF1',
    });
    expect(options(capturePicker(page), FILES)).toContainEqual({
      value: 'file:caps/a.pcap',
      text: 'caps/a.pcap (2.0 KiB)',
    });
  });

  it('frames the capture from /webshark/ and links to it', async () => {
    axios.post.mockResolvedValue({
      data: { capture: 'exp1/web 1', live: true, title: 'web IF1' },
    });
    const page = await opened({ exp: 'exp1', vm: 'web', iface: '1' });
    expect(frame(page).attributes()).toMatchObject({
      src: '/webshark/embed?capture=exp1%2Fweb%201&embed=1',
      title: 'web IF1',
    });
    const newTab = page.find('.webshark-new-tab');
    expect(newTab.attributes()).toMatchObject({
      href: '/webshark/embed?capture=exp1%2Fweb%201',
      target: '_blank',
    });
    expect(textOf(newTab)).toBe('Open in new tab');
    expect(textOf(page.find('.tag.is-danger'))).toBe('Live');
    expect(textOf(copyButton(page))).toBe('Copy Wireshark command');
    expect(textOf(page)).toContain(
      'Copy a command that streams this capture into a local Wireshark',
    );
  });
});

describe('the experiment page button', () => {
  const button = (page) => page.find('.webshark-exp-link');

  it.each([
    {
      when: 'no experiment is picked',
      query: {},
      allowed: allowAll,
      label: 'Pick an experiment to open its page',
    },
    {
      when: 'the role may not get the experiment',
      query: { exp: 'exp1' },
      allowed: denyOnly(['experiments', 'get', 'exp1']),
      label: 'You do not have permission to open the exp1 experiment page',
    },
  ])(
    'is greyed out, saying why, when $when',
    async ({ query, allowed, label }) => {
      rbac.allowed = allowed;
      const page = await opened(query);
      const control = button(page);
      expect(control.element.tagName).toBe('BUTTON');
      expect(control.attributes()).toMatchObject({
        'aria-disabled': 'true',
        'aria-label': label,
      });
      expect(control.attributes('href')).toBeUndefined();
      const [tip] = page
        .findAllComponents({ name: 'BTooltip' })
        .filter((t) => t.find('.webshark-exp-link').exists());
      expect(textOf(tip.vm.$refs.content)).toBe(label);

      const before = page.router.currentRoute.value.fullPath;
      await control.trigger('click');
      await flushPromises();
      expect(page.router.currentRoute.value.fullPath).toBe(before);
    },
  );

  it("goes to the picked experiment's page", async () => {
    const page = await opened({ exp: 'exp1' });
    const link = button(page);
    expect(link.element.tagName).toBe('A');
    expect(link.attributes()).toMatchObject({
      href: '/experiment/exp1',
      'aria-label': 'Open the exp1 experiment page',
    });
    expect(link.attributes('aria-disabled')).toBeUndefined();

    await link.trigger('click');
    await flushPromises();
    const route = page.router.currentRoute.value;
    expect(route.name).toBe('experiment');
    expect(route.params).toEqual({ id: 'exp1' });
  });

  it('follows the experiment picker', async () => {
    const page = await opened({});
    expect(button(page).element.tagName).toBe('BUTTON');
    await experimentPicker(page).setValue('exp2');
    await flushPromises();
    expect(button(page).attributes('href')).toBe('/experiment/exp2');
  });

  it('sits right after the experiment picker', async () => {
    const page = await opened({ exp: 'exp1' });
    const bar = [...page.find('.webshark-bar').element.children];
    const picker = bar.findIndex((el) => el.classList.contains('webshark-exp'));
    expect(bar[picker + 1].querySelector('.webshark-exp-link')).not.toBeNull();
  });
});

describe('a capture with no packets', () => {
  const spinner = (page) => page.find('.webshark-spinner');
  const LIVE_EMPTY =
    'No packets yet. The capture has not recorded any packets. They show here when they arrive.';

  it('watches the capture the server named, under the base path', async () => {
    await opened({ exp: 'exp1', vm: 'web', iface: '1' });
    expect(watches().map((w) => w.url)).toEqual([
      '/webshark/watch?capture=exp1%2Fweb-1',
    ]);
  });

  it('says a live capture has no packets yet, then frames it when they arrive', async () => {
    FakeEventSource.size = 24;
    const page = await opened({ exp: 'exp1', vm: 'web', iface: '1' });
    expect(frame(page).exists()).toBe(false);
    expect(emptyText(page)).toBe(LIVE_EMPTY);
    expect(textOf(page)).not.toContain('Drop pcap');
    expect(spinner(page).exists()).toBe(false);
    const [watch] = watches();
    expect(watch.closed).toBe(false);

    watch.emit('capture-changed', { size: 24, kind: 'append' });
    await flushPromises();
    expect(emptyText(page)).toBe(LIVE_EMPTY);

    watch.emit('capture-changed', { size: 120, kind: 'append' });
    await flushPromises();
    expect(frame(page).attributes('src')).toBe(
      '/webshark/embed?capture=exp1%2Fweb-1&embed=1',
    );
    expect(page.find('.webshark-empty').exists()).toBe(false);
    // WebShark follows the capture from here
    expect(watch.closed).toBe(true);
  });

  it('says a saved file has no packets, and stops watching it', async () => {
    FakeEventSource.size = 0;
    const page = await opened({ exp: 'exp1', file: 'caps/a.pcap' });
    expect(frame(page).exists()).toBe(false);
    expect(emptyText(page)).toBe('caps/a.pcap has no packets.');
    expect(watches()[0].closed).toBe(true);
  });

  it('frames a capture whose size it cannot tell', async () => {
    FakeEventSource.size = undefined;
    const page = await opened({ exp: 'exp1', vm: 'web', iface: '1' });
    expect(frame(page).exists()).toBe(false);
    watches()[0].emit('error');
    await flushPromises();
    expect(frame(page).exists()).toBe(true);
    expect(watches()[0].closed).toBe(true);
  });

  it('keeps saying a capture is empty when its watch reconnects', async () => {
    FakeEventSource.size = 24;
    const page = await opened({ exp: 'exp1', vm: 'web', iface: '1' });
    watches()[0].emit('error');
    await flushPromises();
    expect(emptyText(page)).toBe(LIVE_EMPTY);
  });

  it('frames the capture where there is no EventSource', async () => {
    vi.stubGlobal('EventSource', undefined);
    const page = await opened({ exp: 'exp1', vm: 'web', iface: '1' });
    expect(frame(page).exists()).toBe(true);
  });

  it('stops watching on another pick and when the page closes', async () => {
    FakeEventSource.size = 24;
    const page = await opened({ exp: 'exp1', vm: 'web', iface: '1' });
    await pick(page, 'live:0:db');
    expect(watches().map((w) => w.closed)).toEqual([true, false]);
    // the first watch answers late, for a capture no longer picked
    watches()[0].closed = false;
    watches()[0].emit('capture-changed', { size: 500 });
    await flushPromises();
    expect(frame(page).exists()).toBe(false);

    page.unmount();
    expect(watches()[1].closed).toBe(true);
  });
});

describe('the opening state', () => {
  it('shows the spinner above the text, not on it, and announces the text', async () => {
    FakeEventSource.size = undefined;
    const page = await opened({ exp: 'exp1', vm: 'web', iface: '1' });
    const empty = page.find('.webshark-empty');
    expect(empty.attributes('role')).toBe('status');
    const [first, second, ...rest] = [...empty.element.children];
    expect(rest).toEqual([]);
    expect(first.classList.contains('webshark-spinner')).toBe(true);
    expect(first.getAttribute('aria-hidden')).toBe('true');
    expect(first.textContent).toBe('');
    expect(second.classList.contains('webshark-empty-text')).toBe(true);
    expect(textOf(second)).toBe('Opening web · IF1…');
    // the frame's overlay is only for WebShark loading in the frame
    expect(page.find('.webshark-frame .loading-overlay').exists()).toBe(false);
  });

  it('shows the spinner while the server readies the capture', async () => {
    const held = Promise.withResolvers();
    const answer = axios.post.getMockImplementation();
    axios.post.mockImplementationOnce(async (...args) => {
      await held.promise;
      return answer(...args);
    });
    const page = await opened({ exp: 'exp1', file: 'caps/a.pcap' });
    expect(page.find('.webshark-spinner').exists()).toBe(true);
    expect(emptyText(page)).toBe('Opening caps/a.pcap…');
    held.resolve();
    await flushPromises();
    expect(page.find('.webshark-spinner').exists()).toBe(false);
  });

  it('shows no spinner with a message that waits for nothing', async () => {
    const page = await opened({});
    expect(page.find('.webshark-spinner').exists()).toBe(false);
    expect(emptyText(page)).toContain('Pick an experiment');
  });
});
