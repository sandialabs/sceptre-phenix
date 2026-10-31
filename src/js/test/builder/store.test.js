import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { computed, createSSRApp, effectScope, h, nextTick, toRaw } from 'vue';
import { renderToString } from 'vue/server-renderer';

const api = vi.hoisted(() => ({
  listDrafts: vi.fn(async () => ({
    all: [
      { id: 'd1', createdBy: 'alice', name: 'Mine' },
      { id: 'd2', createdBy: 'bob', name: 'Theirs' },
    ],
    published: [],
    damaged: [],
  })),
  // A create answers with the draft alone, as the server does: neither the
  // document it was sent nor a history.
  createDraft: vi.fn(async () => ({
    draft: { id: 'd1', createdBy: 'alice' },
    document: null,
    history: null,
    cursor: 0,
    etag: '"1"',
  })),
  getDraft: vi.fn(async () => ({
    draft: { id: 'd1', createdBy: 'alice' },
    document: null,
    history: [{ id: 's1', summary: 'first' }],
    cursor: 0,
    etag: '"1"',
  })),
  deleteDraft: vi.fn(async () => true),
  cleanUpStorage: vi.fn(async () => ({ drafts: [], documents: 0, content: 0 })),
  // A save answers with the draft alone, as the server does: the snapshot
  // its cursor is on, but no history.
  appendSnapshot: vi.fn(async () => ({
    draft: { id: 'd1', createdBy: 'alice', snapshotId: 's2' },
    history: null,
    cursor: 1,
    etag: '"2"',
  })),
  moveCursor: vi.fn(async () => ({
    draft: { id: 'd1', createdBy: 'alice' },
    history: null,
    etag: '"3"',
  })),
  listSnapshots: vi.fn(async () => [{ id: 's1', summary: 'first' }]),
  getSnapshot: vi.fn(async () => ({ document: null })),
  publish: vi.fn(async () => ({
    result: {
      status: 'succeeded',
      ok: true,
      partial: false,
      stages: [{ name: 'topology', status: 'created', message: '' }],
      failed: [],
      warnings: [],
      errors: [],
      topology: { name: 'core' },
      scenario: null,
      experiment: null,
    },
    etag: '"4"',
  })),
  previewPublish: vi.fn(async () => ({
    changes: null,
    warningIssues: [],
    errorIssues: [],
  })),
  generate: vi.fn(async () => ({ document: null, warnings: ['a warning'] })),
  convertLegacy: vi.fn(async () => ({
    document: null,
    warnings: [],
    source: null,
  })),
  listDocuments: vi.fn(async () => [{ id: 'p1', name: 'Published' }]),
  getDocument: vi.fn(async () => sampleDocument().doc),
  // The document a topology references: here, its Builder file's.
  getTopologyDocument: vi.fn(async (name) => ({
    source: 'file',
    id: `file/${name}`,
    target: name,
    kind: 'Topology',
    config: `Topology/${name}`,
    path: `/phenix/topologies/${name}.builder.json`,
    digest: FILE_DIGEST,
    size: 1024,
    topologyDiffers: false,
    document: sampleDocument().doc,
  })),
  deleteDocument: vi.fn(async () => true),
  getSources: vi.fn(async () => ({
    images: [],
    topologies: ['core'],
    scenarios: [],
    experiments: [],
  })),
  getSchema: vi.fn(async () => ({ $defs: { device: { type: 'object' } } })),
  listDisks: vi.fn(async () => ['ubuntu.qc2']),
  getScenario: vi.fn(async () => ({ apps: [{ name: 'ntp' }] })),
  getConfig: vi.fn(async (kind, name) => ({
    apiVersion: 'phenix.sandia.gov/v2',
    kind,
    metadata: {
      name,
      annotations: { topology: 'plant, mill', owner: 'ops' },
    },
    spec: { apps: [{ name: 'ntp' }] },
  })),
  createConfig: vi.fn(async () => {}),
  updateConfig: vi.fn(async () => {}),
  deleteSnapshot: vi.fn(),
  preflight: vi.fn(async () => ({ checks: [] })),
  // The server's icon library: empty, and taking every upload as it is.
  listIcons: vi.fn(async () => ({ icons: [], maxIcons: 2000 })),
  uploadIcon: vi.fn(async (icon) => ({ icon: { ...icon }, created: true })),
}));

vi.mock('@/builder/api.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, builderApi: api };
});

// The merge as merge.js makes it, which a test can make fail.
vi.mock('@/builder/merge.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, mergeDocuments: vi.fn(actual.mergeDocuments) };
});

// The digest of what the Builder file of the mocked topologies holds.
const FILE_DIGEST = vi.hoisted(() => `sha256:${'f'.repeat(64)}`);

// This device's local drafts, as the in-memory store keeps them. One per
// test, shared by every queue the test opens.
const device = vi.hoisted(() => ({ store: null }));

vi.mock('@/builder/idb.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, createDraftStore: () => device.store };
});

import ImportDialog from '@/components/builder/dialogs/ImportDialog.vue';

import { serverCopyOf } from '@/builder/autosave.js';
import { DUPLICATE_NEEDS_NODES } from '@/builder/clipboard.js';
import { iconLibrary } from '@/builder/iconLibrary.js';
import { mergeDocuments } from '@/builder/merge.js';
import {
  addNetwork,
  addNode,
  connect,
  connectionChanges,
  createDocument,
  findNode,
  groupNodes,
  nodeLayer,
  setDocumentInfo,
  setLayers,
  setNodeLayer,
  STAMP_KEYS,
  ungroup,
  updateNode,
  withStamp,
} from '@/builder/model.js';
import { updateBlocker } from '@/builder/publish.js';
import { builderSchemaV1 } from '@/builder/schema.js';
import { resetSettings, setSetting } from '@/builder/settings.js';
import {
  draftForPublished,
  MERGE_BASE_TIMEOUT_MS,
  useBuilderStore,
} from '@/builder/store.js';
import { useRefusalIssues } from '@/components/builder/dialogs/message.js';

import { sampleDocument } from './fixtures.js';
import { createMemoryStore } from './memory-store.js';
import { base64Of, ICON_DATA, png } from './png.js';

let store;

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  device.store = createMemoryStore();
  store = useBuilderStore();
  store.newDocument({ name: 'Test' });
});

function httpError(status) {
  return Object.assign(new Error(`status ${status}`), {
    response: { status, data: {} },
  });
}

async function withDraft() {
  await store.initAutosave({ draftId: 'd1', etag: '"1"' });
  store.history.currentEntry().serverSnapshotId = 's1';
}

describe('documents', () => {
  test('a new document is wire shaped and has its own history', () => {
    expect(store.doc.$schema).toContain('schemas/builder/v1');
    expect(store.canUndo).toBe(false);
    expect(store.summary.devices).toBe(0);
  });

  test('the notes of the diagram are set in one undo step, and a blank one is dropped', () => {
    const steps = store.history.entries.length;

    expect(store.setDiagramNotes(['First', ' \n', 'Second'])).toBeTruthy();
    expect(store.doc.metadata.notes).toEqual(['First', 'Second']);
    expect(store.history.entries).toHaveLength(steps + 1);
    expect(store.announcement).toMatch(/^Updated diagram notes/);

    // The same notes take no step.
    expect(store.setDiagramNotes(['First', 'Second'])).toBeNull();
    expect(store.history.entries).toHaveLength(steps + 1);

    store.undo();
    expect(store.doc.metadata).not.toHaveProperty('notes');
    store.redo();
    expect(store.doc.metadata.notes).toEqual(['First', 'Second']);

    // Deleting the last note leaves the diagram without notes.
    expect(store.setDiagramNotes(['First'])).toBeTruthy();
    expect(store.setDiagramNotes([])).toBeTruthy();
    expect(store.doc.metadata).not.toHaveProperty('notes');
  });

  test('a note the server would refuse takes no step, and the diagram keeps its notes', () => {
    expect(store.setDiagramNotes(['First'])).toBeTruthy();

    const steps = store.history.entries.length;
    const doc = store.doc;

    for (const refused of ['x'.repeat(4097), 'bell\u0007', 'one\r\ntwo']) {
      expect(store.setDiagramNotes(['First', refused])).toBeNull();
      expect(store.setDiagramNotes([refused])).toBeNull();
    }

    expect(store.doc).toBe(doc);
    expect(store.doc.metadata.notes).toEqual(['First']);
    expect(store.history.entries).toHaveLength(steps);

    // The longest note is written.
    expect(store.setDiagramNotes(['x'.repeat(4096)])).toBeTruthy();
    expect(store.doc.metadata.notes).toEqual(['x'.repeat(4096)]);
  });

  test('a read-only draft keeps its notes', () => {
    store.readOnly = true;

    expect(store.setDiagramNotes(['First'])).toBeNull();
    expect(store.doc.metadata).not.toHaveProperty('notes');
    expect(store.error).toBe('This draft is read only.');
  });

  test('the diagram’s icon size is set in one undo step, named after the size, and Small leaves no key', () => {
    const steps = store.history.entries.length;

    expect(store.setIconSize('large')).toBeTruthy();
    expect(store.doc.iconSize).toBe('large');
    expect(store.history.entries).toHaveLength(steps + 1);
    expect(store.history.undoLabel()).toBe(
      "Changed the diagram's icon size to Large",
    );
    expect(store.announcement).toMatch(
      /^Changed the diagram's icon size to Large/,
    );

    // The size the diagram has takes no step.
    expect(store.setIconSize('large')).toBeNull();
    expect(store.history.entries).toHaveLength(steps + 1);

    store.undo();
    expect(store.doc).not.toHaveProperty('iconSize');
    store.redo();
    expect(store.doc.iconSize).toBe('large');

    expect(store.setIconSize('medium')).toBeTruthy();
    expect(store.history.undoLabel()).toBe(
      "Changed the diagram's icon size to Medium",
    );

    // Small is what a diagram without the key draws.
    expect(store.setIconSize('small')).toBeTruthy();
    expect(store.doc).not.toHaveProperty('iconSize');
    expect(store.history.entries).toHaveLength(steps + 3);
    expect(store.history.undoLabel()).toBe(
      "Changed the diagram's icon size to Small",
    );
    store.undo();
    expect(store.doc.iconSize).toBe('medium');
  });

  test('the diagram’s icon size is refused while a conflict is resolved, and in a read-only draft', () => {
    const steps = store.history.entries.length;
    const doc = store.doc;

    store.resolvingConflict = true;

    expect(store.setIconSize('large')).toBeNull();
    expect(store.doc).toBe(doc);
    expect(store.announcement).toBe(
      'Not changed: the conflict is not resolved yet. Edit again after you resolve it.',
    );

    store.resolvingConflict = false;
    store.readOnly = true;

    expect(store.setIconSize('medium')).toBeNull();
    expect(store.doc).toBe(doc);
    expect(store.doc).not.toHaveProperty('iconSize');
    expect(store.error).toBe('This draft is read only.');
    expect(store.history.entries).toHaveLength(steps);
  });

  test('starting a new document detaches the previous draft queue', () => {
    const dispose = vi.fn();

    store.autosave = { dispose };
    store.queueWork = Promise.resolve();
    store.newDocument({ name: 'Replacement' });

    expect(dispose).toHaveBeenCalledOnce();
    expect(store.autosave).toBeNull();
    expect(store.queueWork).toBeNull();
  });

  test('an unsupported document is refused with a message', () => {
    const result = store.setDocument({ $schema: 'nope', revision: 1 });

    expect(result).toBeNull();
    expect(store.error).toMatch(/schema/i);
  });

  test('a valid document replaces the editor contents', () => {
    const { doc } = sampleDocument();

    expect(store.setDocument(doc)).toBeTruthy();
    expect(store.summary.devices).toBe(2);
    expect(store.canUndo).toBe(false);
  });

  // A draft saved while a renamed network's switch kept its old name
  // opens with the switch named after its network.
  test('a switch still labelled with an old network name is named after its network', () => {
    const { doc, sw } = sampleDocument();
    const stale = {
      ...doc,
      networks: doc.networks.map((network) => ({ ...network, name: 'MGMT' })),
    };

    expect(findNode(stale, sw.id).label).toBe('EXP');
    expect(store.setDocument(stale)).toBeTruthy();
    expect(findNode(store.doc, sw.id).label).toBe('MGMT');
    expect(store.canUndo).toBe(false);
  });

  // A document that is read in keeps a network with no switch.
  test('a network with no switch stays when the document is read in', () => {
    const { doc, sw } = sampleDocument();
    const bare = {
      ...doc,
      nodes: doc.nodes.filter((node) => node.id !== sw.id),
      edges: [],
    };

    expect(store.setDocument(bare)).toBeTruthy();
    expect(store.doc.networks).toHaveLength(1);
  });
});

describe('a network whose last switch is deleted', () => {
  test('goes in the same undo step when nothing else uses it', async () => {
    await withDraft();
    const { doc, network, sw } = sampleDocument();

    store.setDocument(doc);
    const steps = store.history.entries.length;

    expect(store.remove({ nodes: [sw.id], edges: [] })).toBe(true);
    expect(store.doc.networks).toEqual([]);
    expect(store.history.entries).toHaveLength(steps + 1);
    expect(store.announcement).toBe(
      'Deleted EXP, 1 connection and network EXP',
    );

    // One Undo brings back the switch and its network.
    store.undo();
    expect(store.doc.networks.map((entry) => entry.id)).toEqual([network.id]);
    expect(findNode(store.doc, sw.id)).toBeTruthy();
    expect(store.doc.edges).toHaveLength(1);

    store.redo();
    expect(store.doc.networks).toEqual([]);
    expect(findNode(store.doc, sw.id)).toBeUndefined();
  });

  test('stays while a VLAN names it, and the checks report it', async () => {
    await withDraft();
    const { doc, network, sw, bravo } = sampleDocument();
    // bravo has an interface with no connection point on the canvas.
    const named = {
      ...doc,
      nodes: doc.nodes.map((node) =>
        node.id === bravo.id
          ? {
              ...node,
              device: {
                ...node.device,
                spec: {
                  ...node.device.spec,
                  network: {
                    interfaces: [
                      ...node.device.spec.network.interfaces,
                      { name: 'eth9', vlan: 'EXP' },
                    ],
                  },
                },
              },
            }
          : node,
      ),
    };

    store.setDocument(named);
    store.remove({ nodes: [sw.id], edges: [] });

    expect(store.doc.networks.map((entry) => entry.id)).toEqual([network.id]);
    expect(store.announcement).toBe('Deleted EXP and 1 connection');
    expect(
      store.issues.filter((entry) => entry.code === 'network.switch.missing'),
    ).toEqual([expect.objectContaining({ networkId: network.id })]);
  });
});

