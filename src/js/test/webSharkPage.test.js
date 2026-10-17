// The WebShark page: pickers, server requests, frame and URL.
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
import { denyOnly } from './helpers/permissions.js';

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

beforeEach(() => {
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
  expect(url).toBe('experiments/exp1/capture-tokens');
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
      'caps/a.pcap (2.05 KB)',
      'B.PCAPNG.gz (10.00 B)',
      'w1.pcap (100.00 B)',
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
      'Could not open web · IF3. It was not found, or is no longer being captured.',
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
    expect(axios.post).toHaveBeenCalledWith('experiments/exp1/capture-tokens', {
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
    expect(notify.error).toHaveBeenCalledWith(err);
    const [{ text, type }] = toasts();
    expect(type).toBe('is-danger');
    expect(text).toContain("You don't have permission");
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
      text: 'caps/a.pcap (2.05 KB)',
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
