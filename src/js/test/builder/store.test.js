import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { computed, effectScope, nextTick } from 'vue';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

const api = vi.hoisted(() => ({
  listDrafts: vi.fn(async () => ({
    mine: [{ id: 'd1', owner: 'alice', name: 'Mine' }],
    shared: [{ id: 'd2', owner: 'bob', name: 'Theirs' }],
    published: [],
  })),
  // A create answers with the draft alone, as the server does: neither the
  // document it was sent nor a history.
  createDraft: vi.fn(async () => ({
    draft: { id: 'd1', owner: 'alice' },
    document: null,
    history: null,
    cursor: 0,
    etag: '"1"',
  })),
  getDraft: vi.fn(async () => ({
    draft: { id: 'd1', owner: 'alice', readOnly: false },
    document: null,
    history: [{ id: 's1', summary: 'first' }],
    cursor: 0,
    etag: '"1"',
  })),
  deleteDraft: vi.fn(async () => true),
  // A save answers with the draft alone, as the server does: the snapshot
  // its cursor is on, but no history.
  appendSnapshot: vi.fn(async () => ({
    draft: { id: 'd1', owner: 'alice', snapshotId: 's2' },
    history: null,
    cursor: 1,
    etag: '"2"',
  })),
  moveCursor: vi.fn(async () => ({
    draft: { id: 'd1', owner: 'alice' },
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
  getShares: vi.fn(async () => ({
    shares: [],
    sharesEtag: '"shares-0"',
    maxShares: 25,
  })),
  updateShares: vi.fn(),
  listShareCandidates: vi.fn(async () => []),
  deleteSnapshot: vi.fn(),
  // The server's icon library: empty, and taking every upload as it is.
  listIcons: vi.fn(async () => ({
    icons: [],
    maxIcons: 64,
    maxBytes: 1048576,
    usedIcons: 0,
    usedBytes: 0,
  })),
  uploadIcon: vi.fn(async (icon) => ({ icon: { ...icon }, created: true })),
}));

vi.mock('@/builder/api.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, builderApi: api };
});

// The digest of what the Builder file of the mocked topologies holds.
const FILE_DIGEST = vi.hoisted(() => `sha256:${'f'.repeat(64)}`);

// This device's local drafts, as the in-memory store keeps them; one per
// test, shared by every queue the test opens.
const device = vi.hoisted(() => ({ store: null }));

vi.mock('@/builder/idb.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, createDraftStore: () => device.store };
});

import { createMemoryStore } from '@/builder/idb.js';
import {
  createDocument,
  findNode,
  setDocumentInfo,
  STAMP_KEYS,
  updateNode,
  withStamp,
} from '@/builder/model.js';
import { builderSchemaV1 } from '@/builder/schema.js';
import { draftForPublished, useBuilderStore } from '@/builder/store.js';
import { useRefusalIssues } from '@/components/builder/dialogs/message.js';

import { sampleDocument } from './fixtures.js';
import { base64Of, ICON_DATA, png } from './png.js';

let store;

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  device.store = createMemoryStore();
  store = useBuilderStore();
  store.newDocument({ name: 'Test' });
});

async function withDraft() {
  await store.initAutosave({ owner: 'alice', draftId: 'd1', etag: '"1"' });
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
      'Not changed: the conflict is being resolved. Edit again once it is.',
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
});

describe('editing commits', () => {
  // The History object is not reactive; a getter read once must still follow
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

    // A replacement reads the stored scenario first and keeps its
    // annotations, with the config's over them, and every topology of
    // both: a PUT would drop them.
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
    expect(api.appendSnapshot.mock.calls[0][2].document.nodes).toHaveLength(1);
    expect(api.appendSnapshot.mock.calls[1][2].document.nodes).toHaveLength(2);
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
      ([, , payload]) => payload.summary === 'Restored previous layout',
    );
    expect(restored).toHaveLength(1);
    expect(geometry(restored[0][2].document)).toEqual(before);

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
    expect(store.announcement).toBe('The diagram is already laid out.');
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
          owner: 'alice',
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
      'alice',
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
      draft: { id: 'd1', owner: 'alice', snapshotId: 's2' },
      history: null,
      etag: '"3"',
    });

    await store.restoreSnapshot('s2');

    expect(api.moveCursor).toHaveBeenCalledWith(
      'alice',
      'd1',
      { snapshotId: 's2' },
      '"1"',
    );
    expect(api.appendSnapshot).not.toHaveBeenCalled();
    expect(store.doc.metadata.id).toBe(doc.metadata.id);
    expect(store.etag).toBe('"3"');
  });

  // The server's answer to a snapshot delete: the draft, as after a save.
  function deletedAnswer({ snapshotId = 's3', cursor = 1, etag = '"4"' }) {
    return {
      draft: { id: 'd1', owner: 'alice', snapshotId, cursor, snapshots: 2 },
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
        draft: { id: 'd1', owner: 'alice', snapshotId: 's2', cursor: 1 },
        history: null,
        cursor: 1,
        etag: '"2"',
      })
      .mockResolvedValueOnce({
        draft: { id: 'd1', owner: 'alice', snapshotId: 's3', cursor: 2 },
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

    expect(api.deleteSnapshot).toHaveBeenCalledWith('alice', 'd1', 's2', '"3"');
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
      'alice',
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
      readDraft({ access: 'owner' }, { etag: '"9"', snapshots: 3 }),
    );

    const refused = await store.deleteSnapshot('s1');

    expect(refused.deleted).toBe(false);
    expect(refused.message).toBe(
      'This draft changed on the server since its history was read. The list now shows the latest history. Try again.',
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
    expect(api.deleteSnapshot).toHaveBeenLastCalledWith(
      'alice',
      'd1',
      's1',
      '"9"',
    );
    expect(store.autosave.record.etag).toBe('"1"');
    expect(store.historyEtag).toBe('"10"');
    expect(store.serverHistory.map((entry) => entry.id)).toEqual(['s2', 's3']);
  });

  test('a delete refused for a change that kept the content takes the new ETag for the queue too', async () => {
    api.getDraft.mockResolvedValueOnce(
      readDraft({ access: 'owner', canShare: true }, { snapshots: 2 }),
    );
    await store.loadDraft('alice', 'd1');
    api.deleteSnapshot.mockRejectedValueOnce(
      Object.assign(new Error('stale'), {
        response: { status: 412, data: {} },
      }),
    );
    // Only who it is shared with changed.
    api.getDraft.mockResolvedValueOnce(
      readDraft(
        { access: 'owner', canShare: true, shares: [{ user: 'bob' }] },
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
      'That is the current snapshot, which cannot be deleted. The list now shows the latest history.',
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
  // failure, itself; the page alert behind it is left alone.
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
      owner: 'alice',
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
      owner: 'alice',
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

  test('legacy cursor-only recovery is discarded without a conflict', async () => {
    await withDraft();

    await store.autosave.attach({
      owner: 'alice',
      draftId: 'd1',
      etag: '"1"',
      entries: [],
      cursor: 0,
      queue: [{ opId: 'old-cursor', kind: 'cursor', index: 0 }],
    });

    expect(await store.recoverLocalHistory('"2"')).toBe(false);
    expect(store.saveState.status).toBe('saved');
    expect(store.autosave.record.queue).toEqual([]);
    expect(store.autosave.record.etag).toBe('"2"');
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
      owner: 'alice',
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
      'alice',
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
      'alice',
      'd1',
      { snapshotId: 's1' },
      '"4"',
    );
    expect(store.saveState.status).toBe('saved');
  });

  // A session that ended while a save was under way left it queued; the
  // server holds it under its operation id, so it is neither sent again nor
  // reported as a conflict, and the rest of the queue goes on.
  test('a recovered save the server already holds is not a conflict', async () => {
    const sent = createDocument({ name: 'Sent' });
    const later = setDocumentInfo(sent, { name: 'Later' });

    await device.store.put(
      {
        key: 'alice::alice::d1',
        actor: 'alice',
        owner: 'alice',
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
      draft: { id: 'd1', owner: 'alice' },
      document: sent,
      history: [
        { id: 's1', current: false },
        { id: 's2', opId: 'c1', current: true },
      ],
      cursor: 1,
      etag: '"2"',
    });
    api.appendSnapshot.mockResolvedValueOnce({
      draft: { id: 'd1', owner: 'alice', snapshotId: 's3' },
      history: null,
      etag: '"3"',
    });

    await store.loadDraft('alice', 'd1');
    await store.saveNow();

    expect(store.hasConflict).toBe(false);
    expect(api.appendSnapshot).toHaveBeenCalledOnce();
    expect(api.appendSnapshot).toHaveBeenCalledWith(
      'alice',
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
    expect(store.announcement).toBe('Connected devices through a new switch');

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

    expect(first.error).toMatch(/Two switches/);
    expect(again.error).toBe(first.error);
    expect(store.notice.text).toBe(first.error);
    // A repeated refusal is announced again (a new live-region entry).
    expect(store.announcementSeq).toBe(seq + 2);
    expect(store.notice.seq).toBe(store.announcementSeq);

    store.dismissNotice();
    expect(store.notice).toBeNull();
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
      draft: { id: 'd2', owner: 'alice' },
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
      expect.anything(),
      renamed,
      expect.anything(),
    );
    expect(store.doc.metadata.name).toBe('Recovered');
    expect(store.draftId).toBe('d1');
  });

  // The fork of a draft another user owns is the actor's, and undo in
  // it moves the fork's cursor to the fork's own snapshots.
  test('after forking a shared draft, undo moves the fork’s cursor', async () => {
    await store.initAutosave({ owner: 'bob', draftId: 'd1', etag: '"1"' });
    store.history.currentEntry().serverSnapshotId = 's1';
    api.appendSnapshot.mockRejectedValueOnce(
      Object.assign(new Error('conflict'), { response: { status: 412 } }),
    );
    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.saveNow();
    expect(store.hasConflict).toBe(true);

    api.createDraft.mockResolvedValueOnce({
      draft: { id: 'd9', owner: 'alice', snapshotId: 'f1' },
      history: null,
      etag: '"f1"',
    });
    api.appendSnapshot.mockResolvedValueOnce({
      draft: { id: 'd9', owner: 'alice', snapshotId: 'f2' },
      history: null,
      etag: '"f2"',
    });
    api.listSnapshots.mockResolvedValueOnce([{ id: 'f1' }, { id: 'f2' }]);

    await store.resolveConflict('fork', { title: 'Mine' });

    expect(api.createDraft.mock.calls[0][0]).not.toHaveProperty('owner');
    expect([store.owner, store.draftId]).toEqual(['alice', 'd9']);
    expect(store.serverHistory).toEqual([{ id: 'f1' }, { id: 'f2' }]);

    store.undo();
    const state = await store.saveNow();

    expect(api.moveCursor).toHaveBeenLastCalledWith(
      'alice',
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
              draft: { id: 'd2', owner: 'alice' },
              document: request.document,
              history: null,
              etag: '"f1"',
            });
        }),
    );
    api.appendSnapshot
      .mockResolvedValueOnce({
        draft: { id: 'd2', owner: 'alice' },
        history: null,
        etag: '"f2"',
      })
      .mockResolvedValueOnce({
        draft: { id: 'd2', owner: 'alice' },
        history: null,
        etag: '"f3"',
      });

    const forking = store.resolveConflict('fork', { title: 'Recovered' });
    const devices = store.summary.devices;

    store.addNode({ kind: 'device', hostname: 'bravo' });
    store.undo();

    expect(store.summary.devices).toBe(devices);
    expect(store.announcement).toBe(
      'Not changed: the conflict is being resolved. Edit again once it is.',
    );

    created();
    await forking;

    expect(store.history.entries).toHaveLength(store.history.index + 1);
    expect(store.history.current()).toEqual(store.doc);
    expect(store.summary.devices).toBe(devices);

    store.addNode({ kind: 'device', hostname: 'charlie' });
    await store.saveNow();

    expect(api.appendSnapshot).toHaveBeenLastCalledWith(
      'alice',
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

    expect(api.getDraft).toHaveBeenCalledWith('alice', 'd1');
  });
});