describe('editing commits', () => {
  // The History object is not reactive. A getter read once must still follow
  // every later edit, undo and redo.
  test('Undo and Redo availability follows the history', async () => {
    await withDraft();
    const undo = computed(() => store.canUndo);
    const redo = computed(() => store.canRedo);

    expect([undo.value, redo.value]).toEqual([false, false]);

    store.addNode({ kind: 'device', hostname: 'alpha' });
    expect([undo.value, redo.value]).toEqual([true, false]);

    store.undo();
    expect([undo.value, redo.value]).toEqual([false, true]);

    store.redo();
    expect([undo.value, redo.value]).toEqual([true, false]);

    store.setDocument(sampleDocument().doc);
    expect([undo.value, redo.value]).toEqual([false, false]);
  });

  test('edits are announced by what they changed, with correct plurals', async () => {
    await withDraft();

    const web = store.addNode({ kind: 'device', hostname: 'web-01' });
    const db = store.addNode({ kind: 'device', hostname: 'db-01' });

    store.select({ nodes: [web.id], edges: [] });
    store.copy();
    expect(store.announcement).toBe('Copied web-01.');

    store.paste();
    expect(store.announcement).toBe('Pasted 1 node');

    store.select({ nodes: [web.id, db.id], edges: [] });
    store.removeSelection();
    expect(store.announcement).toBe('Deleted web-01 and db-01');

    store.select({ nodes: [], edges: [] });
    store.copy();
    expect(store.announcement).toBe('Nothing is selected to copy.');

    // Listing the scenarios the diagram lists already changes nothing.
    const entries = store.history.size;
    expect(store.setScenarios([])).toBeNull();
    expect(store.history.size).toBe(entries);
    expect(store.announcement).toBe('Nothing is selected to copy.');
  });

  test('the scenarios are replaced in one undo step, and stored scenarios read again', async () => {
    await withDraft();

    const entries = store.history.size;

    store.setScenarios(['plant-ntp', 'plant-attack']);
    expect(store.doc.scenarios).toEqual(['plant-ntp', 'plant-attack']);
    expect(store.history.size).toBe(entries + 1);
    expect(store.announcement).toMatch(/^Updated scenarios/);

    store.setScenarios([]);
    expect(store.doc).not.toHaveProperty('scenarios');
    expect(store.history.size).toBe(entries + 2);

    // A scenario stored from the dialog is created, or replaced, and the
    // reading of its apps the Inspector had is dropped.
    const config = {
      apiVersion: 'phenix.sandia.gov/v2',
      kind: 'Scenario',
      metadata: { name: 'plant-ntp' },
      spec: { apps: [] },
    };

    store.storedScenarios['plant-ntp'] = { content: { apps: [] } };
    api.getSources.mockClear();

    await store.saveScenarioConfig(config);
    expect(api.createConfig).toHaveBeenCalledWith(config);
    expect(store.storedScenarios).not.toHaveProperty('plant-ntp');
    expect(api.getSources).toHaveBeenCalled();

    // A replacement keeps the stored scenario's annotations and topologies,
    // which a PUT would drop.
    store.storedScenarios['plant-ntp'] = { content: { apps: [] } };

    await store.saveScenarioConfig(
      {
        ...config,
        metadata: {
          name: 'plant-ntp',
          annotations: { topology: 'mill,dock', owner: 'lab', note: 'new' },
        },
      },
      { replace: true },
    );
    expect(api.getConfig).toHaveBeenCalledWith('Scenario', 'plant-ntp');
    expect(api.updateConfig).toHaveBeenCalledWith({
      ...config,
      metadata: {
        name: 'plant-ntp',
        annotations: {
          topology: 'plant, mill,dock',
          owner: 'lab',
          note: 'new',
        },
      },
    });
    expect(store.storedScenarios).not.toHaveProperty('plant-ntp');

    // A file without annotations keeps the stored ones as they are.
    await store.saveScenarioConfig(config, { replace: true });
    expect(api.updateConfig).toHaveBeenLastCalledWith({
      ...config,
      metadata: {
        name: 'plant-ntp',
        annotations: { topology: 'plant, mill', owner: 'ops' },
      },
    });

    // A stored scenario that cannot be read is not replaced.
    api.updateConfig.mockClear();
    api.getConfig.mockRejectedValueOnce(new Error('not found'));
    await expect(
      store.saveScenarioConfig(config, { replace: true }),
    ).rejects.toThrow('not found');
    expect(api.updateConfig).not.toHaveBeenCalled();

    // The scenario editor's text is the whole config: an exact replacement
    // reads nothing first and keeps nothing of the stored annotations.
    api.getConfig.mockClear();
    store.storedScenarios['plant-ntp'] = { content: { apps: [] } };
    await store.saveScenarioConfig(config, { replace: true, exact: true });
    expect(api.getConfig).not.toHaveBeenCalled();
    expect(api.updateConfig).toHaveBeenLastCalledWith(config);
    expect(store.storedScenarios).not.toHaveProperty('plant-ntp');

    // The server's refusal reaches the dialog.
    api.createConfig.mockRejectedValueOnce(new Error('refused'));
    await expect(store.saveScenarioConfig(config)).rejects.toThrow('refused');
  });

  test('an edit made during a conflict says it is not saved', async () => {
    await withDraft();
    store.saveState = { ...store.saveState, status: 'conflict' };

    store.addNode({ kind: 'note' });

    expect(store.announcement).toBe(
      'Added note. Not saved: this draft changed on the server.',
    );

    // Undo and redo are held back by the conflict too.
    store.undo();
    expect(store.announcement).toBe(
      'Undid Added note. Not saved: this draft changed on the server.',
    );
    store.redo();
    expect(store.announcement).toBe(
      'Redid Added note. Not saved: this draft changed on the server.',
    );
  });

  test('removing items keeps the rest of the selection', async () => {
    await withDraft();

    const a = store.addNode({ kind: 'device', hostname: 'a' });
    const b = store.addNode({ kind: 'device', hostname: 'b' });
    const c = store.addNode({ kind: 'device', hostname: 'c' });

    store.select({ nodes: [a.id, b.id], edges: [] });
    store.remove({ nodes: [c.id], edges: [] });
    expect(store.selection).toEqual({ nodes: [a.id, b.id], edges: [] });

    store.remove({ nodes: [b.id], edges: [] });
    expect(store.selection).toEqual({ nodes: [a.id], edges: [] });

    store.removeSelection();
    expect(store.selection).toEqual({ nodes: [], edges: [] });
    await store.saveNow();
  });

  test('each edit is one history entry and one snapshot', async () => {
    await withDraft();

    store.addNode({ kind: 'device', hostname: 'alpha' });
    store.addNode({ kind: 'switch', networkName: 'EXP' });

    await store.saveNow();

    expect(store.canUndo).toBe(true);
    expect(api.appendSnapshot).toHaveBeenCalledTimes(2);
    expect(api.appendSnapshot.mock.calls[0][1].document.nodes).toHaveLength(1);
    expect(api.appendSnapshot.mock.calls[1][1].document.nodes).toHaveLength(2);
  });

  test('moving several nodes is a single commit', async () => {
    await withDraft();

    const first = store.addNode({ kind: 'device', hostname: 'a' });
    const second = store.addNode({ kind: 'device', hostname: 'b' });

    await store.saveNow();
    api.appendSnapshot.mockClear();

    store.moveNodes([
      { id: first.id, position: { x: 10, y: 10 } },
      { id: second.id, position: { x: 20, y: 20 } },
    ]);

    await store.saveNow();

    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);
  });

  // A click whose pointer wobbled less than a grid step ends as a drag to
  // where the node already is.
  test('a move that leaves every node in place is not an edit', async () => {
    await withDraft();

    const node = store.addNode({ kind: 'device', hostname: 'a' });
    const other = store.addNode({ kind: 'device', hostname: 'b' });
    await store.saveNow();
    api.appendSnapshot.mockClear();
    const entries = store.history.size;
    const announced = store.announcementSeq;

    expect(
      store.moveNodes([{ id: node.id, position: { ...node.position } }]),
    ).toBe(false);
    expect(store.announcementSeq).toBe(announced);
    await store.saveNow();

    expect(store.history.size).toBe(entries);
    expect(api.appendSnapshot).not.toHaveBeenCalled();

    // Only the nodes that moved are named and saved.
    const moved = { x: other.position.x + 16, y: other.position.y };
    expect(
      store.moveNodes([
        { id: node.id, position: { ...node.position } },
        { id: other.id, position: moved },
      ]),
    ).toBe(true);
    expect(store.announcement).toBe('Moved b');
    expect(store.doc.nodes.find((n) => n.id === other.id).position).toEqual(
      moved,
    );
    expect(store.history.size).toBe(entries + 1);

    // Nor is ungrouping a node that is not a group, moving a node to the
    // group it is in, or resizing a node to its size.
    const size = { width: 200, height: 100 };
    store.resizeNode(node.id, size);
    expect(store.announcement).toBe('Resized a to 200 by 100');
    const after = store.history.size;
    const doc = store.doc;

    store.select({ nodes: [node.id], edges: [] });
    store.ungroup();
    expect(store.announcement).toBe('Select a group to ungroup.');
    expect(store.setParent(node.id, null)).toBeNull();
    expect(store.resizeNode(node.id, { ...size })).toBeNull();
    expect(store.doc).toBe(doc);
    expect(store.history.size).toBe(after);
  });

  test('keyboard nudges move the whole selection in one commit', async () => {
    await withDraft();

    const first = store.addNode({ kind: 'device', hostname: 'a' });
    const second = store.addNode({ kind: 'device', hostname: 'b' });

    store.select({ nodes: [first.id, second.id], edges: [] });

    await store.saveNow();
    api.appendSnapshot.mockClear();

    store.nudgeSelection(8, 0);
    await store.saveNow();

    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);
    expect(store.doc.nodes[0].position.x).toBe(first.position.x + 8);
    expect(store.doc.nodes[1].position.x).toBe(second.position.x + 8);
    expect(store.announcement).toBe('Moved 2 nodes right by 8');

    // One node is named, with where it went.
    store.select({ nodes: [first.id], edges: [] });
    store.nudgeSelection(0, -1);
    expect(store.announcement).toBe(
      `Moved a to x ${first.position.x + 8}, y ${first.position.y - 1}`,
    );
  });

  // Where every node is and how big it is, as plain data.
  function geometry(doc) {
    return JSON.parse(
      JSON.stringify(
        doc.nodes.map(({ id, position, size }) => ({ id, position, size })),
      ),
    );
  }

  test('an automatic layout can be put back as one undoable commit', async () => {
    await withDraft();

    const a = store.addNode({
      kind: 'device',
      hostname: 'a',
      position: { x: 500, y: 7 },
    });
    const b = store.addNode({
      kind: 'device',
      hostname: 'b',
      position: { x: 3, y: 300 },
    });
    store.select({ nodes: [a.id, b.id], edges: [] });
    // Layout resizes a group, and restoring it puts the size back too.
    store.group();
    const before = geometry(store.doc);
    const restore = computed(() => store.canRestoreLayout);

    expect(restore.value).toBe(false);

    await store.layout();
    const laidOut = geometry(store.doc);

    expect(laidOut).not.toEqual(before);
    expect(restore.value).toBe(true);

    // The layout's own save does not drop it.
    await vi.waitFor(async () => {
      expect((await store.saveNow()).pending).toBe(0);
    });
    expect(restore.value).toBe(true);
    const entries = store.history.size;

    store.restoreLayout();

    expect(geometry(store.doc)).toEqual(before);
    expect(store.announcement).toBe('Restored previous layout');
    expect(store.history.size).toBe(entries + 1);
    // It is put back once.
    expect(restore.value).toBe(false);

    // ...as one snapshot.
    await vi.waitFor(async () => {
      expect((await store.saveNow()).pending).toBe(0);
    });
    const restored = api.appendSnapshot.mock.calls.filter(
      ([, payload]) => payload.summary === 'Restored previous layout',
    );
    expect(restored).toHaveLength(1);
    expect(geometry(restored[0][1].document)).toEqual(before);

    store.undo();
    expect(store.announcement).toBe('Undid Restored previous layout.');
    expect(geometry(store.doc)).toEqual(laidOut);
    expect(restore.value).toBe(false);
  });

  test('the layout to put back is dropped when the diagram changes', async () => {
    await withDraft();

    store.addNode({
      kind: 'device',
      hostname: 'a',
      position: { x: 500, y: 7 },
    });
    const restore = computed(() => store.canRestoreLayout);

    // Another edit.
    await store.layout();
    expect(restore.value).toBe(true);
    store.addNode({ kind: 'device', hostname: 'b', position: { x: 3, y: 0 } });
    expect(restore.value).toBe(false);

    // Undo, and a redo back to the layout.
    await store.layout();
    store.undo();
    expect(restore.value).toBe(false);
    store.redo();
    expect(restore.value).toBe(false);

    // Panning or zooming is not a change to the diagram.
    store.undo();
    await store.layout();
    store.setViewport({ x: 40, y: 20, zoom: 2 });
    expect(restore.value).toBe(true);

    // A read-only draft offers no restore, and a restore changes nothing.
    store.readOnly = true;
    const entries = store.history.size;
    expect(restore.value).toBe(false);
    expect(store.restoreLayout()).toBeNull();
    expect(store.history.size).toBe(entries);
    expect(store.announcement).toBe('There is no earlier layout to restore.');
    store.readOnly = false;

    // A layout that moves nothing, as after an undo and a redo back to a
    // layout, is not an edit: no undo step, and nothing to put back.
    store.undo();
    store.redo();
    const laidOut = store.history.size;
    expect(await store.layout()).toBeNull();
    expect(store.history.size).toBe(laidOut);
    expect(store.announcement).toBe('The diagram already has this layout.');
    expect(restore.value).toBe(false);

    // Another document.
    store.undo();
    await store.layout();
    expect(restore.value).toBe(true);
    store.setDocument(sampleDocument().doc);
    expect(restore.value).toBe(false);

    await store.layout();
    expect(restore.value).toBe(true);
    store.newDocument({ name: 'Another' });
    expect(restore.value).toBe(false);
  });

  // Past its byte limit the server keeps fewer snapshots than the undo
  // history does, and refuses a move to one it dropped.
  test('undo stops at the oldest snapshot the server keeps', async () => {
    await withDraft();

    for (const [n, kept] of [
      [2, 2],
      [3, 3],
      [4, 2],
    ]) {
      api.appendSnapshot.mockResolvedValueOnce({
        draft: {
          id: 'd1',
          createdBy: 'alice',
          snapshotId: `s${n}`,
          snapshots: kept,
          cursor: kept - 1,
        },
        history: null,
        etag: `"${n}"`,
      });
      store.addNode({ kind: 'device', hostname: `node-${n}` });
      await store.saveNow();
    }

    expect(
      store.history.entries.map((entry) => entry.serverSnapshotId),
    ).toEqual(['s3', 's4']);
    expect(store.canUndo).toBe(true);

    store.undo();

    expect(store.canUndo).toBe(false);
    expect(store.summary.devices).toBe(2);
  });

  test('undo and redo move the server cursor rather than writing a snapshot', async () => {
    await withDraft();

    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.saveNow();
    api.appendSnapshot.mockClear();

    store.undo();
    await store.saveNow();

    expect(api.moveCursor).toHaveBeenCalledWith(
      'd1',
      { snapshotId: 's1' },
      expect.any(String),
    );
    expect(api.appendSnapshot).not.toHaveBeenCalled();

    store.redo();
    await store.saveNow();

    expect(api.moveCursor).toHaveBeenCalledTimes(2);
  });

  test('a read-only draft never queues a snapshot', async () => {
    await withDraft();
    store.readOnly = true;
    const before = store.doc;

    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.saveNow();

    expect(api.appendSnapshot).not.toHaveBeenCalled();
    expect(store.doc).toBe(before);
    expect(store.error).toMatch(/read only/i);
  });

  test('restoring history moves the server cursor without appending', async () => {
    const { doc } = sampleDocument();

    await withDraft();
    store.serverHistory = [
      { id: 's1', summary: 'first' },
      { id: 's2', summary: 'second' },
    ];
    api.getSnapshot.mockResolvedValueOnce({ document: doc });
    api.moveCursor.mockResolvedValueOnce({
      draft: { id: 'd1', createdBy: 'alice', snapshotId: 's2' },
      history: null,
      etag: '"3"',
    });

    await store.restoreSnapshot('s2');

    expect(api.moveCursor).toHaveBeenCalledWith(
      'd1',
      { snapshotId: 's2' },
      '"1"',
    );
    expect(api.appendSnapshot).not.toHaveBeenCalled();
    expect(store.doc.metadata.id).toBe(doc.metadata.id);
    expect(store.etag).toBe('"3"');
  });

  test('history is not restored without a draft, with unsaved changes or for an unknown snapshot', async () => {
    // A new diagram is not a draft yet.
    expect(await store.restoreSnapshot('s2')).toBeNull();
    expect(store.error).toBe('Open a saved draft before restoring history.');

    await withDraft();
    store.serverHistory = [{ id: 's1' }, { id: 's2' }];

    // The save of the latest change is refused, so it is not on the server.
    api.appendSnapshot.mockRejectedValueOnce(httpError(403));
    store.addNode({ kind: 'device', hostname: 'alpha' });
    const edited = store.doc;

    expect(await store.restoreSnapshot('s2')).toBeNull();
    expect(store.error).toMatch(
      /^Your latest changes are not saved yet\. You cannot restore history now\./,
    );
    expect(store.doc).toBe(edited);

    store.newDocument({ name: 'Test' });
    await withDraft();
    store.serverHistory = [{ id: 's1' }];

    expect(await store.restoreSnapshot('s2')).toBeNull();
    expect(store.error).toMatch(
      /^That snapshot is no longer in the draft history/,
    );
    expect(api.getSnapshot).not.toHaveBeenCalled();
    expect(api.moveCursor).not.toHaveBeenCalled();
  });

  test('history is not restored when the snapshot cannot be read', async () => {
    await withDraft();
    store.serverHistory = [{ id: 's1' }, { id: 's2' }];
    api.getSnapshot.mockRejectedValueOnce(httpError(500));

    const before = store.doc;

    expect(await store.restoreSnapshot('s2')).toBeNull();
    expect(store.error).toMatch(/^Could not restore the snapshot\./);
    expect(api.moveCursor).not.toHaveBeenCalled();
    expect(store.doc).toBe(before);
  });

  // A cursor move left in the queue would move the server to the snapshot
  // later, while the editor still shows the diagram as it was.
  test('a cursor move that is not saved is withdrawn, and the diagram stays as it was', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });

    try {
      const { doc } = sampleDocument();

      for (const failure of [new Error('network'), httpError(403)]) {
        store.newDocument({ name: 'Test' });
        await withDraft();
        store.serverHistory = [{ id: 's1' }, { id: 's2' }];
        api.getSnapshot.mockResolvedValueOnce({ document: doc });
        api.moveCursor.mockRejectedValueOnce(failure);

        const before = store.doc;
        const calls = api.moveCursor.mock.calls.length;

        expect(await store.restoreSnapshot('s2'), failure.message).toBeNull();
        expect(store.error).toBe(
          'Could not restore the snapshot: the server did not move the draft to it. Try again.',
        );
        expect(store.doc).toBe(before);
        expect(store.autosave.record.queue).toEqual([]);
        expect(store.saveState.pending).toBe(0);

        // No retry sends it later.
        await vi.runAllTimersAsync();
        expect(api.moveCursor).toHaveBeenCalledTimes(calls + 1);
      }

      // Offline, nothing is left to send, so the draft is saved.
      store.newDocument({ name: 'Test' });
      await withDraft();
      store.serverHistory = [{ id: 's1' }, { id: 's2' }];
      api.getSnapshot.mockResolvedValueOnce({ document: doc });
      api.moveCursor.mockRejectedValueOnce(new Error('network'));
      await store.restoreSnapshot('s2');

      expect(store.saveState.status).toBe('saved');
    } finally {
      vi.useRealTimers();
    }
  });

  test('preflight checks run on the saved draft only', async () => {
    const report = { checks: [] };

    api.preflight.mockResolvedValue(report);

    // A new diagram is not a draft yet.
    expect(await store.runPreflight({ checks: ['images'] })).toEqual({
      error:
        'Preflight checks run on a saved draft. Save this diagram as a draft first.',
    });

    await withDraft();

    expect(
      await store.runPreflight({
        checks: ['images', 'vlans'],
        experiment: 'x',
      }),
    ).toEqual({ report });
    expect(api.preflight).toHaveBeenLastCalledWith('d1', {
      checks: ['images', 'vlans'],
      experiment: 'x',
    });

    // The checks would not see a change the server does not have.
    api.appendSnapshot.mockRejectedValueOnce(httpError(403));
    store.addNode({ kind: 'device', hostname: 'alpha' });
    api.preflight.mockClear();

    expect((await store.runPreflight({ checks: ['images'] })).error).toMatch(
      /^Your latest changes are not saved on the server yet\. The checks would not see them\./,
    );
    expect(api.preflight).not.toHaveBeenCalled();

    // A read-only draft is not saved first.
    store.readOnly = true;
    api.appendSnapshot.mockClear();

    expect(await store.runPreflight({ checks: ['images'] })).toEqual({
      report,
    });
    expect(api.appendSnapshot).not.toHaveBeenCalled();

    api.preflight.mockRejectedValueOnce(httpError(500));

    expect((await store.runPreflight({ checks: ['images'] })).error).toMatch(
      /^Could not run the preflight checks\./,
    );
  });

  // The server's answer to a snapshot delete: the draft, as after a save.
  function deletedAnswer({ snapshotId = 's3', cursor = 1, etag = '"4"' }) {
    return {
      draft: { id: 'd1', createdBy: 'alice', snapshotId, cursor, snapshots: 2 },
      document: null,
      history: null,
      cursor,
      etag,
    };
  }

  test('deleting a snapshot sends the queue’s ETag, takes the answer’s, and undo skips it', async () => {
    await withDraft();
    api.appendSnapshot
      .mockResolvedValueOnce({
        draft: { id: 'd1', createdBy: 'alice', snapshotId: 's2', cursor: 1 },
        history: null,
        cursor: 1,
        etag: '"2"',
      })
      .mockResolvedValueOnce({
        draft: { id: 'd1', createdBy: 'alice', snapshotId: 's3', cursor: 2 },
        history: null,
        cursor: 2,
        etag: '"3"',
      });
    store.addNode({ kind: 'device', hostname: 'alpha' });
    store.addNode({ kind: 'device', hostname: 'beta' });
    await store.saveNow();
    store.serverHistory = [
      { id: 's1' },
      { id: 's2' },
      { id: 's3', current: true },
    ];
    api.deleteSnapshot.mockResolvedValueOnce(deletedAnswer({}));

    await expect(store.deleteSnapshot('s2')).resolves.toEqual({
      deleted: true,
      message: '',
    });

    expect(api.deleteSnapshot).toHaveBeenCalledWith('d1', 's2', '"3"');
    expect(store.etag).toBe('"4"');
    expect(store.autosave.record.etag).toBe('"4"');
    expect(store.autosave.record.serverHead).toEqual({
      snapshotId: 's3',
      cursor: 1,
      snapshots: 2,
    });
    expect(store.historyEtag).toBeNull();
    expect(store.serverHistory.map((entry) => entry.id)).toEqual(['s1', 's3']);
    expect(
      store.history.entries.map((entry) => entry.serverSnapshotId),
    ).toEqual(['s1', 's3']);

    // Undo goes to the snapshot before the one deleted, with the new ETag.
    store.undo();
    await store.saveNow();
    expect(api.moveCursor).toHaveBeenLastCalledWith(
      'd1',
      { snapshotId: 's1' },
      '"4"',
    );
  });

  test('a delete refused for a change made elsewhere reads the draft again, and the next try uses that read', async () => {
    const conflict = Object.assign(new Error('stale'), {
      response: { status: 412, data: {} },
    });

    await withDraft();
    store.serverHistory = [{ id: 's1', current: true }];
    api.deleteSnapshot.mockRejectedValueOnce(conflict);
    // Someone else saved meanwhile: the draft has another head.
    api.getDraft.mockResolvedValueOnce(
      readDraft({}, { etag: '"9"', snapshots: 3 }),
    );

    const refused = await store.deleteSnapshot('s1');

    expect(refused.deleted).toBe(false);
    expect(refused.message).toBe(
      'This draft changed on the server after Builder read its history. The list now shows the latest history. Try again.',
    );
    expect(store.serverHistory.map((entry) => entry.id)).toEqual([
      's1',
      's2',
      's3',
    ]);
    // The queue keeps its own ETag: its next save meets the change.
    expect(store.autosave.record.etag).toBe('"1"');
    expect(store.historyEtag).toBe('"9"');

    api.deleteSnapshot.mockResolvedValueOnce(
      deletedAnswer({ etag: '"10"', snapshotId: 's3', cursor: 1 }),
    );
    await expect(store.deleteSnapshot('s1')).resolves.toMatchObject({
      deleted: true,
    });
    expect(api.deleteSnapshot).toHaveBeenLastCalledWith('d1', 's1', '"9"');
    expect(store.autosave.record.etag).toBe('"1"');
    expect(store.historyEtag).toBe('"10"');
    expect(store.serverHistory.map((entry) => entry.id)).toEqual(['s2', 's3']);
  });

  test('a delete refused for a change that kept the content takes the new ETag for the queue too', async () => {
    api.getDraft.mockResolvedValueOnce(readDraft({}, { snapshots: 2 }));
    await store.loadDraft('d1');
    api.deleteSnapshot.mockRejectedValueOnce(
      Object.assign(new Error('stale'), {
        response: { status: 412, data: {} },
      }),
    );
    // Only its publication changed.
    api.getDraft.mockResolvedValueOnce(
      readDraft(
        { publication: { documentId: 'p1' } },
        { etag: '"5"', snapshots: 2 },
      ),
    );

    expect((await store.deleteSnapshot('s1')).deleted).toBe(false);
    expect(store.etag).toBe('"5"');
    expect(store.autosave.record.etag).toBe('"5"');
    expect(store.historyEtag).toBeNull();
  });

  test('the current snapshot, one gone, and a read-only draft are not deleted', async () => {
    await withDraft();
    const refusal = (status) =>
      Object.assign(new Error('refused'), { response: { status, data: {} } });

    api.deleteSnapshot.mockRejectedValueOnce(refusal(409));
    expect((await store.deleteSnapshot('s1')).message).toBe(
      'That is the current snapshot. You cannot delete it. The list now shows the latest history.',
    );
    api.deleteSnapshot.mockRejectedValueOnce(refusal(404));
    expect((await store.deleteSnapshot('s0')).message).toBe(
      'That snapshot is no longer in the draft history. The list now shows the latest history.',
    );
    api.deleteSnapshot.mockRejectedValueOnce(refusal(403));
    expect((await store.deleteSnapshot('s0')).message).toMatch(
      /^Could not delete the snapshot\. /,
    );
    // Each refusal but the last read the list again.
    expect(api.getDraft).toHaveBeenCalledTimes(2);
    expect(store.error).toBe('');

    api.deleteSnapshot.mockClear();
    store.readOnly = true;
    expect(await store.deleteSnapshot('s0')).toEqual({
      deleted: false,
      message: 'This draft is read only.',
    });
    expect(api.deleteSnapshot).not.toHaveBeenCalled();
  });

  // A save answers without the history, which is kept rather than emptied,
  // so History and a restore still find its snapshots.
  test('a save keeps the server history it does not carry', async () => {
    await withDraft();
    store.serverHistory = [{ id: 's1' }];

    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.saveNow();

    expect(store.serverHistory).toEqual([{ id: 's1' }]);
    expect(store.history.currentEntry().serverSnapshotId).toBe('s2');
  });

  // The History dialog opens at once and shows the read under way, and its
  // failure, itself. The page alert behind it is left alone.
  test('reading the history is marked loading, and fails into the dialog', async () => {
    await withDraft();
    // The next read waits until the function returned is called.
    const held = () => {
      let answer;
      api.listSnapshots.mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            answer = resolve;
          }),
      );

      return (value) => answer(value);
    };

    let release = held();
    const read = store.fetchHistory();
    expect(store.historyLoading).toBe(true);
    release([{ id: 's1' }, { id: 's2' }]);
    await read;
    expect(store.historyLoading).toBe(false);
    expect(store.serverHistory).toEqual([{ id: 's1' }, { id: 's2' }]);

    api.listSnapshots.mockRejectedValueOnce(new Error('Network Error'));
    await store.fetchHistory();
    expect(store.historyLoading).toBe(false);
    expect(store.historyError).toMatch(/^Could not read the draft history\. /);
    expect(store.error).toBe('');
    expect(store.serverHistory).toEqual([{ id: 's1' }, { id: 's2' }]);

    // Only the last read fills the list: one that answers after it, or for
    // a draft no longer open, is dropped.
    release = held();
    const overtaken = store.fetchHistory();
    api.listSnapshots.mockResolvedValueOnce([{ id: 's3' }]);
    await store.fetchHistory();
    expect(store.historyError).toBe('');
    release([{ id: 'old' }]);
    await overtaken;
    expect(store.serverHistory).toEqual([{ id: 's3' }]);

    release = held();
    const moved = store.fetchHistory();
    store.draftId = 'd2';
    release([{ id: 'other' }]);
    await moved;
    expect(store.serverHistory).toEqual([{ id: 's3' }]);
    expect(store.historyLoading).toBe(false);
  });

  test('cached history without pending operations cannot replace server state', async () => {
    await withDraft();
    const serverDocument = store.doc;
    const staleDocument = sampleDocument().doc;

    await store.autosave.attach({
      draftId: 'd1',
      etag: '"0"',
      entries: [{ id: 'stale', label: 'stale', snapshot: staleDocument }],
      cursor: 0,
      queue: [],
    });

    expect(await store.recoverLocalHistory('"2"')).toBe(false);
    expect(store.doc).toBe(serverDocument);
    expect(store.autosave.record.entries).toEqual([]);
    expect(store.autosave.record.etag).toBe('"2"');
  });

  test('pending recovery based on a stale ETag becomes a conflict', async () => {
    await withDraft();
    const pendingDocument = sampleDocument().doc;

    await store.autosave.attach({
      draftId: 'd1',
      etag: '"1"',
      entries: [
        { id: 'pending', label: 'offline edit', snapshot: pendingDocument },
      ],
      cursor: 0,
      queue: [
        {
          opId: 'pending',
          kind: 'snapshot',
          commitId: 'pending',
          label: 'offline edit',
        },
      ],
    });
    api.appendSnapshot.mockClear();

    expect(await store.recoverLocalHistory('"2"')).toBe(true);
    expect(store.saveState.status).toBe('conflict');
    expect(store.doc).toEqual(pendingDocument);
    expect(api.appendSnapshot).not.toHaveBeenCalled();
    expect(store.autosave.record.etag).toBe('"1"');
  });

  test('recovered edits after undo keep the final branch aligned with the server', async () => {
    await withDraft();
    const abandoned = setDocumentInfo(sampleDocument().doc, {
      name: 'Abandoned',
    });
    const final = setDocumentInfo(sampleDocument().doc, { name: 'Final' });

    // As the server answers: the draft alone, without its history.
    api.appendSnapshot
      .mockResolvedValueOnce({
        draft: { snapshotId: 's2' },
        history: null,
        cursor: 1,
        etag: '"2"',
      })
      .mockResolvedValueOnce({
        draft: { snapshotId: 's3' },
        history: null,
        cursor: 1,
        etag: '"4"',
      });
    api.moveCursor.mockResolvedValueOnce({
      draft: { snapshotId: 's1' },
      history: null,
      cursor: 0,
      etag: '"3"',
    });
    await store.autosave.attach({
      draftId: 'd1',
      etag: '"1"',
      entries: [
        { id: 'abandoned', label: 'abandoned', snapshot: abandoned },
        { id: 'final', label: 'final', snapshot: final },
      ],
      cursor: 1,
      queue: [
        { kind: 'snapshot', commitId: 'abandoned', label: 'abandoned' },
        { kind: 'cursor', snapshotId: 's1' },
        { kind: 'snapshot', commitId: 'final', label: 'final' },
      ],
    });

    expect(await store.recoverLocalHistory('"1"')).toBe(true);
    expect({
      document: store.doc.metadata.name,
      saveState: store.saveState,
      index: store.history.index,
      entries: store.history.entries.map((entry) => ({
        id: entry.id,
        name: entry.snapshot.metadata.name,
        serverSnapshotId: entry.serverSnapshotId,
      })),
    }).toEqual({
      document: 'Final',
      saveState: expect.objectContaining({ status: 'saved' }),
      index: 1,
      entries: [
        expect.objectContaining({ name: 'Test', serverSnapshotId: 's1' }),
        expect.objectContaining({ id: 'final', name: 'Final' }),
      ],
    });
    expect(store.history.entries.map((entry) => entry.id)).not.toContain(
      'abandoned',
    );
    expect(api.moveCursor).toHaveBeenCalledWith(
      'd1',
      { snapshotId: 's1' },
      '"2"',
    );
    expect(store.history.entries[1].serverSnapshotId).toBe('s3');

    // Undo goes back to the snapshot the server kept, not the abandoned
    // one it dropped.
    store.undo();
    await store.saveNow();
    expect(api.moveCursor).toHaveBeenLastCalledWith(
      'd1',
      { snapshotId: 's1' },
      '"4"',
    );
    expect(store.saveState.status).toBe('saved');
  });

  // The server holds a save left queued under its operation id: it is not
  // sent again nor a conflict, and the rest of the queue goes on.
  test('a recovered save the server already holds is not a conflict', async () => {
    const sent = createDocument({ name: 'Sent' });
    const later = setDocumentInfo(sent, { name: 'Later' });

    await device.store.put(
      {
        key: 'alice::d1',
        actor: 'alice',
        draftId: 'd1',
        etag: '"1"',
        entries: [
          { id: 'c1', label: 'Sent' },
          { id: 'c2', label: 'Later' },
        ],
        queue: [
          { opId: 'c1', kind: 'snapshot', commitId: 'c1', label: 'Sent' },
          { opId: 'c2', kind: 'snapshot', commitId: 'c2', label: 'Later' },
        ],
      },
      {
        write: [
          { id: 'c1', snapshot: sent },
          { id: 'c2', snapshot: later },
        ],
      },
    );
    api.getDraft.mockResolvedValueOnce({
      draft: { id: 'd1', createdBy: 'alice' },
      document: sent,
      history: [
        { id: 's1', current: false },
        { id: 's2', opId: 'c1', current: true },
      ],
      cursor: 1,
      etag: '"2"',
    });
    api.appendSnapshot.mockResolvedValueOnce({
      draft: { id: 'd1', createdBy: 'alice', snapshotId: 's3' },
      history: null,
      etag: '"3"',
    });

    await store.loadDraft('d1');
    await store.saveNow();

    expect(store.hasConflict).toBe(false);
    expect(api.appendSnapshot).toHaveBeenCalledOnce();
    expect(api.appendSnapshot).toHaveBeenCalledWith(
      'd1',
      expect.objectContaining({ summary: 'Later', opId: 'c2' }),
      '"2"',
    );
    expect(store.doc.metadata.name).toBe('Later');
    expect(store.saveState).toMatchObject({ status: 'saved', pending: 0 });
    expect(await device.store.all()).toEqual([]);
  });
});

describe('connections', () => {
  test('connecting a device to a switch commits an edge', async () => {
    await withDraft();

    const device = store.addNode({ kind: 'device', hostname: 'alpha' });
    const sw = store.addNode({ kind: 'switch', networkName: 'EXP' });
    const iface = store.addInterface(device.id, {});

    const result = store.connect({
      sourceNodeId: device.id,
      sourceHandleId: iface.id,
      targetNodeId: sw.id,
    });

    expect(result.edge).toBeTruthy();
    expect(store.doc.edges).toHaveLength(1);
    expect(store.errors).toEqual([]);

    // Its own color is an undoable edit, as its label is.
    store.updateEdge(result.edge.id, { color: '#c0392b' });
    expect(store.doc.edges[0].color).toBe('#c0392b');
    store.undo();
    expect(store.doc.edges[0]).not.toHaveProperty('color');
    store.redo();
    expect(store.doc.edges[0].color).toBe('#c0392b');
    expect(store.errors).toEqual([]);
  });

  test('Select all leaves the connections out while the canvas hides them', async () => {
    await withDraft();

    const device = store.addNode({ kind: 'device', hostname: 'alpha' });
    const sw = store.addNode({ kind: 'switch', networkName: 'EXP' });
    const iface = store.addInterface(device.id, {});
    const { edge } = store.connect({
      sourceNodeId: device.id,
      sourceHandleId: iface.id,
      targetNodeId: sw.id,
    });

    store.selectAll();
    expect(store.selection).toEqual({
      nodes: [device.id, sw.id],
      edges: [edge.id],
    });
    expect(store.announcement).toBe('Selected everything.');

    store.selectAll({ edges: false });
    expect(store.selection).toEqual({ nodes: [device.id, sw.id], edges: [] });
    expect(store.announcement).toBe('Selected every node.');
  });

  // The Inspector applies a device's spec through updateNode.
  test('an interface VLAN applied to a device connects it, in one undoable commit', async () => {
    await withDraft();

    const device = store.addNode({ kind: 'device', hostname: 'alpha' });
    const sw = store.addNode({ kind: 'switch', networkName: 'EXP' });
    store.addInterface(device.id, {});

    const vlanOf = () =>
      store.doc.nodes.find((node) => node.id === device.id).device.spec.network
        .interfaces[0].vlan;
    const applyVLAN = (vlan) => {
      const spec = JSON.parse(
        JSON.stringify(
          store.doc.nodes.find((node) => node.id === device.id).device.spec,
        ),
      );

      spec.network.interfaces[0].vlan = vlan;
      store.updateNode(device.id, { device: { spec } });
    };
    const entries = store.history.size;

    applyVLAN('EXP');
    expect(store.history.size).toBe(entries + 1);
    expect(store.doc.edges).toEqual([
      expect.objectContaining({ targetNodeId: sw.id }),
    ]);

    applyVLAN('GHOST');
    expect(store.doc.edges).toEqual([]);
    expect(vlanOf()).toBe('GHOST');

    store.undo();
    expect(store.doc.edges).toHaveLength(1);
    expect(vlanOf()).toBe('EXP');
    store.undo();
    expect(store.doc.edges).toEqual([]);
    expect(vlanOf()).toBe('');
    store.redo();
    store.redo();
    expect(vlanOf()).toBe('GHOST');
    await store.saveNow();
  });

  // A document that is read in can keep a network with no switch.
  test('a VLAN naming a network with no switch adds one, in the same commit', async () => {
    await withDraft();
    const { doc, network, sw, bravo } = sampleDocument();

    store.setDocument({
      ...doc,
      nodes: doc.nodes.filter((node) => node.id !== sw.id),
      edges: [],
    });
    const before = store.doc;
    const entries = store.history.size;
    const spec = JSON.parse(
      JSON.stringify(findNode(store.doc, bravo.id).device.spec),
    );
    const switches = () =>
      store.doc.nodes.filter((node) => node.kind === 'switch');

    spec.network.interfaces[0].vlan = 'EXP';
    store.updateNode(bravo.id, { device: { spec } });

    expect(switches()).toEqual([
      expect.objectContaining({ switch: { networkId: network.id } }),
    ]);
    expect(store.doc.edges).toEqual([
      expect.objectContaining({
        sourceNodeId: bravo.id,
        targetNodeId: switches()[0].id,
        networkId: network.id,
      }),
    ]);
    expect(store.history.size).toBe(entries + 1);
    // The Inspector announces these changes after "Updated device bravo".
    expect(connectionChanges(before, store.doc, bravo.id)).toEqual([
      'added a switch for network EXP',
      'connected eth0 to network EXP',
    ]);

    // One Undo takes back the switch with the connection.
    store.undo();
    expect(switches()).toEqual([]);
    expect(store.doc.edges).toEqual([]);
    expect(store.doc.networks.map((entry) => entry.id)).toEqual([network.id]);
  });

  test('an invalid connection is refused and announced', async () => {
    await withDraft();

    const first = store.addNode({ kind: 'device', hostname: 'a' });
    const second = store.addNode({ kind: 'device', hostname: 'b' });
    const result = store.connect({
      sourceNodeId: first.id,
      targetNodeId: second.id,
    });

    expect(result.error).toBeTruthy();
    expect(store.doc.edges).toHaveLength(0);
    expect(store.announcement).toBe(result.error);
  });

  test('drag-to-connect is one commit, and a refusal is shown and announced', async () => {
    await withDraft();

    const a = store.addNode({ kind: 'device', hostname: 'a' });
    const b = store.addNode({ kind: 'device', hostname: 'b' });
    const before = store.history.entries.length;
    const joined = store.connectNodes({
      sourceNodeId: a.id,
      targetNodeId: b.id,
    });

    expect(joined.edges).toHaveLength(2);
    expect(store.history.entries.length).toBe(before + 1);
    expect(store.history.undoLabel()).toBe(
      'Connected devices through a new switch',
    );
    // After it, the addresses the new switch's subnet gave them.
    expect(store.announcement).toBe(
      'Set the address of eth0 of a to 10.1.0.1/24 and eth0 of b to 10.1.0.2/24.',
    );

    const sw = joined.node;
    const other = store.addNode({ kind: 'switch' });
    const seq = store.announcementSeq;
    const first = store.connectNodes({
      sourceNodeId: sw.id,
      targetNodeId: other.id,
    });
    const again = store.connectNodes({
      sourceNodeId: sw.id,
      targetNodeId: other.id,
    });

    expect(first.error).toMatch(/You cannot connect two switches/);
    expect(again.error).toBe(first.error);
    expect(store.notice.text).toBe(first.error);
    // A repeated refusal is announced again (a new live-region entry).
    expect(store.announcementSeq).toBe(seq + 2);
    expect(store.notice.seq).toBe(store.announcementSeq);

    store.dismissNotice();
    expect(store.notice).toBeNull();
  });
});

