// The State of Health layout registry and the layouts themselves.
import BundledELK from 'elkjs/lib/elk.bundled.js';
import fc from 'fast-check';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  DEFAULT_LAYOUT,
  FALLBACK_LAYOUT,
  GROUPS_MAX_NODES,
  LAYOUTS,
  LAYOUT_STORAGE_KEY,
  OLD_LAYOUT_STORAGE_KEY,
  hasLayoutPref,
  layoutOptions,
  readLayoutPref,
  resolveLayout,
  runLayout,
  saveLayoutPref,
} from '@/utils/soh/layouts.js';
import { layoutInput } from '@/utils/soh/graph.js';
import { blockedStorage, memoryStorage } from './helpers/storage.js';
import { sohFixture } from './helpers/sohFixture.js';

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const soh = sohFixture();
const input = layoutInput(soh.nodes, soh.edges);

// A registry none of whose layouts has loaded yet (the registry remembers
// what it loaded), with each layout's load() spied on.
async function freshRegistry() {
  vi.resetModules();
  const registry = await import('@/utils/soh/layouts.js');
  const load = Object.fromEntries(
    registry.LAYOUTS.map((l) => [l.id, vi.spyOn(l, 'load')]),
  );
  return { ...registry, load };
}

describe('layout registry', () => {
  it('offers the five layouts with VLAN groups as the default and force as the fallback', () => {
    expect(layoutOptions(0).map((l) => l.id)).toEqual([
      'force',
      'radial',
      'layered',
      'groups',
      'elk',
    ]);
    expect(DEFAULT_LAYOUT).toBe('groups');
    expect(FALLBACK_LAYOUT).toBe('force');
    // the hint marks the default, and only it
    expect(
      layoutOptions(0)
        .filter((l) => l.hint.includes('default'))
        .map((l) => l.id),
    ).toEqual(['groups']);
  });

  it('loads a layout module when it is first asked for, and only then', async () => {
    const { runLayout, load } = await freshRegistry();
    expect((await runLayout('radial', input)).id).toBe('radial');
    await runLayout('radial', input);

    expect(load.radial).toHaveBeenCalledTimes(1);
    for (const id of ['force', 'layered', 'groups', 'elk']) {
      expect(load[id]).not.toHaveBeenCalled();
    }
  });

  it('falls back to force and reports an unknown layout', async () => {
    const onError = vi.fn();
    const run = await runLayout('nope', input, { onError });
    expect(run.id).toBe('force');
    expect(onError).toHaveBeenCalledWith(
      expect.objectContaining({ message: 'unknown layout "nope"' }),
      'nope',
    );
  });

  it('falls back to force and reports the error when a layout fails to load', async () => {
    const { load, runLayout } = await freshRegistry();
    load.radial.mockRejectedValueOnce(new Error('chunk failed'));
    const onError = vi.fn();

    const run = await runLayout(
      'radial',
      { nodes: [], edges: [] },
      { onError },
    );
    expect(run.id).toBe('force');
    expect(run.result).toBeNull();
    expect(onError).toHaveBeenCalledWith(
      expect.objectContaining({ message: 'chunk failed' }),
      'radial',
    );

    // the next pick tries again
    expect((await runLayout('radial', input)).id).toBe('radial');
    expect(load.radial).toHaveBeenCalledTimes(2);
  });

  it('leaves a layout file gone after a server update to the page, not the global notice', async () => {
    const { load, runLayout } = await freshRegistry();
    const { handlePreloadError } = await import('@/utils/staleChunk.js');
    const gone = new TypeError(
      'Failed to fetch dynamically imported module: /assets/elk-1a2b.js',
    );
    const notify = vi.fn();
    const seen = [];
    // as Vite reports a failed import() before the import rejects
    load.elk.mockImplementationOnce(() =>
      Promise.reject(gone).catch((err) => {
        seen.push(handlePreloadError(err, null, { notify }));
        throw err;
      }),
    );
    const onError = vi.fn();

    expect((await runLayout('elk', input, { onError })).id).toBe('force');
    expect(onError).toHaveBeenCalledWith(gone, 'elk');
    expect(seen).toEqual([null]);
    expect(notify).not.toHaveBeenCalled();
  });

  it('falls back when a layout computes no positions', async () => {
    const { load, runLayout } = await freshRegistry();
    load.layered.mockResolvedValueOnce({ kind: 'static', compute: () => ({}) });
    const onError = vi.fn();

    const run = await runLayout(
      'layered',
      { nodes: [], edges: [] },
      { onError },
    );
    expect(run.id).toBe('force');
    expect(onError).toHaveBeenCalledWith(
      expect.objectContaining({
        message: 'layout "layered" returned no positions',
      }),
      'layered',
    );
  });

  it('disables the VLAN groups layout above its node cap, with a reason', () => {
    const small = layoutOptions(GROUPS_MAX_NODES);
    expect(small.find((o) => o.id === 'groups').disabled).toBe(false);
    const big = layoutOptions(GROUPS_MAX_NODES + 1);
    const groups = big.find((o) => o.id === 'groups');
    expect(groups.disabled).toBe(true);
    expect(groups.reason).toMatch(/300 nodes/);
    expect(big.filter((o) => o.disabled).map((o) => o.id)).toEqual(['groups']);
  });

  it('draws the default for an unknown choice and force for a too-big one', () => {
    expect(resolveLayout('nope', 10)).toEqual({ id: 'groups', notice: '' });
    expect(resolveLayout('nope', 1000).id).toBe('force');
    expect(resolveLayout('groups', 10)).toEqual({ id: 'groups', notice: '' });
    const r = resolveLayout('groups', 1000);
    expect(r.id).toBe('force');
    expect(r.notice).toMatch(/Showing Force/);
  });

  it('draws a choice exactly when the selector offers it', () => {
    const id = fc.oneof(
      fc.constantFrom(...LAYOUTS.map((l) => l.id)),
      fc.string(),
    );
    const count = fc.oneof(
      fc.nat(5000),
      fc.integer({ min: -2, max: 2 }).map((d) => GROUPS_MAX_NODES + d),
    );
    fc.assert(
      fc.property(id, count, (choice, nodes) => {
        const options = layoutOptions(nodes);
        expect(options.filter((o) => o.disabled).map((o) => o.id)).toEqual(
          nodes > GROUPS_MAX_NODES ? ['groups'] : [],
        );
        const offered = options.find((o) => o.id === choice);
        expect(resolveLayout(choice, nodes)).toEqual(
          offered?.disabled
            ? { id: 'force', notice: `${offered.reason}. Showing Force.` }
            : nodes > GROUPS_MAX_NODES && !offered
              ? {
                  id: 'force',
                  notice: `${options.find((o) => o.id === 'groups').reason}. Showing Force.`,
                }
              : { id: offered ? choice : 'groups', notice: '' },
        );
      }),
    );
  });
});

