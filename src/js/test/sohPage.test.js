// The State of Health page: what it shows above and around the graph, the
// layout it draws, the SOH runs it follows from the broker's `apps/soh`
// messages, and its SVG and GEXF downloads.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/utils/rbac.js', () => ({ roleAllowed: () => true }));
const axios = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock('@/utils/axios.js', () => ({ default: axios }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ token: 't', features: [], role: null }),
}));
vi.mock('@/utils/websocket', () => ({
  addWsHandler: () => {},
  removeWsHandler: () => {},
}));
// a loader whose load() fetches as the real one does, without the cache
vi.mock('@/utils/pageLoader.js', () => ({
  createPageLoader: (options) => ({
    start: () => {},
    stop: () => {},
    load: () => options.fetch(new AbortController().signal),
  }),
  pageStatus: {},
}));
const notifyError = vi.hoisted(() => vi.fn());
vi.mock('@/utils/errorNotif', () => ({
  useErrorNotification: notifyError,
  showError: vi.fn(),
}));
const saver = vi.hoisted(() => ({ saveAs: vi.fn() }));
vi.mock('file-saver', () => ({ default: saver }));

import { GROUPS_MAX_NODES } from '@/utils/soh/layouts.js';
import StateOfHealth from '@/views/StateOfHealth.vue';
import { makeContext } from './helpers/context.js';
import { buttons, renderSSR, stubBrowser, textOf } from './helpers/render.js';
import { sohFixture } from './sohFixture.js';