// The model places the new switch of a connection, of a VLAN typed in the
// Inspector, and a node that leaves a group (see freeSpot in model.js).
describe("the user's Snap to grid setting", () => {
  afterEach(() => resetSettings(null));

  // Two devices off the grid, a third in a group off the grid, and a
  // network with no switch.
  function offGrid() {
    const a = store.addNode({
      kind: 'device',
      hostname: 'a',
      position: { x: 1, y: 3 },
    });
    const b = store.addNode({
      kind: 'device',
      hostname: 'b',
      position: { x: 405, y: 203 },
    });
    const c = store.addNode({
      kind: 'device',
      hostname: 'c',
      position: { x: 1001, y: 1003 },
    });
    store.selection = { nodes: [c.id], edges: [] };
    store.group();
    store.commit(addNetwork(store.doc, { name: 'LAN' }).doc, 'Added LAN');

    return { a, b, c };
  }

  const onGrid = (point) => point.x % 16 === 0 && point.y % 16 === 0;

  // The places, in the order: the switch between a and b, the switch of
  // LAN beside c, and c out of its group.
  function placed({ a, b, c }) {
    const joined = store.connectNodes({
      sourceNodeId: a.id,
      targetNodeId: b.id,
    }).node.position;
    const spec = structuredClone(toRaw(findNode(store.doc, c.id).device.spec));
    spec.network = {
      interfaces: [{ name: 'eth0', type: 'ethernet', vlan: 'LAN' }],
    };
    const before = new Set(store.doc.nodes.map((node) => node.id));
    store.updateNode(c.id, { device: { spec } });
    const hub = store.doc.nodes.find(
      (node) => node.kind === 'switch' && !before.has(node.id),
    ).position;
    store.setParent(c.id, null);

    return [joined, hub, findNode(store.doc, c.id).position];
  }

  test('is off by default, so places are not on the grid', () => {
    for (const position of placed(offGrid())) {
      expect(onGrid(position), JSON.stringify(position)).toBe(false);
    }
  });

  test('puts them on the grid while it is on', () => {
    setSetting('snapToGrid', true, null);

    for (const position of placed(offGrid())) {
      expect(onGrid(position), JSON.stringify(position)).toBe(true);
    }
  });
});

describe('conflicts', () => {
  test('there is no way to overwrite the server copy', async () => {
    await withDraft();

    expect(store.keepLocal).toBeUndefined();
    expect(typeof store.resolveConflict).toBe('function');
  });

  test('saving local history as a new draft keeps every commit', async () => {
    await withDraft();

    api.appendSnapshot.mockRejectedValueOnce(
      Object.assign(new Error('conflict'), { response: { status: 412 } }),
    );

    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.saveNow();

    expect(store.hasConflict).toBe(true);

    api.appendSnapshot.mockResolvedValue({
      draft: { id: 'd2', createdBy: 'alice' },
      etag: '"2"',
    });

    await store.resolveConflict('fork', { title: 'Recovered' });

    // The server titles a draft after its document's name.
    const renamed = expect.objectContaining({
      document: expect.objectContaining({
        metadata: expect.objectContaining({ name: 'Recovered' }),
      }),
    });

    expect(api.createDraft).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Recovered' }),
    );
    expect(api.createDraft).toHaveBeenLastCalledWith(renamed);
    expect(api.appendSnapshot).toHaveBeenLastCalledWith(
      expect.anything(),
      renamed,
      expect.anything(),
    );
    expect(store.doc.metadata.name).toBe('Recovered');
    expect(store.draftId).toBe('d1');
  });

  // Undo in a fork moves the fork's cursor to the fork's own snapshots.
  test('after forking a draft, undo moves the fork’s cursor', async () => {
    await store.initAutosave({ draftId: 'd1', etag: '"1"' });
    store.history.currentEntry().serverSnapshotId = 's1';
    api.appendSnapshot.mockRejectedValueOnce(
      Object.assign(new Error('conflict'), { response: { status: 412 } }),
    );
    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.saveNow();
    expect(store.hasConflict).toBe(true);

    api.createDraft.mockResolvedValueOnce({
      draft: { id: 'd9', createdBy: 'alice', snapshotId: 'f1' },
      history: null,
      etag: '"f1"',
    });
    api.appendSnapshot.mockResolvedValueOnce({
      draft: { id: 'd9', createdBy: 'alice', snapshotId: 'f2' },
      history: null,
      etag: '"f2"',
    });
    api.listSnapshots.mockResolvedValueOnce([{ id: 'f1' }, { id: 'f2' }]);

    await store.resolveConflict('fork', { title: 'Mine' });

    expect(api.createDraft.mock.calls[0][0].forkOf).toBe('d1');
    expect([store.createdBy, store.draftId]).toEqual(['alice', 'd9']);
    expect(store.serverHistory).toEqual([{ id: 'f1' }, { id: 'f2' }]);

    store.undo();
    const state = await store.saveNow();

    expect(api.moveCursor).toHaveBeenLastCalledWith(
      'd9',
      { snapshotId: 'f1' },
      '"f2"',
    );
    expect(state.status).toBe('saved');
  });

  // An edit made while the fork is saved would be left out of it.
  test('an edit made while saving the history as a new draft is refused, not lost', async () => {
    await withDraft();
    api.appendSnapshot.mockRejectedValueOnce(
      Object.assign(new Error('conflict'), { response: { status: 412 } }),
    );
    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.saveNow();
    expect(store.hasConflict).toBe(true);

    let created;
    api.createDraft.mockImplementationOnce(
      (request) =>
        new Promise((resolve) => {
          created = () =>
            resolve({
              draft: { id: 'd2', createdBy: 'alice' },
              document: request.document,
              history: null,
              etag: '"f1"',
            });
        }),
    );
    api.appendSnapshot
      .mockResolvedValueOnce({
        draft: { id: 'd2', createdBy: 'alice' },
        history: null,
        etag: '"f2"',
      })
      .mockResolvedValueOnce({
        draft: { id: 'd2', createdBy: 'alice' },
        history: null,
        etag: '"f3"',
      });

    const forking = store.resolveConflict('fork', { title: 'Recovered' });
    const devices = store.summary.devices;

    store.addNode({ kind: 'device', hostname: 'bravo' });
    store.undo();

    expect(store.summary.devices).toBe(devices);
    expect(store.announcement).toBe(
      'Not changed: the conflict is not resolved yet. Edit again after you resolve it.',
    );

    created();
    await forking;

    expect(store.history.entries).toHaveLength(store.history.index + 1);
    expect(store.history.current()).toEqual(store.doc);
    expect(store.summary.devices).toBe(devices);

    store.addNode({ kind: 'device', hostname: 'charlie' });
    await store.saveNow();

    expect(api.appendSnapshot).toHaveBeenLastCalledWith(
      'd2',
      expect.objectContaining({
        document: expect.objectContaining({
          metadata: expect.objectContaining({ name: 'Recovered' }),
        }),
      }),
      '"f2"',
    );
  });

  // " (local copy)" must not take a name past the server's limit.
  test('the fork of a diagram with a long name is titled within the name limit', async () => {
    const bytes = (text) => new TextEncoder().encode(text).length;

    for (const name of ['x'.repeat(505), 'é'.repeat(250)]) {
      store.newDocument({ name });
      await withDraft();
      api.appendSnapshot.mockRejectedValueOnce(
        Object.assign(new Error('conflict'), { response: { status: 412 } }),
      );
      store.addNode({ kind: 'device', hostname: 'alpha' });
      await store.saveNow();

      await store.resolveConflict('fork');

      const { title } = api.createDraft.mock.lastCall[0];

      expect(title).toMatch(/^(x+|é+) \(local copy\)$/);
      expect(bytes(title)).toBeLessThanOrEqual(512);
      expect(bytes(title)).toBeGreaterThan(510);
      expect(store.doc.metadata.name).toBe(title);
    }
  });

  test('reloading the server version discards local work explicitly', async () => {
    await withDraft();

    api.appendSnapshot.mockRejectedValueOnce(
      Object.assign(new Error('conflict'), { response: { status: 412 } }),
    );

    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.saveNow();
    await store.resolveConflict('reload');

    expect(api.getDraft).toHaveBeenCalledWith('d1');
  });

  // The local changes go only once the server version is in hand.
  test('reloading keeps the local changes when the server version cannot be read', async () => {
    await withDraft();

    const server = toRaw(store.doc);

    api.appendSnapshot.mockRejectedValueOnce(httpError(412));

    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.saveNow();

    const edited = store.doc;
    const queue = store.autosave.record.queue;

    expect(queue).toHaveLength(1);

    api.getDraft.mockRejectedValueOnce(new Error('network'));

    expect(await store.resolveConflict('reload')).toBeNull();
    expect(store.error).toMatch(/^Could not load the draft\./);
    expect(store.doc).toBe(edited);
    expect(store.autosave.record.queue).toEqual(queue);
    expect(store.saveState.pending).toBe(1);
    expect(store.saveState.status).toBe('conflict');

    // Once the server answers, the reload discards them and reads it once.
    api.getDraft.mockClear();
    api.getDraft.mockResolvedValueOnce({
      draft: { id: 'd1', createdBy: 'bob' },
      document: server,
      history: [{ id: 's2', current: true }],
      cursor: 0,
      etag: '"5"',
    });

    expect(await store.resolveConflict('reload')).not.toBeNull();
    expect(api.getDraft).toHaveBeenCalledOnce();
    expect(store.autosave.record.queue).toEqual([]);
    expect(store.saveState.pending).toBe(0);
    expect(store.doc.nodes).toHaveLength(server.nodes.length);
    expect(store.etag).toBe('"5"');
  });
});

describe('merging a conflict', () => {
  const head = (snapshotId, cursor) => ({
    snapshotId,
    cursor,
    snapshots: cursor + 1,
  });
  const refused = () =>
    Object.assign(new Error('conflict'), { response: { status: 412 } });

  function changeNode(doc, id, change) {
    return {
      ...doc,
      nodes: doc.nodes.map((node) => (node.id === id ? change(node) : node)),
    };
  }

  function rename(doc, id, name) {
    return changeNode(doc, id, (node) => ({
      ...node,
      label: name,
      device: {
        ...node.device,
        hostname: name,
        spec: {
          ...node.device.spec,
          general: { ...node.device.spec.general, hostname: name },
        },
      },
    }));
  }

  function move(doc, id, position) {
    return changeNode(doc, id, (node) => ({ ...node, position }));
  }

  // The sample diagram open as draft d1, at snapshot s1 with ETag "1".
  async function opened() {
    const { doc, alpha, bravo } = sampleDocument();

    store.setDocument(doc, { label: 'Draft loaded', announce: false });
    await store.initAutosave({
      draftId: 'd1',
      etag: '"1"',
      serverHead: head('s1', 0),
    });
    store.history.currentEntry().serverSnapshotId = 's1';

    return { base: toRaw(store.doc), alpha, bravo };
  }

  // The draft as a read finds it, saved last by `by` at snapshot `id`.
  function read(document, { id = 's2', cursor = 1, etag = '"2"', by = 'bob' }) {
    return {
      draft: {
        id: 'd1',
        createdBy: 'alice',
        snapshotId: id,
        cursor,
        snapshots: cursor + 1,
        lastModifiedBy: by,
      },
      document,
      history: [{ id: 's1' }, { id }],
      cursor,
      etag,
    };
  }

  // The answer to a save stored as snapshot `id`.
  function stored(id, cursor, etag) {
    return {
      draft: {
        id: 'd1',
        createdBy: 'alice',
        snapshotId: id,
        cursor,
        snapshots: cursor + 1,
      },
      history: null,
      cursor,
      etag,
    };
  }

  // The next save meets bob's version `theirs`, which the read after it
  // finds. The save after that is stored when `saved`.
  function savedFirst(theirs, { saved = true } = {}) {
    api.appendSnapshot.mockRejectedValueOnce(refused());
    api.getDraft.mockResolvedValueOnce(read(theirs, {}));

    if (saved) {
      api.appendSnapshot.mockResolvedValueOnce(stored('s3', 2, '"3"'));
    }
  }

  test('changes that do not clash are merged and saved as one snapshot with the server ETag', async () => {
    vi.stubEnv('VITE_AUTH', 'enabled');

    try {
      const { base, alpha, bravo } = await opened();

      savedFirst(rename(base, alpha.id, 'alpha-2'));
      store.commit(
        move(toRaw(store.doc), bravo.id, { x: 96, y: 240 }),
        'Moved bravo',
      );

      await vi.waitFor(() => {
        expect(api.appendSnapshot).toHaveBeenCalledTimes(2);
        expect(store.saveState.status).toBe('saved');
      });

      const [id, payload, etag] = api.appendSnapshot.mock.lastCall;

      expect([id, etag]).toEqual(['d1', '"2"']);
      expect(payload.summary).toBe('Merged changes from bob');
      expect(findNode(payload.document, alpha.id).device.hostname).toBe(
        'alpha-2',
      );
      expect(findNode(payload.document, bravo.id).position).toEqual({
        x: 96,
        y: 240,
      });
      expect(findNode(store.doc, alpha.id).device.hostname).toBe('alpha-2');
      expect(findNode(store.doc, bravo.id).position).toEqual({ x: 96, y: 240 });
      expect(store.announcement).toBe("Merged bob's changes with yours.");
      expect(store.merge).toBeNull();
      expect(store.hasConflict).toBe(false);
      expect(store.etag).toBe('"3"');

      // Undo goes back to bob's version, without the move.
      expect(store.history.entries).toHaveLength(2);
      expect(store.history.undoLabel()).toBe('Merged changes from bob');
      expect(store.history.entries[0].serverSnapshotId).toBe('s2');
      expect(store.history.entries[1].serverSnapshotId).toBe('s3');
      store.undo();
      expect(findNode(store.doc, alpha.id).device.hostname).toBe('alpha-2');
      expect(findNode(store.doc, bravo.id).position).toEqual({ x: 0, y: 200 });
      await store.saveNow();
      expect(api.moveCursor).toHaveBeenLastCalledWith(
        'd1',
        { snapshotId: 's2' },
        '"3"',
      );
    } finally {
      vi.unstubAllEnvs();
    }
  });

  test('a field both changed is reviewed: nothing is sent until the user chooses', async () => {
    // Without sign-in, everyone is the same user: the merge names no one.
    vi.stubEnv('VITE_AUTH', 'disabled');

    const { base, alpha } = await opened();

    savedFirst(rename(base, alpha.id, 'theirs'));
    store.commit(rename(toRaw(store.doc), alpha.id, 'mine'), 'Renamed alpha');

    await vi.waitFor(() => expect(store.merge?.status).toBe('review'));
    vi.unstubAllEnvs();
    expect(store.merge).toMatchObject({ from: 'another editor', clashes: 1 });
    expect(store.hasConflict).toBe(true);
    expect(store.conflictShown).toBe(true);
    expect(store.resolvingConflict).toBe(false);
    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);

    const review = store.mergeReview();

    expect(review.clashes.map((clash) => clash.label)).toEqual(['alpha name']);
    expect(store.mergeProblems({ [review.clashes[0].key]: 'mine' })).toEqual(
      [],
    );

    expect(
      await store.saveMergeChoices({ [review.clashes[0].key]: 'mine' }),
    ).toEqual({ saved: true, issues: [] });
    await vi.waitFor(() => expect(store.saveState.status).toBe('saved'));

    expect(api.appendSnapshot).toHaveBeenLastCalledWith(
      'd1',
      expect.objectContaining({
        summary: 'Merged changes from another editor',
      }),
      '"2"',
    );
    expect(findNode(store.doc, alpha.id).device.hostname).toBe('mine');
    expect(store.merge).toBeNull();
    expect(store.conflictShown).toBe(false);
  });

  test('a merged diagram the strict check refuses is reviewed, never saved', async () => {
    const { base } = await opened();

    savedFirst(addNode(base, { kind: 'device', hostname: 'charlie' }).doc, {
      saved: false,
    });
    store.commit(
      addNode(toRaw(store.doc), { kind: 'device', hostname: 'charlie' }).doc,
      'Added charlie',
    );

    await vi.waitFor(() => expect(store.merge?.status).toBe('review'));
    expect(store.merge.clashes).toBe(0);

    const problems = store.mergeProblems();

    expect(problems.join('\n')).toMatch(/duplicate hostname "charlie"/);
    expect(await store.saveMergeChoices({})).toEqual({
      saved: false,
      issues: problems,
    });
    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);
    expect(store.hasConflict).toBe(true);
  });

  test('without the version the changes started from, merging is unavailable and saving a new draft still works', async () => {
    const { base, alpha, bravo } = await opened();

    // The editor no longer holds snapshot s1, and the server no longer
    // keeps it.
    store.history.currentEntry().serverSnapshotId = 's0';
    api.getSnapshot.mockRejectedValueOnce(
      Object.assign(new Error('gone'), { response: { status: 404 } }),
    );
    savedFirst(rename(base, alpha.id, 'alpha-2'), { saved: false });
    store.commit(
      move(toRaw(store.doc), bravo.id, { x: 96, y: 240 }),
      'Moved bravo',
    );

    await vi.waitFor(() => expect(store.merge?.status).toBe('unavailable'));
    expect(api.getSnapshot).toHaveBeenCalledWith('d1', 's1');
    expect(store.merge.reason).toBe(
      'Merging is not available: the server no longer keeps the version that your changes started from, or Builder could not read it.',
    );
    expect(store.conflictShown).toBe(true);
    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);

    await store.resolveConflict('fork', { title: 'Mine' });

    expect(api.createDraft).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Mine' }),
    );
    expect(store.merge).toBeNull();
    expect(store.hasConflict).toBe(false);
  });

  test('a third editor saving first merges again, from the version the merge was saved on', async () => {
    const { base, alpha, bravo } = await opened();
    const theirs = rename(base, alpha.id, 'alpha-2');
    const third = setDocumentInfo(theirs, { description: 'From carol' });

    savedFirst(theirs, { saved: false });
    api.appendSnapshot.mockRejectedValueOnce(refused());
    api.getDraft.mockResolvedValueOnce(
      read(third, { id: 's3', cursor: 2, etag: '"4"', by: 'carol' }),
    );
    api.appendSnapshot.mockResolvedValueOnce(stored('s4', 3, '"5"'));

    store.commit(
      move(toRaw(store.doc), bravo.id, { x: 96, y: 240 }),
      'Moved bravo',
    );

    await vi.waitFor(() => {
      expect(api.appendSnapshot).toHaveBeenCalledTimes(3);
      expect(store.saveState.status).toBe('saved');
    });

    const [, payload, etag] = api.appendSnapshot.mock.lastCall;

    expect(etag).toBe('"4"');
    expect(payload.document.metadata.description).toBe('From carol');
    expect(findNode(payload.document, alpha.id).device.hostname).toBe(
      'alpha-2',
    );
    expect(findNode(payload.document, bravo.id).position).toEqual({
      x: 96,
      y: 240,
    });
    // Bob's version, which the history holds, is the base of the second
    // merge: nothing is read for it.
    expect(api.getSnapshot).not.toHaveBeenCalled();
    expect(store.etag).toBe('"5"');
  });

  test('an edit made while the base is read is refused, not lost', async () => {
    const { base, alpha, bravo } = await opened();
    let answer;

    store.history.currentEntry().serverSnapshotId = 's0';
    api.getSnapshot.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          answer = () => resolve({ document: base });
        }),
    );
    savedFirst(rename(base, alpha.id, 'alpha-2'));
    store.commit(
      move(toRaw(store.doc), bravo.id, { x: 96, y: 240 }),
      'Moved bravo',
    );

    await vi.waitFor(() => expect(answer).toBeTypeOf('function'));
    expect(store.merge?.status).toBe('merging');
    expect(store.conflictShown).toBe(false);
    store.addNode({ kind: 'device', hostname: 'charlie' });
    expect(store.summary.devices).toBe(2);
    expect(store.announcement).toBe(
      'Not changed: the conflict is not resolved yet. Edit again after you resolve it.',
    );

    answer();
    await vi.waitFor(() => expect(store.saveState.status).toBe('saved'));
    expect(findNode(store.doc, alpha.id).device.hostname).toBe('alpha-2');
    expect(store.summary.devices).toBe(2);
  });

  test('a merge that fails is unavailable, with why, and the other ways out stay offered', async () => {
    const { base, alpha, bravo } = await opened();

    mergeDocuments.mockImplementationOnce(() => {
      throw new Error('out of memory');
    });
    savedFirst(rename(base, alpha.id, 'alpha-2'), { saved: false });
    store.commit(
      move(toRaw(store.doc), bravo.id, { x: 96, y: 240 }),
      'Moved bravo',
    );

    await vi.waitFor(() => expect(store.merge?.status).toBe('unavailable'));
    expect(store.merge.reason).toBe('Merging failed: out of memory');
    expect(store.conflictShown).toBe(true);
    expect(store.resolvingConflict).toBe(false);
    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);

    await store.resolveConflict('fork', { title: 'Mine' });

    expect(api.createDraft).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Mine' }),
    );
    expect(store.merge).toBeNull();
    expect(store.hasConflict).toBe(false);
  });

  test('a base the server does not send in time leaves merging unavailable, and the panel shows', async () => {
    const { base, alpha } = await opened();

    // The editor no longer holds snapshot s1, and the server never answers.
    store.history.currentEntry().serverSnapshotId = 's0';
    api.getSnapshot.mockImplementationOnce(() => new Promise(() => {}));
    vi.useFakeTimers();

    try {
      store.autosave.conflict(undefined, {
        server: serverCopyOf(read(rename(base, alpha.id, 'alpha-2'), {})),
      });

      expect(api.getSnapshot).toHaveBeenCalledWith('d1', 's1');
      expect(store.merge.status).toBe('merging');
      expect(store.conflictShown).toBe(false);

      await vi.advanceTimersByTimeAsync(MERGE_BASE_TIMEOUT_MS - 1);
      expect(store.merge.status).toBe('merging');

      await vi.advanceTimersByTimeAsync(1);
      await vi.waitFor(() => expect(store.merge.status).toBe('unavailable'));
      expect(store.merge.reason).toBe(
        'Merging is not available: the server took too long to send the version your changes started from.',
      );
      expect(store.conflictShown).toBe(true);
      expect(store.resolvingConflict).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  test('keeping their deletion of a device you connected drops the connection, and the merge names it', async () => {
    const { base, bravo } = await opened();
    const sw = base.nodes.find((node) => node.kind === 'switch');

    // Bob deleted bravo, which you connect.
    savedFirst({
      ...base,
      nodes: base.nodes.filter((node) => node.id !== bravo.id),
    });
    store.commit(
      connect(toRaw(store.doc), {
        sourceNodeId: bravo.id,
        sourceHandleId: bravo.device.interfaces[0].id,
        targetNodeId: sw.id,
      }).doc,
      'Connected bravo',
    );

    await vi.waitFor(() => expect(store.merge?.status).toBe('review'));

    const [clash] = store.mergeReview().clashes;

    expect(clash.key).toBe(`nodes["${bravo.id}"]`);
    expect(clash.theirsText).toBe(
      'delete bravo and drop your connection to it',
    );
    expect(await store.saveMergeChoices({ [clash.key]: 'theirs' })).toEqual({
      saved: true,
      issues: [],
    });
    expect(store.announcement).toMatch(
      /^Merged .+'s changes with yours\. Dropped the connection from bravo to .+: someone deleted the device or interface that it connects\.$/,
    );
    expect(findNode(store.doc, bravo.id)).toBeFalsy();
    expect(store.doc.edges).toHaveLength(base.edges.length);
    await vi.waitFor(() => expect(store.saveState.status).toBe('saved'));
    expect(api.appendSnapshot).toHaveBeenCalledTimes(2);
  });

  test('keeping their ungroup of a group you put a node in moves the node out, and the merge names it', async () => {
    const { base: plain, alpha, bravo } = await opened();
    const { doc: base, group } = groupNodes(plain, [alpha.id], {
      title: 'Zone A',
    });

    // The draft as last saved has alpha in Zone A.
    store.setDocument(base, { label: 'Draft loaded', announce: false });
    store.history.currentEntry().serverSnapshotId = 's1';

    // Bob ungrouped Zone A. You put bravo in it.
    savedFirst(ungroup(base, group.id));
    store.commit(
      changeNode(toRaw(store.doc), bravo.id, (node) => ({
        ...node,
        parentId: group.id,
      })),
      'Grouped bravo',
    );

    await vi.waitFor(() => expect(store.merge?.status).toBe('review'));

    const [clash] = store.mergeReview().clashes;

    expect(clash.key).toBe(`nodes["${group.id}"]`);
    expect(clash.theirsText).toBe('delete Zone A and move your node out of it');
    expect(await store.saveMergeChoices({ [clash.key]: 'theirs' })).toEqual({
      saved: true,
      issues: [],
    });
    expect(store.announcement).toBe(
      "Merged bob's changes with yours. Moved bravo out of Zone A: someone deleted its group.",
    );
    expect(findNode(store.doc, group.id)).toBeFalsy();
    expect(findNode(store.doc, bravo.id).parentId).toBeUndefined();
    await vi.waitFor(() => expect(store.saveState.status).toBe('saved'));
  });

  test('while the merge runs, the save state says the changes are being merged, which is announced once', async () => {
    const { base, alpha } = await opened();
    const merging = 'Merging your changes with the server version…';

    // The editor no longer holds snapshot s1, and the server never answers,
    // for either merge.
    store.history.currentEntry().serverSnapshotId = 's0';
    api.getSnapshot
      .mockImplementationOnce(() => new Promise(() => {}))
      .mockImplementationOnce(() => new Promise(() => {}));
    vi.useFakeTimers();

    try {
      store.autosave.conflict(undefined, {
        server: serverCopyOf(read(rename(base, alpha.id, 'alpha-2'), {})),
      });

      expect(store.merge.status).toBe('merging');
      expect(store.mergingConflict).toBe(true);
      expect(store.saveStateText).toBe(merging);
      expect(store.announcement).toBe(merging);

      // A newer version arrives while it runs: the merge is made again,
      // and not announced again.
      const announced = store.announcementSeq;

      store.mergeConflict(
        serverCopyOf(
          read(rename(base, alpha.id, 'carol'), {
            id: 's3',
            cursor: 2,
            etag: '"3"',
            by: 'carol',
          }),
        ),
      );
      expect(store.merge.status).toBe('merging');
      expect(store.saveStateText).toBe(merging);
      expect(store.announcementSeq).toBe(announced);
      // Edits stay refused.
      expect(store.editRefusal).not.toBe('');

      await vi.advanceTimersByTimeAsync(MERGE_BASE_TIMEOUT_MS);
      await vi.waitFor(() => expect(store.merge.status).toBe('unavailable'));
      expect(store.mergingConflict).toBe(false);
      expect(store.saveStateText).toBe('This draft changed on the server');
    } finally {
      vi.useRealTimers();
    }
  });

  test('saving the reviewed merge while another merge runs saves nothing, and says so', async () => {
    const { base, alpha } = await opened();

    savedFirst(rename(base, alpha.id, 'theirs'), { saved: false });
    store.commit(rename(toRaw(store.doc), alpha.id, 'mine'), 'Renamed alpha');
    await vi.waitFor(() => expect(store.merge?.status).toBe('review'));

    const key = store.mergeReview().clashes[0].key;

    // Carol saves too: the merge is made again with her version.
    store.autosave.conflict(undefined, {
      server: serverCopyOf(
        read(rename(base, alpha.id, 'carol'), {
          id: 's3',
          cursor: 2,
          etag: '"3"',
          by: 'carol',
        }),
      ),
    });
    expect(store.merge.status).toBe('merging');

    expect(await store.saveMergeChoices({ [key]: 'mine' })).toEqual({
      saved: false,
      busy: true,
      issues: [],
    });

    await vi.waitFor(() => expect(store.merge?.status).toBe('review'));
    expect(store.mergeReview().clashes[0].theirs).toBe('carol');
    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);
  });

  test('a reviewed merge that cannot be saved keeps the review, the history and the other ways out, and saves on a retry', async () => {
    const { base, alpha } = await opened();

    savedFirst(rename(base, alpha.id, 'theirs'), { saved: false });
    store.commit(rename(toRaw(store.doc), alpha.id, 'mine'), 'Renamed alpha');
    await vi.waitFor(() => expect(store.merge?.status).toBe('review'));

    const key = store.mergeReview().clashes[0].key;
    const etag = store.etag;
    const entries = store.history.entries.map((entry) => entry.id);
    const rebase = vi
      .spyOn(store.autosave, 'rebase')
      .mockRejectedValueOnce(new Error('The queue is full.'));

    expect(await store.saveMergeChoices({ [key]: 'mine' })).toEqual({
      saved: false,
      issues: [],
      error: 'Builder could not save the merged diagram: The queue is full.',
    });
    expect(rebase).toHaveBeenCalledTimes(1);
    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);

    // Nothing of the merge is kept: the review, the conflict panel and its
    // other ways out stay, over the user's own diagram and history.
    expect(store.merge).toMatchObject({ status: 'review', clashes: 1 });
    expect(store.hasConflict).toBe(true);
    expect(store.conflictShown).toBe(true);
    expect(store.resolvingConflict).toBe(false);
    expect(store.etag).toBe(etag);
    expect(store.history.entries.map((entry) => entry.id)).toEqual(entries);
    expect(store.history.undoLabel()).toBe('Renamed alpha');
    expect(findNode(store.doc, alpha.id).device.hostname).toBe('mine');
    expect(store.mergeReview().clashes.map((clash) => clash.key)).toEqual([
      key,
    ]);

    // The same choices save once the queue takes them.
    api.appendSnapshot.mockResolvedValueOnce(stored('s3', 2, '"3"'));
    expect(await store.saveMergeChoices({ [key]: 'mine' })).toEqual({
      saved: true,
      issues: [],
    });
    await vi.waitFor(() => expect(store.saveState.status).toBe('saved'));
    expect(api.appendSnapshot).toHaveBeenLastCalledWith(
      'd1',
      expect.objectContaining({
        summary: expect.stringMatching(/^Merged changes from /),
      }),
      '"2"',
    );
    expect(findNode(store.doc, alpha.id).device.hostname).toBe('mine');
    expect(store.merge).toBeNull();
  });

  test('a reviewed merge that cannot be saved can still be saved as a new draft', async () => {
    const { base, alpha } = await opened();

    savedFirst(rename(base, alpha.id, 'theirs'), { saved: false });
    store.commit(rename(toRaw(store.doc), alpha.id, 'mine'), 'Renamed alpha');
    await vi.waitFor(() => expect(store.merge?.status).toBe('review'));

    const key = store.mergeReview().clashes[0].key;

    vi.spyOn(store.autosave, 'rebase').mockRejectedValueOnce(
      new Error('The queue is full.'),
    );
    expect(await store.saveMergeChoices({ [key]: 'mine' })).toMatchObject({
      saved: false,
    });

    await store.resolveConflict('fork', { title: 'Mine' });

    expect(api.createDraft).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Mine' }),
    );
    expect(store.merge).toBeNull();
    expect(store.hasConflict).toBe(false);
    expect(findNode(store.doc, alpha.id).device.hostname).toBe('mine');
  });

  test('a recovered queue based on an older ETag is merged with the draft as read', async () => {
    vi.stubEnv('VITE_AUTH', 'enabled');

    try {
      const { doc: base, alpha, bravo } = sampleDocument();
      const moved = move(base, bravo.id, { x: 96, y: 240 });

      // A previous session on this device left an edit of snapshot s1
      // unsaved. bob has saved s2 since.
      await device.store.put(
        {
          key: 'alice::d1',
          actor: 'alice',
          draftId: 'd1',
          etag: '"1"',
          serverHead: head('s1', 0),
          entries: [{ id: 'c1', label: 'Moved bravo' }],
          cursor: 0,
          queue: [
            {
              opId: 'c1',
              kind: 'snapshot',
              commitId: 'c1',
              label: 'Moved bravo',
            },
          ],
        },
        { write: [{ id: 'c1', snapshot: moved }] },
      );
      api.getDraft.mockResolvedValueOnce(
        read(rename(base, alpha.id, 'alpha-2'), {}),
      );
      api.getSnapshot.mockResolvedValueOnce({ document: base });
      api.appendSnapshot.mockResolvedValueOnce(stored('s3', 2, '"3"'));

      await store.loadDraft('d1');
      await vi.waitFor(() => {
        expect(api.appendSnapshot).toHaveBeenCalledTimes(1);
        expect(store.saveState.status).toBe('saved');
      });

      const [id, payload, etag] = api.appendSnapshot.mock.lastCall;

      expect([id, etag]).toEqual(['d1', '"2"']);
      expect(api.getSnapshot).toHaveBeenCalledWith('d1', 's1');
      expect(payload.summary).toBe('Merged changes from bob');
      expect(findNode(payload.document, alpha.id).device.hostname).toBe(
        'alpha-2',
      );
      expect(findNode(payload.document, bravo.id).position).toEqual({
        x: 96,
        y: 240,
      });
      expect(store.announcement).toBe("Merged bob's changes with yours.");
      expect(store.merge).toBeNull();
    } finally {
      vi.unstubAllEnvs();
    }
  });

  test("changes the signed-in user saved elsewhere are another tab's", async () => {
    vi.stubEnv('VITE_AUTH', 'enabled');

    try {
      const { base, alpha, bravo } = await opened();

      api.appendSnapshot.mockRejectedValueOnce(refused());
      api.getDraft.mockResolvedValueOnce(
        read(rename(base, alpha.id, 'alpha-2'), { by: 'alice' }),
      );
      api.appendSnapshot.mockResolvedValueOnce(stored('s3', 2, '"3"'));
      store.commit(
        move(toRaw(store.doc), bravo.id, { x: 96, y: 240 }),
        'Moved bravo',
      );

      await vi.waitFor(() => {
        expect(api.appendSnapshot).toHaveBeenCalledTimes(2);
        expect(store.saveState.status).toBe('saved');
      });

      expect(api.appendSnapshot.mock.lastCall[1].summary).toBe(
        'Merged changes from another tab',
      );
      expect(store.announcement).toBe(
        "Merged another tab's changes with yours.",
      );
    } finally {
      vi.unstubAllEnvs();
    }
  });

  test('a save sent again after the server stored it, its answer lost, is saved and never merged', async () => {
    const { bravo } = await opened();
    const moved = move(toRaw(store.doc), bravo.id, { x: 96, y: 240 });

    // The server stored the save as s2 but its answer was lost: the resend
    // is refused, and the read after it finds s2 newest and current.
    api.appendSnapshot.mockRejectedValueOnce(refused());
    api.getDraft.mockImplementationOnce(async () => ({
      ...read(moved, { by: 'alice' }),
      history: [
        { id: 's1', current: false },
        { id: 's2', opId: store.history.currentEntry().id, current: true },
      ],
    }));
    store.commit(moved, 'Moved bravo');

    await vi.waitFor(() => {
      expect(api.getDraft).toHaveBeenCalledTimes(1);
      expect(store.saveState.status).toBe('saved');
    });

    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);
    expect(mergeDocuments).not.toHaveBeenCalled();
    expect(store.merge).toBeNull();
    expect(store.hasConflict).toBe(false);
    expect(store.etag).toBe('"2"');
    // The edit stays one undo step, which is the snapshot the server
    // stored.
    expect(store.history.undoLabel()).toBe('Moved bravo');
    expect(store.history.currentEntry().serverSnapshotId).toBe('s2');
    expect(findNode(store.doc, bravo.id).position).toEqual({ x: 96, y: 240 });
  });

  test('discarding from the review clears the merge and loads the server version', async () => {
    const { base, alpha } = await opened();
    const theirs = rename(base, alpha.id, 'theirs');

    savedFirst(theirs, { saved: false });
    store.commit(rename(toRaw(store.doc), alpha.id, 'mine'), 'Renamed alpha');
    await vi.waitFor(() => expect(store.merge?.status).toBe('review'));

    api.getDraft.mockResolvedValueOnce(read(theirs, {}));
    await store.resolveConflict('reload');

    expect(api.getDraft).toHaveBeenLastCalledWith('d1');
    expect(store.merge).toBeNull();
    expect(store.hasConflict).toBe(false);
    expect(store.conflictShown).toBe(false);
    expect(findNode(store.doc, alpha.id).device.hostname).toBe('theirs');
    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);
  });
});

