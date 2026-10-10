// Devices from included topologies (device.includedFrom): shown, moved, and
// otherwise left as their own topology defines them.

import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia, setActivePinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

// The signed-in user, whose role a test may set.
const phenix = vi.hoisted(() => ({ username: 'alice', role: undefined }));

vi.mock('@/store.js', () => ({ usePhenixStore: () => phenix }));

// With authentication off the server answers this user's own lists.
const api = vi.hoisted(() => ({
  createDraft: vi.fn(),
  listDrafts: vi.fn(async () => ({ mine: [], shared: [], published: [] })),
}));

vi.mock('@/builder/api.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, builderApi: { ...actual.builderApi, ...api } };
});

vi.mock('@/builder/idb.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, createDraftStore: () => actual.createMemoryStore() };
});

import {
  inspectorLock,
  inspectorTarget,
  uiSchemaForKind,
} from '@/builder/adapters/forms.js';
import { toFlowNodes } from '@/builder/adapters/vueflow.js';
import { decodeDocument, parseDocument } from '@/builder/decode.js';
import BuilderInspector from '@/components/builder/BuilderInspector.vue';
import {
  addInterface,
  addNetwork,
  addNode,
  canConnect,
  combineIncluded,
  connect,
  documentSummary,
  findNode,
  moveNodes,
  removalKept,
  removalRefusal,
  removeElements,
  removeInterface,
  renameInterface,
  updateNetwork,
  updateNode,
} from '@/builder/model.js';
import {
  buildOutline,
  connectionList,
  outlineLabel,
} from '@/builder/outline.js';
import { builderSchemaV1 } from '@/builder/schema.js';
import { useBuilderStore } from '@/builder/store.js';
import { validateDocument } from '@/builder/validate.js';

import { sampleDocument, tags } from './fixtures.js';

// The sample document with bravo included from topology "shared" and
// connected to the EXP switch, the way generation marks it.
function includedDocument() {
  const sample = sampleDocument();
  const connected = connect(sample.doc, {
    sourceNodeId: sample.bravo.id,
    sourceHandleId: sample.bravo.device.interfaces[0].id,
    targetNodeId: sample.sw.id,
  });
  const doc = {
    ...connected.doc,
    source: { kind: 'topology', name: 'root', includeTopologies: ['shared'] },
    nodes: connected.doc.nodes.map((node) =>
      node.id === sample.bravo.id
        ? { ...node, device: { ...node.device, includedFrom: 'shared' } }
        : node,
    ),
  };

  return {
    ...sample,
    doc,
    bravo: findNode(doc, sample.bravo.id),
    bravoEdge: connected.edge,
  };
}

const errorsOf = (doc) =>
  validateDocument(doc).filter((issue) => issue.level === 'error');

describe('decoding and validation', () => {
  test('a generated document with an included device is accepted', () => {
    const { doc } = includedDocument();

    expect(decodeDocument(doc).nodes).toEqual(doc.nodes);
    expect(() => parseDocument(doc)).not.toThrow();
    // Its own topology's to fix: no unconnected-interface warnings for it.
    expect(
      validateDocument(doc).filter((issue) => issue.message.includes('bravo')),
    ).toEqual([]);
  });

  test('an included device needs a document that includes topologies', () => {
    const { doc } = includedDocument();

    expect(errorsOf({ ...doc, source: { kind: 'manual' } })).toEqual([
      expect.objectContaining({
        path: 'nodes[2].device.includedFrom',
        message:
          'device is included from topology "shared", but the document includes no topologies',
      }),
    ]);

    const spaced = {
      ...doc,
      nodes: doc.nodes.map((node) =>
        node.device?.includedFrom
          ? { ...node, device: { ...node.device, includedFrom: 'a b' } }
          : node,
      ),
    };

    expect(errorsOf(spaced)[0].message).toMatch(
      /must not be blank or contain whitespace/,
    );
  });
});

