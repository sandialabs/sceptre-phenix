// The WebShark page: its pickers, what it asks the server, its frame, and its
// URL. Most tests run its options on a plain object standing in for the
// component; the rendering tests use the real Buefy components.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const rbac = vi.hoisted(() => ({ allowed: () => true }));
vi.mock('@/utils/rbac.js', () => ({
  roleAllowed: (...args) => rbac.allowed(...args),
}));

const axios = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock('@/utils/axios.js', () => ({ default: axios }));

const notify = vi.hoisted(() => vi.fn());
vi.mock('@/utils/errorNotif.js', () => ({ useErrorNotification: notify }));

const store = vi.hoisted(() => ({}));
vi.mock('@/store.js', () => ({ usePhenixStore: () => store }));

import { cachePage, clearPageCache } from '@/utils/pageCache.js';
import { experimentKey } from '@/utils/pageData.js';
import { fileTarget } from '@/utils/webshark.js';
import WebShark from '@/views/WebShark.vue';
import { flush } from './helpers/async.js';
import { makeContext } from './helpers/context.js';
import { renderSSR } from './helpers/render.js';

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

beforeEach(() => {
  rbac.allowed = () => true;
  Object.assign(store, {
    features: ['webshark'],
    featuresLoaded: true,
    token: 'jwt.token',
  });
  axios.get.mockReset();
  axios.get.mockImplementation(async (url) => {
    if (!(url in SERVER)) throw new Error(`unexpected GET ${url}`);
    return { data: SERVER[url] };
  });
  axios.post.mockReset();
  axios.post.mockImplementation(async (_, body) => ({
    data: {
      capture: body.path
        ? `exp1/${body.path}`
        : `exp1/${body.vm}-${body.interface}`,
      live: !body.path,
      title: 'a capture',
    },
  }));
  notify.mockReset();
});