describe('server data', () => {
  test('drafts, damaged drafts, published documents and sources are loaded', async () => {
    const damaged = [{ id: 'x1', createdBy: 'alice', damaged: true }];

    api.listDrafts.mockResolvedValueOnce({
      all: [{ id: 'd1' }, { id: 'd2' }],
      published: [],
      damaged,
    });
    await store.fetchDrafts();
    await store.fetchDocuments();
    await store.fetchSources();

    expect(store.drafts.all).toHaveLength(2);
    expect(store.damagedDrafts).toEqual(damaged);
    expect(store.documents).toHaveLength(1);
    expect(store.sources.topologies).toEqual(['core']);
  });

  test('a draft someone deleted says so as it opens, and leaves the lists', async () => {
    api.getDraft.mockRejectedValueOnce(
      Object.assign(new Error('not found'), {
        response: {
          status: 404,
          data: { message: 'phenix cannot find draft d2' },
        },
      }),
    );
    api.listDrafts.mockClear();

    await expect(store.loadDraft('d2')).resolves.toBeNull();
    expect(store.error).toBe(
      'Could not load the draft. Phenix cannot find draft d2.',
    );
    expect(api.listDrafts).toHaveBeenCalledOnce();
  });

  test('opening a published document creates a separate sourced draft', async () => {
    const opened = await store.openPublishedDocument('published-1');

    expect(opened).toBeTruthy();
    expect(api.createDraft).toHaveBeenCalledWith(
      expect.objectContaining({
        sourceToken: 'builder-doc/published-1',
        document: expect.objectContaining({ $schema: expect.any(String) }),
      }),
    );
  });

  test('a failed published-document fork preserves the current draft', async () => {
    const before = store.doc;

    api.createDraft.mockRejectedValueOnce(new Error('create failed'));

    await expect(
      store.openPublishedDocument('published-1'),
    ).resolves.toBeNull();
    expect(store.doc).toBe(before);

    api.getDocument.mockResolvedValueOnce({ nodes: [] });

    await expect(
      store.openPublishedDocument('published-1'),
    ).resolves.toBeNull();
    expect(store.error).toMatch(
      /^Could not open the published diagram\. The server sent a diagram the Builder cannot open: Unsupported builder document schema/,
    );
    expect(store.doc).toBe(before);
  });

  test('a published diagram opens read only; editing it makes one draft, then reopens that draft', async () => {
    store.serverHistory = [{ id: 'old' }];

    const viewed = await store.viewPublishedDocument('published-1');

    expect(viewed.metadata.name).toBe(sampleDocument().doc.metadata.name);
    expect(api.createDraft).not.toHaveBeenCalled();
    expect(store.readOnly).toBe(true);
    expect(store.published).toEqual(
      expect.objectContaining({ id: 'published-1' }),
    );
    expect(store.autosave).toBeNull();
    expect(store.serverHistory).toEqual([]);
    expect(store.announcement).toMatch(/, read only\.$/);

    await expect(store.editPublished()).resolves.toBeTruthy();
    // The diagram shown is the one saved: it is not read again.
    expect(api.getDocument).toHaveBeenCalledTimes(1);
    expect(api.createDraft).toHaveBeenCalledWith(
      expect.objectContaining({ sourceToken: 'builder-doc/published-1' }),
    );
    expect(store.readOnly).toBe(false);
    expect(store.published).toBeNull();

    // The draft made from it and changed last opens again. Times are
    // compared as times, as the server drops trailing zeros.
    const { doc } = sampleDocument();
    api.listDrafts.mockResolvedValueOnce({
      all: [
        { id: 'other', createdBy: 'alice', sourceToken: 'builder-doc/p2' },
        {
          id: 'd6',
          createdBy: 'alice',
          sourceToken: 'builder-doc/published-1',
          updated: '2026-09-02T00:00:00Z',
        },
        {
          id: 'd7',
          createdBy: 'alice',
          sourceToken: 'builder-doc/published-1',
          updated: '2026-09-02T00:00:00.1Z',
        },
      ],
      published: [],
    });
    api.getDraft.mockResolvedValueOnce({
      draft: { id: 'd7', createdBy: 'alice' },
      document: doc,
      history: [{ id: 's1' }],
      cursor: 0,
      etag: '"7"',
    });

    await expect(
      store.openPublishedDocument('published-1'),
    ).resolves.toBeTruthy();
    expect(api.getDraft).toHaveBeenLastCalledWith('d7');
    expect(store.draftId).toBe('d7');
    expect(store.announcement).toBe(
      `Opened the draft of published diagram ${doc.metadata.name}.`,
    );
    expect(api.createDraft).toHaveBeenCalledTimes(1);
  });

  test('a schema failure is reported, not swallowed', async () => {
    api.getSchema.mockRejectedValueOnce(new Error('boom'));

    await store.fetchSchema();

    expect(store.schema.$id).toBe(builderSchemaV1.$id);
    // In plain words: no "schema" jargon.
    expect(store.schemaError).toMatch(
      /^Could not load this server's form fields\. .*built-in fields of the Builder instead/,
    );
    expect(store.schemaError).not.toMatch(/schema/i);
  });

  test('an unusable server schema is reported too', async () => {
    api.getSchema.mockResolvedValueOnce({ definitions: {} });

    await store.fetchSchema();

    expect(store.schemaError).toBeTruthy();
  });

  test('a usable server schema replaces the bundle silently', async () => {
    await store.fetchSchema();

    expect(store.schemaError).toBe('');
  });

  test('disk images are unknown until listed, and drive images checked after', async () => {
    store.setDocument(sampleDocument().doc);
    const driveWarnings = () =>
      store.issues.filter((issue) => issue.path.endsWith('.image'));

    expect(store.disks).toBeNull();
    expect(driveWarnings()).toEqual([]);

    await expect(store.fetchDisks()).resolves.toEqual(['ubuntu.qc2']);
    expect(store.disks).toEqual(['ubuntu.qc2']);
    expect(driveWarnings()).toEqual([]);

    api.listDisks.mockResolvedValueOnce(['other.qc2']);
    await store.fetchDisks();
    expect(driveWarnings()).toHaveLength(2);
    expect(store.errors).toEqual([]);
  });

  test('a disk listing that fails or lists nothing leaves the images unknown, quietly', async () => {
    const failures = [
      Object.assign(new Error('forbidden'), { response: { status: 403 } }),
      Object.assign(new Error('no minimega'), { response: { status: 500 } }),
      new Error('Network Error'),
    ];

    for (const failure of failures) {
      store.disks = ['ubuntu.qc2'];
      api.listDisks.mockRejectedValueOnce(failure);

      await expect(store.fetchDisks()).resolves.toBeNull();
      expect(store.disks).toBeNull();
    }

    // The server lists none when minimega is not running.
    api.listDisks.mockResolvedValueOnce([]);
    await store.fetchDisks();
    expect(store.disks).toBeNull();
    expect(store.error).toBe('');
    expect(store.errorSeq).toBe(0);
  });

  test('a disk listing asked for while one is under way waits for it', async () => {
    await expect(
      Promise.all([store.fetchDisks(), store.fetchDisks()]),
    ).resolves.toEqual([['ubuntu.qc2'], ['ubuntu.qc2']]);
    expect(api.listDisks).toHaveBeenCalledOnce();

    await store.fetchDisks();
    expect(api.listDisks).toHaveBeenCalledTimes(2);
  });

  test('a stored scenario is read for its apps, and the last read kept while one is under way', async () => {
    const ntp = { apps: [{ name: 'ntp' }] };

    await store.fetchScenario('ntp-scn');
    expect(api.getScenario).toHaveBeenCalledWith('ntp-scn');
    expect(store.storedScenarios['ntp-scn']).toMatchObject({
      content: ntp,
      problem: '',
      loading: false,
    });

    // Two reads at once: the first to answer is the older, and dropped.
    let answer;
    api.getScenario.mockImplementationOnce(
      () => new Promise((resolve) => (answer = resolve)),
    );
    api.getScenario.mockResolvedValueOnce({ apps: [] });
    const older = store.fetchScenario('ntp-scn');

    expect(store.storedScenarios['ntp-scn']).toMatchObject({
      content: ntp,
      loading: true,
    });
    await store.fetchScenario('ntp-scn');
    answer(ntp);
    await older;
    expect(store.storedScenarios['ntp-scn'].content).toEqual({ apps: [] });

    api.getScenario.mockRejectedValueOnce(
      Object.assign(new Error('forbidden'), { response: { status: 403 } }),
    );
    await store.fetchScenario('ntp-scn');
    expect(store.storedScenarios['ntp-scn']).toMatchObject({
      content: null,
      problem: 'forbidden',
      loading: false,
    });
    expect(store.error).toBe('');
  });

  test('Download reads a stored scenario again, and says why it cannot', async () => {
    const before = { apps: [{ name: 'ntp' }] };
    const now = { apps: [{ name: 'scada', hosts: [{ hostname: 'plc' }] }] };

    api.getScenario.mockResolvedValueOnce(before);
    await store.fetchScenario('plant-scn');
    api.getScenario.mockResolvedValueOnce(now);

    // Not the content the Inspector read before: the server's now.
    await expect(store.readScenario('plant-scn')).resolves.toEqual({
      content: now,
      problem: '',
    });
    expect(api.getScenario).toHaveBeenLastCalledWith('plant-scn');
    expect(store.storedScenarios['plant-scn'].content).toEqual(now);

    api.getScenario.mockRejectedValueOnce(
      Object.assign(new Error('forbidden'), { response: { status: 403 } }),
    );
    await expect(store.readScenario('plant-scn')).resolves.toEqual({
      content: null,
      problem: 'forbidden',
    });
    expect(store.error).toBe('');
  });

  test('publishing sends only the intent and the ETag', async () => {
    await withDraft();

    const intent = {
      mode: 'topology',
      topology: { name: 'core', action: 'create' },
    };
    const result = await store.publish(intent);

    expect(api.publish).toHaveBeenCalledWith('d1', intent, '"1"');
    expect(api.publish.mock.calls[0][1].document).toBeUndefined();
    expect(result.ok).toBe(true);
    // The topology now exists, so the list the next publish chooses create
    // or update from is read again.
    expect(api.getSources).toHaveBeenCalledOnce();
    expect(store.sources.topologies).toEqual(['core']);
    // So is the list of published diagrams, which says what it may update.
    expect(api.listDocuments).toHaveBeenCalledOnce();
  });

  test('a publish preview keeps only the answer to the last one asked for', async () => {
    await withDraft();

    const intent = (action) => ({
      mode: 'topology',
      topology: { name: 'core', action },
    });
    const answer = (action) => ({
      changes: {
        topology: { name: 'core', action },
        experiment: null,
        includes: [],
        scenarios: [],
        images: [],
        vlanAliases: [],
      },
      warningIssues: [],
      errorIssues: [],
    });

    let answerFirst;

    api.previewPublish.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          answerFirst = resolve;
        }),
    );
    api.previewPublish.mockResolvedValueOnce(answer('update'));

    const first = store.previewPublish(intent('create'));
    const second = store.previewPublish(intent('update'));

    await expect(second).resolves.toEqual(answer('update'));
    // The first answers last: a later preview made it stale.
    answerFirst(answer('create'));
    await expect(first).resolves.toBeNull();
    expect(api.previewPublish).toHaveBeenCalledWith('d1', intent('create'));
    expect(api.previewPublish.mock.calls.at(-1)[1].document).toBeUndefined();

    // A failure is answered, not thrown, and is no error of the store: the
    // preview never stands in the way of publishing.
    api.previewPublish.mockRejectedValueOnce(
      Object.assign(new Error('unavailable'), {
        response: { status: 500, data: { message: 'store unavailable' } },
      }),
    );

    const failed = await store.previewPublish(intent('create'));

    expect(failed).toMatchObject({ failed: true });
    expect(failed.message).toMatch(/^Could not read what publishing changes\./);
    expect(store.error).toBe('');

    // A failure a later preview made stale is dropped too.
    let failFirst;

    api.previewPublish.mockImplementationOnce(
      () =>
        new Promise((_, reject) => {
          failFirst = reject;
        }),
    );

    const stale = store.previewPublish(intent('create'));

    await store.previewPublish(intent('update'));
    failFirst(new Error('late'));
    await expect(stale).resolves.toBeNull();
  });

  test('Publish knows which config the draft was loaded from and what it saved', async () => {
    api.createDraft.mockImplementationOnce(async (request) => ({
      draft: {
        id: 'd1',
        createdBy: 'alice',
        sourceToken: request.sourceToken,
        digest: 'sha256:1',
        updated: '2026-03-04T05:06:07Z',
      },
      document: null,
      history: null,
      cursor: 0,
      etag: '"1"',
    }));

    await store.createDraft({ sourceToken: 'builder-doc/p1' });
    await store.fetchDocuments();

    expect(store.publishDraft).toMatchObject({
      sourceToken: 'builder-doc/p1',
      digest: 'sha256:1',
      documents: [{ id: 'p1' }],
    });
    // Download dates a GEXF file by it.
    expect(store.draftRecord.updated).toBe('2026-03-04T05:06:07Z');

    // A publish keeps the draft record the server returns.
    api.publish.mockResolvedValueOnce({
      result: {
        ok: true,
        partial: false,
        stages: [],
        draft: {
          id: 'd1',
          sourceToken: 'builder-doc/p1',
          digest: 'sha256:2',
          publication: { topologyTarget: 'core', documentId: 'p2' },
        },
      },
      etag: '"2"',
    });
    await store.publish({
      mode: 'topology',
      topology: { name: 'core', action: 'create' },
    });
    // With the publication it records, which the draft may update again.
    expect(store.publishDraft).toMatchObject({
      id: 'd1',
      digest: 'sha256:2',
      publication: { topologyTarget: 'core', documentId: 'p2' },
    });

    // A new diagram is no draft at all.
    store.newDocument();
    expect(store.publishDraft).toMatchObject({
      id: '',
      sourceToken: '',
      digest: '',
      publication: null,
      updated: '',
    });
  });

  test('a refused config is reported on its field, and the list is read again', async () => {
    await withDraft();

    api.publish.mockRejectedValueOnce(
      Object.assign(new Error('conflict'), {
        response: {
          status: 409,
          data: {
            code: 'publish.topology.exists',
            message: 'config core already exists. Choose update explicitly',
          },
        },
      }),
    );

    const intent = {
      mode: 'topology',
      topology: { name: 'core', action: 'create' },
    };

    await expect(store.publish(intent)).resolves.toBeNull();
    // This draft was not loaded from "core", so publishing again cannot
    // update it either, and the message does not promise that it will.
    expect(store.error).toBe(
      'Could not publish the diagram. A topology named "core" already exists. This diagram cannot update it. ' +
        'You did not import the diagram from it, open the diagram from its published diagram or publish the diagram to it. ' +
        'Enter another name to create a new topology.',
    );
    expect(store.errorField).toBe('topologyName');
    expect(api.getSources).toHaveBeenCalledOnce();
    expect(api.listDocuments).toHaveBeenCalledOnce();
    expect(store.sources.topologies).toEqual(['core']);

    // The server's refusal of an update from a draft not loaded from the
    // config says the same, on the same field.
    api.publish.mockRejectedValueOnce(
      Object.assign(new Error('conflict'), {
        response: {
          status: 409,
          data: {
            code: 'publish.topology.not-source',
            message: 'topology core is not the source of this draft',
          },
        },
      }),
    );

    await expect(
      store.publish({
        mode: 'topology',
        topology: { name: 'core', action: 'update' },
      }),
    ).resolves.toBeNull();
    expect(store.error).toMatch(
      /^Could not publish the diagram\. A topology named "core" already exists\. This diagram cannot update it\. .* Enter another name to create a new topology\.$/,
    );
    expect(store.errorField).toBe('topologyName');

    // A 412 is about the draft, not a config.
    api.publish.mockRejectedValueOnce(
      Object.assign(new Error('changed'), { response: { status: 412 } }),
    );

    await expect(store.publish(intent)).resolves.toBeNull();
    expect(store.error).toMatch(/^This draft changed on the server/);
    expect(store.errorField).toBe('');
  });

  // Someone else changed the topology after this draft published it. Only
  // this draft stays refused, so the dialog offers a new name.
  test('a config changed since this draft published it stays refused for that draft', async () => {
    await withDraft();

    const changed = () =>
      Object.assign(new Error('conflict'), {
        response: {
          status: 409,
          data: {
            code: 'publish.topology.changed',
            message: 'Topology core changed after this draft published it.',
          },
        },
      });
    const intent = {
      mode: 'topology',
      topology: { name: 'core', action: 'update' },
    };

    expect(store.publishDraft.changedTargets).toEqual([]);

    api.publish.mockRejectedValueOnce(changed());
    await expect(store.publish(intent)).resolves.toBeNull();

    expect(store.error).toBe(
      'Could not publish the diagram. The topology "core" changed after this diagram published it. Publishing would overwrite that change. ' +
        'Import the topology again to edit it as it is now, or enter another name to create a new topology.',
    );
    expect(store.errorField).toBe('topologyName');
    expect(store.publishDraft.changedTargets).toEqual(['topology/core']);
    expect(
      updateBlocker('topology', 'core', ['core'], store.publishDraft),
    ).toBe('changed');

    // A second refusal adds the experiment and keeps the topology.
    api.publish.mockRejectedValueOnce(
      Object.assign(new Error('conflict'), {
        response: {
          status: 409,
          data: { code: 'publish.experiment.changed', message: '' },
        },
      }),
    );
    await store.publish({
      mode: 'experiment',
      topology: { name: 'core', action: 'update' },
      experiment: { name: 'lab', action: 'update' },
    });

    expect(store.publishDraft.changedTargets).toEqual([
      'topology/core',
      'experiment/lab',
    ]);

    // Another draft has no such refusal.
    await store.initAutosave({ draftId: 'd2', etag: '"1"' });

    expect(store.publishDraft.changedTargets).toEqual([]);
    expect(
      updateBlocker('topology', 'core', ['core'], store.publishDraft),
    ).not.toBe('changed');
  });

  test('a diagram the server will not publish says which interfaces to fix', async () => {
    await withDraft();

    const vlan =
      'interface #2 of device "server" has no VLAN: connect it to a network, or type a VLAN for it';

    api.publish.mockRejectedValueOnce(
      Object.assign(new Error('unprocessable'), {
        response: {
          status: 422,
          data: {
            message: `phenix cannot publish topology lab: ${vlan}`,
            cause: `validating topology projection: ${vlan}`,
          },
        },
      }),
    );

    await expect(
      store.publish({
        mode: 'topology',
        topology: { name: 'lab', action: 'create' },
      }),
    ).resolves.toBeNull();
    expect(store.error).toBe(
      `Could not publish the diagram. Phenix cannot publish topology lab: ${vlan}.`,
    );
  });

  test('the issues a refusal lists show until the dialog closes or the diagram changes', async () => {
    await withDraft();

    const intent = {
      mode: 'topology',
      topology: { name: 'lab', action: 'create' },
    };
    const refuse = () =>
      api.publish.mockRejectedValueOnce(
        Object.assign(new Error('unprocessable'), {
          response: {
            status: 422,
            data: {
              code: 'invalid-document',
              message: 'invalid builder document',
              issues: [
                {
                  path: 'nodes[1].device.hostname',
                  message: 'a device needs a hostname',
                },
              ],
            },
          },
        }),
      );
    const refused = [
      {
        severity: 'error',
        message: 'a device needs a hostname',
        path: 'nodes[1].device.hostname',
      },
    ];

    // The Publish dialog opens, and the server refuses its publish.
    let dialog = effectScope();

    store.publishIssues = [{ severity: 'error', message: 'from before' }];
    dialog.run(() => useRefusalIssues(store));
    expect(store.publishIssues).toEqual([]);

    refuse();
    await expect(store.publish(intent)).resolves.toBeNull();
    expect(store.publishIssues).toEqual(refused);

    // A save's answer changes only who saved the diagram and when.
    store.doc = withStamp(store.doc, { updatedBy: 'alice' });
    await nextTick();
    expect(store.publishIssues).toEqual(refused);

    // An edit changes the diagram the issues were about.
    store.commit(
      setDocumentInfo(store.doc, { name: 'Renamed' }),
      'Renamed the diagram',
    );
    await nextTick();
    expect(store.publishIssues).toEqual([]);

    // Refused again, then the dialog closes.
    refuse();
    await store.publish(intent);
    expect(store.publishIssues).toEqual(refused);
    dialog.stop();
    expect(store.publishIssues).toEqual([]);

    // A diagram changed with the dialog closed shows nothing when it opens.
    store.publishIssues = refused;
    dialog = effectScope();
    dialog.run(() => useRefusalIssues(store));
    expect(store.publishIssues).toEqual([]);
    dialog.stop();
  });

  test('Go to selects what an issue is about and asks for focus on its field once', () => {
    const { doc, alpha, edge } = sampleDocument();

    store.doc = doc;

    // The sample's nodes are the switch, alpha and bravo, in that order.
    expect(
      store.goToIssue({
        path: 'nodes[1].device.hostname',
        message: 'a device needs a hostname',
      }),
    ).toBe(true);
    expect(store.selection).toEqual({ nodes: [alpha.id], edges: [] });
    expect(store.focusRequest).toEqual({
      kind: 'nodes',
      id: alpha.id,
      field: 'hostname',
      token: 1,
    });

    expect(store.goToIssue({ edgeId: edge.id, message: 'label' })).toBe(true);
    expect(store.selection).toEqual({ nodes: [], edges: [edge.id] });
    expect(store.focusRequest).toEqual({
      kind: 'edges',
      id: edge.id,
      field: '',
      token: 2,
    });

    // An issue the diagram has nothing for selects nothing and asks nothing.
    for (const issue of [
      'experiment publication failed',
      { nodeId: 'gone', message: 'a device needs a hostname' },
      {
        path: 'nodes[9].device.hostname',
        message: 'a device needs a hostname',
      },
    ]) {
      expect(store.goToIssue(issue), JSON.stringify(issue)).toBe(false);
    }
    expect(store.selection).toEqual({ nodes: [], edges: [edge.id] });
    expect(store.focusRequest.token).toBe(2);

    // The Inspector takes a request with a field once, and only the latest.
    store.goToIssue({ nodeId: alpha.id, field: 'hostname', message: '' });
    expect(store.takeFocusRequest(2)).toBeNull();
    expect(store.takeFocusRequest(3)).toEqual({
      kind: 'nodes',
      id: alpha.id,
      field: 'hostname',
      token: 3,
    });
    expect(store.takeFocusRequest(3)).toBeNull();
    expect(store.focusRequest).toMatchObject({ token: 3, taken: true });

    // A request without a field is not the Inspector's to take.
    store.goToIssue({ edgeId: edge.id, message: 'label' });
    expect(store.focusRequest).toEqual({
      kind: 'edges',
      id: edge.id,
      field: '',
      token: 4,
    });
    expect(store.takeFocusRequest(4)).toBeNull();
  });

  test('Go to a connection while connections are hidden selects its source node', () => {
    const { doc, edge } = sampleDocument();

    store.doc = doc;
    setSetting('showConnections', false, null);

    try {
      expect(
        store.goToIssue({ edgeId: edge.id, field: 'label', message: 'label' }),
      ).toBe(true);
      expect(store.selection).toEqual({
        nodes: [edge.sourceNodeId],
        edges: [],
      });
      expect(store.focusRequest).toEqual({
        kind: 'nodes',
        id: edge.sourceNodeId,
        field: '',
        hiddenEdge: true,
        token: 1,
      });
      expect(store.takeFocusRequest(1)).toBeNull();
    } finally {
      resetSettings(null);
    }
  });

  test('publishing waits for the queue to be confirmed by the server', async () => {
    await withDraft();

    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.publish({
      mode: 'topology',
      topology: { name: 'core', action: 'create' },
    });

    // The pending commit is flushed first so the cursor snapshot the server
    // publishes is the one on screen.
    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);
    expect(api.appendSnapshot.mock.invocationCallOrder[0]).toBeLessThan(
      api.publish.mock.invocationCallOrder[0],
    );
    expect(api.publish.mock.calls[0][2]).toBe('"2"');
  });

  // Two edits made within one save's latency are both saved before the
  // publish goes out, rather than the publish being refused.
  test('edits made just before publishing are all saved first', async () => {
    await withDraft();
    let revision = 1;
    api.appendSnapshot.mockImplementation(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20));
      revision += 1;

      return {
        draft: { id: 'd1', createdBy: 'alice', snapshotId: `s${revision}` },
        history: null,
        etag: `"${revision}"`,
      };
    });

    store.addNode({ kind: 'device', hostname: 'alpha' });
    store.addNode({ kind: 'device', hostname: 'bravo' });
    const result = await store.publish({
      mode: 'topology',
      topology: { name: 'core', action: 'create' },
    });

    expect(store.error).toBe('');
    expect(result).toMatchObject({ ok: true });
    expect(api.appendSnapshot).toHaveBeenCalledTimes(2);
    expect(api.publish.mock.calls[0][2]).toBe('"3"');
  });

  // An edit made during a publish is saved after it, against the publish's
  // ETag, so the user's own publish is no conflict.
  test('an edit made while publishing is saved after it', async () => {
    await withDraft();
    let finish;
    api.publish.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = () =>
            resolve({
              result: { status: 'succeeded', ok: true, stages: [] },
              etag: '"published"',
            });
        }),
    );

    const publishing = store.publish({
      mode: 'topology',
      topology: { name: 'core', action: 'create' },
    });
    await vi.waitFor(() => expect(finish).toBeTypeOf('function'));

    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.queueWork;
    expect(api.appendSnapshot).not.toHaveBeenCalled();
    expect(store.saveState).toMatchObject({ status: 'idle', pending: 1 });

    finish();
    await publishing;
    const saved = await store.saveNow();

    expect(api.appendSnapshot).toHaveBeenCalledOnce();
    expect(api.appendSnapshot.mock.calls[0][2]).toBe('"published"');
    expect(saved).toMatchObject({ status: 'saved', pending: 0 });
    expect(store.hasConflict).toBe(false);
  });

  test('a draft that cannot be saved is never published', async () => {
    await withDraft();

    api.appendSnapshot.mockRejectedValueOnce(
      Object.assign(new Error('conflict'), { response: { status: 412 } }),
    );

    store.addNode({ kind: 'device', hostname: 'alpha' });

    const result = await store.publish({
      mode: 'topology',
      topology: { name: 'core', action: 'create' },
    });

    expect(result).toBeNull();
    expect(api.publish).not.toHaveBeenCalled();
    expect(store.error).toMatch(/conflict/i);
  });

  test('an unsaved diagram is not published', async () => {
    const result = await store.publish({
      mode: 'topology',
      topology: { name: 'core', action: 'create' },
    });

    expect(result).toBeNull();
    expect(api.publish).not.toHaveBeenCalled();
    expect(store.error).toMatch(/draft/i);
  });

  test('a diagram with errors is not published', async () => {
    await withDraft();

    store.addNode({ kind: 'switch', networkName: 'EXP' });
    store.doc.networks[0].name = '';

    expect(store.errors.length).toBeGreaterThan(0);

    await expect(
      store.publish({
        mode: 'topology',
        topology: { name: 'core', action: 'create' },
      }),
    ).resolves.toBeNull();
    expect(api.publish).not.toHaveBeenCalled();
  });

  test('a partial publish is reported and the new ETag is kept', async () => {
    await withDraft();

    api.publish.mockResolvedValueOnce({
      result: {
        status: 'partial',
        ok: false,
        partial: true,
        stages: [
          { name: 'topology', status: 'created', message: '' },
          { name: 'experiment', status: 'failed', message: 'name taken' },
        ],
        failed: [{ name: 'experiment', status: 'failed' }],
        warnings: [],
        errors: [],
      },
      etag: '"9"',
    });

    const result = await store.publish({
      mode: 'topology-experiment',
      topology: { name: 'core', action: 'create' },
      experiment: { name: 'exp', action: 'create' },
    });

    expect(result.partial).toBe(true);
    expect(store.error).toMatch(/failures/i);
    expect(store.etag).toBe('"9"');
  });

  test('a read only draft is not published', async () => {
    await withDraft();
    store.readOnly = true;

    await expect(
      store.publish({
        mode: 'topology',
        topology: { name: 'core', action: 'create' },
      }),
    ).resolves.toBeNull();
    expect(api.publish).not.toHaveBeenCalled();
  });

  // Import opens the diagram only once accepted (see ImportDialog), in a
  // separate draft that never reuses the open draft's queue.
  test('generation leaves the open draft alone, and opening the import starts a separate draft', async () => {
    const { doc } = sampleDocument();

    await withDraft();

    const before = {
      doc: store.doc,
      autosave: store.autosave,
      draftId: store.draftId,
      etag: store.etag,
    };
    const dispose = vi.spyOn(store.autosave, 'dispose');
    api.generate.mockResolvedValueOnce({
      document: doc,
      warnings: [],
      source: { fullName: 'Topology/core', stored: true },
    });

    const result = await store.generate({ kind: 'topology', name: 'core' });

    expect(result.document).toEqual(doc);
    expect(dispose).not.toHaveBeenCalled();
    expect({
      doc: store.doc,
      autosave: store.autosave,
      draftId: store.draftId,
      etag: store.etag,
    }).toEqual(before);
    expect(store.doc).toBe(before.doc);

    expect(store.openImported(result)).toEqual(doc);
    expect(store.doc).toEqual(doc);
    expect(dispose).toHaveBeenCalledOnce();
    expect(store.autosave).toBeNull();
    expect(store.createdBy).toBe('');
    expect(store.etag).toBeNull();
    expect(store.canUndo).toBe(false);
  });

  // The server adds the note naming the copied config (checked in Go). The
  // store passes the notes through unchanged.
  test('a copy opens with the notes the server sent, unchanged', async () => {
    const { doc } = sampleDocument();
    const copied = {
      ...doc,
      metadata: {
        ...doc.metadata,
        name: 'core-copy',
        notes: ['Owned by the lab.', 'Copied from core'],
      },
    };

    api.generate.mockResolvedValueOnce({
      document: copied,
      warnings: [],
      source: { fullName: 'Topology/core', stored: true },
    });

    const result = await store.generate({
      kind: 'topology',
      name: 'core',
      copy: true,
    });

    expect(result.detached).toBe(true);
    expect(store.openImported(result)?.metadata.notes).toEqual([
      'Owned by the lab.',
      'Copied from core',
    ]);
    expect(store.doc.metadata.notes).toEqual([
      'Owned by the lab.',
      'Copied from core',
    ]);
  });

  // The user may still cancel on the warnings, so ImportDialog announces the
  // import once its draft exists.
  test('an import announces nothing before its draft exists', async () => {
    const { doc } = sampleDocument();

    store.announce('Before.');

    for (const warnings of [[], ['one']]) {
      api.generate.mockResolvedValueOnce({
        document: doc,
        warnings,
        source: { fullName: 'Topology/core', stored: true },
      });

      const result = await store.generate({ kind: 'topology', name: 'core' });

      expect(result).toMatchObject({ warnings });
      expect(store.announcement).toBe('Before.');

      store.openImported(result);
      expect(store.announcement).toBe('Before.');
    }
  });

  // The diagram is checked as setDocument checks it before the open draft
  // is detached, so one the editor refuses leaves that draft as it was.
  test('opening an import the editor refuses leaves the open draft, its queue and ETag as they were', async () => {
    await withDraft();

    const open = () => ({
      doc: store.doc,
      history: store.history,
      autosave: store.autosave,
      draftId: store.draftId,
      etag: store.etag,
    });
    const before = open();
    const dispose = vi.spyOn(store.autosave, 'dispose');

    expect(before).toMatchObject({ draftId: 'd1' });
    expect(before.etag).toBeTruthy();
    expect(before.autosave).toBeTruthy();
    expect(
      store.openImported({
        document: { ...sampleDocument().doc, revision: 99 },
        warnings: [],
        source: { fullName: 'Topology/core', stored: true },
      }),
    ).toBeNull();
    expect(store.error).toMatch(/Unsupported builder document revision 99/);
    expect(dispose).not.toHaveBeenCalled();
    expect(open()).toEqual(before);
    expect(store.doc).toBe(before.doc);
    expect(store.autosave).toBe(before.autosave);
  });

  test('generate rejects an unreadable document without changing the current draft', async () => {
    await withDraft();

    const beforeDocument = store.doc;
    const beforeAutosave = store.autosave;
    const result = await store.generate({ kind: 'topology', name: 'core' });

    expect(result).toBeNull();
    // The server answered, so this is not a connection failure. The reason
    // is the decoder's.
    expect(store.error).toBe(
      'Could not import the diagram. The server sent a diagram the Builder cannot open: A builder document must be a JSON object.',
    );
    expect(store.errorField).toBe('');
    expect(store.doc).toBe(beforeDocument);
    expect(store.autosave).toBe(beforeAutosave);
    expect(store.draftId).toBe('d1');

    api.generate.mockResolvedValueOnce({
      document: { ...sampleDocument().doc, revision: 99 },
      warnings: [],
    });
    await store.generate({ content: 'kind: Topology' });
    expect(store.error).toMatch(
      /^Could not import the diagram\. The server sent a diagram the Builder cannot open: Unsupported builder document revision 99 \(expected \d+\)\.$/,
    );
    expect(store.errorField).toBe('');
    expect(store.doc).toBe(beforeDocument);
  });

  test('a generate source the server refuses is reported on its field', async () => {
    const refusal = () =>
      Object.assign(new Error('refused'), {
        response: {
          status: 422,
          data: { message: 'the Builder cannot open Scenario configs' },
        },
      });

    api.generate.mockRejectedValueOnce(refusal());
    await store.generate({ content: 'kind: Scenario' });
    expect(store.error).toBe(
      'Could not import the diagram. The Builder cannot open Scenario configs.',
    );
    expect(store.errorField).toBe('content');

    api.generate.mockRejectedValueOnce(refusal());
    await store.generate({ kind: 'topology', name: 'core' });
    expect(store.errorField).toBe('name');

    // An unreachable server is not about what was sent.
    api.generate.mockRejectedValueOnce(new Error('offline'));
    await store.generate({ content: 'kind: Topology' });
    expect(store.errorField).toBe('');
  });

  test('a stored generate source removed since the list was read is reported on its field', async () => {
    const missing = () =>
      Object.assign(new Error('missing'), {
        response: {
          status: 404,
          data: { message: 'phenix cannot find config Topology/gone' },
        },
      });

    api.generate.mockRejectedValueOnce(missing());
    await store.generate({ kind: 'topology', name: 'gone' });

    // The list is read again, so the removed config is no longer offered.
    expect(api.getSources).toHaveBeenCalledOnce();
    expect(store.error).toBe(
      'Could not import the diagram. The topology "gone" no longer exists. Choose another one.',
    );
    expect(store.errorField).toBe('name');

    // A stored source the server still lists is not called removed, but the
    // refusal is still about the name that chose it.
    api.generate.mockRejectedValueOnce(missing());
    await store.generate({ kind: 'topology', name: 'core' });
    expect(store.error).toBe(
      'Could not import the diagram. Phenix cannot find config Topology/gone.',
    );
    expect(store.errorField).toBe('name');
  });

  test('a copy and a combined import say they are linked to no config', async () => {
    const { doc } = sampleDocument();
    const answer = {
      document: {
        ...setDocumentInfo(doc, { name: 'core-copy' }),
        source: { kind: 'manual' },
      },
      warnings: [],
      source: { fullName: 'Topology/core', name: 'core', stored: true },
    };
    const stored = { kind: 'topology', name: 'core' };

    for (const request of [
      { ...stored, copy: true, newName: 'core-copy' },
      { ...stored, includes: 'combine', newName: 'core-copy' },
      { content: 'kind: Topology', includes: 'combine', newName: 'core-copy' },
    ]) {
      api.generate.mockResolvedValueOnce(structuredClone(answer));

      const result = await store.generate(request);

      expect(api.generate).toHaveBeenLastCalledWith(request);
      expect(result.detached).toBe(true);
      // The source is still the config that was read.
      expect(result.source.fullName).toBe('Topology/core');
      expect(result.document.metadata.name).toBe('core-copy');
    }

    // A plain import, and one that keeps its includes, is still linked.
    for (const request of [stored, { ...stored, includes: 'keep' }]) {
      api.generate.mockResolvedValueOnce(structuredClone(answer));

      expect(await store.generate(request)).not.toHaveProperty('detached');
    }
  });

  // The field is picked by the refusal's code, not its words.
  test('a new topology name the server refuses is reported on its field', async () => {
    const refusal = (message, code) =>
      Object.assign(new Error('refused'), {
        response: { status: 422, data: { message, cause: '', code } },
      });
    const request = { kind: 'topology', name: 'core', copy: true };

    api.generate.mockRejectedValueOnce(
      refusal(
        'new topology name "core" is the name of the imported topology. Enter another name',
        'import.name.source',
      ),
    );
    await expect(
      store.generate({ ...request, newName: 'core' }),
    ).resolves.toBeNull();
    expect(store.error).toBe(
      'Could not import the diagram. New topology name "core" is the name of the imported topology. Enter another name.',
    );
    expect(store.errorField).toBe('newName');

    api.generate.mockRejectedValueOnce(
      refusal(
        'new topology name "a b" is not valid. Use 1 to 512 letters, numbers, underscores, at signs, periods and hyphens',
        'import.name.invalid',
      ),
    );
    await store.generate({ ...request, newName: 'a b' });
    expect(store.errorField).toBe('newName');

    // Words alone, without the code, do not name the field.
    api.generate.mockRejectedValueOnce(
      refusal('new topology name "a b" is not valid', 'request.unprocessable'),
    );
    await store.generate({ ...request, newName: 'a b' });
    expect(store.errorField).toBe('name');

    // Any other refusal of the same request is about its source.
    api.generate.mockRejectedValueOnce(
      refusal(
        'an import can combine or copy only a topology',
        'request.unprocessable',
      ),
    );
    await store.generate({ ...request, newName: 'core-copy' });
    expect(store.errorField).toBe('name');
  });

  // Upload opens over the open draft and may still be canceled, so the
  // conversion only returns the diagram. The dialog loads it.
  test('a legacy conversion returns the diagram and leaves the open draft as it is', async () => {
    const { doc } = sampleDocument();

    await withDraft();
    store.announce('Before.');

    const beforeDocument = store.doc;
    const beforeAutosave = store.autosave;
    const dispose = vi.spyOn(store.autosave, 'dispose');

    api.convertLegacy.mockResolvedValueOnce({
      document: doc,
      warnings: ['The diagram has no nodes.'],
      source: null,
    });

    const result = await store.convertLegacy({
      content: '<mxGraphModel/>',
      name: 'plant',
    });

    expect(api.convertLegacy).toHaveBeenCalledWith({
      content: '<mxGraphModel/>',
      name: 'plant',
    });
    // A diagram without a topology names no config: its draft takes the
    // token of an upload, which updates none.
    expect(result).toEqual({
      document: doc,
      warnings: ['The diagram has no nodes.'],
      source: null,
      sourceToken: 'uploaded/legacy-xml',
    });
    expect(dispose).not.toHaveBeenCalled();
    expect(store.doc).toBe(beforeDocument);
    expect(store.autosave).toBe(beforeAutosave);
    expect(store.draftId).toBe('d1');
    expect(store.etag).toBe('"1"');
    expect(store.error).toBe('');
    expect(store.announcement).toBe('Before.');
  });

  test('a legacy conversion of a Topology config gives its source and no token', async () => {
    const { doc } = sampleDocument();
    const source = {
      kind: 'Topology',
      name: 'plant',
      fullName: 'Topology/plant',
      stored: false,
      builder: 'builder-xml',
    };

    api.convertLegacy.mockResolvedValueOnce({
      document: doc,
      warnings: [],
      source,
    });

    const result = await store.convertLegacy({ content: 'kind: Topology' });

    // The view then makes the token of a config file from the source.
    expect(result).toEqual({ document: doc, warnings: [], source });
    expect(result).not.toHaveProperty('sourceToken');
    expect(api.convertLegacy).toHaveBeenCalledWith({
      content: 'kind: Topology',
      name: '',
    });
  });

  // The server's words, a topology name with an id in it too.
  test.each([
    [
      422,
      'this is not a legacy Builder diagram. A legacy diagram is mxGraph XML, or a Topology config with the builder-xml annotation',
    ],
    [
      422,
      'topology lab-123e4567-e89b-12d3-a456-426614174000 has no legacy Builder diagram (no builder-xml annotation). Select Import to make a diagram from it',
    ],
    [413, 'the legacy diagram is larger than 5242880 bytes'],
    [400, 'the request needs content'],
  ])(
    'a legacy file the server refuses (%i) is reported in its words, on the file',
    async (status, message) => {
      api.convertLegacy.mockRejectedValueOnce(
        Object.assign(new Error('refused'), {
          response: { status, data: { message, cause: '' } },
        }),
      );

      await expect(
        store.convertLegacy({ content: 'hello' }),
      ).resolves.toBeNull();
      expect(store.error).toBe(
        `Could not convert the legacy diagram. ${message[0].toUpperCase()}${message.slice(1)}.`,
      );
      expect(store.errorField).toBe('content');
    },
  );

  test('a legacy conversion that fails for another reason is not about the file', async () => {
    const convert = () => store.convertLegacy({ content: '<mxGraphModel/>' });

    // A role without the permission, and an unreachable server.
    api.convertLegacy.mockRejectedValueOnce(
      Object.assign(new Error('forbidden'), {
        response: {
          status: 403,
          data: {
            message:
              'converting a legacy Builder diagram: user alice does not have permission',
          },
        },
      }),
    );
    await expect(convert()).resolves.toBeNull();
    expect(store.error).toBe(
      'Could not convert the legacy diagram. Converting a legacy Builder diagram: user alice does not have permission.',
    );
    expect(store.errorField).toBe('');

    api.convertLegacy.mockRejectedValueOnce(new Error('offline'));
    await convert();
    expect(store.error).toBe(
      'Could not convert the legacy diagram. The Builder could not reach the server. Check the connection and try again.',
    );
    expect(store.errorField).toBe('');

    // The server answered with a diagram the Builder cannot open: the
    // reason is the decoder's, and the open document stays.
    const before = store.doc;
    await convert();
    expect(store.error).toBe(
      'Could not convert the legacy diagram. The server sent a diagram the Builder cannot open: A builder document must be a JSON object.',
    );
    expect(store.errorField).toBe('');
    expect(store.doc).toBe(before);

    // Whatever else the check of that diagram throws is no connection
    // problem either: the server did answer.
    const unreadable = new Proxy(
      {},
      {
        get() {
          throw new TypeError('x.map is not a function');
        },
      },
    );

    api.convertLegacy.mockResolvedValueOnce({
      document: unreadable,
      warnings: [],
      source: null,
    });
    await expect(convert()).resolves.toBeNull();
    expect(store.error).toBe(
      'Could not convert the legacy diagram. The server sent a diagram the Builder cannot open.',
    );
    expect(store.errorField).toBe('');
    expect(store.doc).toBe(before);

    api.generate.mockResolvedValueOnce({
      document: unreadable,
      warnings: [],
      source: { fullName: 'Topology/core', stored: true },
    });
    await expect(
      store.generate({ kind: 'topology', name: 'core' }),
    ).resolves.toBeNull();
    expect(store.error).toBe(
      'Could not import the diagram. The server sent a diagram the Builder cannot open.',
    );
    expect(store.doc).toBe(before);
  });

  test('deleting a draft passes its ETag', async () => {
    // What this device kept for the draft goes with it, whoever edited it
    // here, and nothing else does.
    const keep = (actor, draftId) =>
      device.store.put({
        key: `${actor}::${draftId}`,
        actor,
        draftId,
        entries: [],
        queue: [{ kind: 'cursor', snapshotId: 's1' }],
      });
    await keep('alice', 'd1');
    await keep('bob', 'd1');
    await keep('alice', 'd2');

    await store.deleteDraft('d1', '"1"', 'Lab network');

    expect(api.deleteDraft).toHaveBeenCalledWith('d1', '"1"');
    expect(store.announcement).toBe('Deleted draft Lab network.');
    expect((await device.store.all()).map((record) => record.key)).toEqual([
      'alice::d2',
    ]);
  });

  // Clean up in the settings asks the server first, and nothing is deleted.
  test('a cleanup plan is a dry run on the server', async () => {
    api.cleanUpStorage.mockResolvedValueOnce({
      drafts: ['d1'],
      documents: 0,
      content: 2,
    });

    await expect(store.planStorageCleanup()).resolves.toEqual({
      drafts: ['d1'],
      documents: 0,
      content: 2,
    });
    expect(api.cleanUpStorage).toHaveBeenLastCalledWith({ dryRun: true });
  });

  // What this device kept for a draft that the cleanup deleted goes with it,
  // and the lists are read again. The open draft gets the page alert.
  test('a cleanup forgets the deleted drafts, and says so of the open one', async () => {
    const keep = (draftId) =>
      device.store.put({
        key: `alice::${draftId}`,
        actor: 'alice',
        draftId,
        entries: [],
        queue: [],
      });
    await keep('d1');
    await keep('d2');
    api.listDrafts.mockClear();
    api.cleanUpStorage.mockResolvedValueOnce({
      drafts: ['d2'],
      documents: 1,
      content: 0,
    });

    await expect(store.cleanUpStorage()).resolves.toEqual({
      drafts: ['d2'],
      documents: 1,
      content: 0,
      open: false,
    });
    expect(api.cleanUpStorage).toHaveBeenLastCalledWith();
    expect(api.listDrafts).toHaveBeenCalledTimes(1);
    expect((await device.store.all()).map((record) => record.key)).toEqual([
      'alice::d1',
    ]);
    expect(store.error).toBe('');

    store.draftId = 'd1';
    api.cleanUpStorage.mockResolvedValueOnce({
      drafts: ['d1'],
      documents: 0,
      content: 0,
    });

    await expect(store.cleanUpStorage()).resolves.toMatchObject({
      open: true,
    });
    expect(store.error).toBe(
      'Clean up deleted this draft, which was published and unchanged. Its published topology stays. Select Download to keep a copy of the diagram.',
    );
    expect(await device.store.all()).toEqual([]);
  });

  // A draft closed for the drafts page, another tab's or another user's,
  // can hold changes that only this device has. When the server would
  // delete it, nothing is cleaned up. Other unsent drafts do not stop it.
  test('a cleanup does nothing when a draft it would delete has unsent changes here', async () => {
    const keep = (actor, draftId) =>
      device.store.put({
        key: `${actor}::${draftId}`,
        actor,
        draftId,
        entries: [],
        queue: [{ kind: 'cursor', snapshotId: 's1' }],
      });
    await keep('bob', 'd2');
    await keep('alice', 'd3');
    api.cleanUpStorage.mockClear();
    api.cleanUpStorage.mockResolvedValueOnce({
      drafts: ['d1', 'd2'],
      documents: 0,
      content: 0,
    });

    await expect(store.cleanUpStorage()).resolves.toEqual({ unsent: 1 });
    expect(api.cleanUpStorage.mock.calls).toEqual([[{ dryRun: true }]]);
    expect(
      (await device.store.all()).map((record) => record.draftId).sort(),
    ).toEqual(['d2', 'd3']);

    api.cleanUpStorage.mockClear();
    api.cleanUpStorage
      .mockResolvedValueOnce({ drafts: ['d1'], documents: 0, content: 0 })
      .mockResolvedValueOnce({ drafts: ['d1'], documents: 0, content: 0 });

    await expect(store.cleanUpStorage()).resolves.toMatchObject({
      drafts: ['d1'],
    });
    expect(api.cleanUpStorage.mock.calls).toEqual([[{ dryRun: true }], []]);
  });

  // The server would take a draft whose edits are only on this device for
  // unchanged. So nothing is cleaned up, and what this device kept stays.
  test('a cleanup does nothing while the open draft has unsaved changes', async () => {
    await withDraft();

    try {
      api.appendSnapshot.mockRejectedValue(
        Object.assign(new Error('unavailable'), {
          response: { status: 503, data: {} },
        }),
      );
      store.addNode({ kind: 'device', hostname: 'alpha' });

      await expect(store.cleanUpStorage()).resolves.toBeNull();
      expect(store.saveState.pending).toBeGreaterThan(0);
      expect(api.cleanUpStorage).not.toHaveBeenCalled();
      expect(
        (await device.store.all()).map((record) => record.draftId),
      ).toContain('d1');
    } finally {
      api.appendSnapshot.mockReset();
    }
  });

  // A card's ETag is older than the server's right after the Drafts button,
  // until the list is read again.
  test('deleting with an old ETag reads the draft again and deletes it', async () => {
    const changed = () =>
      Object.assign(new Error('changed'), { response: { status: 412 } });

    api.deleteDraft.mockRejectedValueOnce(changed());
    api.getDraft.mockResolvedValueOnce({ draft: { id: 'd1' }, etag: '"2"' });

    expect(await store.deleteDraft('d1', '"1"', 'Lab network')).toBe(true);
    expect(api.getDraft).toHaveBeenCalledWith('d1');
    expect(api.deleteDraft).toHaveBeenLastCalledWith('d1', '"2"');
    expect(store.announcement).toBe('Deleted draft Lab network.');
    expect(store.error).toBe('');

    // Once only: a draft that changes again is left, and the alert says so.
    api.deleteDraft.mockClear();
    api.deleteDraft
      .mockRejectedValueOnce(changed())
      .mockRejectedValueOnce(changed());
    api.getDraft.mockResolvedValueOnce({ draft: { id: 'd1' }, etag: '"3"' });

    expect(await store.deleteDraft('d1', '"2"')).toBe(false);
    expect(api.deleteDraft).toHaveBeenCalledTimes(2);
    expect(store.error).toBe(
      'Could not delete the draft. It changed on the server during the delete. Try again.',
    );

    // A draft gone meanwhile says so.
    api.deleteDraft.mockClear();
    api.deleteDraft.mockRejectedValueOnce(changed());
    api.getDraft.mockRejectedValueOnce(
      Object.assign(new Error('gone'), { response: { status: 404 } }),
    );

    expect(await store.deleteDraft('d1', '"2"')).toBe(false);
    expect(api.deleteDraft).toHaveBeenCalledTimes(1);
    expect(store.error).toBe(
      'Could not delete the draft. This draft no longer exists on the server.',
    );
  });

  test('deleting a published topology removes its card, once', async () => {
    store.documents = [
      { id: 'p1', kind: 'Topology', target: 'lab', etag: '"a1"' },
      { id: 'p2', kind: 'Topology', target: 'core', etag: '"a2"' },
    ];
    store.sources = {
      ...store.sources,
      topologies: [{ name: 'lab' }, { name: 'core' }],
    };

    let finish;
    api.deleteDocument.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const deleting = store.deletePublished(store.documents[0]);

    // A second click while the first runs sends nothing.
    expect(store.deletingDocuments).toEqual(['p1']);
    expect(await store.deletePublished(store.documents[0])).toBe(false);
    expect(api.deleteDocument).toHaveBeenCalledOnce();

    finish(true);
    expect(await deleting).toBe(true);
    expect(api.deleteDocument).toHaveBeenCalledWith('p1', '"a1"');
    expect(store.documents.map((entry) => entry.id)).toEqual(['p2']);
    // Publish no longer offers to update it.
    expect(store.sources.topologies).toEqual([{ name: 'core' }]);
    expect(store.announcement).toBe('Deleted topology lab.');
    expect(store.deletingDocuments).toEqual([]);
  });

  test('a published topology the server does not delete keeps its card', async () => {
    const refused = (status, message) =>
      Object.assign(new Error('refused'), {
        response: { status, data: { message } },
      });
    const lab = { id: 'p1', kind: 'Topology', target: 'lab' };

    store.documents = [lab];

    for (const [error, message] of [
      [
        refused(
          422,
          'You can delete only published topologies here. Delete experiments from the Experiments page.',
        ),
        'Could not delete topology lab. You can delete only published topologies here. Delete experiments from the Experiments page.',
      ],
      [
        refused(
          403,
          'deleting config Topology/lab: user alice does not have permission',
        ),
        'Could not delete topology lab. Deleting config Topology/lab: user alice does not have permission.',
      ],
      [
        new Error('Network Error'),
        'Could not delete topology lab. The Builder could not reach the server. Check the connection and try again.',
      ],
    ]) {
      api.deleteDocument.mockRejectedValueOnce(error);

      expect(await store.deletePublished(lab)).toBe(false);
      expect(store.error).toBe(message);
      expect(store.documents).toEqual([lab]);
      expect(store.deletingDocuments).toEqual([]);
    }

    expect(api.listDocuments).not.toHaveBeenCalled();

    // Deleted or published again elsewhere: the list is read again.
    api.deleteDocument.mockRejectedValueOnce(
      refused(404, 'phenix cannot find document p1'),
    );
    api.listDocuments.mockResolvedValueOnce([]);

    expect(await store.deletePublished(lab)).toBe(false);
    expect(store.error).toBe(
      'Could not delete topology lab. Someone deleted it or published it again after Builder read the list.',
    );
    expect(api.listDocuments).toHaveBeenCalledOnce();
    expect(store.documents).toEqual([]);

    // Changed elsewhere after the list was read: the list is read again.
    store.documents = [lab];
    api.deleteDocument.mockRejectedValueOnce(
      refused(412, 'topology lab changed after you last read it'),
    );
    api.listDocuments.mockResolvedValueOnce([lab]);

    expect(await store.deletePublished(lab)).toBe(false);
    expect(store.error).toBe(
      'Could not delete topology lab. It changed after Builder read the list. The list now shows it as it is. Check it, and then delete it again.',
    );
    expect(api.listDocuments).toHaveBeenCalledTimes(2);
    expect(store.documents).toEqual([lab]);
  });

  test('a page error is cleared when the operation next succeeds', async () => {
    api.listDrafts.mockRejectedValueOnce(
      Object.assign(new Error('boom'), {
        response: { status: 500, data: { message: 'list failed' } },
      }),
    );

    await store.fetchDrafts();
    // The server's reason is shown as a sentence.
    expect(store.error).toBe('Could not list drafts. List failed.');

    // The same failure twice is shown (and announced) twice.
    const seq = store.errorSeq;
    store.setError(store.error);
    expect(store.errorSeq).toBe(seq + 1);

    await store.fetchDrafts();
    expect(store.error).toBe('');
  });
});