describe('model', () => {
  test('an included device moves, but keeps its name, spec and interfaces', () => {
    const { doc, bravo } = includedDocument();

    const moved = moveNodes(doc, [{ id: bravo.id, position: { x: 5, y: 6 } }]);
    expect(findNode(moved, bravo.id).position).toEqual({ x: 5, y: 6 });

    const renamed = updateNode(doc, bravo.id, {
      label: 'other',
      device: { hostname: 'other', spec: { general: { hostname: 'other' } } },
      position: { x: 1, y: 2 },
    });
    expect(findNode(renamed, bravo.id).device).toEqual(bravo.device);
    expect(findNode(renamed, bravo.id).position).toEqual({ x: 1, y: 2 });

    const handle = bravo.device.interfaces[0].id;
    expect(addInterface(doc, bravo.id).handle).toBeNull();
    expect(removeInterface(doc, bravo.id, handle)).toBe(doc);
    expect(renameInterface(doc, bravo.id, handle, 'eth9')).toBe(doc);
  });

  test('an included device is never connected differently', () => {
    const { doc, alpha, bravo, sw } = includedDocument();
    const reason =
      'bravo comes from included topology shared, so it is read only here. Change it in shared.';

    expect(
      canConnect(doc, { sourceNodeId: bravo.id, targetNodeId: sw.id }),
    ).toEqual({ valid: false, reason });
    expect(
      canConnect(doc, { sourceNodeId: alpha.id, targetNodeId: bravo.id }),
    ).toEqual({ valid: false, reason });
    expect(
      connect(doc, { sourceNodeId: bravo.id, targetNodeId: sw.id }).error,
    ).toBe(reason);

    // The document's own devices still join the included device's switch.
    const added = addNode(doc, { kind: 'device', hostname: 'charlie' });
    expect(
      connect(added.doc, {
        sourceNodeId: added.node.id,
        targetNodeId: sw.id,
      }).edge,
    ).toBeTruthy();
  });

  test('removing keeps the included device, its connection and its switch', () => {
    const { doc, alpha, bravo, bravoEdge, sw, network } = includedDocument();
    const group = addNode(doc, { kind: 'group', title: 'G' });
    const grouped = {
      ...group.doc,
      nodes: group.doc.nodes.map((node) =>
        node.id === bravo.id ? { ...node, parentId: group.node.id } : node,
      ),
    };

    const next = removeElements(grouped, {
      nodes: grouped.nodes.map((node) => node.id),
      edges: grouped.edges.map((edge) => edge.id),
      networks: [network.id],
    });

    expect(next.nodes.map((node) => node.id).sort()).toEqual(
      [bravo.id, sw.id].sort(),
    );
    expect(next.edges.map((edge) => edge.id)).toEqual([bravoEdge.id]);
    expect(next.networks).toEqual(doc.networks);
    // Its group went, so it left the group rather than dangle.
    expect(findNode(next, bravo.id)).not.toHaveProperty('parentId');
    expect(findNode(next, alpha.id)).toBeUndefined();
    expect(errorsOf(next)).toEqual([]);
  });

  test('the network an included device is on keeps its name', () => {
    const { doc, network } = includedDocument();
    const next = updateNetwork(doc, network.id, {
      name: 'LAN',
      description: 'lab',
    });

    expect(next.networks[0]).toMatchObject({
      name: 'EXP',
      description: 'lab',
    });
  });

  test('an included device left in a deleted nested group joins the group that stays', () => {
    const { doc, bravo } = includedDocument();
    const outer = addNode(doc, { kind: 'group', title: 'Outer' });
    const inner = addNode(outer.doc, {
      kind: 'group',
      title: 'Inner',
      parentId: outer.node.id,
    });
    const nested = {
      ...inner.doc,
      nodes: inner.doc.nodes.map((node) =>
        node.id === bravo.id ? { ...node, parentId: inner.node.id } : node,
      ),
    };
    const selection = { nodes: [inner.node.id] };

    const next = removeElements(nested, selection);

    expect(findNode(next, inner.node.id)).toBeUndefined();
    expect(findNode(next, bravo.id).parentId).toBe(outer.node.id);
    expect(errorsOf(next)).toEqual([]);
    // The member it keeps counts, although only its group was selected.
    expect(removalKept(nested, selection)).toEqual([
      'bravo comes from included topology shared, so it is read only here. Change it in shared.',
    ]);

    // With both groups gone it has no group left.
    const both = removeElements(nested, { nodes: [outer.node.id] });
    expect(findNode(both, bravo.id)).not.toHaveProperty('parentId');
  });

  test('the summary counts included devices, their connections and what only they use', () => {
    const { doc } = includedDocument();
    // charlie, also included, is alone on a network of its own.
    const services = addNetwork(doc, { name: 'SERVICES' });
    const hub = addNode(services.doc, {
      kind: 'switch',
      networkId: services.network.id,
    });
    const charlie = addNode(hub.doc, {
      kind: 'device',
      hostname: 'charlie',
      interfaces: [{ name: 'eth0' }],
    });
    const connected = connect(charlie.doc, {
      sourceNodeId: charlie.node.id,
      sourceHandleId: charlie.node.device.interfaces[0].id,
      targetNodeId: hub.node.id,
    });
    const next = {
      ...connected.doc,
      nodes: connected.doc.nodes.map((node) =>
        node.id === charlie.node.id
          ? { ...node, device: { ...node.device, includedFrom: 'shared' } }
          : node,
      ),
    };

    // EXP stays the document's own: alpha is on it too.
    expect(documentSummary(next)).toMatchObject({
      devices: 3,
      included: 2,
      switches: 2,
      includedSwitches: 1,
      networks: 2,
      includedNetworks: 1,
      links: 3,
      includedLinks: 2,
    });
  });
});

