import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

import {
  GROUPING_STRATEGIES,
  applyGroups,
  nothingToGroup,
  planGroups,
} from '@/builder/grouping.js';
import {
  DEFAULT_NETWORK_COLORS,
  addNetwork,
  addNode,
  connect,
  createDocument,
  findNode,
  groupNodes,
  sizeOf,
} from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';
import { validateDocument } from '@/builder/validate.js';

// A small plant: every device on MGMT, the turbines and a router on ot, the
// router and a workstation on site, pwds on MGMT alone, and a note.
function plant() {
  let doc = createDocument({ name: 'Plant' });
  const networks = {};
  const switches = {};
  const devices = {};

  for (const [name, color] of [
    ['MGMT', '#2f6fbf'],
    ['ot', '#b05c17'],
    ['site', ''],
  ]) {
    const made = addNetwork(doc, { name, color });

    doc = made.doc;
    networks[name] = made.network;

    const sw = addNode(doc, {
      kind: 'switch',
      networkId: made.network.id,
      position: { x: 0, y: 0 },
    });

    doc = sw.doc;
    switches[name] = sw.node;
  }

  const hosts = {
    'wtg-01': ['ot', 'MGMT'],
    'wtg-02': ['ot', 'MGMT'],
    'WTG-10': ['ot', 'MGMT'],
    'rtr-site': ['ot', 'site', 'MGMT'],
    'ws-01': ['site'],
    pwds: ['MGMT'],
  };

  Object.entries(hosts).forEach(([hostname, joined], index) => {
    const made = addNode(doc, {
      kind: 'device',
      hostname,
      position: { x: index * 40, y: index * 30 },
    });

    doc = made.doc;
    devices[hostname] = made.node;

    for (const network of joined) {
      doc = connect(doc, {
        sourceNodeId: made.node.id,
        targetNodeId: switches[network].id,
      }).doc;
    }
  });

  doc = addNode(doc, { kind: 'note', text: 'hello' }).doc;

  return { doc, networks, switches, devices };
}

const ids = (nodes) => nodes.map((node) => node.id);