describe('layout preference', () => {
  it('remembers the choice', () => {
    const storage = memoryStorage();
    vi.stubGlobal('localStorage', storage);
    expect(readLayoutPref()).toBe('groups');
    expect(hasLayoutPref()).toBe(false);
    saveLayoutPref('elk');
    expect(storage.getItem(LAYOUT_STORAGE_KEY)).toBe('elk');
    expect(readLayoutPref()).toBe('elk');
    expect(hasLayoutPref()).toBe(true);
  });

  it.each([
    ['drops force, which was also stored when a layout failed', 'force', null],
    ['keeps another layout picked before', 'elk', 'elk'],
    ['drops an unknown value', 'bogus', null],
  ])('moves the old choice once: %s', (_, old, kept) => {
    const storage = memoryStorage();
    vi.stubGlobal('localStorage', storage);
    storage.setItem(OLD_LAYOUT_STORAGE_KEY, old);

    expect(readLayoutPref()).toBe(kept ?? 'groups');
    expect(storage.getItem(OLD_LAYOUT_STORAGE_KEY)).toBeNull();
    expect(storage.getItem(LAYOUT_STORAGE_KEY)).toBe(kept);

    // a choice made since is kept
    saveLayoutPref('radial');
    expect(readLayoutPref()).toBe('radial');
  });

  it('keeps a newer choice over the old one', () => {
    const storage = memoryStorage();
    vi.stubGlobal('localStorage', storage);
    storage.setItem(OLD_LAYOUT_STORAGE_KEY, 'elk');
    storage.setItem(LAYOUT_STORAGE_KEY, 'layered');
    expect(readLayoutPref()).toBe('layered');
    expect(storage.getItem(OLD_LAYOUT_STORAGE_KEY)).toBeNull();
  });

  it('reads back any saved choice it knows, and the default for the rest', () => {
    const known = LAYOUTS.map((l) => l.id);
    fc.assert(
      fc.property(fc.oneof(fc.constantFrom(...known), fc.string()), (id) => {
        vi.stubGlobal('localStorage', memoryStorage());
        saveLayoutPref(id);
        expect(readLayoutPref()).toBe(known.includes(id) ? id : 'groups');
      }),
    );
  });

  it.each([
    [
      'an unknown value',
      () => {
        const storage = memoryStorage();
        storage.setItem(LAYOUT_STORAGE_KEY, 'bogus');
        return storage;
      },
    ],
    ['storage that throws', blockedStorage],
    ['no storage', () => undefined],
  ])('draws the default over %s', (_, storage) => {
    vi.stubGlobal('localStorage', storage());
    expect(readLayoutPref()).toBe('groups');
    expect(() => saveLayoutPref('elk')).not.toThrow();
  });
});