describe('presentation', () => {
  test('the outline and canvas name where an included device comes from', () => {
    const { doc, bravo, bravoEdge } = includedDocument();

    expect(outlineLabel(doc, bravo)).toBe(
      'Device bravo, from included topology shared, read only, 1 connection, on EXP',
    );

    const row = buildOutline(doc).find((item) => item.id === bravo.id);

    expect(row.includedFrom).toBe('shared');
    // The palette's Disconnect offers its connection, but refuses it.
    expect(
      connectionList(doc).map((link) => [link.name, Boolean(link.locked)]),
    ).toEqual([
      ['alpha (eth0) to EXP', false],
      ['bravo (eth0) to EXP', true],
    ]);
    expect(
      connectionList(doc).find((link) => link.id === bravoEdge.id).locked,
    ).toBe(removalRefusal(doc, 'edges', bravoEdge.id));

    const flow = toFlowNodes(doc).find((node) => node.id === bravo.id);
    expect(flow.data.includedFrom).toBe('shared');
    expect(flow.ariaLabel).toMatch(/from included topology shared, read only/);
  });

  test('the Inspector locks an included device, and the name of its network', () => {
    const { doc, alpha, bravo, sw } = includedDocument();

    expect(inspectorLock(doc, { type: 'node', id: bravo.id })).toEqual({
      all: true,
      fields: [],
      note:
        'Defined by included topology shared, so it is read only here. ' +
        'Change it in shared and import again, or combine the included ' +
        'nodes into a new draft to edit them here. It can still be moved.',
    });

    const network = inspectorLock(doc, { type: 'node', id: sw.id });
    expect(network).toEqual({
      all: false,
      fields: ['name'],
      note:
        'Network EXP cannot be renamed: bravo from included topology shared is on it. ' +
        'Its other fields can still change.',
    });

    const controls = uiSchemaForKind(builderSchemaV1, 'switch', {
      readonly: network.fields,
    }).elements;
    expect(
      controls.find((control) => control.scope === '#/properties/name').options,
    ).toEqual({ readonly: true });
    expect(
      controls.find((control) => control.scope === '#/properties/alias')
        .options,
    ).toBeUndefined();

    expect(inspectorLock(doc, { type: 'node', id: alpha.id }).note).toBe('');
    expect(inspectorTarget(doc, { type: 'node', id: bravo.id }).kind).toBe(
      'device',
    );
  });
});