describe('Auto-group plans', () => {
  test('offers grouping by network first, then by name', () => {
    expect(GROUPING_STRATEGIES.map((strategy) => strategy.id)).toEqual([
      'network',
      'name',
    ]);
  });

  test('by network: each switch with its devices, each device in its smallest network', () => {
    const { doc, networks, switches, devices } = plant();

    expect(planGroups(doc, 'network')).toEqual([
      {
        title: 'MGMT',
        color: networks.MGMT.color,
        members: [switches.MGMT.id, devices.pwds.id],
      },
      {
        title: 'ot',
        color: networks.ot.color,
        members: ids([
          switches.ot,
          devices['wtg-01'],
          devices['wtg-02'],
          devices['WTG-10'],
        ]),
      },
      {
        title: 'site',
        color: networks.site.color,
        members: ids([switches.site, devices['rtr-site'], devices['ws-01']]),
      },
    ]);

    // A network with no color of its own (as imported) gives its group the
    // palette color its connections are drawn in.
    const uncolored = structuredClone(doc);
    uncolored.networks[2].color = '';

    expect(planGroups(uncolored, 'network')[2].color).toBe(
      DEFAULT_NETWORK_COLORS[2],
    );
  });

  test('by name: devices of a family, switches and one-offs left out', () => {
    const { doc, devices } = plant();
    const planned = planGroups(doc, 'name');

    // The family as the first name writes it; WTG-10 is of it too.
    expect(planned.map((group) => [group.title, group.members])).toEqual([
      ['wtg', ids([devices['wtg-01'], devices['wtg-02'], devices['WTG-10']])],
    ]);
    expect(planned[0].color).toMatch(/^#[0-9a-f]{6}$/);
  });

  test('leaves grouped nodes and existing groups alone, and follows a selection', () => {
    const { doc, switches, devices } = plant();
    const grouped = groupNodes(doc, [devices['wtg-01'].id, switches.ot.id]).doc;

    expect(
      planGroups(grouped, 'network').find((group) => group.title === 'ot'),
    ).toEqual(
      expect.objectContaining({
        members: ids([devices['wtg-02'], devices['WTG-10']]),
      }),
    );

    // Only the selection; the note and one selected node make no group.
    const selection = ids([switches.site, devices['ws-01'], devices.pwds]);

    expect(
      planGroups(doc, 'network', { selection }).map((group) => group.members),
    ).toEqual([ids([switches.site, devices['ws-01']])]);
    expect(
      planGroups(doc, 'name', { selection: [devices['wtg-01'].id] }),
    ).toEqual([]);
  });

  test('makes the groups around their members, named apart from existing ones', () => {
    const { doc, devices } = plant();
    const titled = addNode(doc, { kind: 'group', title: 'ot' }).doc;
    const { doc: next, groups } = applyGroups(
      titled,
      planGroups(titled, 'network'),
    );

    expect(groups.map((group) => group.group.title)).toEqual([
      'MGMT',
      'ot 2',
      'site',
    ]);
    expect(groups[1].group.color).toBe('#b05c17');

    for (const group of groups) {
      const members = next.nodes.filter((node) => node.parentId === group.id);
      const box = findNode(next, group.id);

      expect(members.length).toBeGreaterThan(1);
      for (const member of members) {
        expect(member.position.x).toBeGreaterThanOrEqual(box.position.x);
        expect(member.position.x + sizeOf(member).width).toBeLessThanOrEqual(
          box.position.x + box.size.width,
        );
      }
    }

    expect(findNode(next, devices['wtg-01'].id).parentId).toBe(groups[1].id);
    expect(
      validateDocument(next).filter((entry) => entry.level === 'error'),
    ).toEqual([]);
  });

  test('says why there is nothing to group', () => {
    expect(nothingToGroup('network')).toBe(
      'Nothing to group: no ungrouped nodes share a network.',
    );
    expect(nothingToGroup('name', { selected: true })).toBe(
      'Nothing to group: no ungrouped selected devices have similar names.',
    );
  });
});

describe('Auto-group in the store', () => {
  let store;

  beforeEach(() => {
    setActivePinia(createPinia());
    store = useBuilderStore();
    store.setDocument(plant().doc);
  });

  // Whether any two of the boxes overlap.
  function overlapping(boxes) {
    return boxes.some((a, i) =>
      boxes
        .slice(i + 1)
        .some(
          (b) =>
            a.position.x < b.position.x + b.size.width &&
            b.position.x < a.position.x + a.size.width &&
            a.position.y < b.position.y + b.size.height &&
            b.position.y < a.position.y + a.size.height,
        ),
    );
  }

  test('groups, then lays out, as one commit that one undo takes back', async () => {
    const before = store.doc;
    const entries = store.history.size;

    const groups = await store.autoGroup('network');

    expect(groups).toHaveLength(3);
    expect(store.history.size).toBe(entries + 1);
    expect(store.announcement).toBe('Created 3 groups by network');

    const boxes = store.doc.nodes.filter((node) => node.kind === 'group');

    expect(boxes).toHaveLength(3);
    expect(overlapping(boxes)).toBe(false);
    // The draft's layout choice is not changed.
    expect(store.doc.layout).toBeUndefined();

    store.undo();
    expect(store.doc).toEqual(before);
  });

  test('nothing to group is no edit', async () => {
    await store.autoGroup('network');
    const entries = store.history.size;

    expect(await store.autoGroup('network')).toBeNull();
    expect(store.announcement).toBe(
      'Nothing to group: no ungrouped nodes share a network.',
    );
    expect(store.history.size).toBe(entries);

    // With nodes selected, only they are grouped.
    store.select({ nodes: [store.doc.nodes[0].id], edges: [] });
    expect(await store.autoGroup('name')).toBeNull();
    expect(store.announcement).toBe(
      'Nothing to group: no ungrouped selected devices have similar names.',
    );
  });

  test('a read-only draft is not grouped', async () => {
    const doc = store.doc;

    store.readOnly = true;

    expect(await store.autoGroup('name')).toBeNull();
    expect(store.error).toBe('This draft is read only.');
    expect(store.doc).toBe(doc);
  });

  test('a layout under way turns it away, and it turns a layout away', async () => {
    const running = store.layout();

    expect(await store.autoGroup('network')).toBeNull();
    expect(store.announcement).toBe('Auto layout is still running.');
    await running;

    const grouping = store.autoGroup('name');

    expect(store.autoGrouping).toBe(true);
    expect(await store.layout()).toBeNull();
    expect(store.announcement).toBe('Auto-group is still running.');
    expect(await grouping).toHaveLength(1);
    expect(store.autoGrouping).toBe(false);
  });
});