describe('server data', () => {
  test('drafts, published documents and sources are loaded', async () => {
    await store.fetchDrafts();
    await store.fetchDocuments();
    await store.fetchSources();

    expect(store.drafts.mine).toHaveLength(1);
    expect(store.drafts.shared).toHaveLength(1);
    expect(store.documents).toHaveLength(1);
    expect(store.sources.topologies).toEqual(['core']);
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
      /^Could not open the published diagram\. The server sent a diagram the editor cannot open: Unsupported builder document schema/,
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

    // The draft made from it, the one changed last, is opened again rather
    // than another copy made. The server's times drop trailing zeros, so
    // they are compared as times, not text.
    const { doc } = sampleDocument();
    api.listDrafts.mockResolvedValueOnce({
      mine: [
        { id: 'other', owner: 'alice', sourceToken: 'builder-doc/p2' },
        {
          id: 'd6',
          owner: 'alice',
          sourceToken: 'builder-doc/published-1',
          updated: '2026-09-02T00:00:00Z',
        },
        {
          id: 'd7',
          owner: 'alice',
          sourceToken: 'builder-doc/published-1',
          updated: '2026-09-02T00:00:00.1Z',
        },
      ],
      shared: [],
      published: [],
    });
    api.getDraft.mockResolvedValueOnce({
      draft: { id: 'd7', owner: 'alice' },
      document: doc,
      history: [{ id: 's1' }],
      cursor: 0,
      etag: '"7"',
    });

    await expect(
      store.openPublishedDocument('published-1'),
    ).resolves.toBeTruthy();
    expect(api.getDraft).toHaveBeenLastCalledWith('alice', 'd7');
    expect(store.draftId).toBe('d7');
    expect(store.announcement).toBe(
      `Opened your draft of published diagram ${doc.metadata.name}.`,
    );
    expect(api.createDraft).toHaveBeenCalledTimes(1);
  });

  test('a schema failure is reported, not swallowed', async () => {
    api.getSchema.mockRejectedValueOnce(new Error('boom'));

    await store.fetchSchema();

    expect(store.schema.$id).toBe(builderSchemaV1.$id);
    expect(store.schemaSource).toBe('bundled');
    // In plain words: no "schema" jargon.
    expect(store.schemaError).toMatch(
      /^Could not load this server's form fields\. .*fields built into the Builder instead/,
    );
    expect(store.schemaError).not.toMatch(/schema/i);
  });

  test('an unusable server schema is reported too', async () => {
    api.getSchema.mockResolvedValueOnce({ definitions: {} });

    await store.fetchSchema();

    expect(store.schemaSource).toBe('bundled');
    expect(store.schemaError).toBeTruthy();
  });

  test('a usable server schema replaces the bundle silently', async () => {
    await store.fetchSchema();

    expect(store.schemaSource).toBe('server');
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

    expect(api.publish).toHaveBeenCalledWith('alice', 'd1', intent, '"1"');
    expect(api.publish.mock.calls[0][2].document).toBeUndefined();
    expect(result.ok).toBe(true);
    expect(store.publishResult).toEqual(result);
    // The topology now exists, so the list the next publish chooses create
    // or update from is read again.
    expect(api.getSources).toHaveBeenCalledOnce();
    expect(store.sources.topologies).toEqual(['core']);
    // So is the list of published diagrams, which says what it may update.
    expect(api.listDocuments).toHaveBeenCalledOnce();
  });

  test('Publish knows which config the draft was loaded from and what it saved', async () => {
    api.createDraft.mockImplementationOnce(async (request) => ({
      draft: {
        id: 'd1',
        owner: 'alice',
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
            message: 'config core already exists; choose update explicitly',
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
      'Could not publish the diagram. A topology named "core" already exists, and this diagram cannot update it: ' +
        'the diagram was not imported from it, opened from its published diagram or published to it. ' +
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
            message:
              'topology core is not the source this draft was loaded from',
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
      /^Could not publish the diagram\. A topology named "core" already exists, and this diagram cannot update it: .* Enter another name to create a new topology\.$/,
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

  test('a diagram the server will not publish says which interfaces to fix', async () => {
    await withDraft();

    const vlan =
      'interface #2 of device "server" has no VLAN: connect it to a network, or type a VLAN for it';

    api.publish.mockRejectedValueOnce(
      Object.assign(new Error('unprocessable'), {
        response: {
          status: 422,
          data: {
            message: `topology lab cannot be published: ${vlan}`,
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
      `Could not publish the diagram. Topology lab cannot be published: ${vlan}.`,
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
                  message: 'hostname is required',
                },
              ],
            },
          },
        }),
      );
    const refused = [
      {
        severity: 'error',
        message: 'hostname is required',
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
        message: 'hostname is required',
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
      { nodeId: 'gone', message: 'hostname is required' },
      { path: 'nodes[9].device.hostname', message: 'hostname is required' },
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
    expect(api.publish.mock.calls[0][3]).toBe('"2"');
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
        draft: { id: 'd1', owner: 'alice', snapshotId: `s${revision}` },
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
    expect(api.publish.mock.calls[0][3]).toBe('"3"');
  });

  // An edit made while a publish is under way (the dialog was closed) is
  // saved after it, against the ETag the publish answered with, so the
  // user's own publish is never reported as a conflict.
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
    expect(api.appendSnapshot.mock.calls[0][3]).toBe('"published"');
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

  test('generation starts a separate draft and detaches the previous queue', async () => {
    const { doc } = sampleDocument();

    await withDraft();

    const dispose = vi.spyOn(store.autosave, 'dispose');
    api.generate.mockResolvedValueOnce({
      document: doc,
      warnings: [],
      source: { fullName: 'Topology/core', stored: true },
    });

    const result = await store.generate({ kind: 'topology', name: 'core' });

    expect(result.document).toEqual(doc);
    expect(dispose).toHaveBeenCalledOnce();
    expect(store.autosave).toBeNull();
    expect(store.owner).toBe('');
    expect(store.etag).toBeNull();
  });

  // store.generate serves Import. The user may still cancel on its
  // warnings, so nothing says it imported anything: ImportDialog has the
  // import announced once the draft exists.
  test('an import announces nothing before its draft exists', async () => {
    const { doc } = sampleDocument();

    store.announce('Before.');

    for (const warnings of [[], ['one']]) {
      api.generate.mockResolvedValueOnce({
        document: doc,
        warnings,
        source: { fullName: 'Topology/core', stored: true },
      });

      await expect(
        store.generate({ kind: 'topology', name: 'core' }),
      ).resolves.toMatchObject({ warnings });
      expect(store.announcement).toBe('Before.');
    }
  });

  test('generate rejects an unreadable document without changing the current draft', async () => {
    await withDraft();

    const beforeDocument = store.doc;
    const beforeAutosave = store.autosave;
    const result = await store.generate({ kind: 'topology', name: 'core' });

    expect(result).toBeNull();
    // The server answered, so this is not a connection failure; the reason
    // is the decoder's.
    expect(store.error).toBe(
      'Could not import the diagram. The server sent a diagram the editor cannot open: A builder document must be a JSON object.',
    );
    expect(store.errorField).toBe('');
    expect(store.doc).toBe(beforeDocument);
    expect(store.autosave).toBe(beforeAutosave);
    expect(store.owner).toBe('alice');
    expect(store.draftId).toBe('d1');

    api.generate.mockResolvedValueOnce({
      document: { ...sampleDocument().doc, revision: 99 },
      warnings: [],
    });
    await store.generate({ content: 'kind: Topology' });
    expect(store.error).toMatch(
      /^Could not import the diagram\. The server sent a diagram the editor cannot open: Unsupported builder document revision 99 \(expected \d+\)\.$/,
    );
    expect(store.errorField).toBe('');
    expect(store.doc).toBe(beforeDocument);
  });

  test('a generate source the server refuses is reported on its field', async () => {
    const refusal = () =>
      Object.assign(new Error('refused'), {
        response: {
          status: 422,
          data: { message: 'Scenario configs cannot be opened in the builder' },
        },
      });

    api.generate.mockRejectedValueOnce(refusal());
    await store.generate({ content: 'kind: Scenario' });
    expect(store.error).toBe(
      'Could not import the diagram. Scenario configs cannot be opened in the builder.',
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
          data: { message: 'config Topology/gone not found' },
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
      'Could not import the diagram. Config Topology/gone not found.',
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

  test('a new topology name the server refuses is reported on its field', async () => {
    const refusal = (message) =>
      Object.assign(new Error('refused'), {
        response: { status: 422, data: { message, cause: '' } },
      });
    const request = { kind: 'topology', name: 'core', copy: true };

    api.generate.mockRejectedValueOnce(
      refusal(
        'new topology name "core" is the name of the imported topology: enter another name',
      ),
    );
    await expect(
      store.generate({ ...request, newName: 'core' }),
    ).resolves.toBeNull();
    expect(store.error).toBe(
      'Could not import the diagram. New topology name "core" is the name of the imported topology: enter another name.',
    );
    expect(store.errorField).toBe('newName');

    api.generate.mockRejectedValueOnce(
      refusal(
        'new topology name "a b" is not allowed: use 1 to 512 letters, numbers, underscores, at signs, periods and hyphens',
      ),
    );
    await store.generate({ ...request, newName: 'a b' });
    expect(store.errorField).toBe('newName');

    // Any other refusal of the same request is about its source.
    api.generate.mockRejectedValueOnce(
      refusal('only a topology can be combined or copied on import'),
    );
    await store.generate({ ...request, newName: 'core-copy' });
    expect(store.errorField).toBe('name');
  });

  // store.convertLegacy serves Upload, which the editor's toolbar opens
  // over the open draft. The user may still cancel on the warnings, so the
  // conversion is only checked here: the dialog loads it once the user
  // continues.
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
    expect(store.owner).toBe('alice');
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

  test('a legacy file the server refuses is reported in its words, on the file', async () => {
    const refusal = (status, message) =>
      Object.assign(new Error('refused'), {
        response: { status, data: { message, cause: '' } },
      });
    const convert = () => store.convertLegacy({ content: 'hello' });

    api.convertLegacy.mockRejectedValueOnce(
      refusal(
        422,
        'this is not a legacy Builder diagram: expected mxGraph XML, or a Topology config with the builder-xml annotation',
      ),
    );
    await expect(convert()).resolves.toBeNull();
    expect(store.error).toBe(
      'Could not convert the legacy diagram. This is not a legacy Builder diagram: expected mxGraph XML, or a Topology config with the builder-xml annotation.',
    );
    expect(store.errorField).toBe('content');

    // The topology's name is shown as the server wrote it, an id in it too.
    const named = 'lab-123e4567-e89b-12d3-a456-426614174000';
    api.convertLegacy.mockRejectedValueOnce(
      refusal(
        422,
        `topology ${named} has no legacy Builder diagram (no builder-xml annotation); use Import to make a diagram from it`,
      ),
    );
    await convert();
    expect(store.error).toBe(
      `Could not convert the legacy diagram. Topology ${named} has no legacy Builder diagram (no builder-xml annotation); use Import to make a diagram from it.`,
    );
    expect(store.errorField).toBe('content');

    api.convertLegacy.mockRejectedValueOnce(
      refusal(413, 'the legacy diagram is larger than 5242880 bytes'),
    );
    await convert();
    expect(store.error).toBe(
      'Could not convert the legacy diagram. The legacy diagram is larger than 5242880 bytes.',
    );
    expect(store.errorField).toBe('content');

    api.convertLegacy.mockRejectedValueOnce(
      refusal(400, 'content is required'),
    );
    await convert();
    expect(store.error).toBe(
      'Could not convert the legacy diagram. Content is required.',
    );
    expect(store.errorField).toBe('content');
  });

  test('a legacy conversion that fails for another reason is not about the file', async () => {
    const convert = () => store.convertLegacy({ content: '<mxGraphModel/>' });

    // A role without the permission, and an unreachable server.
    api.convertLegacy.mockRejectedValueOnce(
      Object.assign(new Error('forbidden'), {
        response: {
          status: 403,
          data: {
            message:
              'converting a legacy builder diagram not allowed for alice',
          },
        },
      }),
    );
    await expect(convert()).resolves.toBeNull();
    expect(store.error).toBe(
      'Could not convert the legacy diagram. Converting a legacy builder diagram not allowed for alice.',
    );
    expect(store.errorField).toBe('');

    api.convertLegacy.mockRejectedValueOnce(new Error('offline'));
    await convert();
    expect(store.error).toBe(
      'Could not convert the legacy diagram. The server could not be reached. Check the connection and try again.',
    );
    expect(store.errorField).toBe('');

    // The server answered with a diagram the editor cannot open: the
    // reason is the decoder's, and the open document stays.
    const before = store.doc;
    await convert();
    expect(store.error).toBe(
      'Could not convert the legacy diagram. The server sent a diagram the editor cannot open: A builder document must be a JSON object.',
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
      'Could not convert the legacy diagram. The server sent a diagram the editor cannot open.',
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
      'Could not import the diagram. The server sent a diagram the editor cannot open.',
    );
    expect(store.doc).toBe(before);
  });

  test('deleting a draft passes its ETag', async () => {
    // What this device kept for the draft goes with it, whoever edited it
    // here, and nothing else does.
    const keep = (actor, owner, draftId) =>
      device.store.put({
        key: `${actor}::${owner}::${draftId}`,
        actor,
        owner,
        draftId,
        entries: [],
        queue: [{ kind: 'cursor', snapshotId: 's1' }],
      });
    await keep('alice', 'alice', 'd1');
    await keep('bob', 'alice', 'd1');
    await keep('alice', 'alice', 'd2');

    await store.deleteDraft('alice', 'd1', '"1"', 'Lab network');

    expect(api.deleteDraft).toHaveBeenCalledWith('alice', 'd1', '"1"');
    expect(store.announcement).toBe('Deleted draft Lab network.');
    expect((await device.store.all()).map((record) => record.key)).toEqual([
      'alice::alice::d2',
    ]);
  });

  // A card's ETag is older than the server's right after Back to drafts,
  // until the list is read again.
  test('deleting with an old ETag reads the draft again and deletes it', async () => {
    const changed = () =>
      Object.assign(new Error('changed'), { response: { status: 412 } });

    api.deleteDraft.mockRejectedValueOnce(changed());
    api.getDraft.mockResolvedValueOnce({ draft: { id: 'd1' }, etag: '"2"' });

    expect(await store.deleteDraft('alice', 'd1', '"1"', 'Lab network')).toBe(
      true,
    );
    expect(api.getDraft).toHaveBeenCalledWith('alice', 'd1');
    expect(api.deleteDraft).toHaveBeenLastCalledWith('alice', 'd1', '"2"');
    expect(store.announcement).toBe('Deleted draft Lab network.');
    expect(store.error).toBe('');

    // Once only: a draft that changes again is left, and the alert says so.
    api.deleteDraft.mockClear();
    api.deleteDraft
      .mockRejectedValueOnce(changed())
      .mockRejectedValueOnce(changed());
    api.getDraft.mockResolvedValueOnce({ draft: { id: 'd1' }, etag: '"3"' });

    expect(await store.deleteDraft('alice', 'd1', '"2"')).toBe(false);
    expect(api.deleteDraft).toHaveBeenCalledTimes(2);
    expect(store.error).toBe(
      'Could not delete the draft. It changed on the server while it was being deleted. Try again.',
    );

    // A draft gone meanwhile says so.
    api.deleteDraft.mockClear();
    api.deleteDraft.mockRejectedValueOnce(changed());
    api.getDraft.mockRejectedValueOnce(
      Object.assign(new Error('gone'), { response: { status: 404 } }),
    );

    expect(await store.deleteDraft('alice', 'd1', '"2"')).toBe(false);
    expect(api.deleteDraft).toHaveBeenCalledTimes(1);
    expect(store.error).toBe(
      'Could not delete the draft. This draft no longer exists on the server.',
    );
  });

  test('deleting a published topology removes its card, once', async () => {
    store.documents = [
      { id: 'p1', kind: 'Topology', target: 'lab' },
      { id: 'p2', kind: 'Topology', target: 'core' },
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
    expect(api.deleteDocument).toHaveBeenCalledWith('p1');
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
          'Only published topologies can be deleted here. Delete experiments from the Experiments page.',
        ),
        'Could not delete topology lab. Only published topologies can be deleted here. Delete experiments from the Experiments page.',
      ],
      [
        refused(403, 'deleting config Topology/lab not allowed for alice'),
        'Could not delete topology lab. Deleting config Topology/lab not allowed for alice.',
      ],
      [
        new Error('Network Error'),
        'Could not delete topology lab. The server could not be reached. Check the connection and try again.',
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
      refused(404, 'document p1 not found'),
    );
    api.listDocuments.mockResolvedValueOnce([]);

    expect(await store.deletePublished(lab)).toBe(false);
    expect(store.error).toBe(
      'Could not delete topology lab. It was deleted or published again since the list was read.',
    );
    expect(api.listDocuments).toHaveBeenCalledOnce();
    expect(store.documents).toEqual([]);
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

// A draft as GET /builder/drafts/{owner}/{draft} answers, with how the
// user reaches it.
function readDraft(draft, { etag = '"1"', snapshots = 1 } = {}) {
  return {
    draft: {
      id: 'd1',
      owner: 'alice',
      snapshotId: `s${snapshots}`,
      cursor: snapshots - 1,
      snapshots,
      readOnly: false,
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

describe('sharing', () => {
  test('an opened draft says whose it is and what the user may do', async () => {
    api.getDraft.mockResolvedValueOnce(
      readDraft({
        access: 'owner',
        canShare: true,
        shares: [{ user: 'bob', access: 'edit' }],
      }),
    );
    await store.loadDraft('alice', 'd1');

    expect(store.isOwner).toBe(true);
    expect(store.canShare).toBe(true);
    expect(store.shareCapable).toBe(true);
    expect(store.shares).toEqual([{ user: 'bob', access: 'edit' }]);
    expect(store.sharedBy).toBe('');

    const { name } = sampleDocument().doc.metadata;

    api.getDraft.mockResolvedValueOnce(
      readDraft({ owner: 'bob', access: 'edit', via: 'share' }),
    );
    await store.loadDraft('bob', 'd1');

    expect(store.isOwner).toBe(false);
    expect(store.canShare).toBe(false);
    expect(store.shares).toEqual([]);
    expect(store.sharedBy).toBe('bob');
    expect(store.announcement).toBe(
      `Opened bob's draft ${name}. You can edit it; others may be editing too.`,
    );

    api.getDraft.mockResolvedValueOnce(
      readDraft({ owner: 'bob', access: 'view', via: 'share', readOnly: true }),
    );
    await store.loadDraft('bob', 'd1');

    expect(store.readOnly).toBe(true);
    expect(store.announcement).toBe(`Opened bob's draft ${name}, view only.`);

    // Missing or not shared: the same words, as the server answers the same.
    const missing = Object.assign(new Error('missing'), {
      response: { status: 404, data: { message: 'draft bob/d1 not found' } },
    });
    api.listDrafts.mockClear();
    api.listDrafts.mockResolvedValueOnce({
      mine: [],
      shared: [],
      others: [],
      published: [],
      damaged: [],
    });
    api.getDraft.mockRejectedValueOnce(missing);
    await store.loadDraft('bob', 'd1');
    expect(store.error).toBe(
      'This draft does not exist, or it is not shared with you.',
    );
    // The lists are read again, so the card leaves them; the alert stays.
    expect(api.listDrafts).toHaveBeenCalledTimes(1);
    expect(store.drafts.shared).toEqual([]);

    api.getDraft.mockRejectedValueOnce(missing);
    await store.loadDraft('alice', 'd1');
    expect(store.error).toMatch(/^Could not load the draft\./);
    expect(api.listDrafts).toHaveBeenCalledTimes(1);
  });

  test('a draft just made may be shared when the user may share any', async () => {
    api.listDrafts.mockResolvedValueOnce({
      mine: [{ id: 'd0', owner: 'alice', canShare: true }],
      shared: [],
      others: [],
      published: [],
      damaged: [],
    });
    await store.fetchDrafts();
    api.listDrafts.mockClear();

    await store.createDraft({ title: 'New' });

    expect(store.canShare).toBe(true);
    expect(store.isOwner).toBe(true);
    expect(api.listDrafts).not.toHaveBeenCalled();

    // Unknown until the lists hold a draft of the user's: they are read
    // again once the new draft exists.
    store.shareCapable = null;
    api.listDrafts.mockResolvedValueOnce({
      mine: [{ id: 'd1', owner: 'alice', canShare: false }],
      shared: [],
      others: [],
      published: [],
      damaged: [],
    });
    await store.createDraft({ title: 'Another' });
    await vi.waitFor(() => expect(store.shareCapable).toBe(false));
    expect(store.canShare).toBe(false);
  });

  test('a share saved for the open draft keeps the ETag only over the same content', async () => {
    api.getDraft.mockResolvedValueOnce(
      readDraft({ access: 'owner', canShare: true }),
    );
    await store.loadDraft('alice', 'd1');

    const answer = (draft, etag) => ({
      shares: draft.shares,
      sharesEtag: '"shares-1"',
      maxShares: 25,
      draft: { ...draft, etag },
      etag,
    });
    const same = readDraft({
      access: 'owner',
      canShare: true,
      shares: [{ user: 'bob', access: 'view' }],
    }).draft;

    api.updateShares.mockResolvedValueOnce(answer(same, '"2"'));
    const saved = await store.saveShares(
      { owner: 'alice', id: 'd1' },
      [{ user: 'bob', access: 'view' }],
      '"shares-0"',
    );

    expect(api.updateShares).toHaveBeenCalledWith(
      'alice',
      'd1',
      [{ user: 'bob', access: 'view' }],
      '"shares-0"',
    );
    expect(saved.sharesEtag).toBe('"shares-1"');
    expect(store.shares).toEqual([{ user: 'bob', access: 'view' }]);
    expect(store.etag).toBe('"2"');
    expect(store.autosave.record.etag).toBe('"2"');

    // Someone saved in between: their ETag is not taken.
    const moved = readDraft(
      { access: 'owner', canShare: true },
      { snapshots: 2 },
    ).draft;
    api.updateShares.mockResolvedValueOnce(answer(moved, '"4"'));
    await store.saveShares({ owner: 'alice', id: 'd1' }, [], '"shares-1"');

    expect(store.shares).toEqual([]);
    expect(store.autosave.record.etag).toBe('"2"');
  });

  test('the users a draft may be shared with are read for its owner', async () => {
    api.listShareCandidates.mockResolvedValueOnce([
      { username: 'bob', name: 'Bob Lee' },
    ]);

    await expect(
      store.loadShareCandidates({ owner: 'alice', id: 'd5' }),
    ).resolves.toEqual([{ username: 'bob', name: 'Bob Lee' }]);
    expect(api.listShareCandidates).toHaveBeenCalledWith('alice', 'd5');
  });

  test('a listed draft takes the draft a share change answered with', async () => {
    await store.fetchDrafts();
    store.drafts.mine = [
      { id: 'd5', owner: 'alice', etag: '"1"', shares: [{ user: 'bob' }] },
      { id: 'd6', owner: 'alice', etag: '"1"' },
    ];
    api.updateShares.mockResolvedValueOnce({
      shares: [],
      sharesEtag: '"shares-2"',
      maxShares: 25,
      draft: { id: 'd5', owner: 'alice', etag: '"7"', canShare: true },
      etag: '"7"',
    });

    await store.saveShares({ owner: 'alice', id: 'd5' }, [], '"shares-1"');

    expect(store.drafts.mine[0]).toEqual({
      id: 'd5',
      owner: 'alice',
      etag: '"7"',
      canShare: true,
      shares: [],
    });
    expect(store.drafts.mine[1].etag).toBe('"1"');
    expect(store.drafts.shared).toHaveLength(1);
  });

  test('losing access to a shared draft makes it read only, and a fork keeps the work', async () => {
    api.getDraft.mockResolvedValueOnce(
      readDraft({ owner: 'bob', access: 'edit', via: 'share' }),
    );
    await store.loadDraft('bob', 'd1');
    api.appendSnapshot.mockRejectedValueOnce(
      Object.assign(new Error('refused'), { response: { status: 403 } }),
    );
    api.getDraft.mockResolvedValueOnce(
      readDraft({ owner: 'bob', access: 'view', via: 'share', readOnly: true }),
    );

    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.saveNow();

    expect(store.accessLost).toBe('view-only');
    expect(store.readOnly).toBe(true);
    expect(store.access).toBe('view');
    expect(store.saveState.status).toBe('forbidden');
    // An edit now is refused rather than kept where no one can save it.
    expect(store.commit(store.doc, 'Nothing')).toBeNull();

    api.createDraft.mockResolvedValueOnce({
      draft: { id: 'd9', owner: 'alice', snapshotId: 'f0' },
      document: null,
      history: null,
      cursor: 0,
      etag: '"1"',
    });
    await store.resolveConflict('fork', { title: 'Mine now' });

    expect(api.createDraft).toHaveBeenCalledWith(
      expect.objectContaining({ forkOf: 'bob/d1' }),
    );
    expect(store.owner).toBe('alice');
    expect(store.draftId).toBe('d9');
    expect(store.readOnly).toBe(false);
    expect(store.accessLost).toBe('');
    expect(store.isOwner).toBe(true);
  });

  test('a share taken away names no one who shared it', async () => {
    api.getDraft.mockResolvedValueOnce(
      readDraft({ owner: 'bob', access: 'edit', via: 'share' }),
    );
    await store.loadDraft('bob', 'd1');
    expect(store.sharedBy).toBe('bob');

    const missing = Object.assign(new Error('missing'), {
      response: { status: 404, data: { message: 'draft bob/d1 not found' } },
    });
    api.appendSnapshot.mockRejectedValueOnce(missing);
    api.getDraft.mockRejectedValueOnce(missing);

    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.saveNow();

    expect(store.accessLost).toBe('gone');
    // No chip, no Share button, and no palette command name bob any more.
    expect(store.sharedBy).toBe('');
  });
});

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
  test('the toggle goes from System to the opposite of what the system shows', () => {
    const element = { dataset: {}, setAttribute() {} };
    const cycle = () =>
      [1, 2, 3].map(() => {
        store.cycleTheme(element);

        return store.theme;
      });

    store.setTheme('light', element);
    expect(store.theme).toBe('light');
    expect(store.announcement).toBe('Theme set to light.');

    // The Settings dialog's choice is not announced.
    const said = store.announcementSeq;
    store.setTheme('dark', element, { announce: false });
    expect(store.theme).toBe('dark');
    expect(store.announcementSeq).toBe(said);

    // No matchMedia here: the system is taken as light.
    store.setTheme('system', element);
    expect(cycle()).toEqual(['dark', 'light', 'system']);

    vi.stubGlobal('window', {
      matchMedia: (query) => ({
        matches: query === '(prefers-color-scheme: dark)',
      }),
    });
    try {
      expect(cycle()).toEqual(['light', 'dark', 'system']);
      expect(store.resolvedTheme).toBe('dark');
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

// The server writes who made the document and who saved it last, and when,
// into the metadata of the copy it stores, and answers a create and a save
// with them. The editor copies them into its own copy, and never makes them
// up.
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
      draft: { id: 'd1', owner: 'alice', snapshotId, stamp },
      history: null,
      etag,
    });
  }

  test('a new draft takes the stamp its create answered with', async () => {
    api.createDraft.mockResolvedValueOnce({
      draft: { id: 'd1', owner: 'alice', snapshotId: 's1', stamp: created },
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

    expect(stampOf(api.appendSnapshot.mock.calls[0][2].document)).toEqual({});
    expect(stampOf(store.doc)).toEqual(saved(1));
    expect(stampOf(store.history.current())).toEqual(saved(1));
    expect(store.doc.nodes).toHaveLength(1);
    // The next edit is built from the stamped diagram and sends that stamp,
    // which the server replaces.
    saveAnswers('s3', saved(2, 'bob'), '"3"');
    store.addNode({ kind: 'device', hostname: 'bravo' });
    await store.saveNow();

    expect(stampOf(api.appendSnapshot.mock.calls[1][2].document)).toEqual(
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
            draft: { id: 'd1', owner: 'alice', snapshotId, stamp },
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
      draft: { id: 'd9', owner: 'alice', snapshotId: 'f1', stamp: forked(1) },
      history: null,
      etag: '"f1"',
    });
    [2, 3].forEach((n) =>
      api.appendSnapshot.mockResolvedValueOnce({
        draft: {
          id: 'd9',
          owner: 'alice',
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

  // A session ended after a save reached the server and before its answer
  // came back, with an edit and an undo back to that save still queued.
  // The entry of that save is the snapshot the server lists: its stamp is
  // read from there, who stored it and when, to the second.
  test('a recovered save the server already holds takes its snapshot’s user and time', async () => {
    const opened = withStamp(createDocument({ name: 'Opened' }), created);
    const sent = setDocumentInfo(opened, { name: 'Sent' });
    const later = setDocumentInfo(opened, { name: 'Later' });

    await device.store.put(
      {
        key: 'alice::alice::d1',
        actor: 'alice',
        owner: 'alice',
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
      draft: { id: 'd1', owner: 'alice' },
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

    await store.loadDraft('alice', 'd1');
    await store.saveNow();

    expect(store.hasConflict).toBe(false);
    expect(api.appendSnapshot).toHaveBeenCalledOnce();
    expect(api.moveCursor).toHaveBeenCalledWith(
      'alice',
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
      draft: { id: 'd1', owner: 'alice', sourceFile: 'plant.builder.json' },
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
      draft: { id: 'd2', owner: 'alice', sourceFile: 'core.yaml' },
      document: createDocument({ name: 'Core' }),
      history: [{ id: 's1' }],
      cursor: 0,
      etag: '"1"',
    });
    await store.loadDraft('alice', 'd2');
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

// A published diagram is edited in the draft that published it, which is
// found by the document its last publication stored: publishing again gives
// the diagram a new id, which no source token names.
describe('the draft a published diagram is edited in', () => {
  const X1 = 'a'.repeat(64);
  const X2 = 'b'.repeat(64);
  const draft = (id, fields = {}) => ({ id, owner: 'alice', ...fields });
  const publishedAs = (documentId) => ({ publication: { documentId } });
  const sharedBy = (id, fields = {}) => ({
    id,
    owner: 'bob',
    via: 'share',
    access: 'edit',
    ...fields,
  });

  test('my draft that published it comes first, then one shared for editing, then my draft made from it', () => {
    const publisher = draft('mine', publishedAs(X2));
    const shared = sharedBy('theirs', publishedAs(X2));
    const opened = draft('copy', { sourceToken: `builder-doc/${X2}` });
    const other = draft('other', publishedAs(X1));
    const token = `builder-doc/${X2}`;

    expect(
      draftForPublished(
        { mine: [other, opened, publisher], shared: [shared] },
        { id: X2 },
        token,
      ),
    ).toEqual({ draft: publisher, how: 'publisher' });
    expect(
      draftForPublished(
        { mine: [other, opened], shared: [shared] },
        { id: X2 },
        token,
      ),
    ).toEqual({ draft: shared, how: 'shared' });
    expect(
      draftForPublished(
        { mine: [other, opened], shared: [] },
        { id: X2 },
        token,
      ),
    ).toEqual({ draft: opened, how: 'opened' });
    expect(
      draftForPublished({ mine: [other], shared: [] }, { id: X2 }, token),
    ).toBeNull();
    expect(draftForPublished({}, { id: X2 }, token)).toBeNull();
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
    const lists = { mine: [older, newer], shared: [] };

    expect(draftForPublished(lists, { id: X2 }, '').draft).toBe(newer);
    expect(
      draftForPublished(lists, { id: X2, draftId: 'older' }, '').draft,
    ).toBe(older);
    // A draft the diagram names that is not listed changes nothing.
    expect(
      draftForPublished(lists, { id: X2, draftId: 'gone' }, '').draft,
    ).toBe(newer);

    const first = sharedBy('s1', {
      ...publishedAs(X2),
      updated: '2026-09-01T00:00:00Z',
    });
    const second = sharedBy('s2', {
      ...publishedAs(X2),
      updated: '2026-09-03T00:00:00Z',
    });

    expect(
      draftForPublished({ mine: [], shared: [first, second] }, { id: X2 }, '')
        .draft,
    ).toBe(second);
    expect(
      draftForPublished(
        { mine: [], shared: [first, second] },
        { id: X2, draftId: 's1' },
        '',
      ).draft,
    ).toBe(first);
  });

  test('a draft shared for viewing, or seen through a role, is not one', () => {
    const viewOnly = sharedBy('view', { ...publishedAs(X2), access: 'view' });
    const byRole = sharedBy('role', { ...publishedAs(X2), via: 'role' });

    expect(
      draftForPublished(
        { mine: [], shared: [viewOnly, byRole] },
        { id: X2 },
        '',
      ),
    ).toBeNull();
  });

  test('without a token only a draft that published the diagram is found', () => {
    const opened = draft('copy', { sourceToken: `builder-doc/${X2}` });

    expect(
      draftForPublished({ mine: [opened], shared: [] }, { id: X2 }, ''),
    ).toBeNull();
    // A draft with no token is never "made from" a diagram without one.
    expect(
      draftForPublished({ mine: [draft('blank')], shared: [] }, { id: '' }, ''),
    ).toBeNull();
  });

  test("a topology's Builder file has no publishing draft", () => {
    const token = `builder-file/plant/${FILE_DIGEST}`;
    // No draft's publication stores a file, whatever its record says.
    const publisher = draft('mine', publishedAs('file/plant'));
    const opened = draft('copy', { sourceToken: token });

    expect(
      draftForPublished(
        { mine: [publisher], shared: [] },
        { id: 'file/plant' },
        token,
      ),
    ).toBeNull();
    expect(
      draftForPublished(
        { mine: [publisher, opened], shared: [] },
        { id: 'file/plant' },
        token,
      ),
    ).toEqual({ draft: opened, how: 'opened' });
  });

  // The defect: draft D opened topology T (token builder-doc/X1), was
  // edited and published T again, which now holds X2. Opening T again made
  // a second draft from X2, because only the token was compared.
  test('a draft that published the diagram again is opened again, and no other draft is made', async () => {
    const { doc } = sampleDocument();

    store.documents = [{ id: X2, target: 'core', draftId: 'd5' }];
    api.listDrafts.mockResolvedValueOnce({
      mine: [
        draft('d5', { sourceToken: `builder-doc/${X1}`, ...publishedAs(X2) }),
      ],
      shared: [],
      published: [],
    });
    api.getDraft.mockResolvedValueOnce({
      draft: { id: 'd5', owner: 'alice' },
      document: doc,
      history: [{ id: 's1' }],
      cursor: 0,
      etag: '"5"',
    });

    await expect(store.openPublishedDocument(X2)).resolves.toBeTruthy();

    expect(api.getDraft).toHaveBeenLastCalledWith('alice', 'd5');
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
      draft: { id, owner: 'alice' },
      document: doc,
      history: [{ id: 's1' }],
      cursor: 0,
      etag: '"5"',
    });

    api.listDrafts.mockResolvedValueOnce({
      mine: [draft('d5', publishedAs(X2))],
      shared: [],
      published: [],
    });
    api.getDraft.mockResolvedValueOnce(loaded('d5'));
    await store.openPublishedDocument(X2, said);
    expect(store.announcement).toBe('Mine, which published it.');

    api.listDrafts.mockResolvedValueOnce({
      mine: [draft('d6', { sourceToken: `builder-doc/${X2}` })],
      shared: [],
      published: [],
    });
    api.getDraft.mockResolvedValueOnce(loaded('d6'));
    await store.openPublishedDocument(X2, said);
    expect(store.announcement).toBe('Mine, made from it.');

    api.listDrafts.mockResolvedValueOnce({
      mine: [],
      shared: [],
      published: [],
    });
    await store.openPublishedDocument(X2, said);
    expect(store.announcement).toBe('New.');
    expect(api.createDraft).toHaveBeenCalledTimes(1);
    expect(api.createDraft).toHaveBeenLastCalledWith(
      expect.objectContaining({ sourceToken: `builder-doc/${X2}` }),
    );
  });

  test('the publishing draft someone shared for editing opens, and says whose it is', async () => {
    const { doc } = sampleDocument();

    api.listDrafts.mockResolvedValueOnce({
      mine: [],
      shared: [sharedBy('d8', publishedAs(X2))],
      published: [],
    });
    api.getDraft.mockResolvedValueOnce({
      draft: { id: 'd8', owner: 'bob', access: 'edit', via: 'share' },
      document: doc,
      history: [{ id: 's1' }],
      cursor: 0,
      etag: '"8"',
    });

    await expect(
      store.openPublishedDocument(X2, { publisher: 'Mine.', resumed: 'Mine.' }),
    ).resolves.toBeTruthy();

    expect(api.getDraft).toHaveBeenLastCalledWith('bob', 'd8');
    expect(api.createDraft).not.toHaveBeenCalled();
    expect(store.owner).toBe('bob');
    // Whose draft it is has been said already: nothing calls it mine.
    expect(store.announcement).toBe(
      `Opened bob's draft ${doc.metadata.name}. You can edit it; others may be editing too.`,
    );
  });

  test('a publishing draft shared for viewing only is left alone: a draft of my own is made', async () => {
    api.listDrafts.mockResolvedValueOnce({
      mine: [],
      shared: [
        sharedBy('d8', { ...publishedAs(X2), access: 'view' }),
        sharedBy('d9', { ...publishedAs(X2), via: 'role' }),
      ],
      published: [],
    });

    await expect(store.openPublishedDocument(X2)).resolves.toBeTruthy();

    expect(api.getDraft).not.toHaveBeenCalled();
    expect(api.createDraft).toHaveBeenCalledWith(
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

    // Opened again while the file holds the same: the draft made from it,
    // the one changed last, not another copy. A draft of the file as it
    // was before, and one of a published diagram, are not it.
    const { doc } = sampleDocument();

    api.listDrafts.mockResolvedValueOnce({
      mine: [
        {
          id: 'old',
          owner: 'alice',
          sourceToken: `builder-file/plant/sha256:${'0'.repeat(64)}`,
          updated: '2026-09-09T00:00:00Z',
        },
        { id: 'doc', owner: 'alice', sourceToken: 'builder-doc/file/plant' },
        {
          id: 'd6',
          owner: 'alice',
          sourceToken: `builder-file/plant/${FILE_DIGEST}`,
          updated: '2026-09-02T00:00:00Z',
        },
        {
          id: 'd7',
          owner: 'alice',
          sourceToken: `builder-file/plant/${FILE_DIGEST}`,
          updated: '2026-09-03T00:00:00Z',
        },
      ],
      shared: [],
      published: [],
    });
    api.getDraft.mockResolvedValueOnce({
      draft: { id: 'd7', owner: 'alice' },
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
    expect(api.getDraft).toHaveBeenLastCalledWith('alice', 'd7');
    expect(store.draftId).toBe('d7');
    expect(store.announcement).toBe(
      'Opened your draft of the diagram of topology plant.',
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
      maxIcons: 64,
      maxBytes: 1048576,
      usedIcons: 0,
      usedBytes: 0,
    });

    await store.viewPublishedDocument('file/plant');

    // Shown read only, the file's own copies draw its icons; nothing is
    // uploaded until it is edited.
    expect(store.doc.icons).toEqual(doc.icons);
    expect(api.uploadIcon).not.toHaveBeenCalled();

    await expect(store.editPublished()).resolves.toBeTruthy();

    const warning =
      "The server already has an icon named hmi that differs from this diagram's. The diagram keeps its own copy, which it shows in place of the server's.";

    // plc is added under its name and leaves the draft; hmi, which the
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
      mine: [
        {
          id: 'd6',
          owner: 'alice',
          sourceToken: `builder-file/plant/${FILE_DIGEST}`,
        },
      ],
      shared: [],
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
      'The Builder file of topology plant changed since it was opened. Open its diagram again.';

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
      'Could not open the diagram of topology plant. The server could not be reached. Check the connection and try again.',
    );

    api.getTopologyDocument.mockRejectedValueOnce(
      refusal(500, 'unable to read the builder document of topology plant'),
    );
    await store.viewPublishedDocument('file/plant');
    expect(store.error).toBe(
      'Could not open the diagram of topology plant. Unable to read the builder document of topology plant.',
    );

    // A create refused with a 409 that is not about a file keeps its text.
    api.createDraft.mockRejectedValueOnce(refusal(409, 'something else'));
    await store.createDraft({ title: 'Test' });
    expect(store.error).toBe(
      'Could not create the draft. This draft changed on the server since you loaded it.',
    );
  });
});

// The server stores a document sent back as it was read as it is, so that
// opening a diagram is not an edit of it. The editor names each switch
// after its network when it opens a document, which must not reach the
// document it sends.
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
      draft: { id: 'd1', owner: 'alice', snapshotId: 's1', stamp },
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

    api.getDraft.mockResolvedValueOnce(
      readDraft({ access: 'owner', experiment: 'core-exp' }),
    );
    await store.loadDraft('alice', 'd1');

    expect(store.experiment).toBe('core-exp');
    expect(store.experimentName).toBe('core-exp');

    // A save answers with the draft alone, and says nothing of it.
    store.addNode({ kind: 'device', hostname: 'alpha' });
    await store.saveNow();
    expect(api.appendSnapshot).toHaveBeenCalledOnce();
    expect(store.experiment).toBe('core-exp');

    // The server leaves the field out once the experiment is gone.
    api.getDraft.mockResolvedValueOnce(readDraft({ access: 'owner' }));
    await store.loadDraft('alice', 'd1');

    expect(store.experiment).toBe('');
    expect(store.experimentName).toBe('');
  });

  test('a draft made from a published diagram names it, and a new diagram none', async () => {
    api.createDraft.mockResolvedValueOnce({
      draft: { id: 'd1', owner: 'alice', experiment: 'core-exp' },
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
      created({ id: 'd7', owner: 'alice', experiment: 'core-exp' }),
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
      created({ id: 'd8', owner: 'alice' }),
    );
    await store.resolveConflict('fork');

    expect(store.draftId).toBe('d8');
    expect(store.experimentName).toBe('');
  });

  test('a publish names the experiment its answer does, also when it failed in part', async () => {
    await withDraft();
    api.publish.mockResolvedValueOnce(
      published({ id: 'd1', owner: 'alice', experiment: 'core-exp' }),
    );
    await store.publish(intent);

    expect(store.experimentName).toBe('core-exp');

    // The same draft publishes the topology alone: its experiment stays.
    api.publish.mockResolvedValueOnce(
      published(
        { id: 'd1', owner: 'alice', experiment: 'core-exp' },
        { status: 'partial', ok: false, partial: true },
      ),
    );
    await store.publish(intent);

    expect(store.error).toMatch(/^Publish finished with failures\./);
    expect(store.experimentName).toBe('core-exp');

    // An answer without a draft record says nothing of the experiment.
    api.publish.mockResolvedValueOnce(published(null));
    await store.publish(intent);

    expect(store.experimentName).toBe('core-exp');

    // The experiment was deleted meanwhile: the draft names none.
    api.publish.mockResolvedValueOnce(published({ id: 'd1', owner: 'alice' }));
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
                owner: 'alice',
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

    expect(api.publish).toHaveBeenCalledWith('alice', 'd1', intent, '"1"');
    expect(result.ok).toBe(true);
    expect(store.publishResult).toEqual(result);
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

    api.getDraft.mockResolvedValueOnce(
      readDraft({ owner: 'bob', access: 'edit', via: 'share' }),
    );

    const loaded = await store.loadDraft('bob', 'd1', { quiet: true });

    expect(loaded).toBe(store.doc);
    expect(store.owner).toBe('bob');
    expect(store.draftId).toBe('d1');
    expect(store.etag).toBe('"1"');
    expect(store.sharedBy).toBe('bob');
    expect(store.readOnly).toBe(false);
    expect(store.autosave).toBeTruthy();
    expect(store.saveState).toMatchObject({ pending: 0 });
    expect(said).toEqual([]);

    // Opened in the editor, the same draft says both.
    api.getDraft.mockResolvedValueOnce(
      readDraft({ owner: 'bob', access: 'edit', via: 'share' }),
    );
    await store.loadDraft('bob', 'd1');

    expect(said).toEqual([
      'Draft loaded',
      `Opened bob's draft ${sampleDocument().doc.metadata.name}. You can edit it; others may be editing too.`,
    ]);
  });

  test('still says what it recovered from this device, and sends it', async () => {
    const saved = api.appendSnapshot.getMockImplementation();
    let offline = true;

    api.getDraft.mockResolvedValue(readDraft({ access: 'owner' }));
    api.appendSnapshot.mockImplementation((...args) =>
      offline ? Promise.reject(new Error('offline')) : saved(...args),
    );

    try {
      await store.loadDraft('alice', 'd1');

      // An edit the server never received, left on this device.
      store.addNode({ kind: 'device', hostname: 'kept' });
      await store.saveNow();
      expect(store.saveState).toMatchObject({ status: 'offline', pending: 1 });
      store.newDocument();
      offline = false;
      api.appendSnapshot.mockClear();

      const said = announcements();

      await store.loadDraft('alice', 'd1', { quiet: true });

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

  // Back to drafts while a draft is being saved: the view takes its queue,
  // which goes on sending in the background (see sendInBackground in
  // Builder.vue).
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
      .mockResolvedValueOnce(readDraft({ access: 'owner' }))
      .mockResolvedValueOnce(readDraft({ id: 'd2', access: 'owner' }));
    // The server takes its time over the first draft's save.
    api.appendSnapshot.mockImplementationOnce(
      (...args) =>
        new Promise((resolve) => {
          answer = () => resolve(saved(...args));
        }),
    );

    try {
      await store.loadDraft('alice', 'd1');
      store.addNode({ kind: 'note' });
      await vi.waitFor(() => expect(answer).toBeTypeOf('function'));
      closed = closeForDrafts();

      await store.loadDraft('alice', 'd2', { quiet: true });

      expect(store.queueWork).toBeNull();

      const result = await Promise.race([
        store.publish(intent),
        new Promise((resolve) => setTimeout(() => resolve('waiting'), 250)),
      ]);

      expect(result).toMatchObject({ ok: true });
      expect(api.publish).toHaveBeenCalledWith('alice', 'd2', intent, '"1"');
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
      .mockResolvedValueOnce(readDraft({ access: 'owner' }))
      .mockResolvedValueOnce(readDraft({ id: 'd2', access: 'owner' }));
    api.appendSnapshot.mockImplementation((owner, id, ...rest) =>
      cut.has(id)
        ? Promise.reject(new Error('offline'))
        : saved(owner, id, ...rest),
    );

    try {
      await store.loadDraft('alice', 'd1');
      store.addNode({ kind: 'note' });
      await store.saveNow();
      expect(store.saveState).toMatchObject({ status: 'offline', pending: 1 });
      expect(store.announcement).toMatch(/Offline: 1 change kept/);
      closed = closeForDrafts();

      const said = announcements();

      await store.loadDraft('alice', 'd2', { quiet: true });
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
          data: { message: 'draft alice/d1 not found' },
        },
      }),
    );

    await expect(
      store.loadDraft('alice', 'd1', { quiet: true }),
    ).resolves.toBeNull();
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
    owner: 'alice',
    title: `Draft ${id}`,
    etag: '"1"',
    ...extra,
  });
  const lists = (mine = []) => ({
    mine,
    shared: [],
    others: [],
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
    api.getShares.mockReset();
    api.updateShares.mockReset();
  });

  test('a draft whose ETag is old is deleted with the one the refusal carries', async () => {
    api.deleteDraft.mockRejectedValueOnce(changed('"7"'));

    expect(await store.deleteDraft('alice', 'd1', '"1"', 'Lab network')).toBe(
      true,
    );
    // The draft is not read: a damaged one could not be.
    expect(api.getDraft).not.toHaveBeenCalled();
    expect(api.deleteDraft.mock.calls).toEqual([
      ['alice', 'd1', '"1"'],
      ['alice', 'd1', '"7"'],
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
        key: `alice::alice::${item.id}`,
        actor: 'alice',
        owner: 'alice',
        draftId: item.id,
        entries: [],
        queue: [],
      });
    }
    api.deleteDraft.mockImplementation(async (_, id) => {
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
      ['alice', 'd1', '"1"'],
      ['alice', 'd2', '"4"'],
      ['alice', 'd3', '"1"'],
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
      d4: [failed(403, 'deleting draft alice/d4 not allowed for alice')],
      d5: [failed(500, 'storage down')],
    };

    api.deleteDraft.mockImplementation(async (_, id) => {
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
      ['d2', 'It changed on the server while it was being deleted. Try again.'],
      ['d4', 'Deleting draft alice/d4 not allowed for alice.'],
      ['d5', 'Storage down.'],
    ]);
    expect(api.deleteDraft.mock.calls.filter(([, id]) => id === 'd1')).toEqual([
      ['alice', 'd1', '"1"'],
      ['alice', 'd1', '"2"'],
    ]);
    // Once only: the third tag is never sent.
    expect(
      api.deleteDraft.mock.calls.filter(([, id]) => id === 'd2'),
    ).toHaveLength(2);
    expect(api.getDraft.mock.calls).toEqual([['alice', 'd3']]);
    expect(api.deleteDraft).toHaveBeenCalledWith('alice', 'd3', '"9"');
    expect(api.listDrafts).toHaveBeenCalledOnce();
    expect(store.error).toBe('');
  });

  test('a draft of the user’s own that is gone already counts as deleted; another user’s may only be hidden', async () => {
    // As the server names a draft it does not find.
    const id = (n) => `e11aa62f-3289-4051-a661-bc2fa634290${n}`;
    const gone = (owner, n) => failed(404, `draft ${owner}/${id(n)} not found`);
    const items = [
      draft(id(1)),
      draft(id(2)),
      draft(id(3)),
      draft(id(4), { owner: 'bob' }),
    ];
    const said = announcements();

    for (const item of items) {
      await device.store.put({
        key: `alice::${item.owner}::${item.id}`,
        actor: 'alice',
        owner: item.owner,
        draftId: item.id,
        entries: [],
        queue: [],
      });
    }
    api.deleteDraft.mockImplementation(async (owner, draftId) => {
      if (draftId === id(1)) {
        return true;
      }

      // The third changed, and was deleted before it could be read again.
      throw draftId === id(3) ? changed() : gone(owner, draftId.at(-1));
    });
    api.getDraft.mockRejectedValue(gone('alice', 3));

    const outcome = await store.deleteDrafts(items);

    expect(outcome.done.map((item) => item.id)).toEqual([id(1), id(2), id(3)]);
    // The server answers the same for a draft the user may no longer see.
    expect(
      outcome.failures.map(({ item, reason }) => [item.id, reason]),
    ).toEqual([
      [
        id(4),
        'It was deleted since the list was read, or you can no longer see it.',
      ],
    ]);
    // What this device kept for the drafts that are gone goes with them.
    expect((await device.store.all()).map((record) => record.owner)).toEqual([
      'bob',
    ]);
    expect(api.listDrafts).toHaveBeenCalledOnce();
    expect(said).toEqual([]);
    expect(store.error).toBe('');
  });

  test('a session that ended, or a server out of reach, ends the run: the rest is not attempted', async () => {
    const items = ['d1', 'd2', 'd3', 'd4', 'd5', 'd6'].map((id) => draft(id));

    for (const [error, reason] of [
      [failed(401), 'Your session has ended. Sign in again to continue.'],
      [
        new Error('Network Error'),
        'The server could not be reached. Check the connection and try again.',
      ],
    ]) {
      api.deleteDraft.mockReset().mockRejectedValue(error);

      const outcome = await store.deleteDrafts(items);

      expect(outcome.done).toEqual([]);
      // The four that were under way fail as the first did; the others
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
    expect(api.deleteDocument.mock.calls).toEqual([['p1'], ['p2'], ['p3']]);
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
    const items = ['lab', 'core', 'edge'].map((target, index) => ({
      id: `p${index + 1}`,
      kind: 'Topology',
      target,
    }));

    store.documents = [...items];
    api.deleteDocument
      .mockRejectedValueOnce(failed(404, 'document p1 not found'))
      .mockResolvedValueOnce(true)
      .mockRejectedValueOnce(
        failed(403, 'deleting config Topology/edge not allowed for alice'),
      );
    api.listDocuments.mockResolvedValue([items[2]]);

    const outcome = await store.deletePublishedMany(items);

    expect(outcome.done).toEqual([items[1]]);
    expect(
      outcome.failures.map(({ item, reason }) => [item.id, reason]),
    ).toEqual([
      ['p1', 'It was deleted or published again since the list was read.'],
      ['p3', 'Deleting config Topology/edge not allowed for alice.'],
    ]);
    expect(store.documents).toEqual([items[2]]);
    expect(store.deletingDocuments).toEqual([]);
    expect(store.error).toBe('');
  });

  describe('sharing several drafts', () => {
    const list = (shares = [], sharesEtag = '"shares-1"') => ({
      shares,
      sharesEtag,
      maxShares: 25,
    });
    // The server's answer to a saved list: the list, and the draft with it.
    const saved = (id, shares) => ({
      ...list(shares, '"shares-2"'),
      draft: { id, owner: 'alice', canShare: true, shares, etag: '"2"' },
      etag: '"2"',
    });

    test('adds the people to each draft, keeps who was there, and puts the drafts on the lists', async () => {
      const items = [draft('d1'), draft('d2'), draft('d3')];
      const said = announcements();
      const progress = [];
      const current = {
        d1: [],
        d2: [
          { user: 'bob', access: 'view' },
          { user: 'dave', access: 'edit' },
        ],
        // Shared with bob for editing already: nothing to save.
        d3: [{ user: 'bob', access: 'edit' }],
      };

      store.drafts = lists(items);
      api.getShares.mockImplementation(async (_, id) => list(current[id]));
      api.updateShares.mockImplementation(async (_, id, shares) =>
        saved(id, shares),
      );

      const outcome = await store.shareDrafts(items, ['bob'], 'edit', {
        onProgress: (done, total) => progress.push([done, total]),
      });

      expect(outcome).toEqual({ done: items, failures: [] });
      expect(api.updateShares.mock.calls).toEqual([
        ['alice', 'd1', [{ user: 'bob', access: 'edit' }], '"shares-1"'],
        [
          'alice',
          'd2',
          [
            { user: 'bob', access: 'edit' },
            { user: 'dave', access: 'edit' },
          ],
          '"shares-1"',
        ],
      ]);
      expect(progress.at(-1)).toEqual([3, 3]);
      // The cards say who the drafts are shared with now, and hold the
      // drafts' new ETags.
      expect(store.drafts.mine[0]).toMatchObject({
        id: 'd1',
        etag: '"2"',
        shares: [{ user: 'bob', access: 'edit' }],
      });
      expect(store.drafts.mine[1].shares).toHaveLength(2);
      expect(store.drafts.mine[2]).toEqual(items[2]);
      expect(said).toEqual([]);
      expect(store.error).toBe('');
    });

    test('a draft that would be shared with too many people is not sent', async () => {
      const full = Array.from({ length: 25 }, (_, index) => ({
        user: `user${index}`,
        access: 'view',
      }));

      api.getShares.mockImplementation(async (_, id) =>
        list(id === 'd1' ? full : full.slice(0, 23)),
      );
      api.updateShares.mockImplementation(async (_, id, shares) =>
        saved(id, shares),
      );

      const outcome = await store.shareDrafts(
        [draft('d1'), draft('d2')],
        ['bob', 'carol', 'user3'],
        'view',
      );

      expect(outcome.done.map((item) => item.id)).toEqual(['d2']);
      expect(
        outcome.failures.map(({ item, reason }) => [item.id, reason]),
      ).toEqual([
        [
          'd1',
          'It would be shared with 27 people. A draft can be shared with at most 25 people.',
        ],
      ]);
      // Someone listed already takes no new place: d2 comes to 25.
      expect(api.updateShares).toHaveBeenCalledOnce();
      expect(api.updateShares.mock.calls[0][2]).toHaveLength(25);
    });

    test('a list that changed meanwhile is read and merged again, once', async () => {
      const reads = {
        d1: [
          list([], '"shares-1"'),
          list([{ user: 'erin', access: 'view' }], '"shares-5"'),
        ],
        d2: [list([], '"shares-1"'), list([], '"shares-6"')],
      };

      api.getShares.mockImplementation(async (_, id) => reads[id].shift());
      api.updateShares.mockImplementation(async (_, id, shares, tag) => {
        if (tag === '"shares-1"' || id === 'd2') {
          throw changed();
        }

        return saved(id, shares);
      });

      const outcome = await store.shareDrafts(
        [draft('d1'), draft('d2')],
        ['bob'],
        'view',
      );

      expect(outcome.done.map((item) => item.id)).toEqual(['d1']);
      // The person someone else added meanwhile is kept.
      expect(api.updateShares).toHaveBeenCalledWith(
        'alice',
        'd1',
        [
          { user: 'erin', access: 'view' },
          { user: 'bob', access: 'view' },
        ],
        '"shares-5"',
      );
      expect(outcome.failures).toEqual([
        {
          item: expect.objectContaining({ id: 'd2' }),
          reason:
            'Who it is shared with changed while it was being shared. Try again.',
        },
      ]);
      expect(api.getShares).toHaveBeenCalledTimes(4);
      expect(api.updateShares).toHaveBeenCalledTimes(4);
    });

    test('people the server refuses are named with why, a draft that is gone says so, and other failures are in the server’s words', async () => {
      api.getShares.mockImplementation(async (_, id) => {
        if (id === 'd3') {
          throw failed(
            404,
            'draft alice/e11aa62f-3289-4051-a661-bc2fa6342903 not found',
          );
        }

        return list(
          id === 'd1' ? [{ user: 'gone', access: 'view', stale: true }] : [],
        );
      });
      api.updateShares.mockImplementation(async (_, id) => {
        throw id === 'd1'
          ? failed(422, 'Some people could not be added.', {
              data: {
                errors: [
                  { user: 'gone', reason: 'account-removed' },
                  { user: 'nobody', reason: 'unknown-user' },
                ],
              },
            })
          : failed(403, 'sharing draft alice/d2 not allowed for alice');
      });

      const outcome = await store.shareDrafts(
        [draft('d1'), draft('d2'), draft('d3')],
        ['gone', 'nobody'],
        'view',
      );

      expect(outcome.done).toEqual([]);
      expect(outcome.failures.map((failure) => failure.reason)).toEqual([
        "gone's account was removed. Remove them and save, then add them again if they have a new account. " +
          'No user named nobody. People must have signed in to phēnix at least once.',
        'Sharing draft alice/d2 not allowed for alice.',
        'It was deleted since the list was read.',
      ]);
      // The stale share is left out, and its person sent as new.
      expect(api.updateShares.mock.calls[0][2]).toEqual([
        { user: 'gone', access: 'view' },
        { user: 'nobody', access: 'view' },
      ]);
    });

    test('a session that ended ends the run', async () => {
      const items = ['d1', 'd2', 'd3', 'd4', 'd5'].map((id) => draft(id));

      api.getShares.mockRejectedValue(failed(401));

      const outcome = await store.shareDrafts(items, ['bob'], 'view');

      expect(outcome.failures.at(-1).reason).toBe('Not attempted.');
      expect(api.getShares).toHaveBeenCalledTimes(4);
      expect(api.updateShares).not.toHaveBeenCalled();
    });
  });
});