describe('combining included devices into a new diagram', () => {
  // The diagram as a keep import leaves it: one include read, one not.
  function imported() {
    const fixture = includedDocument();

    return {
      ...fixture,
      doc: {
        ...fixture.doc,
        source: {
          kind: 'topology',
          name: 'root',
          apiVersion: 'phenix.sandia.gov/v1',
          digest: `sha256:${'a'.repeat(64)}`,
          updatedAt: '2026-09-01T00:00:00Z',
          importedAt: '2026-09-02T00:00:00Z',
          includeTopologies: ['shared', 'gone'],
          unresolvedIncludes: ['gone'],
          annotations: { owner: 'ops' },
          warnings: ['included topology "gone" could not be read'],
        },
      },
    };
  }

  test("every included device becomes the diagram's own", () => {
    const { doc, bravo } = imported();
    const combined = combineIncluded(doc, 'root-combined');

    expect(documentSummary(combined).included).toBe(0);
    expect(findNode(combined, bravo.id).device).not.toHaveProperty(
      'includedFrom',
    );
    // It is a device like any other now: it can be renamed.
    const renamed = updateNode(combined, bravo.id, {
      device: { hostname: 'charlie' },
    });
    expect(findNode(renamed, bravo.id).device.hostname).toBe('charlie');
    expect(errorsOf(combined)).toEqual([]);
    expect(() => parseDocument(combined)).not.toThrow();
  });

  test('the new diagram is linked to no config and keeps only the includes that were never read', () => {
    const { doc } = imported();
    const combined = combineIncluded(doc, 'root-combined');

    expect(combined.metadata.id).not.toBe(doc.metadata.id);
    expect(combined.metadata.id).toMatch(/^[0-9a-f-]{36}$/);
    expect(combined.metadata.name).toBe('root-combined');
    expect(combined.source).toEqual({
      kind: 'manual',
      importedAt: '2026-09-02T00:00:00Z',
      includeTopologies: ['gone'],
    });
  });

  test('a diagram whose includes were all read includes nothing afterwards', () => {
    const { doc } = includedDocument();

    expect(combineIncluded(doc, 'root-combined').source).toEqual({
      kind: 'manual',
    });
    // A diagram drawn here has no source, and gets one that says so.
    expect(combineIncluded({ ...doc, source: undefined }, 'x').source).toEqual({
      kind: 'manual',
    });
  });

  test('everything else is copied as it is, and the diagram it came from is left alone', () => {
    const { doc, alpha, bravo } = imported();
    const marked = {
      ...doc,
      metadata: { ...doc.metadata, description: 'Plant floor' },
      scenarios: ['plant-apps'],
      templates: [{ id: alpha.id, name: 'PLC', device: { spec: {} } }],
      icons: { abc: { data: 'AAAA' } },
    };
    const before = structuredClone(marked);
    const combined = combineIncluded(marked, 'root-combined');

    expect(marked).toEqual(before);

    // Everything but what a combined diagram replaces: its identifier and
    // name, its source and its nodes.
    const without = (object, keys) =>
      Object.fromEntries(
        Object.entries(object).filter(([key]) => !keys.includes(key)),
      );
    const rest = (document) => ({
      ...without(document, ['metadata', 'source', 'nodes']),
      metadata: without(document.metadata, ['id', 'name']),
    });
    const { nodes } = combined;
    const { nodes: oldNodes } = marked;

    expect(rest(combined)).toEqual(rest(marked));
    // Only the mark of the included device differs.
    expect(nodes.map((node) => node.id)).toEqual(oldNodes.map((n) => n.id));
    expect(findNode(combined, alpha.id)).toBe(findNode(marked, alpha.id));
    expect(findNode(combined, bravo.id)).toEqual({
      ...bravo,
      device: Object.fromEntries(
        Object.entries(bravo.device).filter(([key]) => key !== 'includedFrom'),
      ),
    });
  });
});