describe('layouts', () => {
  // the layout's output, as the page gets it; a failure fails the test
  // rather than falling back to force
  async function layout(id, graph = input) {
    const run = await runLayout(id, graph, {
      onError: (err) => {
        throw err;
      },
    });
    return run.result;
  }

  it.each(['radial', 'layered', 'groups', 'elk'])(
    '%s places every node at a finite, distinct position',
    async (id) => {
      const out = await layout(id);
      expect(out.positions.size).toBe(input.nodes.length);
      const seen = new Set();
      for (const n of input.nodes) {
        const p = out.positions.get(n.id);
        expect(Number.isFinite(p.x) && Number.isFinite(p.y)).toBe(true);
        seen.add(`${Math.round(p.x)},${Math.round(p.y)}`);
      }
      expect(seen.size).toBe(input.nodes.length);
      for (const g of out.groups ?? []) {
        for (const m of g.members) expect(out.positions.has(m)).toBe(true);
      }
    },
  );

  // A graph as GET /soh gives it: VMs, routers and firewalls, each wired to
  // some VLANs; a connected one also has a core router on every VLAN.
  const graphArb = (connected) =>
    fc
      .integer({ min: connected ? 1 : 0, max: 4 })
      .map((vlans) => Array.from({ length: vlans }, (_, i) => 100 + i))
      .chain((vlanIds) =>
        fc.tuple(
          fc.constant(vlanIds),
          fc.array(
            fc.record({
              image: fc.constantFrom('linux', 'linux', 'router', 'firewall'),
              on: fc.subarray(vlanIds, { minLength: connected ? 1 : 0 }),
            }),
            { maxLength: 12 },
          ),
        ),
      )
      .map(([vlanIds, drawn]) => {
        const vms = connected
          ? [...drawn, { image: 'router', on: vlanIds }]
          : drawn;
        const nodes = [
          ...vlanIds.map((id) => ({ id, label: `vlan${id}`, image: 'switch' })),
          ...vms.map(({ image }, id) => ({ id, label: `vm${id}`, image })),
        ];
        const edges = vms.flatMap((vm, i) =>
          vm.on.map((v) => ({ id: `${i}-${v}`, source: i, target: v })),
        );
        return layoutInput(nodes, edges);
      });

  it.each([
    ['radial', true],
    ['radial', false],
    ['layered', false],
    ['groups', false],
    ['elk', false],
  ])(
    '%s places the nodes of any graph at finite, distinct positions (connected: %s)',
    async (id, connected) => {
      await fc.assert(
        fc.asyncProperty(graphArb(connected), async (graph) => {
          const { positions, groups = [] } = await layout(id, graph);
          const seen = new Set();
          for (const n of graph.nodes) {
            const p = positions.get(n.id);
            expect(Number.isFinite(p?.x) && Number.isFinite(p?.y)).toBe(true);
            seen.add(`${Math.round(p.x)},${Math.round(p.y)}`);
          }
          expect(seen.size).toBe(graph.nodes.length);
          for (const g of groups) {
            for (const m of g.members) expect(positions.has(m)).toBe(true);
          }
        }),
        { numRuns: 20 },
      );
    },
  );

  it('radial keeps components it cannot reach apart', async () => {
    const graph = layoutInput(
      [
        { id: 0, label: 'a', image: 'linux' },
        { id: 1, label: 'b', image: 'linux' },
      ],
      [],
    );
    const { positions } = await layout('radial', graph);
    const [a, b] = [0, 1].map((id) => positions.get(id));
    expect(Math.hypot(a.x - b.x, a.y - b.y)).toBeGreaterThan(1);
  });

  it('packs single-homed hosts into VLAN blocks in the layered layouts', async () => {
    for (const id of ['layered', 'elk']) {
      const out = await layout(id);
      // dmz, corp and ot have single-homed hosts; internet has none
      expect(out.groups.map((g) => g.members[0]).sort()).toEqual([
        101, 102, 103,
      ]);
      // corp's nine hosts sit in a 3 by 3 grid under it
      const corp = out.groups.find((g) => g.members[0] === 102).members;
      const hosts = corp.slice(1).map((id) => out.positions.get(id));
      expect(hosts).toHaveLength(9);
      expect(new Set(hosts.map((p) => p.x)).size).toBe(3);
      expect(new Set(hosts.map((p) => p.y)).size).toBe(3);
      // the multi-homed historian is in no block, and its edges are drawn in
      // full
      const historian = input.nodes.find((n) => n.label === 'historian').id;
      for (const g of out.groups) expect(g.members).not.toContain(historian);
      const hEdges = input.edges
        .filter((e) => e.source === historian)
        .map((e) => e.id);
      for (const e of hEdges) expect(out.faintEdges.has(e)).toBe(false);
    }
  });

  it('puts the router that reaches the most with fewest hops in the middle of the radial tree', async () => {
    const out = await layout('radial');
    const rtr = out.positions.get(1);
    expect(Math.hypot(rtr.x, rtr.y)).toBeLessThan(1e-6);
  });

  it('boxes each VLAN in the VLAN groups layout', async () => {
    const out = await layout('groups');
    expect(out.groups.map((g) => g.label).sort()).toEqual([
      'corp',
      'dmz',
      'internet',
      'ot',
    ]);
  });

  it('runs the force layout as a live simulation', async () => {
    const { mod, result } = await runLayout('force', input);
    expect(mod.kind).toBe('simulation');
    expect(result).toBeNull();
    const nodes = input.nodes.map((n) => ({ ...n }));
    const links = input.edges.map((e) => ({ ...e }));
    const sim = mod
      .createSimulation(nodes, links, { width: 600, height: 400 })
      .stop();
    sim.tick(5);
    expect(nodes.every((n) => Number.isFinite(n.x))).toBe(true);
  });
});

