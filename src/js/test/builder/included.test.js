// Devices from included topologies (device.includedFrom): shown, moved, and
// otherwise left as their own topology defines them.

import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia, setActivePinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

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

import { sampleDocument } from './fixtures.js';

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
        'Change it in shared, then import it again from the drafts page. It can still be moved.',
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

// Tags of one element name in rendered HTML, without the closed dialogs the
// oneOf renderers keep for their confirmation.
function tags(html, name) {
  const visible = html.replace(/<dialog[\s\S]*?<\/dialog>/g, '');

  return visible.match(new RegExp(`<${name}\\b[^>]*>`, 'g')) || [];
}

// Renders the whole Inspector for the included device bravo.
async function renderIncludedInspector({ readOnly = false } = {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(BuilderInspector) });
  app.use(pinia);

  const store = useBuilderStore(pinia);
  const { doc, bravo } = includedDocument();
  store.doc = doc;
  store.readOnly = readOnly;
  store.select({ nodes: [bravo.id] });

  return renderToString(app);
}

describe('Inspector of an included device', () => {
  test('its fields are read only, not disabled, and its lists have no buttons', async () => {
    const html = await renderIncludedInspector();
    // Its position is the diagram's own, so it can still be changed.
    const fields = ['input', 'select', 'textarea']
      .flatMap((name) => tags(html, name))
      .filter((tag) => !tag.includes('id="inspector-position-'));

    expect(html).toContain('data-testid="inspector-included-note"');
    expect(fields.length).toBeGreaterThan(3);
    // Tab reaches every field, and its value is read at full contrast.
    expect(fields.filter((tag) => /\sdisabled\b/.test(tag))).toEqual([]);
    // A select cannot be read only, so it shows its choice as text.
    expect(tags(html, 'select')).toEqual([]);
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

  test('in a read-only draft its fields are disabled like the rest', async () => {
    const html = await renderIncludedInspector({ readOnly: true });
    const fields = ['input', 'select', 'textarea'].flatMap((name) =>
      tags(html, name),
    );

    expect(fields.filter((tag) => !/\sdisabled\b/.test(tag))).toEqual([]);
  });
});