describe('store', () => {
  let store;
  let fixture;

  beforeEach(() => {
    setActivePinia(createPinia());
    store = useBuilderStore();
    fixture = includedDocument();
    store.setDocument(fixture.doc);
  });

  test('deleting only an included device is refused and says why', () => {
    const before = store.doc;

    expect(store.remove({ nodes: [fixture.bravo.id], edges: [] })).toBe(false);
    expect(store.doc).toBe(before);
    expect(store.notice.text).toBe(
      'bravo comes from included topology shared, so it is read only here. Change it in shared.',
    );
    expect(store.announcement).toBe(store.notice.text);
  });

  test('deleting everything keeps what is read only, and says so', () => {
    store.selectAll();
    store.removeSelection();

    expect(store.doc.nodes.map((node) => node.id).sort()).toEqual(
      [fixture.bravo.id, fixture.sw.id].sort(),
    );
    expect(store.announcement).toBe(
      'Deleted alpha and 1 connection. Kept 3 read-only items of included topologies',
    );
    // What stayed is still selected.
    expect(store.selection.nodes.sort()).toEqual(
      [fixture.bravo.id, fixture.sw.id].sort(),
    );
  });

  test('deleting a group counts the read-only members it keeps', () => {
    store.select({ nodes: [fixture.alpha.id, fixture.bravo.id] });
    const group = store.group();

    store.remove({ nodes: [group.id], edges: [] });

    expect(findNode(store.doc, fixture.bravo.id)).not.toHaveProperty(
      'parentId',
    );
    expect(findNode(store.doc, fixture.alpha.id)).toBeUndefined();
    expect(store.announcement).toMatch(
      /^Deleted .*\. Kept 1 read-only item of included topologies$/,
    );
  });

  test('renaming, re-interfacing and renaming the network are refused', () => {
    const before = store.doc;
    const handle = fixture.bravo.device.interfaces[0].id;

    store.updateNode(fixture.bravo.id, { device: { hostname: 'x' } });
    store.addInterface(fixture.bravo.id, {});
    store.removeInterface(fixture.bravo.id, handle);
    store.renameInterface(fixture.bravo.id, handle, 'eth9');
    store.updateNetwork(fixture.network.id, { name: 'LAN' });
    store.removeNetwork(fixture.network.id);

    expect(store.doc).toBe(before);
    expect(store.notice.text).toBe(
      'Network EXP cannot be removed: bravo from included topology shared is on it.',
    );

    // Moving is the diagram's own, and so is the rest of the network.
    store.updateNode(fixture.bravo.id, { position: { x: 9, y: 9 } });
    store.updateNetwork(fixture.network.id, { name: 'EXP', alias: 7 });
    expect(findNode(store.doc, fixture.bravo.id).position).toEqual({
      x: 9,
      y: 9,
    });
    expect(store.doc.networks[0].alias).toBe(7);
  });
});