// A draft as GET /builder/drafts/{draft} answers.
function readDraft(draft = {}, { etag = '"1"', snapshots = 1 } = {}) {
  return {
    draft: {
      id: 'd1',
      createdBy: 'alice',
      snapshotId: `s${snapshots}`,
      cursor: snapshots - 1,
      snapshots,
      ...draft,
    },
    document: sampleDocument().doc,
    history: Array.from({ length: snapshots }, (_, index) => ({
      id: `s${index + 1}`,
    })),
    cursor: snapshots - 1,
    etag,
  };
}

describe('save state announcements', () => {
  test('a problem is announced once, and so is the recovery', () => {
    const offline = { status: 'offline', pending: 1, message: 'x' };

    store.announceSaveState({ status: 'saved', pending: 0 });
    const seq = store.announcementSeq;

    store.announceSaveState({ status: 'saving', pending: 1 });
    expect(store.announcementSeq).toBe(seq);

    store.announceSaveState(offline);
    expect(store.announcement).toMatch(/^Offline: 1 change kept/);

    // Retrying on a timer ends in the same state: nothing new to say.
    store.announceSaveState({ status: 'saving', pending: 1 });
    store.announceSaveState(offline);
    expect(store.announcementSeq).toBe(seq + 1);

    store.announceSaveState({ status: 'saved', pending: 0 });
    expect(store.announcement).toBe('All changes saved.');

    // Discarding the work behind a conflict is not a recovery: it is
    // announced by the discard itself, not as "All changes saved".
    store.announceSaveState(offline);
    store.announceSaveState({ status: 'conflict', pending: 1 });
    const beforeDiscard = store.announcementSeq;
    store.announceSaveState({ status: 'saved', pending: 0 });
    expect(store.announcementSeq).toBe(beforeDiscard);
  });

  test('Save now always says how it ended', async () => {
    await withDraft();
    const seq = store.announcementSeq;

    await store.saveNow({ announce: true });

    expect(store.announcementSeq).toBe(seq + 1);
    expect(store.announcement).toBe('All changes saved.');
    expect(store.announcementSlot).toBe('save');

    // What the save left out is said with it.
    const note =
      "The Inspector's changes are not applied yet: 1 field needs attention.";

    await store.saveNow({ announce: true, note });
    expect(store.announcement).toBe(`Saved. ${note}`);
  });

  test('Retry saving says how it ended, even when it failed the same way', async () => {
    const failure = () =>
      Object.assign(new Error('boom'), {
        response: { status: 500, data: { message: 'storage down' } },
      });

    await withDraft();
    api.appendSnapshot.mockRejectedValueOnce(failure());
    store.addNode({ kind: 'note' });
    await store.queueWork;
    expect(store.saveState.status).toBe('error');
    expect(store.announcement).toMatch(/^Could not save your changes/);

    api.appendSnapshot.mockRejectedValueOnce(failure());
    const seq = store.announcementSeq;
    await store.retrySave({ announce: true });

    expect(store.announcementSeq).toBe(seq + 1);
    expect(store.announcement).toMatch(/^Could not save your changes/);

    await store.retrySave({ announce: true });

    expect(store.announcementSeq).toBe(seq + 2);
    expect(store.announcement).toBe('All changes saved.');
  });
});