afterEach(() => {
  clearPageCache();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

// the page with the given URL query; picking changes the query, which the
// page follows as its $route watcher does
function page(query = {}) {
  const ctx = makeContext(WebShark, {
    $route: { name: 'webshark', query },
    $nextTick: (fn) => fn?.(),
    $refs: {},
    $buefy: { toast: { open: vi.fn() }, dialog: { alert: vi.fn() } },
    // there is no window to fit in
    fitFrame: () => {},
  });
  ctx.$router = {
    replace: vi.fn((to) => {
      ctx.$route = { name: 'webshark', query: to.query };
      ctx.applyQuery();
    }),
  };
  return ctx;
}

async function opened(query) {
  const ctx = page(query);
  WebShark.created.call(ctx);
  await flush();
  return ctx;
}

describe('the pickers', () => {
  it('list the experiments by name, then their captures', async () => {
    const ctx = await opened({ exp: 'exp1' });
    expect(ctx.experimentOptions).toEqual([
      { name: 'exp1', running: true },
      { name: 'exp2', running: false },
    ]);
    expect(ctx.liveOptions.map((t) => t.label)).toEqual([
      'db · IF0',
      'web · IF1',
    ]);
    expect(ctx.fileOptions.map((t) => [t.path, t.size])).toEqual([
      ['caps/a.pcap', 2048],
      ['B.PCAPNG.gz', 10],
      ['w1.pcap', 100],
    ]);
    expect(axios.get).toHaveBeenCalledWith('experiments/exp1/files', {
      params: { sortCol: 'date', sortDir: 'desc' },
    });
    expect(ctx.listsLoading).toBe(false);
    expect(axios.post).not.toHaveBeenCalled();
  });

  it("name live captures after the interface's network when known", async () => {
    cachePage(experimentKey('exp1'), {
      vms: [{ name: 'web', networks: ['DMZ (149)', 'MGMT (151)'] }],
    });
    const ctx = await opened({ exp: 'exp1' });
    expect(ctx.liveOptions.map((t) => t.label)).toEqual([
      'db · IF0',
      'web · IF1 (mgmt)',
    ]);
  });

  it('leave out what the role may not open', async () => {
    rbac.allowed = (resource, verb, name) =>
      !(resource === 'experiments/files' && verb === 'get') &&
      !(resource === 'vms/captures' && name === 'exp1/db');
    const ctx = await opened({ exp: 'exp1' });
    expect(ctx.liveOptions.map((t) => t.label)).toEqual(['web · IF1']);
    expect(ctx.fileOptions).toEqual([]);
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
    const ctx = await opened({ exp: 'exp1' });
    expect(notify).toHaveBeenCalledWith(err);
    expect(ctx.liveOptions).toEqual([]);
    expect(ctx.fileOptions).toHaveLength(3);
  });

  it('put the picked experiment in the URL and list its captures', async () => {
    const ctx = await opened({});
    expect(ctx.exp).toBe(null);
    ctx.pickExperiment('exp1');
    expect(ctx.$router.replace).toHaveBeenCalledWith({
      query: { exp: 'exp1' },
    });
    await flush();
    expect(ctx.exp).toBe('exp1');
    expect(ctx.liveOptions).toHaveLength(2);
  });
});

describe('opening a capture', () => {
  it('posts a saved file by path and frames it from /webshark/', async () => {
    const ctx = await opened({ exp: 'exp1' });
    ctx.pickCapture('file:caps/a.pcap');
    expect(ctx.$router.replace).toHaveBeenCalledWith({
      query: { exp: 'exp1', file: 'caps/a.pcap' },
    });
    expect(ctx.opening).toBe(true);
    await flush();

    expect(axios.post).toHaveBeenCalledTimes(1);
    expect(axios.post).toHaveBeenCalledWith('experiments/exp1/webshark', {
      path: 'caps/a.pcap',
    });
    expect(ctx.opening).toBe(false);
    expect(ctx.frameURL).toBe(
      '/webshark/embed?capture=exp1%2Fcaps%2Fa.pcap&embed=1',
    );
    expect(ctx.newTabURL).toBe('/webshark/embed?capture=exp1%2Fcaps%2Fa.pcap');
    // a finished file has nothing to stream
    expect(ctx.streamTarget).toBe(null);
  });

  it('posts a live capture by VM and interface', async () => {
    const ctx = await opened({ exp: 'exp1' });
    ctx.pickCapture('live:1:web');
    expect(ctx.$router.replace).toHaveBeenCalledWith({
      query: { exp: 'exp1', vm: 'web', iface: '1' },
    });
    await flush();

    expect(axios.post).toHaveBeenCalledWith('experiments/exp1/webshark', {
      vm: 'web',
      interface: 1,
    });
    expect(ctx.capture.live).toBe(true);
    expect(ctx.frameURL).toBe('/webshark/embed?capture=exp1%2Fweb-1&embed=1');
    expect(ctx.streamTarget).toMatchObject({ vm: 'web', iface: 1 });
  });

  it('streams a saved file that is still being captured', async () => {
    axios.post.mockResolvedValue({
      data: { capture: 'w1', live: true, title: 'w1.pcap' },
    });
    const ctx = await opened({ exp: 'exp1', file: 'w1.pcap' });
    expect(ctx.streamTarget).toMatchObject({ vm: 'web', iface: 1 });
  });

  it('shows why a capture could not be opened', async () => {
    const err = Object.assign(new Error('404'), {
      response: { status: 404, data: 'capture not found' },
    });
    axios.post.mockRejectedValue(err);
    const ctx = await opened({ exp: 'exp1', vm: 'web', iface: '3' });
    expect(notify).toHaveBeenCalledWith(err);
    expect(ctx.capture).toBe(null);
    expect(ctx.opening).toBe(false);
    expect(ctx.emptyText).toBe(
      'Could not open web · IF3. It was not found, or is no longer being captured.',
    );
  });

  it('refuses an answer that names no capture', async () => {
    axios.post.mockResolvedValue({ data: {} });
    const ctx = await opened({ exp: 'exp1', file: 'caps/a.pcap' });
    expect(notify).toHaveBeenCalled();
    expect(ctx.capture).toBe(null);
  });

  it('keeps only the answer for the last pick', async () => {
    const answers = [];
    axios.post.mockImplementation(
      (_, body) => new Promise((resolve) => answers.push({ body, resolve })),
    );
    const ctx = await opened({ exp: 'exp1' });
    ctx.pickCapture('file:caps/a.pcap');
    ctx.pickCapture('live:0:db');
    answers[1].resolve({ data: { capture: 'db', live: true } });
    answers[0].resolve({ data: { capture: 'a', live: false } });
    await flush();
    expect(ctx.capture.capture).toBe('db');
  });
});

describe('deep links', () => {
  it('open the saved file the URL names', async () => {
    const ctx = await opened({ exp: 'exp1', file: 'caps/a.pcap' });
    expect(ctx.exp).toBe('exp1');
    expect(ctx.selected.key).toBe('file:caps/a.pcap');
    expect(axios.post).toHaveBeenCalledTimes(1);
    expect(axios.post).toHaveBeenCalledWith('experiments/exp1/webshark', {
      path: 'caps/a.pcap',
    });
    expect(ctx.frameURL).toBe(
      '/webshark/embed?capture=exp1%2Fcaps%2Fa.pcap&embed=1',
    );
  });

  it('open the running capture the URL names', async () => {
    const ctx = await opened({ exp: 'exp1', vm: 'web', iface: '1' });
    expect(ctx.selected.key).toBe('live:1:web');
    expect(axios.post).toHaveBeenCalledWith('experiments/exp1/webshark', {
      vm: 'web',
      interface: 1,
    });
    // the listed entry, not a second one for the URL
    expect(ctx.liveOptions.map((t) => t.key)).toEqual([
      'live:0:db',
      'live:1:web',
    ]);
  });

  it('keep a file the list does not have pickable', async () => {
    const ctx = await opened({ exp: 'exp1', file: 'gone.pcap' });
    expect(ctx.fileOptions.map((t) => t.path)).toContain('gone.pcap');
  });

  it('wait for the features before asking the server anything', async () => {
    store.features = [];
    store.featuresLoaded = false;
    const ctx = page({ exp: 'exp1', file: 'caps/a.pcap' });
    WebShark.created.call(ctx);
    await flush();
    expect(axios.get).not.toHaveBeenCalled();

    store.features = ['webshark'];
    WebShark.watch.installed.call(ctx, true);
    await flush();
    expect(axios.post).toHaveBeenCalledWith('experiments/exp1/webshark', {
      path: 'caps/a.pcap',
    });
  });
});

describe('Copy Wireshark command', () => {
  async function live() {
    vi.stubEnv('VITE_AUTH', 'enabled');
    vi.stubGlobal('window', {
      location: { origin: 'https://phenix.example' },
    });
    return opened({ exp: 'exp1', vm: 'web', iface: '1' });
  }
  const command =
    "curl -fsSN --max-redirs 0 -L -H 'X-Phenix-Auth-Token: Bearer jwt.token'" +
    " 'https://phenix.example/api/v1/experiments/exp1/vms/web/captures/1/stream'" +
    ' | wireshark -k -i -';

  it('copies a command that streams the capture into Wireshark', async () => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    const ctx = await live();
    await ctx.copyWiresharkCommand();
    expect(writeText).toHaveBeenCalledWith(command);
    expect(ctx.$buefy.toast.open).toHaveBeenCalled();
  });

  // a UI built without sign-in holds a placeholder token
  it.each([
    ['the UI has no sign-in', 'disabled', 'jwt.token'],
    ['the UI was built without an auth mode', '', 'jwt.token'],
    ['no one is signed in', 'enabled', ''],
  ])('leaves the token out when %s', async (_, auth, token) => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    const ctx = await live();
    vi.stubEnv('VITE_AUTH', auth);
    store.token = token;
    await ctx.copyWiresharkCommand();
    expect(writeText).toHaveBeenCalledWith(
      "curl -fsSN --max-redirs 0 -L 'https://phenix.example/api/v1/experiments/exp1/vms/web/captures/1/stream'" +
        ' | wireshark -k -i -',
    );
  });

  it('shows the command when there is no clipboard', async () => {
    vi.stubGlobal('navigator', {});
    const ctx = await live();
    await ctx.copyWiresharkCommand();
    const [{ message }] = ctx.$buefy.dialog.alert.mock.calls[0];
    expect(message).toContain(command);
  });

  it('explains a sign-in proxy, which the command must get past', async () => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    const ctx = await live();
    vi.stubEnv('VITE_AUTH', 'proxy');
    await ctx.copyWiresharkCommand();
    expect(writeText).toHaveBeenCalledWith(command);
    expect(ctx.$buefy.toast.open).not.toHaveBeenCalled();
    const [{ title, message }] = ctx.$buefy.dialog.alert.mock.calls[0];
    expect(title).toBe('Copied the Wireshark command');
    expect(message).toContain('sign-in proxy');
    expect(message).toContain("-b 'NAME=VALUE'");
    expect(message).toContain(
      "phenix vm capture stream 'exp1' 'web' 1 | wireshark -k -i -",
    );
  });
});