describe('Combine included nodes into a new draft', () => {
  let store;
  let fixture;

  beforeEach(() => {
    vi.clearAllMocks();
    setActivePinia(createPinia());
    store = useBuilderStore();
    fixture = includedDocument();
    store.setDocument({
      ...fixture.doc,
      metadata: { ...fixture.doc.metadata, name: 'Riverside water' },
      source: {
        kind: 'topology',
        name: 'root',
        includeTopologies: ['shared', 'site-b'],
        unresolvedIncludes: ['site-b'],
      },
    });
    store.sources = {
      images: [],
      scenarios: [],
      experiments: [],
      topologies: [{ name: 'root' }, { name: 'Riverside-water-combined' }],
    };
    api.createDraft.mockImplementation(async (request) => ({
      draft: { id: 'd9', owner: 'alice' },
      document: request.document,
      history: null,
      cursor: 0,
      etag: '"1"',
    }));
  });

  test('makes a draft linked to no config, named so that no stored topology has the name', async () => {
    const open = store.doc;
    const created = await store.combineIncluded();

    expect(created).toBeTruthy();

    const [sent] = api.createDraft.mock.calls[0];

    // "Riverside water" as a config name, and a topology has "-combined".
    expect(sent.title).toBe('Riverside-water-combined-2');
    expect(sent.sourceToken).toBe('');
    expect(sent.document.metadata.name).toBe('Riverside-water-combined-2');
    expect(sent.document.metadata.id).not.toBe(open.metadata.id);
    expect(sent.document.source).toEqual({
      kind: 'manual',
      includeTopologies: ['site-b'],
    });
    expect(documentSummary(sent.document).included).toBe(0);

    // The new draft is the one open, and it can be edited.
    expect(store.draftId).toBe('d9');
    expect(store.doc.metadata.name).toBe('Riverside-water-combined-2');
    expect(store.summary.included).toBe(0);
    store.updateNode(fixture.bravo.id, { device: { hostname: 'charlie' } });
    expect(findNode(store.doc, fixture.bravo.id).device.hostname).toBe(
      'charlie',
    );
  });

  // Combining the same diagram again must not make a second draft of the
  // same name: both would propose one topology name at Publish.
  test('names the draft so that no draft of mine has the title either', async () => {
    api.listDrafts.mockResolvedValueOnce({
      mine: [
        { id: 'd1', owner: 'alice', title: 'Riverside water' },
        { id: 'd2', owner: 'alice', title: 'Riverside-water-combined-2' },
      ],
      shared: [{ id: 'd3', owner: 'bob', title: 'Riverside-water-combined-3' }],
      published: [],
    });

    await store.combineIncluded();

    const [sent] = api.createDraft.mock.calls[0];

    // A topology has "-combined" and a draft of mine "-combined-2".
    // Another user's draft takes no name of mine.
    expect(sent.title).toBe('Riverside-water-combined-3');
    expect(sent.document.metadata.name).toBe('Riverside-water-combined-3');

    // When the drafts cannot be read, those listed last say.
    api.listDrafts.mockRejectedValueOnce(new Error('offline'));
    api.createDraft.mockClear();
    store.setDocument({
      ...fixture.doc,
      metadata: { ...fixture.doc.metadata, name: 'Riverside water' },
      source: { kind: 'topology', name: 'root', includeTopologies: ['shared'] },
    });

    await store.combineIncluded();

    expect(api.createDraft.mock.calls[0][0].title).toBe(
      'Riverside-water-combined-3',
    );
  });

  test('says what was combined, and which includes the new draft keeps', async () => {
    await store.combineIncluded();

    expect(store.announcement).toBe(
      'Combined 1 included node into new draft Riverside-water-combined-2. ' +
        'Draft Riverside water is unchanged. ' +
        'It still includes site-b, whose nodes are not in the diagram.',
    );
  });

  test('a draft that cannot be made is reported, and nothing says it was', async () => {
    api.createDraft.mockRejectedValueOnce({
      response: { status: 403, data: { message: 'forbidden' } },
    });

    await expect(store.combineIncluded()).resolves.toBeNull();
    expect(store.error).toMatch(/^Could not create the draft\./);
    expect(store.announcement).not.toMatch(/^Combined/);
  });
});

// A role that may make drafts, and one that may only look.
const CREATOR = {
  name: 'included-test-creator',
  policies: [
    { resources: ['configs'], resourceNames: ['*'], verbs: ['list', 'create'] },
  ],
};
const VIEWER = {
  name: 'included-test-viewer',
  policies: [{ resources: ['configs'], resourceNames: ['*'], verbs: ['list'] }],
};