describe('theme', () => {
  // The toggle's order is nextTheme's (see theme.contract.test.js).
  test('a theme set is announced, unless the Settings dialog sets it', () => {
    const element = { dataset: {}, setAttribute() {} };

    store.setTheme('light', element);
    expect(store.theme).toBe('light');
    expect(store.announcement).toBe('Theme set to light.');

    const said = store.announcementSeq;
    store.setTheme('dark', element, { announce: false });
    expect(store.theme).toBe('dark');
    expect(store.announcementSeq).toBe(said);

    // No matchMedia here: the system is taken as light.
    store.setTheme('system', element);
    store.cycleTheme(element);
    expect(store.theme).toBe('dark');

    vi.stubGlobal('window', {
      matchMedia: (query) => ({
        matches: query === '(prefers-color-scheme: dark)',
      }),
    });
    try {
      store.setTheme('system', element);
      expect(store.resolvedTheme).toBe('dark');
      store.cycleTheme(element);
      expect(store.theme).toBe('light');
    } finally {
      vi.unstubAllGlobals();
    }
  });

  test('blocked site data leaves the default theme instead of throwing', () => {
    const element = { dataset: {}, setAttribute() {} };
    const saved = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      get() {
        throw new Error('SecurityError: access denied');
      },
    });
    try {
      expect(store.initTheme(element)).toBe('light');
      expect(store.theme).toBe('system');
      expect(store.setTheme('dark', element)).toBe('dark');
    } finally {
      if (saved) {
        Object.defineProperty(globalThis, 'localStorage', saved);
      } else {
        delete globalThis.localStorage;
      }
    }
  });
});

// The server stamps who made and last saved the document, and when. The
// editor copies the stamps and never makes them up.
describe('who made and last saved the diagram', () => {
  const created = {
    createdBy: 'alice',
    createdAt: '2026-10-01T15:04:05Z',
    updatedBy: 'alice',
    updatedAt: '2026-10-01T15:04:05Z',
  };
  const saved = (seconds, by = 'alice') => ({
    createdBy: 'alice',
    createdAt: '2026-10-01T15:04:05Z',
    updatedBy: by,
    updatedAt: `2026-10-01T16:00:${String(seconds).padStart(2, '0')}Z`,
  });
  const stampOf = (doc) =>
    Object.fromEntries(
      STAMP_KEYS.filter((key) => key in doc.metadata).map((key) => [
        key,
        doc.metadata[key],
      ]),
    );

  // A save that answers with the stamp the server wrote.
  function saveAnswers(snapshotId, stamp, etag) {
    api.appendSnapshot.mockResolvedValueOnce({
      draft: { id: 'd1', createdBy: 'alice', snapshotId, stamp },
      history: null,
      etag,
    });
  }

  test('a new draft takes the stamp its create answered with', async () => {
    api.createDraft.mockResolvedValueOnce({
      draft: { id: 'd1', createdBy: 'alice', snapshotId: 's1', stamp: created },
      document: null,
      history: null,
      cursor: 0,
      etag: '"1"',
    });

    await store.createDraft({ title: 'Test' });

    // What was sent names no one: the editor does not sign its documents.
    expect(stampOf(api.createDraft.mock.calls[0][0].document)).toEqual({});
    expect(stampOf(store.doc)).toEqual(created);
    expect(Object.keys(store.doc.metadata)).toEqual([
      'id',
      'name',
      'description',
      ...STAMP_KEYS,
    ]);
    // The history starts from the stamped document, so the first edit is
    // built from it.
    expect(store.history.current()).toEqual(store.doc);
    expect(stampOf(store.history.currentEntry().snapshot)).toEqual(created);
  });

  test('a create that answers with no stamp leaves the diagram as it was', async () => {
    await store.createDraft({ title: 'Test' });

    expect(stampOf(store.doc)).toEqual({});
  });

  test('a confirmed save stamps its entry and the diagram shown', async () => {
    await withDraft();
    saveAnswers('s2', saved(1), '"2"');

    store.addNode({ kind: 'device', hostname: 'alpha' });

    // Until the server answers, the diagram shows the save before.
    expect(stampOf(store.doc)).toEqual({});

    await store.saveNow();

    expect(stampOf(api.appendSnapshot.mock.calls[0][1].document)).toEqual({});
    expect(stampOf(store.doc)).toEqual(saved(1));
    expect(stampOf(store.history.current())).toEqual(saved(1));
    expect(store.doc.nodes).toHaveLength(1);
    // The next edit is built from the stamped diagram and sends that stamp,
    // which the server replaces.
    saveAnswers('s3', saved(2, 'bob'), '"3"');
    store.addNode({ kind: 'device', hostname: 'bravo' });
    await store.saveNow();

    expect(stampOf(api.appendSnapshot.mock.calls[1][1].document)).toEqual(
      saved(1),
    );
    expect(stampOf(store.doc)).toEqual(saved(2, 'bob'));
  });

  // A pan makes the diagram another object than its history entry.
  test('a save confirmed after a pan stamps the diagram and keeps the pan', async () => {
    await withDraft();
    saveAnswers('s2', saved(1), '"2"');

    store.addNode({ kind: 'device', hostname: 'alpha' });
    store.setViewport({ x: 40, y: -8, zoom: 2 });
    await store.saveNow();

    expect(stampOf(store.doc)).toEqual(saved(1));
    expect(store.doc.viewport).toEqual({ x: 40, y: -8, zoom: 2 });
    expect(stampOf(store.history.current())).toEqual(saved(1));
    expect(store.history.current().viewport).toEqual({ x: 0, y: 0, zoom: 1 });
  });

  test('a save confirmed after the next edit stamps its own entry only', async () => {
    await withDraft();

    // Each save answers when the test says so.
    const confirm = [];
    const answered = (snapshotId, stamp, etag) => () =>
      new Promise((resolve) => {
        confirm.push(() =>
          resolve({
            draft: { id: 'd1', createdBy: 'alice', snapshotId, stamp },
            history: null,
            etag,
          }),
        );
      });

    api.appendSnapshot
      .mockImplementationOnce(answered('s2', saved(1), '"2"'))
      .mockImplementationOnce(answered('s3', saved(2), '"3"'));

    store.addNode({ kind: 'device', hostname: 'alpha' });
    await vi.waitFor(() => expect(confirm).toHaveLength(1));
    store.addNode({ kind: 'device', hostname: 'bravo' });

    const [, first, second] = store.history.entries;

    confirm[0]();
    await vi.waitFor(() => expect(confirm).toHaveLength(2));

    // The first edit's entry holds its save. The diagram shown is the
    // second edit's, which is not saved yet: it shows no save of its own.
    expect(stampOf(first.snapshot)).toEqual(saved(1));
    expect(first.snapshot.nodes).toHaveLength(1);
    expect(stampOf(second.snapshot)).toEqual({});
    expect(stampOf(store.doc)).toEqual({});
    expect(store.doc.nodes).toHaveLength(2);

    confirm[1]();
    await store.saveNow();

    expect(stampOf(first.snapshot)).toEqual(saved(1));
    expect(stampOf(second.snapshot)).toEqual(saved(2));
    expect(stampOf(store.doc)).toEqual(saved(2));
    expect(store.doc.nodes).toHaveLength(2);
  });

  test('undo shows the stamp of the save it goes back to', async () => {
    await withDraft();
    saveAnswers('s2', saved(1), '"2"');
    saveAnswers('s3', saved(2, 'bob'), '"3"');

    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.saveNow();
    store.addNode({ kind: 'device', hostname: 'bravo' });
    await store.saveNow();
    expect(stampOf(store.doc)).toEqual(saved(2, 'bob'));

    store.undo();
    await store.saveNow();

    // A cursor move stores no document, and answers with no stamp.
    expect(stampOf(store.doc)).toEqual(saved(1));

    store.redo();
    await store.saveNow();

    expect(stampOf(store.doc)).toEqual(saved(2, 'bob'));
  });

  test('saving the history as a new draft stamps each entry as the new draft stored it', async () => {
    await withDraft();
    saveAnswers('s2', saved(1), '"2"');
    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.saveNow();

    api.appendSnapshot.mockRejectedValueOnce(
      Object.assign(new Error('conflict'), { response: { status: 412 } }),
    );
    store.addNode({ kind: 'device', hostname: 'bravo' });
    await store.saveNow();
    expect(store.hasConflict).toBe(true);

    const forked = (seconds) => ({
      ...saved(seconds, 'carol'),
      updatedAt: `2026-10-02T09:00:${String(seconds).padStart(2, '0')}Z`,
    });

    api.createDraft.mockResolvedValueOnce({
      draft: {
        id: 'd9',
        createdBy: 'alice',
        snapshotId: 'f1',
        stamp: forked(1),
      },
      history: null,
      etag: '"f1"',
    });
    [2, 3].forEach((n) =>
      api.appendSnapshot.mockResolvedValueOnce({
        draft: {
          id: 'd9',
          createdBy: 'alice',
          snapshotId: `f${n}`,
          stamp: forked(n),
        },
        history: null,
        etag: `"f${n}"`,
      }),
    );

    await store.resolveConflict('fork', { title: 'Mine' });

    expect(
      store.history.entries.map((entry) => stampOf(entry.snapshot)),
    ).toEqual([forked(1), forked(2), forked(3)]);
    expect(stampOf(store.doc)).toEqual(forked(3));
    expect(store.doc.metadata.name).toBe('Mine');
    // No file name is sent: a fork is not an upload.
    expect(api.createDraft.mock.calls[0][0]).not.toHaveProperty('sourceFile');

    store.undo();
    expect(stampOf(store.doc)).toEqual(forked(2));
    await store.saveNow();
  });

  // A save whose answer was lost takes its stamp (who, and when to the
  // second) from the snapshot the server lists.
  test('a recovered save the server already holds takes its snapshot’s user and time', async () => {
    const opened = withStamp(createDocument({ name: 'Opened' }), created);
    const sent = setDocumentInfo(opened, { name: 'Sent' });
    const later = setDocumentInfo(opened, { name: 'Later' });

    await device.store.put(
      {
        key: 'alice::d1',
        actor: 'alice',
        draftId: 'd1',
        etag: '"1"',
        cursor: 0,
        entries: [
          { id: 'c1', label: 'Sent' },
          { id: 'c2', label: 'Later' },
        ],
        queue: [
          { opId: 'c1', kind: 'snapshot', commitId: 'c1', label: 'Sent' },
          { opId: 'c2', kind: 'snapshot', commitId: 'c2', label: 'Later' },
          { opId: 'cursor-c1-2', kind: 'cursor', commitId: 'c1' },
        ],
      },
      {
        write: [
          { id: 'c1', snapshot: sent },
          { id: 'c2', snapshot: later },
        ],
      },
    );
    api.getDraft.mockResolvedValueOnce({
      draft: { id: 'd1', createdBy: 'alice' },
      // The server's copy of that save, which the editor reads but does
      // not put in place of the entry this device holds.
      document: withStamp(sent, saved(30, 'bob')),
      history: [
        { id: 's1', current: false, createdBy: 'alice' },
        {
          id: 's2',
          opId: 'c1',
          current: true,
          createdBy: 'bob',
          createdAt: '2026-10-01T16:00:30.123456Z',
        },
      ],
      cursor: 1,
      etag: '"2"',
    });
    saveAnswers('s3', saved(45), '"3"');

    await store.loadDraft('d1');
    await store.saveNow();

    expect(store.hasConflict).toBe(false);
    expect(api.appendSnapshot).toHaveBeenCalledOnce();
    expect(api.moveCursor).toHaveBeenCalledWith(
      'd1',
      { snapshotId: 's2' },
      '"3"',
    );
    // The undo left the diagram at the save recovered, which this device
    // held without a stamp.
    expect(store.doc.metadata.name).toBe('Sent');
    expect(store.history.currentEntry().id).toBe('c1');
    expect(stampOf(store.doc)).toEqual(saved(30, 'bob'));

    store.redo();
    expect(store.doc.metadata.name).toBe('Later');
    expect(stampOf(store.doc)).toEqual(saved(45));
    await store.saveNow();
  });

  test('the draft record says which uploaded file the draft was made from', async () => {
    expect(store.draftRecord.sourceFile).toBe('');

    api.createDraft.mockResolvedValueOnce({
      draft: { id: 'd1', createdBy: 'alice', sourceFile: 'plant.builder.json' },
      document: null,
      history: null,
      cursor: 0,
      etag: '"1"',
    });

    await store.createDraft({
      title: 'Plant',
      sourceFile: 'plant.builder.json',
    });

    expect(api.createDraft).toHaveBeenLastCalledWith(
      expect.objectContaining({ sourceFile: 'plant.builder.json' }),
    );
    expect(store.draftRecord.sourceFile).toBe('plant.builder.json');

    // A draft loaded says so too, and another diagram has none.
    api.getDraft.mockResolvedValueOnce({
      draft: { id: 'd2', createdBy: 'alice', sourceFile: 'core.yaml' },
      document: createDocument({ name: 'Core' }),
      history: [{ id: 's1' }],
      cursor: 0,
      etag: '"1"',
    });
    await store.loadDraft('d2');
    expect(store.draftRecord.sourceFile).toBe('core.yaml');

    store.newDocument();
    expect(store.draftRecord.sourceFile).toBe('');
  });

  // Only an Upload and an Import of a config file name a file (see
  // importReady in Builder.vue). Nothing else sends one.
  test('a file name is sent only when one is given, and only one the server records', async () => {
    const sentWith = () => api.createDraft.mock.calls.at(-1)[0];

    await store.createDraft({ title: 'Blank' });
    expect('sourceFile' in sentWith()).toBe(true);
    expect(sentWith().sourceFile).toBeUndefined();
    expect(JSON.parse(JSON.stringify(sentWith()))).not.toHaveProperty(
      'sourceFile',
    );

    await store.createDraft({
      title: 'Imported',
      sourceToken: 'Topology/core',
    });
    expect(sentWith().sourceFile).toBeUndefined();

    // A name the server would refuse the draft for is left out.
    for (const name of ['dir/plant.json', '..', 'a'.repeat(256), 'a\nb']) {
      await store.createDraft({ title: 'Upload', sourceFile: name });
      expect(sentWith().sourceFile).toBeUndefined();
    }

    await store.openPublishedDocument('published-1');
    expect(sentWith().sourceToken).toBe('builder-doc/published-1');
    expect(sentWith().sourceFile).toBeUndefined();

    await store.openPublishedDocument('file/plant');
    expect(sentWith().sourceToken).toBe(`builder-file/plant/${FILE_DIGEST}`);
    expect(sentWith().sourceFile).toBeUndefined();
  });
});

// A published diagram is edited in the draft whose last publication stored
// it: publishing again gives it a new id, which no source token names.
describe('the draft a published diagram is edited in', () => {
  const X1 = 'a'.repeat(64);
  const X2 = 'b'.repeat(64);
  const draft = (id, fields = {}) => ({ id, createdBy: 'alice', ...fields });
  const publishedAs = (documentId) => ({ publication: { documentId } });

  test('the draft that published it comes first, then a draft made from it', () => {
    const publisher = draft('publisher', publishedAs(X2));
    const opened = draft('copy', { sourceToken: `builder-doc/${X2}` });
    const other = draft('other', publishedAs(X1));
    const token = `builder-doc/${X2}`;

    expect(
      draftForPublished([other, opened, publisher], { id: X2 }, token),
    ).toEqual({ draft: publisher, how: 'publisher' });
    expect(draftForPublished([other, opened], { id: X2 }, token)).toEqual({
      draft: opened,
      how: 'opened',
    });
    expect(draftForPublished([other], { id: X2 }, token)).toBeNull();
    expect(draftForPublished([], { id: X2 }, token)).toBeNull();
    expect(draftForPublished(undefined, undefined, '')).toBeNull();
  });

  test('among several, the draft the diagram names wins, then the one changed last', () => {
    const older = draft('older', {
      ...publishedAs(X2),
      updated: '2026-09-02T00:00:00Z',
    });
    // The server's times drop trailing zeros: compared as times, not text.
    const newer = draft('newer', {
      ...publishedAs(X2),
      updated: '2026-09-02T00:00:00.1Z',
    });
    const drafts = [older, newer];

    expect(draftForPublished(drafts, { id: X2 }, '').draft).toBe(newer);
    expect(
      draftForPublished(drafts, { id: X2, draftId: 'older' }, '').draft,
    ).toBe(older);
    // A draft the diagram names that is not listed changes nothing.
    expect(
      draftForPublished(drafts, { id: X2, draftId: 'gone' }, '').draft,
    ).toBe(newer);
  });

  test('without a token only a draft that published the diagram is found', () => {
    const opened = draft('copy', { sourceToken: `builder-doc/${X2}` });

    expect(draftForPublished([opened], { id: X2 }, '')).toBeNull();
    // A draft with no token is never "made from" a diagram without one.
    expect(draftForPublished([draft('blank')], { id: '' }, '')).toBeNull();
  });

  test("a topology's Builder file has no publishing draft", () => {
    const token = `builder-file/plant/${FILE_DIGEST}`;
    // No draft's publication stores a file, whatever its record says.
    const publisher = draft('mine', publishedAs('file/plant'));
    const opened = draft('copy', { sourceToken: token });

    expect(
      draftForPublished([publisher], { id: 'file/plant' }, token),
    ).toBeNull();
    expect(
      draftForPublished([publisher, opened], { id: 'file/plant' }, token),
    ).toEqual({ draft: opened, how: 'opened' });
  });

  test('a draft that published the diagram again is opened again, and no other draft is made', async () => {
    const { doc } = sampleDocument();

    store.documents = [{ id: X2, target: 'core', draftId: 'd5' }];
    api.listDrafts.mockResolvedValueOnce({
      all: [
        draft('d5', { sourceToken: `builder-doc/${X1}`, ...publishedAs(X2) }),
      ],
      published: [],
    });
    api.getDraft.mockResolvedValueOnce({
      draft: { id: 'd5', createdBy: 'alice' },
      document: doc,
      history: [{ id: 's1' }],
      cursor: 0,
      etag: '"5"',
    });

    await expect(store.openPublishedDocument(X2)).resolves.toBeTruthy();

    expect(api.getDraft).toHaveBeenLastCalledWith('d5');
    expect(api.createDraft).not.toHaveBeenCalled();
    // The published document is not read: the draft is what opens.
    expect(api.getDocument).not.toHaveBeenCalled();
    expect(store.draftId).toBe('d5');
    expect(store.announcement).toBe(
      `Opened the draft that published diagram ${doc.metadata.name}.`,
    );
  });

  test('the caller may say what opening each kind of draft is called', async () => {
    const { doc } = sampleDocument();
    const said = {
      announcement: 'New.',
      resumed: 'Mine, made from it.',
      publisher: 'Mine, which published it.',
    };
    const loaded = (id) => ({
      draft: { id, createdBy: 'alice' },
      document: doc,
      history: [{ id: 's1' }],
      cursor: 0,
      etag: '"5"',
    });

    api.listDrafts.mockResolvedValueOnce({
      all: [draft('d5', publishedAs(X2))],
      published: [],
    });
    api.getDraft.mockResolvedValueOnce(loaded('d5'));
    await store.openPublishedDocument(X2, said);
    expect(store.announcement).toBe('Mine, which published it.');

    api.listDrafts.mockResolvedValueOnce({
      all: [draft('d6', { sourceToken: `builder-doc/${X2}` })],
      published: [],
    });
    api.getDraft.mockResolvedValueOnce(loaded('d6'));
    await store.openPublishedDocument(X2, said);
    expect(store.announcement).toBe('Mine, made from it.');

    api.listDrafts.mockResolvedValueOnce({
      all: [],
      published: [],
    });
    await store.openPublishedDocument(X2, said);
    expect(store.announcement).toBe('New.');
    expect(api.createDraft).toHaveBeenCalledTimes(1);
    expect(api.createDraft).toHaveBeenLastCalledWith(
      expect.objectContaining({ sourceToken: `builder-doc/${X2}` }),
    );
  });
});

