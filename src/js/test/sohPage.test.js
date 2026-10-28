// The State of Health page: what surrounds the graph, its layout, the SOH runs
// it follows from `apps/soh` messages, and its SVG and GEXF downloads.
import { flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const {
  axios,
  fetching,
  loaderSpy,
  modules,
  notify,
  rbac,
  resetMocks,
  saver,
  ws,
} = await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
// a loader whose load() fetches as the real one does, without the cache
vi.mock('@/utils/pageLoader.js', fetching);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('file-saver', modules.fileSaver);

import { layoutInput } from '@/utils/soh/graph.js';
import {
  GROUPS_MAX_NODES,
  LAYOUT_STORAGE_KEY,
  runLayout,
} from '@/utils/soh/layouts.js';
import { UPDATED_MESSAGE } from '@/utils/staleChunk.js';
import StateOfHealth from '@/views/StateOfHealth.vue';
import { buttons, mountComponent, toasts } from './helpers/mount.js';
import { denyOnly } from './helpers/permissions.js';
import { sohFixture } from './helpers/sohFixture.js';

// The page on experiment demo with this state, not attached to the
// document, which so has no #graph to draw into.
const open = (state = {}, component = StateOfHealth) =>
  mountComponent(component, { route: '/soh/demo', data: state });

const big = Array.from({ length: GROUPS_MAX_NODES + 1 }, (_, id) => ({
  id,
  image: 'linux',
}));

// the radio button that picks this layout
const layoutButton = (page, label) => page.find(`[aria-label="${label}"]`);

// the layout buttons: the label of each, and whether it is chosen or off
const layoutButtons = (page) =>
  page.findAll('.soh-layout-seg .control').map((c) => ({
    label: c.attributes('aria-label'),
    chosen: c.find('label').classes('is-selected'),
    off: c.find('label').classes('is-disabled'),
  }));

// the line under the layout buttons: the chosen one's hint, or a spinner's
const layoutHint = (page) => page.find('.soh-layout-hint').text();

// the layout button shown as chosen
const chosenLayout = (page) => layoutButtons(page).find((b) => b.chosen).label;

// why the bar draws another layout than the one chosen, or undefined
const layoutNote = (page) => {
  const note = page.find('.soh-layout-note');
  return note.exists() ? note.text() : undefined;
};

beforeEach(() => {
  resetMocks({ store: { token: 't', features: [], role: null } });
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('rendering', () => {
  const { nodes, edges } = sohFixture();

  it('counts the VMs and VLANs the filter shows', async () => {
    const counts = async (state) =>
      (await open(state))
        .findAll('.soh-count')
        .map((c) => c.text().replace(/\s+/g, ' '))
        .join(' ');

    expect(await counts({ nodes, edges })).toBe('19 VMs 4 VLANs');
    const one = [nodes[0], nodes.find((n) => n.image === 'switch')];
    expect(await counts({ nodes: one })).toBe('1 VM 1 VLAN');
  });

  it('offers each layout, starting with the one last chosen', async () => {
    localStorage.setItem(LAYOUT_STORAGE_KEY, 'elk');
    const page = await open({ nodes, edges });

    expect(layoutButtons(page)).toEqual(
      ['Force', 'Radial tree', 'Layered', 'VLAN groups', 'ELK layered'].map(
        (label) => ({ label, chosen: label == 'ELK layered', off: false }),
      ),
    );
    expect(page.text()).toContain('Top-down tiers with host blocks (ELK)');
  });

  it('turns VLAN groups off above its node cap, saying why', async () => {
    const page = await open({ nodes: big });

    expect(
      layoutButtons(page)
        .filter((b) => b.off)
        .map((b) => b.label),
    ).toEqual(['VLAN groups']);
    expect(page.text()).toContain('VLAN groups is off above 300 nodes');
  });

  it('holds the layouts, Fit and Download until there is a graph', async () => {
    const page = await open({ nodes: null });

    expect(page.text()).toContain(
      'There are no nodes matching your filter criteria!',
    );
    expect(layoutButtons(page).map((b) => b.off)).toEqual(Array(5).fill(true));
    expect(buttons(page)).toEqual([
      'Refresh Network',
      'Loading… (disabled)',
      'Fit (disabled)',
      'Download (disabled)',
    ]);
    expect(page.findAll('.soh-download-item b').map((b) => b.text())).toEqual([
      'PNG',
      'SVG',
      'GEXF',
    ]);
  });

  it.each([
    ['says the page is loading', { loaded: false }, 'Loading…'],
    [
      'says the experiment is not running',
      { running: false },
      'Experiment Not Running',
    ],
    [
      'says the first run is under way',
      { running: true, sohRunning: true },
      'SOH Is Initializing',
      'SOH is running its first checks.',
    ],
    [
      'says a run is under way',
      { running: true, sohRunning: true, sohInitialized: true },
      'SOH Is Running',
    ],
    [
      'says SOH waits for its first run',
      { running: true },
      'Waiting for First Run',
      'SOH is waiting for its first run, which starts after post-start.',
    ],
    [
      'says a disabled soh app is running its first checks',
      { running: true, sohRunning: true, sohDisabled: true },
      'SOH Is Initializing',
      'SOH is running its first checks.',
    ],
  ])('holds the SOH button and %s', async (_, state, label, note) => {
    const page = await open({ nodes, loaded: true, ...state });
    expect(buttons(page)).toEqual([
      'Refresh Network',
      `${label} (disabled)`,
      'Fit',
      'Download',
    ]);
    const shown = page.find('#soh-run-note');
    expect(shown.exists() ? shown.text() : undefined).toBe(note);
    if (note) {
      expect(
        page.findAll('.soh-actions button')[1].attributes('aria-describedby'),
      ).toBe('soh-run-note');
    }
  });

  it('offers Run SOH for a disabled soh app that has not run, saying so', async () => {
    axios.post.mockResolvedValue({});
    const page = await open({ nodes, loaded: true, running: true });
    // as GET experiments/demo/soh answers for such an app
    loaderSpy.options.apply({
      started: true,
      soh_disabled: true,
      soh_initialized: false,
      soh_running: false,
      nodes,
      edges: [],
    });
    await flushPromises();

    expect(buttons(page).slice(0, 2)).toEqual(['Refresh Network', 'Run SOH']);
    expect(page.find('#soh-run-note').text()).toBe(
      'The soh app is disabled in the scenario and has not run. Run SOH starts it manually.',
    );
    await page.findAll('.soh-actions button')[1].trigger('click');
    expect(axios.post).toHaveBeenCalledWith('experiments/demo/trigger', null, {
      params: { apps: 'soh' },
    });

    // once it has run, it is like any other
    loaderSpy.options.apply({
      started: true,
      soh_disabled: true,
      soh_initialized: true,
      nodes,
      edges: [],
    });
    await flushPromises();
    expect(buttons(page)[1]).toBe('Run SOH');
    expect(page.find('#soh-run-note').exists()).toBe(false);

    // a role that may not trigger apps has no Run SOH to start it with
    rbac.allowed = denyOnly(['experiments/trigger', 'create']);
    const viewer = await open({
      nodes,
      loaded: true,
      running: true,
      sohDisabled: true,
    });
    expect(buttons(viewer).slice(0, 2)).toEqual(['Refresh Network', 'Fit']);
    expect(viewer.find('#soh-run-note').text()).toBe(
      'The soh app is disabled in the scenario and has not run.',
    );
  });

  it.each([
    ['no flows', [], []],
    ['no flow matrix', null, null],
    [
      'flows',
      ['web', 'db'],
      [
        [0, 3],
        [2, 0],
      ],
      'Network Volume',
    ],
  ])('shows the tabs for a network with %s', async (_, hosts, flows, extra) => {
    const page = await open();
    // the network as GET experiments/demo/soh answers it
    loaderSpy.options.apply({ nodes, edges, hosts, host_flows: flows });
    await flushPromises();
    expect(page.findAll('.tabs li').map((li) => li.text().trim())).toEqual(
      ['Topology Graph', extra].filter(Boolean),
    );
  });

  it('offers Run SOH once SOH has started in the running experiment', async () => {
    const page = await open({
      nodes,
      loaded: true,
      running: true,
      sohInitialized: true,
    });
    expect(buttons(page)).toEqual([
      'Refresh Network',
      'Run SOH',
      'Fit',
      'Download',
    ]);
  });
});

describe('SOH runs', () => {
  // the page, opened on a running experiment whose SOH has run before, and
  // a function that sends it an `apps/soh` message and gives its SOH button
  async function opened() {
    const page = await open({
      loaded: true,
      running: true,
      sohInitialized: true,
    });
    const soh = async (action, name = 'demo') => {
      const msg = { resource: { type: 'apps/soh', name, action } };
      for (const handler of ws.handlers) handler(msg);
      await flushPromises();
      // it follows the Refresh Network button
      return buttons(page)[1];
    };
    return { page, soh };
  }

  it('shows a run from start to success, then reloads the graph', async () => {
    axios.get.mockResolvedValue({ data: {} });
    const { page, soh } = await opened();
    expect(buttons(page)[1]).toBe('Run SOH');

    expect(await soh('start')).toBe('SOH Is Running (disabled)');
    expect(axios.get).not.toHaveBeenCalled();

    expect(await soh('success')).toBe('Run SOH');
    expect(axios.get.mock.calls.map(([url]) => url)).toEqual([
      'experiments/demo/soh',
    ]);
  });

  it('reports a failed run and offers another', async () => {
    const { soh } = await opened();

    await soh('start');
    expect(await soh('error')).toBe('Run SOH');
    expect(notify.show).toHaveBeenCalledWith(
      'Could not trigger the State of Health update.',
    );
    expect(toasts()).toEqual([]);
    expect(axios.get).not.toHaveBeenCalled();
  });

  it('offers another run once one is canceled', async () => {
    axios.get.mockResolvedValue({ data: {} });
    const { soh } = await opened();

    await soh('start');
    expect(await soh('canceled')).toBe('Run SOH');
    expect(notify.show).not.toHaveBeenCalled();
  });

  it('ignores runs in other experiments', async () => {
    const { soh } = await opened();

    await soh('start', 'other');
    expect(await soh('success', 'other')).toBe('Run SOH');
    expect(axios.get).not.toHaveBeenCalled();
  });
});

describe('downloading the graph', () => {
  // The page attached to the document, so that it draws the load it is
  // given in the layered layout, and a function that downloads it in a format.
  async function drawn(state = {}) {
    localStorage.setItem(LAYOUT_STORAGE_KEY, 'layered');
    const page = await mountComponent(StateOfHealth, {
      route: '/soh/demo',
      attachTo: document.body,
    });
    // a circle per node once it is drawn
    const load = (more) => {
      const state = { started: true, ...sohFixture(), ...more };
      loaderSpy.options.apply(state);
      return vi.waitFor(() =>
        expect(document.querySelectorAll('#graph circle')).toHaveLength(
          state.nodes.length,
        ),
      );
    };
    await load(state);
    const download = async (format) => {
      const item = page
        .findAll('.soh-download-item')
        .find((i) => i.find('b').text() == format);
      await item.trigger('click');
      // the exporters load on first use
      await vi.waitFor(() => expect(saver.saveAs).toHaveBeenCalled());
      return saver.saveAs.mock.calls[0];
    };
    return Object.assign(download, { page, load });
  }

  it("saves the graph with the experiment's VMs and the positions shown", async () => {
    vi.useFakeTimers({
      now: new Date(2026, 8, 30, 12, 0, 0),
      toFake: ['Date'],
    });
    axios.get.mockResolvedValue({
      data: { vms: [{ name: 'historian', host: 'compute7' }] },
    });

    const [blob, name] = await (await drawn())('GEXF');
    expect(axios.get).toHaveBeenCalledWith('experiments/demo/vms', undefined);
    expect(name).toBe('demo-soh-layered-20260930-120000.gexf');
    const xml = await blob.text();
    expect(xml).toContain('experiment demo: 19 VMs and 4 VLANs');
    expect(xml).toContain('<attvalue for="host" value="compute7"/>');
    // where the layered layout put each node, with Gephi's y axis up
    const { nodes, edges } = sohFixture();
    const { result } = await runLayout('layered', layoutInput(nodes, edges));
    const round = (v) => Math.round(v * 100) / 100;
    for (const n of nodes) {
      const { x, y } = result.positions.get(n.id);
      expect(xml).toMatch(
        new RegExp(
          `<node id="${n.id}"(?:(?!</node>)[^])*<viz:position x="${round(x)}" y="${round(-y)}" z="0"/>`,
        ),
      );
    }
  });

  it('saves it without VM details the user may not read', async () => {
    axios.get.mockRejectedValue(new Error('forbidden'));

    const [blob] = await (await drawn())('GEXF');
    expect(await blob.text()).toContain(
      'VM details (host, addresses, CPUs, memory, disk) not available',
    );
  });

  it('titles the SVG with the experiment, what the filter shows, the layout and the time', async () => {
    const now = new Date(2026, 8, 30, 12, 0, 0);
    vi.useFakeTimers({ now, toFake: ['Date'] });
    // the OS and VLAN icons the export embeds
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(new Blob(['icon']))),
    );
    axios.get.mockResolvedValue({ data: {} });
    const download = await drawn();
    await download.page
      .find('input[type="radio"][value="running"]')
      .setValue(true);
    expect(axios.get).toHaveBeenCalledWith(
      'experiments/demo/soh?statusFilter=running',
      expect.anything(),
    );
    // the server sends only the nodes the filter shows
    await download.load({
      nodes: sohFixture().nodes.filter(
        (n) => n.status === 'running' || n.image === 'switch',
      ),
    });

    const [blob, name] = await download('SVG');
    expect(notify.error).not.toHaveBeenCalled();
    expect(name).toBe('demo-soh-layered-20260930-120000.svg');
    const svg = await blob.text();
    expect(svg).toContain('>State of Health: demo<');
    expect(svg).toContain(
      `>6 VMs · 4 VLANs · filter: Running · layout: Layered · ${now.toLocaleString()}<`,
    );
    expect(svg).not.toContain('/src/assets/imgs/');
  });
});

describe('the graph drawn', () => {
  it('draws into its own view and recolors only its own nodes, whatever else is on the page', async () => {
    // another element with the graph's id, before the page's in the document
    const other = document.createElement('div');
    other.id = 'graph';
    other.innerHTML = '<svg><circle fill="black"></circle></svg>';
    document.body.append(other);
    localStorage.setItem(LAYOUT_STORAGE_KEY, 'layered');
    const page = await mountComponent(StateOfHealth, {
      route: '/soh/demo',
      attachTo: document.body,
    });
    const state = { started: true, ...sohFixture() };
    loaderSpy.options.apply(state);
    const circles = () => page.findAll('#graph circle');
    await vi.waitFor(() => expect(circles()).toHaveLength(state.nodes.length));
    const fills = () => circles().map((c) => c.attributes('fill'));
    const before = fills();

    for (const handler of ws.handlers) {
      handler({
        resource: { type: 'experiment/vm', name: 'demo/fw', action: 'stop' },
      });
    }
    expect(fills()).not.toEqual(before);
    expect(other.innerHTML).toBe('<svg><circle fill="black"></circle></svg>');
  });
});

describe('the layout drawn', () => {
  // the page lays out the graph and stops short of drawing it, as the
  // document has no #graph

  it('draws Force instead of VLAN groups for a graph too big for it, saying why once', async () => {
    localStorage.setItem(LAYOUT_STORAGE_KEY, 'groups');
    const page = await open();

    // two loads of the graph, one notice
    for (let i = 0; i < 2; i++) {
      loaderSpy.options.apply({ started: true, nodes: big, edges: [] });
      await flushPromises();
    }
    const notice =
      'VLAN groups is off above 300 nodes (this graph has 301): it would ' +
      'take many seconds to compute. Showing Force.';
    expect(toasts()).toEqual([{ text: notice, type: 'is-warning' }]);
    expect(layoutHint(page)).not.toBe('Computing layout…');
    // the bar shows the layout drawn, and why
    expect(chosenLayout(page)).toBe('Force');
    expect(layoutNote(page)).toBe('VLAN groups is off above 300 nodes.');
    expect(localStorage.getItem(LAYOUT_STORAGE_KEY)).toBe('groups');
  });

  it('draws VLAN groups by default, and Force without a notice for a graph too big for it', async () => {
    const small = await open({ nodes: sohFixture().nodes });
    expect(layoutButtons(small).find((b) => b.chosen).label).toBe(
      'VLAN groups',
    );
    expect(layoutHint(small)).toBe('Hosts boxed by VLAN (cola.js, default)');
    expect(layoutNote(small)).toBeUndefined();

    const page = await open();
    loaderSpy.options.apply({ started: true, nodes: big, edges: [] });
    await flushPromises();
    expect(toasts()).toEqual([]);
    expect(chosenLayout(page)).toBe('Force');
    expect(layoutHint(page)).toBe('Physics simulation');
    expect(layoutNote(page)).toBe('VLAN groups is off above 300 nodes.');
    // viewing the page picks nothing
    expect(localStorage.getItem(LAYOUT_STORAGE_KEY)).toBeNull();
  });

  // the page from modules loaded afresh, whose layout id fails to load with err
  async function failingLayout(id, err) {
    vi.resetModules();
    const { LAYOUTS } = await import('@/utils/soh/layouts.js');
    vi.spyOn(
      LAYOUTS.find((l) => l.id === id),
      'load',
    ).mockRejectedValue(err);
    const { default: fresh } = await import('@/views/StateOfHealth.vue');
    const { nodes, edges } = sohFixture();
    return open({ nodes, edges, loaded: true }, fresh);
  }

  it('offers a reload when a layout file is gone after a server update', async () => {
    const page = await failingLayout(
      'elk',
      new TypeError(
        'Failed to fetch dynamically imported module: https://h/igor/bb-8/phenix/assets/elk-1a2b.js',
      ),
    );
    await layoutButton(page, 'ELK layered').find('input').setValue(true);
    await vi.waitFor(() => expect(page.find('.soh-stale').exists()).toBe(true));

    expect(page.find('.soh-stale').text()).toContain(UPDATED_MESSAGE);
    expect(page.find('.soh-stale').attributes('role')).toBe('alert');
    expect(notify.error).not.toHaveBeenCalled();
    // the graph uses Force, and the user's pick is kept for the next visit
    expect(layoutButtons(page).find((b) => b.chosen).label).toBe('Force');
    expect(localStorage.getItem(LAYOUT_STORAGE_KEY)).toBe('elk');

    const reload = vi
      .spyOn(window.location, 'reload')
      .mockImplementation(() => {});
    await page.find('.soh-stale button').trigger('click');
    expect(reload).toHaveBeenCalledTimes(1);
    reload.mockRestore();
  });

  it('reports another layout failure and falls back to Force', async () => {
    const page = await failingLayout('radial', new Error('no core router'));
    await layoutButton(page, 'Radial tree').find('input').setValue(true);
    await vi.waitFor(() => expect(notify.error).toHaveBeenCalled());

    expect(notify.error.mock.calls[0][0].message).toBe(
      'The Radial tree layout failed, so the graph uses the Force layout: no core router',
    );
    expect(page.find('.soh-stale').exists()).toBe(false);
    expect(layoutButtons(page).find((b) => b.chosen).label).toBe('Force');
    // the next visit does not fail the same way again
    expect(localStorage.getItem(LAYOUT_STORAGE_KEY)).toBeNull();
  });

  // The page from modules loaded afresh, and the layout libraries it has
  // loaded, in order: none has loaded yet, whichever tests ran before.
  async function freshPage(state) {
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
    return { page: await open(state, fresh), libraries };
  }

  it("loads each layout's library only once that layout is picked", async () => {
    // a VM and its VLAN: the libraries load no faster for a bigger graph
    const { nodes, edges } = sohFixture();
    const [edge] = edges;
    const pair = nodes.filter((n) => [edge.source, edge.target].includes(n.id));
    // the last one picked before, so that picking each one changes it
    localStorage.setItem(LAYOUT_STORAGE_KEY, 'elk');
    const { page, libraries } = await freshPage({
      nodes: pair,
      edges: [edge],
      loaded: true,
    });

    // none with the page
    expect(libraries).toEqual([]);
    for (const [layout, loaded] of [
      ['Force', []],
      ['Radial tree', []],
      ['Layered', ['dagre']],
      ['VLAN groups', ['dagre', 'webcola']],
      ['ELK layered', ['dagre', 'webcola', 'elkjs']],
    ]) {
      await layoutButton(page, layout).find('input').setValue(true);
      await vi.waitFor(() =>
        expect(layoutHint(page)).not.toBe('Computing layout…'),
      );
      await flushPromises();
      expect(libraries, layout).toEqual(loaded);
    }
    // and each layout ran without falling back to Force
    expect(notify.error.mock.calls.map(([e]) => e.message)).toEqual([]);
    expect(layoutButtons(page).find((b) => b.chosen).label).toBe('ELK layered');
    expect(toasts()).toEqual([]);
  });
});