// Renders the whole Inspector for the included device bravo, or for
// another node of the same diagram.
async function renderIncludedInspector({
  readOnly = false,
  role,
  select = 'bravo',
} = {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(BuilderInspector) });
  app.use(pinia);

  const store = useBuilderStore(pinia);
  const fixture = includedDocument();
  store.doc = fixture.doc;
  store.readOnly = readOnly;
  store.select({ nodes: [fixture[select].id] });
  phenix.role = role;

  try {
    return await renderToString(app);
  } finally {
    phenix.role = undefined;
  }
}

describe('Inspector of an included device', () => {
  test('its fields are read only, not disabled, and its lists have no buttons', async () => {
    const html = await renderIncludedInspector();
    // Its position is the diagram's own, so it can still be changed.
    const fields = ['input', 'select', 'textarea']
      .flatMap((name) => tags(html, name, { skipDialogs: true }))
      .filter((tag) => !tag.includes('id="inspector-position-'));

    expect(html).toContain('data-testid="inspector-included-note"');
    expect(fields.length).toBeGreaterThan(3);
    // Tab reaches every field, and its value is read at full contrast.
    expect(fields.filter((tag) => /\sdisabled\b/.test(tag))).toEqual([]);
    // A select cannot be read only, so it shows its choice as text.
    expect(tags(html, 'select', { skipDialogs: true })).toEqual([]);
    expect(
      fields.filter(
        (tag) => !/\sreadonly\b/.test(tag) && !/aria-readonly="true"/.test(tag),
      ),
    ).toEqual([]);
    expect(html).toContain('value="bravo"');
    // Nothing can be added to, removed from or moved in its lists.
    expect(html).not.toMatch(/aria-label="(Remove|Move) /);
    expect(html).not.toMatch(/>\s*Add (interface|drive|connection point)/);
  });

  test('in a read-only draft its fields are locked like the rest', async () => {
    const html = await renderIncludedInspector({ readOnly: true });
    const fields = ['input', 'select', 'textarea'].flatMap((name) =>
      tags(html, name, { skipDialogs: true }),
    );

    expect(fields.length).toBeGreaterThan(0);
    expect(fields.filter((tag) => /\sdisabled\b/.test(tag))).toEqual([]);
    expect(
      fields.filter((tag) => !/\sreadonly\b|aria-readonly="true"/.test(tag)),
    ).toEqual([]);
  });

  test('a role that may create drafts is offered the combined draft, under the note', async () => {
    const html = await renderIncludedInspector({ role: CREATOR });
    const [button] = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="inspector-combine"'),
    );

    expect(button).toContain('type="button"');
    expect(button).toContain('aria-describedby="inspector-included-note"');
    expect(html).toMatch(
      /data-testid="inspector-combine"[^>]*>\s*Combine into a new draft\s*</,
    );
    expect(html.indexOf('id="inspector-included-note"')).toBeLessThan(
      html.indexOf('data-testid="inspector-combine"'),
    );
    // A view-only draft is not changed by it, so it is offered there too.
    expect(
      await renderIncludedInspector({ role: CREATOR, readOnly: true }),
    ).toContain('data-testid="inspector-combine"');
  });

  test('no one else is, and no other node offers it', async () => {
    expect(await renderIncludedInspector()).not.toContain('inspector-combine');
    expect(await renderIncludedInspector({ role: VIEWER })).not.toContain(
      'inspector-combine',
    );
    // The switch an included device is on has a note, but nothing to combine.
    const network = await renderIncludedInspector({
      role: CREATOR,
      select: 'sw',
    });

    expect(network).toContain('data-testid="inspector-included-note"');
    expect(network).not.toContain('inspector-combine');
    expect(
      await renderIncludedInspector({ role: CREATOR, select: 'alpha' }),
    ).not.toContain('inspector-combine');
  });
});