// A topology whose builder-doc annotation names a Builder file, and no
// published document, is listed with a handle for an id ("file/<name>").
describe('the diagram of a topology read from its Builder file', () => {
  const row = {
    source: 'file',
    id: 'file/plant',
    target: 'plant',
    kind: 'Topology',
    config: 'Topology/plant',
    path: '/phenix/topologies/plant.builder.json',
  };
  const refusal = (status, message) =>
    Object.assign(new Error('refused'), {
      response: { status, data: { message, cause: '' } },
    });

  test('opens read only, and says where it was read from', async () => {
    store.documents = [row];
    api.getTopologyDocument.mockResolvedValueOnce({
      ...row,
      digest: FILE_DIGEST,
      size: 1024,
      topologyDiffers: true,
      document: sampleDocument().doc,
    });

    const viewed = await store.viewPublishedDocument('file/plant');

    expect(viewed.metadata.name).toBe('Sample');
    expect(api.getTopologyDocument).toHaveBeenCalledWith('plant');
    expect(api.getDocument).not.toHaveBeenCalled();
    expect(api.createDraft).not.toHaveBeenCalled();
    expect(store.readOnly).toBe(true);
    expect(store.autosave).toBeNull();
    expect(store.published).toMatchObject({
      id: 'file/plant',
      name: 'Sample',
      target: 'plant',
      source: 'file',
      path: '/phenix/topologies/plant.builder.json',
      digest: FILE_DIGEST,
      topologyDiffers: true,
    });
    expect(store.announcement).toBe(
      'Opened the diagram of topology plant, read only.',
    );
  });

  test('editing it makes a draft named by what the file held, then reopens that draft', async () => {
    await store.viewPublishedDocument('file/plant');
    await expect(store.editPublished()).resolves.toBeTruthy();

    // The diagram shown is the one saved: the file is not read again.
    expect(api.getTopologyDocument).toHaveBeenCalledTimes(1);
    expect(api.createDraft).toHaveBeenCalledWith(
      expect.objectContaining({
        sourceToken: `builder-file/plant/${FILE_DIGEST}`,
        document: expect.objectContaining({
          metadata: expect.objectContaining({ name: 'Sample' }),
        }),
      }),
    );
    expect(store.readOnly).toBe(false);
    expect(store.published).toBeNull();
    expect(store.announcement).toBe(
      'Opened the diagram of topology plant as a new draft.',
    );

    // Opened again, the file's draft changed last opens, not a new copy, nor
    // a draft of the file as it was or of a published diagram.
    const { doc } = sampleDocument();

    api.listDrafts.mockResolvedValueOnce({
      all: [
        {
          id: 'old',
          createdBy: 'alice',
          sourceToken: `builder-file/plant/sha256:${'0'.repeat(64)}`,
          updated: '2026-09-09T00:00:00Z',
        },
        {
          id: 'doc',
          createdBy: 'alice',
          sourceToken: 'builder-doc/file/plant',
        },
        {
          id: 'd6',
          createdBy: 'alice',
          sourceToken: `builder-file/plant/${FILE_DIGEST}`,
          updated: '2026-09-02T00:00:00Z',
        },
        {
          id: 'd7',
          createdBy: 'alice',
          sourceToken: `builder-file/plant/${FILE_DIGEST}`,
          updated: '2026-09-03T00:00:00Z',
        },
      ],
      published: [],
    });
    api.getDraft.mockResolvedValueOnce({
      draft: { id: 'd7', createdBy: 'alice' },
      document: doc,
      history: [{ id: 's1' }],
      cursor: 0,
      etag: '"7"',
    });

    await expect(
      store.openPublishedDocument('file/plant'),
    ).resolves.toBeTruthy();
    // The file is read to learn what it holds now.
    expect(api.getTopologyDocument).toHaveBeenCalledTimes(2);
    expect(api.getDraft).toHaveBeenLastCalledWith('d7');
    expect(store.draftId).toBe('d7');
    expect(store.announcement).toBe(
      'Opened the draft of the diagram of topology plant.',
    );
    expect(api.createDraft).toHaveBeenCalledTimes(1);
  });

  test('editing it adds the custom icons the file carries to the icon library, as an upload does', async () => {
    const sample = sampleDocument();
    const theirs = base64Of(png(2, 2, [200, 40, 80, 255]));
    const ours = base64Of(png(2, 2, [10, 120, 40, 255]));
    let doc = updateNode(sample.doc, sample.alpha.id, {
      device: { icon: 'plc' },
    });

    doc = updateNode(doc, sample.bravo.id, { device: { icon: 'hmi' } });
    doc = { ...doc, icons: { plc: { data: ICON_DATA }, hmi: { data: ours } } };

    api.getTopologyDocument.mockResolvedValueOnce({
      ...row,
      digest: FILE_DIGEST,
      size: 1024,
      topologyDiffers: false,
      document: doc,
    });
    // The library has no plc, and an hmi of other bytes.
    api.listIcons.mockResolvedValueOnce({
      icons: [{ name: 'hmi', aliases: [], data: theirs }],
      maxIcons: 2000,
    });

    await store.viewPublishedDocument('file/plant');

    // Shown read only, the file's own copies draw its icons. Nothing is
    // uploaded until it is edited.
    expect(store.doc.icons).toEqual(doc.icons);
    expect(api.uploadIcon).not.toHaveBeenCalled();

    await expect(store.editPublished()).resolves.toBeTruthy();

    const warning =
      "The server already has an icon named hmi that differs from this diagram's. The diagram keeps its own copy, which it shows in place of the server's.";

    // plc is added under its name and leaves the draft. hmi, which the
    // library has with other bytes, stays, with a warning.
    expect(api.uploadIcon).toHaveBeenCalledTimes(1);
    expect(api.uploadIcon).toHaveBeenCalledWith({
      name: 'plc',
      data: ICON_DATA,
    });
    expect(api.createDraft).toHaveBeenCalledWith(
      expect.objectContaining({
        sourceToken: `builder-file/plant/${FILE_DIGEST}`,
        document: expect.objectContaining({ icons: { hmi: { data: ours } } }),
      }),
    );
    expect(store.doc.icons).toEqual({ hmi: { data: ours } });
    expect(findNode(store.doc, sample.alpha.id).device.icon).toBe('plc');
    expect(store.announcement).toBe(
      `Opened the diagram of topology plant as a new draft. ${warning}`,
    );
    expect(store.notice).toMatchObject({ text: warning });
  });

  test('a file that changed since the draft was made gets a new draft', async () => {
    const changed = `sha256:${'e'.repeat(64)}`;

    api.listDrafts.mockResolvedValueOnce({
      all: [
        {
          id: 'd6',
          createdBy: 'alice',
          sourceToken: `builder-file/plant/${FILE_DIGEST}`,
        },
      ],
      published: [],
    });
    api.getTopologyDocument.mockResolvedValueOnce({
      ...row,
      digest: changed,
      topologyDiffers: false,
      document: setDocumentInfo(sampleDocument().doc, { name: 'Changed' }),
    });

    await expect(
      store.openPublishedDocument('file/plant'),
    ).resolves.toBeTruthy();
    expect(api.getDraft).not.toHaveBeenCalled();
    expect(api.createDraft).toHaveBeenCalledWith(
      expect.objectContaining({
        title: 'Changed',
        sourceToken: `builder-file/plant/${changed}`,
      }),
    );
  });

  // Published since the list was read: the topology's document is a
  // published one now, with an id, and the draft is made from that.
  test('a topology published since it was listed opens as its published diagram', async () => {
    api.getTopologyDocument.mockResolvedValue({
      source: 'store',
      id: 'p7',
      target: 'plant',
      kind: 'Topology',
      digest: FILE_DIGEST,
      topologyDiffers: false,
      document: sampleDocument().doc,
    });

    try {
      await store.viewPublishedDocument('file/plant');
      expect(store.published).toMatchObject({
        id: 'p7',
        source: 'store',
        target: 'plant',
        path: '',
        digest: '',
      });
      expect(store.announcement).toBe(
        'Opened published diagram Sample, read only.',
      );

      await store.editPublished();
      expect(api.createDraft).toHaveBeenLastCalledWith(
        expect.objectContaining({ sourceToken: 'builder-doc/p7' }),
      );

      await store.openPublishedDocument('file/plant');
      expect(api.createDraft).toHaveBeenLastCalledWith(
        expect.objectContaining({ sourceToken: 'builder-doc/p7' }),
      );
    } finally {
      api.getTopologyDocument.mockReset();
    }
  });

  // Why a file cannot be used is the server's sentence, shown as it is.
  test.each([
    [
      404,
      'Builder file /phenix/topologies/0f8fad5b-d9cb-469f-a165-70867728950e/plant.builder.json does not exist on this phenix server.',
    ],
    [
      422,
      'Builder file /srv/plant.builder.json is outside /phenix, the directory phenix reads Builder files from.',
    ],
    [413, 'Builder file /phenix/plant.builder.json is larger than 5 MiB.'],
    [
      422,
      'Builder file /phenix/plant.builder.json is not a valid Builder document. Upload it in the Builder to see why.',
    ],
    [
      422,
      'Builder file /phenix/plant.builder.json does not match the digest topology plant records for it.',
    ],
  ])(
    'a file the server cannot use (%i) is reported in its words',
    async (status, message) => {
      const before = store.doc;

      api.getTopologyDocument.mockRejectedValueOnce(refusal(status, message));
      await expect(
        store.viewPublishedDocument('file/plant'),
      ).resolves.toBeNull();
      expect(store.error).toBe(
        `Could not open the diagram of topology plant. ${message}`,
      );
      expect(store.doc).toBe(before);
      expect(store.published).toBeNull();

      api.getTopologyDocument.mockRejectedValueOnce(refusal(status, message));
      await expect(
        store.openPublishedDocument('file/plant'),
      ).resolves.toBeNull();
      expect(store.error).toBe(
        `Could not open the diagram of topology plant. ${message}`,
      );
      expect(api.createDraft).not.toHaveBeenCalled();
    },
  );

  test('a file that changed between viewing and editing is reported in the server’s words', async () => {
    const changed =
      'The Builder file of topology plant changed after Builder opened it. Open its diagram again.';

    await store.viewPublishedDocument('file/plant');
    api.createDraft.mockRejectedValueOnce(refusal(409, changed));

    await expect(store.editPublished()).resolves.toBeNull();
    expect(store.error).toBe(`Could not create the draft. ${changed}`);
    // The diagram stays as it was shown.
    expect(store.published).toMatchObject({ id: 'file/plant' });
    expect(store.readOnly).toBe(true);
  });

  test('a failure that is not about the file is described as any other', async () => {
    api.getTopologyDocument.mockRejectedValueOnce(new Error('offline'));
    await store.viewPublishedDocument('file/plant');
    expect(store.error).toBe(
      'Could not open the diagram of topology plant. The Builder could not reach the server. Check the connection and try again.',
    );

    api.getTopologyDocument.mockRejectedValueOnce(
      refusal(500, 'unable to read the Builder document of topology plant'),
    );
    await store.viewPublishedDocument('file/plant');
    expect(store.error).toBe(
      'Could not open the diagram of topology plant. Unable to read the Builder document of topology plant.',
    );

    // A create refused with a 409 that is not about a file keeps its text.
    api.createDraft.mockRejectedValueOnce(refusal(409, 'something else'));
    await store.createDraft({ title: 'Test' });
    expect(store.error).toBe(
      'Could not create the draft. This draft changed on the server since you loaded it.',
    );
  });
});

// Opening a diagram is no edit of it: the switch names the editor gives on
// open must not reach the document it sends back.
describe('editing a published diagram sends it back as it was read', () => {
  function staleDocument() {
    const { doc, sw } = sampleDocument();

    return {
      sw,
      doc: {
        ...withStamp(doc, {
          createdBy: 'carol',
          createdAt: '2026-09-01T08:00:00Z',
          updatedBy: 'dave',
          updatedAt: '2026-09-02T09:30:00Z',
        }),
        nodes: doc.nodes.map((node) =>
          node.id === sw.id ? { ...node, label: 'OLD' } : node,
        ),
      },
    };
  }

  test.each([
    ['a published document', 'published-1', 'getDocument', (doc) => doc],
    [
      'a Builder file',
      'file/plant',
      'getTopologyDocument',
      (doc) => ({
        source: 'file',
        target: 'plant',
        path: '/phenix/plant.builder.json',
        digest: FILE_DIGEST,
        topologyDiffers: false,
        document: doc,
      }),
    ],
  ])('%s', async (_, id, read, answer) => {
    const { doc, sw } = staleDocument();
    const stamp = {
      createdBy: 'carol',
      createdAt: '2026-09-01T08:00:00Z',
      updatedBy: 'dave',
      updatedAt: '2026-09-02T09:30:00Z',
    };

    api[read].mockResolvedValueOnce(answer(structuredClone(doc)));
    // An unchanged copy answers with the stamp the document had.
    api.createDraft.mockResolvedValueOnce({
      draft: { id: 'd1', createdBy: 'alice', snapshotId: 's1', stamp },
      document: null,
      history: null,
      cursor: 0,
      etag: '"1"',
    });

    await store.viewPublishedDocument(id);

    // Shown with the switch named after its network.
    expect(findNode(store.doc, sw.id).label).toBe('EXP');

    await store.editPublished();

    const sent = api.createDraft.mock.calls[0][0].document;

    expect(sent).toEqual(doc);
    expect(JSON.stringify(sent)).toBe(JSON.stringify(doc));
    // The draft opens as it was shown, with the stamp the document had.
    expect(findNode(store.doc, sw.id).label).toBe('EXP');
    expect(store.doc.metadata).toMatchObject(stamp);
    expect(store.history.current()).toEqual(store.doc);

    // Opened without viewing it first, the same document is sent.
    api[read].mockResolvedValueOnce(answer(structuredClone(doc)));
    await store.openPublishedDocument(id);
    expect(api.createDraft.mock.calls[1][0].document).toEqual(doc);
  });
});

describe('the experiment a draft was published with', () => {
  const intent = {
    mode: 'topology-experiment',
    topology: { name: 'core', action: 'create' },
    experiment: { name: 'core-exp', action: 'create' },
  };

  // A publish answer, with the draft record as the server sends it.
  function published(draft, extra = {}) {
    return {
      result: {
        status: 'succeeded',
        ok: true,
        partial: false,
        stages: [],
        draft,
        ...extra,
      },
      etag: '"2"',
    };
  }

  test('a draft read from the server names it, and one without it names none', async () => {
    expect(store.experiment).toBe('');
    expect(store.experimentName).toBe('');

    api.getDraft.mockResolvedValueOnce(readDraft({ experiment: 'core-exp' }));
    await store.loadDraft('d1');

    expect(store.experiment).toBe('core-exp');
    expect(store.experimentName).toBe('core-exp');

    // A save answers with the draft alone, and says nothing of it.
    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.saveNow();
    expect(api.appendSnapshot).toHaveBeenCalledOnce();
    expect(store.experiment).toBe('core-exp');

    // The server leaves the field out once the experiment is gone.
    api.getDraft.mockResolvedValueOnce(readDraft());
    await store.loadDraft('d1');

    expect(store.experiment).toBe('');
    expect(store.experimentName).toBe('');
  });

  test('a draft made from a published diagram names it, and a new diagram none', async () => {
    api.createDraft.mockResolvedValueOnce({
      draft: { id: 'd1', createdBy: 'alice', experiment: 'core-exp' },
      document: null,
      history: null,
      cursor: 0,
      etag: '"1"',
    });
    await store.createDraft({ sourceToken: 'builder-doc/p1' });

    expect(store.experimentName).toBe('core-exp');

    store.newDocument();
    expect(store.experiment).toBe('');
    expect(store.experimentName).toBe('');

    // A create that names none leaves none.
    await store.createDraft();
    expect(store.experiment).toBe('');
  });

  test('a draft saved from local history after a conflict names the experiment its create does', async () => {
    const conflict = () =>
      Object.assign(new Error('conflict'), { response: { status: 412 } });
    const created = (draft) => ({
      draft,
      document: null,
      history: null,
      cursor: 0,
      etag: '"1"',
    });

    await withDraft();

    // The new draft forks one that published with an experiment.
    api.appendSnapshot.mockRejectedValueOnce(conflict());
    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.saveNow();
    expect(store.hasConflict).toBe(true);
    api.createDraft.mockResolvedValueOnce(
      created({ id: 'd7', createdBy: 'alice', experiment: 'core-exp' }),
    );
    await store.resolveConflict('fork');

    expect(store.draftId).toBe('d7');
    expect(store.experimentName).toBe('core-exp');

    // The experiment is gone by the time it is forked again.
    api.appendSnapshot.mockRejectedValueOnce(conflict());
    store.addNode({ kind: 'device', hostname: 'bravo' });
    await store.saveNow();
    expect(store.hasConflict).toBe(true);
    api.createDraft.mockResolvedValueOnce(
      created({ id: 'd8', createdBy: 'alice' }),
    );
    await store.resolveConflict('fork');

    expect(store.draftId).toBe('d8');
    expect(store.experimentName).toBe('');
  });

  test('a publish names the experiment its answer does, also when it failed in part', async () => {
    await withDraft();
    api.publish.mockResolvedValueOnce(
      published({ id: 'd1', createdBy: 'alice', experiment: 'core-exp' }),
    );
    await store.publish(intent);

    expect(store.experimentName).toBe('core-exp');

    // The same draft publishes the topology alone: its experiment stays.
    api.publish.mockResolvedValueOnce(
      published(
        { id: 'd1', createdBy: 'alice', experiment: 'core-exp' },
        { status: 'partial', ok: false, partial: true },
      ),
    );
    await store.publish(intent);

    expect(store.error).toBe(
      'Publish finished with failures. It wrote some configs.',
    );
    expect(store.experimentName).toBe('core-exp');

    // One the server says can be finished says how.
    api.publish.mockResolvedValueOnce(
      published(
        { id: 'd1', createdBy: 'alice', experiment: 'core-exp' },
        { status: 'partial', ok: false, partial: true, retry: true },
      ),
    );
    await store.publish(intent);

    expect(store.error).toBe(
      'Publish finished with failures. It wrote some configs. Publish again with the same choices to finish. The server skips the steps that are already done.',
    );

    // An answer without a draft record says nothing of the experiment.
    api.publish.mockResolvedValueOnce(published(null));
    await store.publish(intent);

    expect(store.experimentName).toBe('core-exp');

    // The experiment was deleted meanwhile: the draft names none.
    api.publish.mockResolvedValueOnce(
      published({ id: 'd1', createdBy: 'alice' }),
    );
    await store.publish(intent);

    expect(store.experimentName).toBe('');
  });

  test('the answer of a publish says nothing of a draft opened meanwhile', async () => {
    await withDraft();
    let finish;
    api.publish.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = () =>
            resolve(
              published({
                id: 'd1',
                createdBy: 'alice',
                digest: 'sha256:9',
                experiment: 'core-exp',
              }),
            );
        }),
    );

    const publishing = store.publish(intent);
    await vi.waitFor(() => expect(finish).toBeTypeOf('function'));

    // The draft is closed for another diagram before the server answers.
    store.newDocument({ name: 'Another' });
    finish();

    const result = await publishing;

    expect(api.publish).toHaveBeenCalledWith('d1', intent, '"1"');
    expect(result.ok).toBe(true);
    expect(store.experiment).toBe('');
    expect(store.etag).toBeNull();
    expect(store.draftRecord).toMatchObject({ id: '', digest: '' });
    expect(store.publishing).toBe(false);
  });

  test('a publish is under way from the save it starts with until its answer is taken', async () => {
    await withDraft();

    // Refused before anything is sent to the server.
    store.readOnly = true;
    await expect(store.publish(intent)).resolves.toBeNull();
    expect(store.publishing).toBe(false);
    store.readOnly = false;

    // Refused for a change the server does not hold.
    api.appendSnapshot.mockRejectedValueOnce(
      Object.assign(new Error('conflict'), { response: { status: 412 } }),
    );
    store.addNode({ kind: 'device', hostname: 'alpha' });

    const refused = store.publish(intent);

    expect(store.publishing).toBe(true);
    await expect(refused).resolves.toBeNull();
    expect(api.publish).not.toHaveBeenCalled();
    expect(store.publishing).toBe(false);
  });

  test('a published diagram shown read only names the experiment its listed row does', async () => {
    api.listDocuments.mockResolvedValueOnce([
      { id: 'published-1', target: 'core', experiment: 'core-exp' },
      { id: 'published-2', target: 'edge' },
    ]);
    await store.fetchDocuments();

    // Not while a draft is open: a row is not the draft's.
    expect(store.experimentName).toBe('');

    await store.viewPublishedDocument('published-1');
    expect(store.published.id).toBe('published-1');
    expect(store.experiment).toBe('');
    expect(store.experimentName).toBe('core-exp');

    await store.viewPublishedDocument('published-2');
    expect(store.experimentName).toBe('');

    // A diagram the lists no longer hold names none.
    await store.viewPublishedDocument('published-3');
    expect(store.experimentName).toBe('');
  });
});

describe('a draft loaded for its card’s Publish', () => {
  // Every message the live region was given.
  function announcements() {
    const said = [];

    store.$onAction(({ name, args }) => {
      if (name === 'announce') {
        said.push(args[0]);
      }
    });

    return said;
  }

  test('is loaded as an opened one is, and says nothing', async () => {
    const said = announcements();

    api.getDraft.mockResolvedValueOnce(readDraft());

    const loaded = await store.loadDraft('d1', { quiet: true });

    expect(loaded).toBe(store.doc);
    expect(store.draftId).toBe('d1');
    expect(store.etag).toBe('"1"');
    expect(store.readOnly).toBe(false);
    expect(store.autosave).toBeTruthy();
    expect(store.saveState).toMatchObject({ pending: 0 });
    expect(said).toEqual([]);

    // Opened in the editor, the same draft says so.
    api.getDraft.mockResolvedValueOnce(readDraft());
    await store.loadDraft('d1');

    expect(said).toEqual(['Draft loaded']);
  });

  test('still says what it recovered from this device, and sends it', async () => {
    const saved = api.appendSnapshot.getMockImplementation();
    let offline = true;

    api.getDraft.mockResolvedValue(readDraft());
    api.appendSnapshot.mockImplementation((...args) =>
      offline ? Promise.reject(new Error('offline')) : saved(...args),
    );

    try {
      await store.loadDraft('d1');

      // An edit the server never received, left on this device.
      store.addNode({ kind: 'device', hostname: 'kept' });
      await store.saveNow();
      expect(store.saveState).toMatchObject({ status: 'offline', pending: 1 });
      store.newDocument();
      offline = false;
      api.appendSnapshot.mockClear();

      const said = announcements();

      await store.loadDraft('d1', { quiet: true });

      expect(said).toContain('Recovered 1 unsaved change from this device.');
      expect(said).not.toContain('Draft loaded');
      expect(store.summary.devices).toBe(3);
      expect(api.appendSnapshot).toHaveBeenCalledOnce();
      expect(store.saveState).toMatchObject({ status: 'saved', pending: 0 });
    } finally {
      api.getDraft.mockReset();
      api.appendSnapshot.mockReset();
    }
  });

  // The Drafts button during a save: the queue goes on sending in the
  // background (see sendInBackground in Builder.vue).
  function closeForDrafts() {
    const queue = store.autosave;

    store.autosave = null;

    return queue;
  }

  test('does not wait for the save of a draft closed while it was being saved', async () => {
    const saved = api.appendSnapshot.getMockImplementation();
    const intent = {
      mode: 'topology',
      topology: { name: 'core', action: 'create' },
    };
    let answer;
    let closed;

    api.getDraft
      .mockResolvedValueOnce(readDraft())
      .mockResolvedValueOnce(readDraft({ id: 'd2' }));
    // The server takes its time over the first draft's save.
    api.appendSnapshot.mockImplementationOnce(
      (...args) =>
        new Promise((resolve) => {
          answer = () => resolve(saved(...args));
        }),
    );

    try {
      await store.loadDraft('d1');
      store.addNode({ kind: 'note' });
      await vi.waitFor(() => expect(answer).toBeTypeOf('function'));
      closed = closeForDrafts();

      await store.loadDraft('d2', { quiet: true });

      expect(store.queueWork).toBeNull();

      const result = await Promise.race([
        store.publish(intent),
        new Promise((resolve) => setTimeout(() => resolve('waiting'), 250)),
      ]);

      expect(result).toMatchObject({ ok: true });
      expect(api.publish).toHaveBeenCalledWith('d2', intent, '"1"');
      expect(store.publishing).toBe(false);
    } finally {
      answer?.();
      await closed?.idle();
      closed?.dispose();
      api.getDraft.mockReset();
      api.appendSnapshot.mockReset();
    }
  });

  test('its first save is not the end of a problem the draft closed before it had', async () => {
    const saved = api.appendSnapshot.getMockImplementation();
    // The drafts whose saves do not reach the server.
    const cut = new Set(['d1']);
    let closed;

    api.getDraft
      .mockResolvedValueOnce(readDraft())
      .mockResolvedValueOnce(readDraft({ id: 'd2' }));
    api.appendSnapshot.mockImplementation((id, ...rest) =>
      cut.has(id) ? Promise.reject(new Error('offline')) : saved(id, ...rest),
    );

    try {
      await store.loadDraft('d1');
      store.addNode({ kind: 'note' });
      await store.saveNow();
      expect(store.saveState).toMatchObject({ status: 'offline', pending: 1 });
      expect(store.announcement).toMatch(/Offline: 1 change kept/);
      closed = closeForDrafts();

      const said = announcements();

      await store.loadDraft('d2', { quiet: true });
      await store.saveNow();

      // The other draft's change is still not saved: nothing says it is.
      expect(store.saveState).toMatchObject({ status: 'saved', pending: 0 });
      expect(closed.state).toMatchObject({ status: 'offline', pending: 1 });
      expect(said).toEqual([]);

      // A problem of this draft's own, and its end, are still said.
      cut.add('d2');
      store.addNode({ kind: 'note' });
      await store.saveNow();
      expect(said.at(-1)).toMatch(/Offline: 1 change kept/);
      cut.delete('d2');
      await store.retrySave();
      expect(said.at(-1)).toBe('All changes saved.');
    } finally {
      closed?.dispose();
      api.getDraft.mockReset();
      api.appendSnapshot.mockReset();
    }
  });

  test('a draft that cannot be read is reported as it is for Open', async () => {
    api.getDraft.mockRejectedValueOnce(
      Object.assign(new Error('gone'), {
        response: {
          status: 404,
          data: { message: 'phenix cannot find draft d1' },
        },
      }),
    );

    await expect(store.loadDraft('d1', { quiet: true })).resolves.toBeNull();
    expect(store.error).toMatch(/^Could not load the draft\./);
    expect(store.autosave).toBeNull();
  });
});