beforeEach(() => {
  stubBrowser();
  axios.get.mockReset();
  saver.saveAs.mockReset();
  notifyError.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

// the page on experiment demo
const page = (fields, component = StateOfHealth) =>
  makeContext(component, {
    $route: { params: { id: 'demo' } },
    $buefy: { toast: { open: vi.fn() } },
    $nextTick: () => Promise.resolve(),
    ...fields,
  });

describe('rendering', () => {
  const render = (data) =>
    renderSSR(StateOfHealth, {}, { route: '/soh/demo', data });
  const { nodes, edges } = sohFixture();

  // the layout buttons: the label of each, and whether it is chosen or off
  const layoutButtons = (html) =>
    [...html.matchAll(/aria-label="([^"]+)"[^>]*><label class="([^"]*)"/g)].map(
      ([, label, cls]) => ({
        label,
        chosen: cls.includes('is-selected'),
        off: cls.includes('is-disabled'),
      }),
    );

  it('counts the VMs and VLANs the filter shows', async () => {
    const counts = (html) =>
      textOf(html.match(/<div class="soh-counts"[\s\S]*?<\/div>/)[0]);

    expect(counts(await render({ nodes, edges }))).toBe('19 VMs 4 VLANs');
    const one = [nodes[0], nodes.find((n) => n.image === 'switch')];
    expect(counts(await render({ nodes: one }))).toBe('1 VM 1 VLAN');
  });

  it('offers each layout, starting with the one last chosen', async () => {
    localStorage.setItem('phenix.soh.layout', 'elk');
    const html = await render({ nodes, edges });

    expect(layoutButtons(html)).toEqual(
      ['Force', 'Radial tree', 'Layered', 'VLAN groups', 'ELK layered'].map(
        (label) => ({ label, chosen: label == 'ELK layered', off: false }),
      ),
    );
    expect(html).toContain('Top-down tiers with host blocks (ELK)');
  });

  it('turns VLAN groups off above its node cap, saying why', async () => {
    const big = Array.from({ length: GROUPS_MAX_NODES + 1 }, (_, id) => ({
      id,
      image: 'linux',
    }));
    const html = await render({ nodes: big });

    expect(
      layoutButtons(html)
        .filter((b) => b.off)
        .map((b) => b.label),
    ).toEqual(['VLAN groups']);
    expect(html).toContain('VLAN groups is off above 300 nodes');
  });

  it('holds the layouts, Fit and Download until there is a graph', async () => {
    const html = await render({ nodes: null });

    expect(textOf(html)).toContain(
      'There are no nodes matching your filter criteria!',
    );
    expect(layoutButtons(html).map((b) => b.off)).toEqual(Array(5).fill(true));
    expect(buttons(html)).toEqual([
      'Refresh Network',
      'Loading… (disabled)',
      'Fit (disabled)',
      'Download (disabled)',
    ]);
    const formats = [...html.matchAll(/<b[^>]*>(PNG|SVG|GEXF)<\/b>/g)];
    expect(formats.map(([, f]) => f)).toEqual(['PNG', 'SVG', 'GEXF']);
  });

  it.each([
    ['says the page is loading', { loaded: false }, 'Loading…'],
    [
      'says the experiment is not running',
      { running: false },
      'Exp Not Running',
    ],
    [
      'says the first run is under way',
      { running: true, sohRunning: true },
      'SOH Is Initializing',
    ],
    [
      'says a run is under way',
      { running: true, sohRunning: true, sohInitialized: true },
      'SOH Is Running',
    ],
    ['says SOH has not started', { running: true }, 'SOH Not Initialized'],
  ])('holds the SOH button and %s', async (_, state, label) => {
    const html = await render({ nodes, loaded: true, ...state });
    expect(buttons(html)).toEqual([
      'Refresh Network',
      `${label} (disabled)`,
      'Fit',
      'Download',
    ]);
  });

  it('offers Run SOH once SOH has started in the running experiment', async () => {
    const html = await render({
      nodes,
      loaded: true,
      running: true,
      sohInitialized: true,
    });
    expect(buttons(html)).toEqual([
      'Refresh Network',
      'Run SOH',
      'Fit',
      'Download',
    ]);
  });
});

describe('SOH runs', () => {
  const soh = (action, name = 'demo') => ({
    resource: { type: 'apps/soh', name, action },
  });

  // the page, opened on a running experiment whose SOH has run before
  async function opened() {
    const ctx = page({ loaded: true, running: true, sohInitialized: true });
    await StateOfHealth.created.call(ctx);
    return ctx;
  }

  // the SOH button, as the page renders in the state ctx is in
  async function sohButton(ctx) {
    const { loaded, running, sohRunning, sohInitialized } = ctx;
    const html = await renderSSR(
      StateOfHealth,
      {},
      {
        route: '/soh/demo',
        data: { loaded, running, sohRunning, sohInitialized },
      },
    );
    const shown = buttons(html);
    // it follows the Refresh Network button
    return shown[shown.indexOf('Refresh Network') + 1];
  }

  it('shows a run from start to success, then reloads the graph', async () => {
    axios.get.mockResolvedValue({ data: {} });
    const ctx = await opened();
    expect(await sohButton(ctx)).toBe('Run SOH');

    ctx.handleWs(soh('start'));
    expect(await sohButton(ctx)).toBe('SOH Is Running (disabled)');
    expect(axios.get).not.toHaveBeenCalled();

    ctx.handleWs(soh('success'));
    expect(await sohButton(ctx)).toBe('Run SOH');
    expect(axios.get.mock.calls.map(([url]) => url)).toEqual([
      'experiments/demo/soh',
    ]);
  });

  it('reports a failed run and offers another', async () => {
    const ctx = await opened();

    ctx.handleWs(soh('start'));
    ctx.handleWs(soh('error'));
    expect(await sohButton(ctx)).toBe('Run SOH');
    expect(ctx.$buefy.toast.open).toHaveBeenCalledWith(
      expect.objectContaining({
        message: 'Triggering State of Health update failed.',
        type: 'is-danger',
      }),
    );
    expect(axios.get).not.toHaveBeenCalled();
  });

  it('ignores runs in other experiments', async () => {
    const ctx = await opened();

    ctx.handleWs(soh('start', 'other'));
    ctx.handleWs(soh('success', 'other'));
    expect(await sohButton(ctx)).toBe('Run SOH');
    expect(axios.get).not.toHaveBeenCalled();
  });
});

describe('downloading the graph', () => {
  // a page with the fixture graph drawn in the layered layout
  function drawn(svg = {}) {
    const { nodes, edges } = sohFixture();
    const graph = {
      svg: { node: () => svg },
      layoutId: 'layered',
      nodes: nodes.map((n, i) => ({ id: n.id, x: i, y: 2 * i })),
    };
    return page({ nodes, edges, running: true, graph });
  }

  it("saves the graph with the experiment's VMs and the positions shown", async () => {
    vi.useFakeTimers({ now: new Date(2026, 8, 30, 12, 0, 0) });
    axios.get.mockResolvedValue({
      data: { vms: [{ name: 'historian', host: 'compute7' }] },
    });

    await drawn().download('gexf');
    expect(axios.get).toHaveBeenCalledWith('experiments/demo/vms');
    const [blob, name] = saver.saveAs.mock.calls[0];
    expect(name).toBe('demo-soh-layered-20260930-120000.gexf');
    const xml = await blob.text();
    expect(xml).toContain('experiment demo: 19 VMs and 4 VLANs');
    expect(xml).toContain('<attvalue for="host" value="compute7"/>');
    expect(xml).toContain('<viz:position x="1" y="-2" z="0"/>');
  });

  it('saves it without VM details the user may not read', async () => {
    axios.get.mockRejectedValue(new Error('forbidden'));

    await drawn().download('gexf');
    const [blob] = saver.saveAs.mock.calls[0];
    expect(await blob.text()).toContain(
      'VM details (host, addresses, CPUs, memory, disk) not available',
    );
  });

  // the drawn graph's <svg>, as much of it as the SVG export reads: one
  // empty layer, not zoomed, and no <defs>
  function svgElement() {
    const layer = {
      children: [],
      childNodes: [],
      getBBox: () => ({ x: 0, y: 0, width: 200, height: 100 }),
      cloneNode: () => ({ ...layer, setAttribute() {}, removeAttribute() {} }),
    };
    return {
      querySelector: (selector) => (selector == ':scope > g' ? layer : null),
      viewBox: { baseVal: null },
      clientWidth: 800,
    };
  }

  it('titles the SVG with the experiment, what the filter shows, the layout and the time', async () => {
    const now = new Date(2026, 8, 30, 12, 0, 0);
    vi.useFakeTimers({ now });
    vi.stubGlobal('getComputedStyle', () => ({ getPropertyValue: () => '' }));
    vi.stubGlobal(
      'XMLSerializer',
      class {
        serializeToString() {
          return '';
        }
      },
    );
    const ctx = drawn(svgElement());
    // the server sends only the nodes the filter shows
    ctx.radioButton = 'running';
    ctx.nodes = ctx.nodes.filter(
      (n) => n.status === 'running' || n.image === 'switch',
    );

    await ctx.download('svg');
    expect(notifyError).not.toHaveBeenCalled();
    const [blob, name] = saver.saveAs.mock.calls[0];
    expect(name).toBe('demo-soh-layered-20260930-120000.svg');
    const svg = await blob.text();
    expect(svg).toContain('>State of Health: demo<');
    expect(svg).toContain(
      `>6 VMs · 4 VLANs · filter: Running · layout: Layered · ${now.toLocaleString()}<`,
    );
  });
});

describe('the layout drawn', () => {
  // the page lays out the graph and stops short of drawing it, as the
  // server-side document has no #graph

  it('draws Force instead of VLAN groups for a graph too big for it, saying why', async () => {
    const big = Array.from({ length: GROUPS_MAX_NODES + 1 }, (_, id) => ({
      id,
      image: 'linux',
    }));
    const ctx = page({ nodes: big, edges: [], layout: 'groups' });

    await ctx.generateGraph();
    const notice =
      'VLAN groups is off above 300 nodes (this graph has 301): it would ' +
      'take many seconds to compute. Showing Force.';
    expect(ctx.layoutNotice).toBe(notice);
    expect(ctx.$buefy.toast.open).toHaveBeenCalledWith(
      expect.objectContaining({ message: notice, type: 'is-warning' }),
    );
    expect(ctx.layoutBusy).toBe(false);
  });

  // The page from modules loaded afresh, and the layout libraries it has
  // loaded, in order: none has loaded yet, whichever tests ran before.
  async function freshPage(fields) {
    vi.resetModules();
    const libraries = [];
    for (const [name, path] of [
      ['dagre', '@dagrejs/dagre'],
      ['webcola', 'webcola/dist/src/layout'],
      ['elkjs', 'elkjs/lib/elk.bundled.js'],
    ]) {
      // registering a mock again drops the copy an earlier test loaded
      vi.doMock(path, async (original) => {
        libraries.push(name);
        return original();
      });
    }
    const { default: fresh } = await import('@/views/StateOfHealth.vue');
    return { ctx: page(fields, fresh), libraries };
  }

  it("loads each layout's library only once that layout is picked", async () => {
    // ELK reaches for the browser window's Error when there is a window,
    // and the stubbed one has none
    vi.stubGlobal('window', undefined);
    // a VM and its VLAN: the libraries load no faster for a bigger graph
    const { nodes, edges } = sohFixture();
    const [edge] = edges;
    const pair = nodes.filter((n) => [edge.source, edge.target].includes(n.id));
    const { ctx, libraries } = await freshPage({ nodes: pair, edges: [edge] });

    // none with the page
    expect(libraries).toEqual([]);
    for (const [layout, loaded] of [
      ['force', []],
      ['radial', []],
      ['layered', ['dagre']],
      ['groups', ['dagre', 'webcola']],
      ['elk', ['dagre', 'webcola', 'elkjs']],
    ]) {
      ctx.layout = layout;
      await ctx.generateGraph();
      expect(libraries, layout).toEqual(loaded);
    }
    // and each layout ran without falling back to Force
    expect(notifyError.mock.calls.map(([e]) => e.message)).toEqual([]);
    expect(ctx.layout).toBe('elk');
    expect(ctx.$buefy.toast.open).not.toHaveBeenCalled();
  });
});