describe('the ELK layout in a browser', () => {
  // ELK workers that lay out with ELK in this thread, or that fail to start
  // while broken is set.
  const workers = [];
  let broken = false;

  class FakeWorker {
    constructor(url) {
      this.url = url;
      this.errors = [];
      this.terminated = false;
      workers.push(this);
      if (broken) {
        queueMicrotask(() => {
          for (const listener of this.errors) listener({});
        });
      }
    }

    addEventListener(type, listener) {
      if (type === 'error') this.errors.push(listener);
    }

    postMessage({ id, cmd, graph, layoutOptions }) {
      if (cmd !== 'layout' || broken) return;
      new BundledELK().layout(graph, { layoutOptions }).then(
        (data) => this.onmessage({ data: { id, data } }),
        (error) => this.onmessage({ data: { id, error } }),
      );
    }

    terminate() {
      this.terminated = true;
    }
  }

  afterEach(() => {
    workers.length = 0;
    broken = false;
    vi.unstubAllEnvs();
  });

  // The registry, loaded afresh, as a browser build runs it.
  async function browserRegistry() {
    vi.stubEnv('MODE', 'production');
    vi.stubGlobal('Worker', FakeWorker);
    return freshRegistry();
  }

  it('lays out in one Web Worker from the elkjs worker file, as ELK does in this thread', async () => {
    const rethrow = (err) => {
      throw err;
    };
    const inThread = await runLayout('elk', input, { onError: rethrow });
    const browser = await browserRegistry();

    const run = await browser.runLayout('elk', input, { onError: rethrow });
    expect(run.result).toEqual(inThread.result);
    await browser.runLayout('elk', input, { onError: rethrow });
    expect(workers).toHaveLength(1);
    expect(workers[0].url).toMatch(/elk-worker\.min\.js/);
    expect(workers[0].terminated).toBe(false);
  });

  it('shows Force when the worker cannot start, and starts a new worker for the next layout', async () => {
    const browser = await browserRegistry();
    const onError = vi.fn();
    broken = true;

    expect((await browser.runLayout('elk', input, { onError })).id).toBe(
      'force',
    );
    expect(onError.mock.calls.map(([err, id]) => [err.message, id])).toEqual([
      ['The ELK layout engine could not start.', 'elk'],
    ]);
    expect(workers[0].terminated).toBe(true);

    broken = false;
    expect((await browser.runLayout('elk', input, { onError })).id).toBe('elk');
    expect(workers).toHaveLength(2);
    expect(onError).toHaveBeenCalledTimes(1);
  });
});