describe('bulk actions on listed drafts and published topologies', () => {
  const failed = (status, message, extra = {}) =>
    Object.assign(new Error(`status ${status}`), {
      response: { status, data: message ? { message } : {}, ...extra },
    });
  // A refused precondition, with the draft's current ETag when the server
  // sends one.
  const changed = (etag) =>
    failed(412, 'changed', etag ? { headers: { etag } } : {});
  const draft = (id, extra = {}) => ({
    id,
    createdBy: 'alice',
    title: `Draft ${id}`,
    etag: '"1"',
    ...extra,
  });
  const lists = (all = []) => ({
    all,
    published: [],
    damaged: [],
  });

  // Every message the live region was given.
  function announcements() {
    const said = [];

    store.$onAction(({ name, args }) => {
      if (name === 'announce') {
        said.push(args[0]);
      }
    });

    return said;
  }

  // Earlier tests leave their own answers on these: each test here starts
  // from a server that does what it is asked.
  beforeEach(() => {
    api.deleteDraft.mockReset().mockResolvedValue(true);
    api.getDraft.mockReset();
    api.deleteDocument.mockReset().mockResolvedValue(true);
    api.listDrafts.mockReset().mockResolvedValue(lists());
    api.listDocuments.mockReset().mockResolvedValue([]);
  });

  test('a draft whose ETag is old is deleted with the one the refusal carries', async () => {
    api.deleteDraft.mockRejectedValueOnce(changed('"7"'));

    expect(await store.deleteDraft('d1', '"1"', 'Lab network')).toBe(true);
    // The draft is not read: a damaged one could not be.
    expect(api.getDraft).not.toHaveBeenCalled();
    expect(api.deleteDraft.mock.calls).toEqual([
      ['d1', '"1"'],
      ['d1', '"7"'],
    ]);
    expect(store.announcement).toBe('Deleted draft Lab network.');
  });

  test('deleting several drafts deletes each with its ETag, and reads the lists once', async () => {
    const items = [draft('d1'), draft('d2', { etag: '"4"' }), draft('d3')];
    const said = announcements();
    const order = [];
    const progress = [];

    for (const item of items) {
      await device.store.put({
        key: `alice::${item.id}`,
        actor: 'alice',
        draftId: item.id,
        entries: [],
        queue: [],
      });
    }
    api.deleteDraft.mockImplementation(async (id) => {
      order.push(`delete ${id}`);

      return true;
    });
    store.setError('An earlier failure.');

    const outcome = await store.deleteDrafts(items, {
      before: async (item) => {
        order.push(`before ${item.id}`);
      },
      onProgress: (done, total) => progress.push([done, total]),
    });

    expect(outcome).toEqual({ done: items, failures: [] });
    expect(api.deleteDraft.mock.calls).toEqual([
      ['d1', '"1"'],
      ['d2', '"4"'],
      ['d3', '"1"'],
    ]);
    // Each draft's background saves end before it is deleted.
    expect(order.filter((step) => step.startsWith('before'))).toHaveLength(3);
    for (const item of items) {
      expect(order.indexOf(`before ${item.id}`)).toBeLessThan(
        order.indexOf(`delete ${item.id}`),
      );
    }
    expect(progress).toEqual([
      [1, 3],
      [2, 3],
      [3, 3],
    ]);
    // What this device kept for them goes with them.
    expect(await device.store.all()).toEqual([]);
    expect(api.listDrafts).toHaveBeenCalledOnce();
    // The caller says what the run came to, and the page alert is left
    // alone.
    expect(said).toEqual([]);
    expect(store.error).toBe('An earlier failure.');
  });

  test('a draft that changed is deleted with its current ETag, once, and each failure says why', async () => {
    const items = ['d1', 'd2', 'd3', 'd4', 'd5'].map((id) => draft(id));
    const answers = {
      // Deleted with the tag its refusal carries.
      d1: [changed('"2"'), true],
      // Changed again meanwhile.
      d2: [changed('"2"'), changed('"3"')],
      // No tag in the refusal: the draft is read for it.
      d3: [changed(), true],
      d4: [failed(403, 'forbidden by the role')],
      d5: [failed(500, 'storage down')],
    };

    api.deleteDraft.mockImplementation(async (id) => {
      const answer = answers[id].shift();

      if (answer instanceof Error) {
        throw answer;
      }

      return answer;
    });
    api.getDraft.mockResolvedValue({ draft: { id: 'd3' }, etag: '"9"' });

    const outcome = await store.deleteDrafts(items);

    expect(outcome.done.map((item) => item.id)).toEqual(['d1', 'd3']);
    expect(
      outcome.failures.map(({ item, reason }) => [item.id, reason]),
    ).toEqual([
      ['d2', 'It changed on the server during the delete. Try again.'],
      ['d4', 'Forbidden by the role.'],
      ['d5', 'Storage down.'],
    ]);
    expect(api.deleteDraft.mock.calls.filter(([id]) => id === 'd1')).toEqual([
      ['d1', '"1"'],
      ['d1', '"2"'],
    ]);
    // Once only: the third tag is never sent.
    expect(
      api.deleteDraft.mock.calls.filter(([id]) => id === 'd2'),
    ).toHaveLength(2);
    expect(api.getDraft.mock.calls).toEqual([['d3']]);
    expect(api.deleteDraft).toHaveBeenCalledWith('d3', '"9"');
    expect(api.listDrafts).toHaveBeenCalledOnce();
    expect(store.error).toBe('');
  });

  test('a draft that is gone already counts as deleted', async () => {
    // As the server names a draft it does not find.
    const id = (n) => `e11aa62f-3289-4051-a661-bc2fa634290${n}`;
    const gone = (n) => failed(404, `phenix cannot find draft ${id(n)}`);
    const items = [draft(id(1)), draft(id(2)), draft(id(3)), draft(id(4))];
    const said = announcements();

    for (const item of items) {
      await device.store.put({
        key: `alice::${item.id}`,
        actor: 'alice',
        draftId: item.id,
        entries: [],
        queue: [],
      });
    }
    await device.store.put({
      key: 'alice::kept',
      actor: 'alice',
      draftId: 'kept',
      entries: [],
      queue: [],
    });
    api.deleteDraft.mockImplementation(async (draftId) => {
      if (draftId === id(1)) {
        return true;
      }

      // The third changed, and was deleted before it could be read again.
      throw draftId === id(3) ? changed() : gone(draftId.at(-1));
    });
    api.getDraft.mockRejectedValue(gone(3));

    const outcome = await store.deleteDrafts(items);

    expect(outcome.done.map((item) => item.id)).toEqual([
      id(1),
      id(2),
      id(3),
      id(4),
    ]);
    expect(outcome.failures).toEqual([]);
    // What this device kept for the drafts that are gone goes with them.
    expect((await device.store.all()).map((record) => record.draftId)).toEqual([
      'kept',
    ]);
    expect(api.listDrafts).toHaveBeenCalledOnce();
    expect(said).toEqual([]);
    expect(store.error).toBe('');
  });

  test('a session that ended, or a server out of reach, ends the run: the rest is not attempted', async () => {
    const items = ['d1', 'd2', 'd3', 'd4', 'd5', 'd6'].map((id) => draft(id));

    for (const [error, reason] of [
      [failed(401), 'Your session has ended. Log in again.'],
      [
        new Error('Network Error'),
        'The Builder could not reach the server. Check the connection and try again.',
      ],
    ]) {
      api.deleteDraft.mockReset().mockRejectedValue(error);

      const outcome = await store.deleteDrafts(items);

      expect(outcome.done).toEqual([]);
      // The four that were under way fail as the first did. The others
      // were never sent.
      expect(outcome.failures.map((failure) => failure.reason)).toEqual([
        reason,
        reason,
        reason,
        reason,
        'Not attempted.',
        'Not attempted.',
      ]);
      expect(api.deleteDraft).toHaveBeenCalledTimes(4);
    }
  });

  test('deleting several published topologies takes them one at a time, off the lists', async () => {
    const items = ['lab', 'core', 'edge'].map((target, index) => ({
      id: `p${index + 1}`,
      kind: 'Topology',
      target,
      etag: `"t${index + 1}"`,
    }));
    const said = announcements();
    const finish = [];
    const progress = [];

    store.documents = [
      ...items,
      { id: 'p9', kind: 'Topology', target: 'kept' },
    ];
    store.sources = {
      ...store.sources,
      topologies: [{ name: 'lab' }, 'core', { name: 'edge' }, { name: 'kept' }],
    };
    api.deleteDocument.mockImplementation(
      () =>
        new Promise((resolve) => {
          finish.push(resolve);
        }),
    );
    api.listDocuments.mockResolvedValue([
      { id: 'p9', kind: 'Topology', target: 'kept' },
    ]);

    const run = store.deletePublishedMany(items, {
      onProgress: (done, total) => progress.push([done, total]),
    });

    // One request at a time, its card saying so.
    await vi.waitFor(() => expect(finish).toHaveLength(1));
    expect(store.deletingDocuments).toEqual(['p1']);
    finish[0](true);
    await vi.waitFor(() => expect(finish).toHaveLength(2));
    expect(store.deletingDocuments).toEqual(['p2']);
    // The first card is gone already, and Publish no longer offers it.
    expect(store.documents.map((entry) => entry.id)).toEqual([
      'p2',
      'p3',
      'p9',
    ]);
    expect(store.sources.topologies).toEqual([
      'core',
      { name: 'edge' },
      { name: 'kept' },
    ]);
    finish[1](true);
    await vi.waitFor(() => expect(finish).toHaveLength(3));
    finish[2](true);

    expect(await run).toEqual({ done: items, failures: [] });
    expect(api.deleteDocument.mock.calls).toEqual([
      ['p1', '"t1"'],
      ['p2', '"t2"'],
      ['p3', '"t3"'],
    ]);
    expect(progress).toEqual([
      [1, 3],
      [2, 3],
      [3, 3],
    ]);
    expect(store.deletingDocuments).toEqual([]);
    expect(store.sources.topologies).toEqual([{ name: 'kept' }]);
    expect(api.listDocuments).toHaveBeenCalledOnce();
    expect(store.documents.map((entry) => entry.id)).toEqual(['p9']);
    expect(said).toEqual([]);
  });

  test('a topology that was not deleted keeps its card, and says why', async () => {
    const items = ['lab', 'core', 'edge', 'spare'].map((target, index) => ({
      id: `p${index + 1}`,
      kind: 'Topology',
      target,
    }));

    store.documents = [...items];
    api.deleteDocument
      .mockRejectedValueOnce(failed(404, 'phenix cannot find document p1'))
      .mockResolvedValueOnce(true)
      .mockRejectedValueOnce(
        failed(
          403,
          'deleting config Topology/edge: user alice does not have permission',
        ),
      )
      .mockRejectedValueOnce(
        failed(412, 'topology spare changed after you last read it'),
      );
    api.listDocuments.mockResolvedValue([items[2], items[3]]);

    const outcome = await store.deletePublishedMany(items);

    expect(outcome.done).toEqual([items[1]]);
    expect(
      outcome.failures.map(({ item, reason }) => [item.id, reason]),
    ).toEqual([
      [
        'p1',
        'Someone deleted it or published it again after Builder read the list.',
      ],
      [
        'p3',
        'Deleting config Topology/edge: user alice does not have permission.',
      ],
      [
        'p4',
        'It changed after Builder read the list. The list now shows it as it is. Check it, and then delete it again.',
      ],
    ]);
    expect(store.documents).toEqual([items[2], items[3]]);
    expect(store.deletingDocuments).toEqual([]);
    expect(store.error).toBe('');
  });

  describe('downloading several drafts or published diagrams', () => {
    // The server's icon library: plc, which the diagrams name.
    const library = (icons) => ({ icons, maxIcons: 2000 });

    // A diagram named `name` whose device alpha names the custom icon plc,
    // of which it carries no copy: the library has it.
    function iconDiagram(name) {
      const sample = sampleDocument();
      const doc = updateNode(sample.doc, sample.alpha.id, {
        device: { icon: 'plc' },
      });

      return { ...doc, metadata: { ...doc.metadata, name } };
    }

    // A draft as GET /builder/drafts/{draft} answers.
    const envelope = (id, document) => ({
      draft: { id, createdBy: 'alice' },
      document,
      history: [],
      cursor: 0,
      etag: '"1"',
    });

    // The library is read once for the page. Each test reads it again, and
    // leaves it empty, as the other tests have it.
    beforeEach(async () => {
      api.listIcons
        .mockReset()
        .mockImplementation(async () =>
          library([{ name: 'plc', aliases: [], data: ICON_DATA }]),
        );
      await iconLibrary.load();
    });

    afterEach(async () => {
      api.getDocument
        .mockReset()
        .mockImplementation(async () => sampleDocument().doc);
      api.listIcons.mockReset().mockImplementation(async () => library([]));
      await iconLibrary.load();
    });

    test('each draft is read, then saved as a Builder JSON file with the custom icons it names from the library, one after another', async () => {
      const items = [draft('d1'), draft('d2')];
      const said = announcements();
      const steps = [];
      const files = [];
      const progress = [];

      api.getDraft.mockImplementation(async (id) => {
        steps.push(`read ${id}`);

        return envelope(id, iconDiagram(`Lab ${id}`));
      });
      store.setError('An earlier failure.');

      const outcome = await store.downloadDrafts(items, {
        save: (file) => {
          steps.push(`save ${file.name}`);
          files.push(file);
        },
        onProgress: (done, total) => progress.push([done, total]),
      });

      expect(outcome).toEqual({ done: items, failures: [] });
      // In order, and one at a time: a draft is saved before the next is
      // read.
      expect(steps).toEqual([
        'read d1',
        'save lab-d1.json',
        'read d2',
        'save lab-d2.json',
      ]);
      expect(progress).toEqual([
        [1, 2],
        [2, 2],
      ]);
      for (const [index, file] of files.entries()) {
        const saved = JSON.parse(file.text);

        expect(saved.metadata.name).toBe(`Lab ${items[index].id}`);
        // The file carries the icon the library has, as Download does.
        expect(saved.icons).toEqual({ plc: { data: ICON_DATA } });
      }
      // The caller says what the run came to, and the page alert is left
      // alone.
      expect(said).toEqual([]);
      expect(store.error).toBe('An earlier failure.');
    });

    test('published diagrams are read as Open reads them, and saved the same way', async () => {
      const items = [{ id: 'p1' }, { id: 'p2' }];
      const names = [];

      api.getDocument
        .mockReset()
        .mockImplementation(async (id) => iconDiagram(`Pub ${id}`));

      const outcome = await store.downloadPublishedMany(items, {
        save: (file) => names.push([file.name, JSON.parse(file.text).icons]),
      });

      expect(outcome).toEqual({ done: items, failures: [] });
      expect(api.getDocument.mock.calls).toEqual([['p1'], ['p2']]);
      expect(names).toEqual([
        ['pub-p1.json', { plc: { data: ICON_DATA } }],
        ['pub-p2.json', { plc: { data: ICON_DATA } }],
      ]);
    });

    test('a draft or published diagram the server no longer has, and a diagram this version cannot read, are listed with why; the others are saved', async () => {
      const gone = (what) => failed(404, `phenix cannot find ${what}`);
      const items = ['d1', 'd2', 'd3', 'd4'].map((id) => draft(id));
      const save = vi.fn();

      api.getDraft.mockImplementation(async (id) => {
        if (id === 'd1' || id === 'd2') {
          throw gone(`draft ${id}`);
        }

        // d3 was saved by a newer version of phenix.
        return envelope(
          id,
          id === 'd3'
            ? { ...iconDiagram('Newer'), revision: 99 }
            : iconDiagram('Kept'),
        );
      });

      const drafts = await store.downloadDrafts(items, { save });

      expect(drafts.done).toEqual([items[3]]);
      expect(
        drafts.failures.map(({ item, reason }) => [item.id, reason]),
      ).toEqual([
        // Whoever created it.
        ['d1', 'Someone deleted it after Builder read the list.'],
        ['d2', 'Someone deleted it after Builder read the list.'],
        [
          'd3',
          'Builder cannot read its diagram and did not download it. A newer version of phenix may have saved it.',
        ],
      ]);
      expect(save).toHaveBeenCalledOnce();
      expect(save.mock.calls[0][0].name).toBe('kept.json');

      // A published diagram deleted or published again since the list was
      // read, and one the server refuses to send.
      const published = [{ id: 'p1' }, { id: 'p2' }, { id: 'p3' }];

      save.mockClear();
      api.getDocument.mockReset().mockImplementation(async (id) => {
        if (id === 'p1') {
          throw gone(`document ${id}`);
        }

        if (id === 'p2') {
          throw failed(500, 'storage down');
        }

        return iconDiagram('Still here');
      });

      const diagrams = await store.downloadPublishedMany(published, { save });

      expect(diagrams.done).toEqual([published[2]]);
      expect(
        diagrams.failures.map(({ item, reason }) => [item.id, reason]),
      ).toEqual([
        [
          'p1',
          'Someone deleted it or published it again after Builder read the list.',
        ],
        ['p2', 'Storage down.'],
      ]);
      expect(save).toHaveBeenCalledOnce();
      expect(store.error).toBe('');
    });

    test('a session that ended, or a server out of reach, ends the run: the rest is not attempted', async () => {
      const items = ['d1', 'd2', 'd3'].map((id) => draft(id));
      const save = vi.fn();

      for (const [error, reason] of [
        [failed(401), 'Your session has ended. Log in again.'],
        [
          new Error('Network Error'),
          'The Builder could not reach the server. Check the connection and try again.',
        ],
      ]) {
        api.getDraft.mockReset().mockRejectedValue(error);
        api.getDocument.mockReset().mockRejectedValue(error);

        const drafts = await store.downloadDrafts(items, { save });

        expect(drafts.done).toEqual([]);
        expect(drafts.failures.map((failure) => failure.reason)).toEqual([
          reason,
          'Not attempted.',
          'Not attempted.',
        ]);
        expect(api.getDraft).toHaveBeenCalledOnce();

        const diagrams = await store.downloadPublishedMany(
          [{ id: 'p1' }, { id: 'p2' }],
          { save },
        );

        expect(diagrams.failures.map((failure) => failure.reason)).toEqual([
          reason,
          'Not attempted.',
        ]);
        expect(api.getDocument).toHaveBeenCalledOnce();
      }
      expect(save).not.toHaveBeenCalled();
    });
  });
});

describe('an Import answer the dialog no longer waits for', () => {
  const SOURCE = { fullName: 'Topology/core', name: 'core', stored: true };

  async function openImport() {
    const pinia = createPinia();
    const onImported = vi.fn();
    const onClose = vi.fn();
    const app = createSSRApp({
      render: () =>
        h(ImportDialog, {
          initial: { kind: 'topology', name: 'core' },
          onImported,
          onClose,
        }),
    });
    let instance = null;

    app.use(pinia);
    app.mixin({
      created() {
        if (this.$.type === ImportDialog) {
          instance = this.$;
        }
      },
    });
    setActivePinia(pinia);
    store = useBuilderStore(pinia);
    store.newDocument({ name: 'Test' });
    await withDraft();
    await renderToString(app);

    return {
      dialog: instance.setupState,
      // Unmounting a component stops its effect scope, as this does to the
      // dialog rendered here.
      unmount: () => instance.scope.stop(),
      onImported,
      onClose,
    };
  }

  // A server answer the test lets go of.
  function deferred() {
    let resolve;
    let reject;
    const promise = new Promise((done, fail) => {
      resolve = done;
      reject = fail;
    });

    return { promise, resolve, reject };
  }

  // What the open draft is, to tell whether anything replaced it.
  function openDraft() {
    return {
      doc: store.doc,
      nodes: store.doc.nodes.length,
      draftId: store.draftId,
      etag: store.etag,
      autosave: store.autosave,
    };
  }

  test('an answer after Cancel changes nothing, though another draft was opened and edited since', async () => {
    const { doc } = sampleDocument();
    const { dialog, onImported, onClose } = await openImport();
    const answer = deferred();

    api.generate.mockReturnValueOnce(answer.promise);

    const submitted = dialog.submit();

    expect(api.generate).toHaveBeenCalledOnce();
    dialog.close();
    expect(onClose).toHaveBeenCalledOnce();

    // Another draft is opened, and edited, before the answer arrives.
    store.newDocument({ name: 'Other' });
    await store.initAutosave({ draftId: 'd2', etag: '"7"' });
    store.commit(
      addNode(store.doc, { kind: 'device', hostname: 'kept' }).doc,
      'Added device kept',
    );

    const before = openDraft();

    expect(before.draftId).toBe('d2');
    expect(before.nodes).toBe(1);

    answer.resolve({ document: doc, warnings: [], source: SOURCE });
    await submitted;

    expect(openDraft()).toEqual(before);
    expect(store.doc).toBe(before.doc);
    expect(store.autosave).toBe(before.autosave);
    expect(onImported).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledOnce();
  });

  // Leaving the page unmounts the dialog without a Cancel.
  test('an answer after the dialog is unmounted changes nothing and emits nothing', async () => {
    const { doc } = sampleDocument();
    const { dialog, unmount, onImported, onClose } = await openImport();
    const answer = deferred();
    const before = openDraft();

    api.generate.mockReturnValueOnce(answer.promise);

    const submitted = dialog.submit();

    expect(api.generate).toHaveBeenCalledOnce();
    unmount();
    answer.resolve({ document: doc, warnings: [], source: SOURCE });
    await submitted;

    expect(openDraft()).toEqual(before);
    expect(store.doc).toBe(before.doc);
    expect(store.autosave).toBe(before.autosave);
    expect(store.error).toBe('');
    expect(onImported).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });

  test('a failure after the dialog is unmounted is not reported', async () => {
    const { dialog, unmount, onImported, onClose } = await openImport();
    const answer = deferred();
    const before = openDraft();

    api.generate.mockReturnValueOnce(answer.promise);

    const submitted = dialog.submit();

    unmount();
    answer.reject(new Error('offline'));
    await submitted;

    expect(openDraft()).toEqual(before);
    expect(store.error).toBe('');
    expect(onImported).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });

  test('a failure after Cancel is not reported', async () => {
    const { dialog, onImported } = await openImport();
    const answer = deferred();

    api.generate.mockReturnValueOnce(answer.promise);

    const submitted = dialog.submit();

    dialog.close();
    answer.reject(new Error('offline'));
    await submitted;

    expect(store.error).toBe('');
    expect(onImported).not.toHaveBeenCalled();
  });

  test('an answer with warnings opens nothing until Continue, and Cancel keeps the open draft', async () => {
    const { doc } = sampleDocument();
    const answer = {
      document: doc,
      warnings: ['Included topology lab was not combined.'],
      source: SOURCE,
    };
    let { dialog, onImported, onClose } = await openImport();
    const before = openDraft();

    api.generate.mockResolvedValueOnce(structuredClone(answer));
    await dialog.submit();

    // The warnings show, and the open draft is as it was.
    expect(dialog.warnings).toEqual(answer.warnings);
    expect(openDraft()).toEqual(before);
    expect(onImported).not.toHaveBeenCalled();

    // Cancel on the warnings, then a Continue too late to count.
    dialog.close();
    dialog.proceed();

    expect(openDraft()).toEqual(before);
    expect(store.doc).toBe(before.doc);
    expect(onImported).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledOnce();

    // Continue opens the diagram in place of the open draft, which is
    // detached with its queue, and has its draft made.
    ({ dialog, onImported, onClose } = await openImport());
    api.generate.mockResolvedValueOnce(structuredClone(answer));
    await dialog.submit();

    const dispose = vi.spyOn(store.autosave, 'dispose');

    dialog.proceed();

    expect(dispose).toHaveBeenCalledOnce();
    expect(store.autosave).toBeNull();
    expect(store.createdBy).toBe('');
    expect(store.etag).toBeNull();
    expect(store.doc).toEqual(doc);
    expect(onImported).toHaveBeenCalledOnce();
    expect(onImported.mock.calls[0][0]).toMatchObject({
      document: store.doc,
      warnings: answer.warnings,
      source: SOURCE,
    });
    expect(onClose).toHaveBeenCalledOnce();
  });

  test('an answer without warnings opens the diagram at once', async () => {
    const { doc } = sampleDocument();
    const { dialog, onImported, onClose } = await openImport();

    api.generate.mockResolvedValueOnce({
      document: doc,
      warnings: [],
      source: SOURCE,
    });
    await dialog.submit();

    expect(store.doc).toEqual(doc);
    expect(store.autosave).toBeNull();
    expect(onImported).toHaveBeenCalledOnce();
    expect(onImported.mock.calls[0][0].document).toEqual(store.doc);
    expect(onClose).toHaveBeenCalledOnce();
  });
});

// Duplicate pastes a copy made of the selection as it is, never what the
// clipboard holds, and leaves the clipboard as it is.
describe('Duplicate', () => {
  test('duplicates the selected nodes without reading or changing the clipboard', () => {
    const { doc, alpha, bravo, edge } = sampleDocument();

    store.setDocument(doc);

    // The clipboard holds alpha.
    store.select({ nodes: [alpha.id], edges: [] });
    store.copy();

    const clipboard = store.clipboard;
    const nodes = store.doc.nodes.length;
    const steps = store.history.entries.length;

    expect(clipboard.nodes).toHaveLength(1);

    // A connection alone has nothing to duplicate: nothing is pasted from
    // the clipboard, and the reason is said.
    store.select({ nodes: [], edges: [edge.id] });

    expect(store.duplicate()).toEqual([]);
    expect(store.doc.nodes).toHaveLength(nodes);
    expect(store.history.entries).toHaveLength(steps);
    expect(store.clipboard).toBe(clipboard);
    expect(store.announcement).toBe(DUPLICATE_NEEDS_NODES);

    // Selected nodes are duplicated, and the clipboard still holds alpha.
    store.select({ nodes: [bravo.id], edges: [] });

    const copies = store.duplicate();

    expect(copies).toHaveLength(1);
    expect(store.doc.nodes).toHaveLength(nodes + 1);
    expect(findNode(store.doc, copies[0]).device.hostname).toBe('bravo-2');
    expect(store.clipboard).toBe(clipboard);
    expect(store.selection).toEqual({ nodes: copies, edges: [] });
    expect(store.history.undoLabel()).toBe('Duplicated 1 node');

    // Nothing selected.
    store.select({ nodes: [], edges: [] });

    expect(store.duplicate()).toEqual([]);
    expect(store.doc.nodes).toHaveLength(nodes + 1);
    expect(store.announcement).toBe('Nothing is selected to duplicate.');
  });

  // commit refuses an edit while a conflict is resolved, and in a read-only
  // draft. The selection must not name nodes the diagram does not have.
  test('a refused add, paste, duplicate or group returns null and keeps the selection', () => {
    const { doc, alpha, bravo } = sampleDocument();

    store.setDocument(doc);
    store.select({ nodes: [alpha.id, bravo.id], edges: [] });
    store.copy();

    const selection = store.selection;
    const steps = store.history.entries.length;
    const edits = {
      addNode: () => store.addNode({ kind: 'device', hostname: 'charlie' }),
      paste: () => store.paste(),
      duplicate: () => store.duplicate(),
      group: () => store.group(),
    };

    for (const refuse of [
      () => (store.resolvingConflict = true),
      () => (store.readOnly = true),
    ]) {
      refuse();

      for (const [name, edit] of Object.entries(edits)) {
        expect(edit(), name).toBeNull();
        expect(store.selection, name).toBe(selection);
      }

      store.resolvingConflict = false;
      store.readOnly = false;
    }

    expect(store.doc.nodes).toHaveLength(doc.nodes.length);
    expect(store.history.entries).toHaveLength(steps);

    // Accepted, each edit selects what it made.
    for (const edit of Object.values(edits)) {
      expect(edit()).not.toBeNull();
      expect(store.selection).not.toBe(selection);
    }
  });
});

describe('Set layer of selection', () => {
  // sampleDocument with two layers, the switch at layer 1 and bravo
  // included from another topology when `included` is set.
  function layered({ included = false } = {}) {
    const sample = sampleDocument();
    let doc = setLayers(sample.doc, [
      { id: 3, label: 'Control' },
      { id: 1, label: 'Field' },
    ]);

    doc = setNodeLayer(doc, sample.sw.id, 1);

    if (included) {
      doc = {
        ...doc,
        source: {
          kind: 'topology',
          name: 'root',
          includeTopologies: ['shared'],
        },
        nodes: doc.nodes.map((node) =>
          node.id === sample.bravo.id
            ? { ...node, device: { ...node.device, includedFrom: 'shared' } }
            : node,
        ),
      };
    }

    store.setDocument(doc);

    return sample;
  }

  const layerOf = (id) => nodeLayer(findNode(store.doc, id));

  test('puts every selected device and switch at a layer in one undo step', () => {
    const { alpha, bravo, sw, edge } = layered();
    const steps = store.history.entries.length;
    const note = store.addNode({ kind: 'note' });

    store.setNodesLayer([alpha.id, bravo.id, sw.id, note.id, edge.id], 3);

    expect([alpha.id, bravo.id, sw.id].map(layerOf)).toEqual([3, 3, 3]);
    expect(store.history.entries).toHaveLength(steps + 2);
    expect(store.history.undoLabel()).toBe(
      'Changed the layer of 3 nodes to Control',
    );
    expect(store.announcement).toBe('Changed the layer of 3 nodes to Control');

    // One Undo puts each back.
    store.undo();
    expect([alpha.id, bravo.id, sw.id].map(layerOf)).toEqual([null, null, 1]);
  });

  test('one node takes the label of its own change, and No layer removes it', () => {
    const { alpha, sw } = layered();

    store.setNodesLayer([alpha.id, sw.id], 1);
    expect(store.history.undoLabel()).toBe(
      'Changed the layer of Device alpha to Field',
    );

    store.setNodesLayer([alpha.id, sw.id], null);
    expect([alpha.id, sw.id].map(layerOf)).toEqual([null, null]);
    expect(store.history.undoLabel()).toBe('Removed the layer of 2 nodes');
  });

  test('takes no step when nothing changes, and says why', () => {
    const { alpha, sw } = layered();
    const steps = store.history.entries.length;

    expect(store.setNodesLayer([sw.id], 1)).toBeNull();
    expect(store.announcement).toBe(
      'Every selected node is at layer Field already.',
    );

    expect(store.setNodesLayer([alpha.id], null)).toBeNull();
    expect(store.announcement).toBe('No selected node has a layer of its own.');

    expect(store.setNodesLayer([], 3)).toBeNull();
    expect(store.announcement).toBe('Select a device or a switch.');

    expect(store.setNodesLayer([alpha.id], 'nine hundred')).toBeNull();
    expect(store.history.entries).toHaveLength(steps);
  });

  test('keeps the layer of an included device, and says how many it kept', () => {
    const { alpha, bravo } = layered({ included: true });

    store.setNodesLayer([alpha.id, bravo.id], 3);
    expect([alpha.id, bravo.id].map(layerOf)).toEqual([3, null]);
    expect(store.history.undoLabel()).toBe(
      'Changed the layer of Device alpha to Control. Kept 1 read-only node of included topologies',
    );

    const steps = store.history.entries.length;

    expect(store.setNodesLayer([bravo.id], 3)).toBeNull();
    expect(store.history.entries).toHaveLength(steps);
    expect(store.announcement).toMatch(
      /^Included topology shared defines bravo/,
    );
  });

  test('says that the nodes it can change are at the layer already', () => {
    const { alpha, bravo } = layered({ included: true });

    store.setNodesLayer([alpha.id], 3);

    const steps = store.history.entries.length;

    // bravo is read only, and is not at 3.
    expect(store.setNodesLayer([alpha.id, bravo.id], 3)).toBeNull();
    expect(store.announcement).toBe(
      'Every selected node that you can change is at layer Control already.',
    );

    store.setNodesLayer([alpha.id], null);
    expect(store.setNodesLayer([alpha.id, bravo.id], null)).toBeNull();
    expect(store.announcement).toBe(
      'No selected node that you can change has a layer of its own.',
    );
    expect(store.history.entries).toHaveLength(steps + 1);
  });

  test('a read-only draft refuses it', () => {
    const { alpha } = layered();

    store.readOnly = true;
    expect(store.setNodesLayer([alpha.id], 3)).toBeNull();
    expect(layerOf(alpha.id)).toBeNull();
    expect(store.error).toBe('This draft is read only.');
  });
});
