// The State of Health layout registry and the layouts themselves.
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  DEFAULT_LAYOUT,
  GROUPS_MAX_NODES,
  LAYOUT_STORAGE_KEY,
  layoutOptions,
  readLayoutPref,
  resolveLayout,
  runLayout,
  saveLayoutPref,
} from '@/utils/soh/layouts.js';
import { layoutInput } from '@/utils/soh/graph.js';
import { blockedStorage, memoryStorage } from './helpers/storage.js';
import { sohFixture } from './sohFixture.js';

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
  it('offers the five layouts with force as the default', () => {
    expect(layoutOptions(0).map((l) => l.id)).toEqual([
      'force',
      'radial',
      'layered',
      'groups',
      'elk',
    ]);
    expect(DEFAULT_LAYOUT).toBe('force');
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

  it('draws the default instead of an unknown or too-big choice', () => {
    expect(resolveLayout('nope', 10)).toEqual({ id: 'force', notice: '' });
    expect(resolveLayout('groups', 10)).toEqual({ id: 'groups', notice: '' });
    const r = resolveLayout('groups', 1000);
    expect(r.id).toBe('force');
    expect(r.notice).toMatch(/Showing Force/);
  });
});

describe('layout preference', () => {
  it('remembers the choice', () => {
    const storage = memoryStorage();
    vi.stubGlobal('localStorage', storage);
    expect(readLayoutPref()).toBe('force');
    saveLayoutPref('elk');
    expect(storage.getItem(LAYOUT_STORAGE_KEY)).toBe('elk');
    expect(readLayoutPref()).toBe('elk');
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
    expect(readLayoutPref()).toBe('force');
    expect(() => saveLayoutPref('elk')).not.toThrow();
  });
});

describe('layouts', () => {
  // the layout's output, as the page gets it; a failure fails the test
  // rather than falling back to force
  async function layout(id) {
    const run = await runLayout(id, input, {
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