describe('rendering', () => {
  const render = (query, data) =>
    renderSSR(WebShark, {}, { route: { name: 'webshark', query }, data });

  it('says when WebShark is not installed', async () => {
    store.features = ['tunneler-download'];
    const html = await render({});
    expect(html).toContain('WebShark is not installed on this phēnix server.');
    expect(html).toContain(
      'href="https://phenix.sceptre.dev/latest/webshark/"',
    );
    expect(html).not.toContain('webshark-bar');
    expect(axios.get).not.toHaveBeenCalled();
  });

  it('waits for the features before saying so', async () => {
    store.features = [];
    store.featuresLoaded = false;
    const html = await render({});
    expect(html).not.toContain('not installed');
    expect(html).toContain('Loading…');
  });

  it('groups live captures and saved files in the capture picker', async () => {
    const html = await render(
      { exp: 'exp1' },
      {
        exp: 'exp1',
        liveCaptures: [
          { key: 'live:1:web', live: true, label: 'web · IF1', vm: 'web' },
        ],
        savedFiles: [fileTarget('caps/a.pcap', 2048)],
      },
    );
    expect(html).toMatch(
      /<optgroup label="Live captures"[^>]*>(<!--\[-->)?<option value="live:1:web"[^>]*>\s*web · IF1\s*<\/option>/,
    );
    expect(html).toMatch(
      /<optgroup label="Saved files"[^>]*>(<!--\[-->)?<option value="file:caps\/a.pcap"[^>]*>\s*caps\/a.pcap \(2.05 KB\)\s*<\/option>/,
    );
  });

  it('frames the capture from /webshark/ and links to it', async () => {
    const html = await render(
      { exp: 'exp1', vm: 'web', iface: '1' },
      {
        exp: 'exp1',
        selected: {
          key: 'live:1:web',
          live: true,
          label: 'web · IF1',
          vm: 'web',
          iface: 1,
        },
        capture: { capture: 'exp1/web 1', live: true, title: 'web IF1' },
      },
    );
    expect(html).toMatch(
      /<iframe src="\/webshark\/embed\?capture=exp1%2Fweb%201&amp;embed=1" title="web IF1"/,
    );
    expect(html).toMatch(
      /href="\/webshark\/embed\?capture=exp1%2Fweb%201" target="_blank"[\s\S]*?Open in new tab/,
    );
    expect(html).toMatch(/class="tag is-danger[\s\S]*?Live/);
    expect(html).toContain('Copy Wireshark command');
    expect(html).toContain(
      'Copy a command that streams this capture into a local Wireshark',
    );
  });
});
