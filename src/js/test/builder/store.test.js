import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { computed } from 'vue';

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
  listDocuments: vi.fn(async () => [{ id: 'p1', name: 'Published' }]),
  getDocument: vi.fn(async () => sampleDocument().doc),
  getSources: vi.fn(async () => ({
    images: [],
    topologies: ['core'],
    scenarios: [],
    experiments: [],
  })),
  getSchema: vi.fn(async () => ({ $defs: { device: { type: 'object' } } })),
  listDisks: vi.fn(async () => ['ubuntu.qc2']),
}));

vi.mock('@/builder/api.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, builderApi: api };
});

// This device's local drafts, as the in-memory store keeps them; one per
// test, shared by every queue the test opens.
const device = vi.hoisted(() => ({ store: null }));

vi.mock('@/builder/idb.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, createDraftStore: () => device.store };
});

import { createMemoryStore } from '@/builder/idb.js';
import { createDocument, findNode } from '@/builder/model.js';
import { builderSchemaV1 } from '@/builder/schema.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

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

    // Removing a scenario that is not there changes nothing.
    const entries = store.history.size;
    store.setScenario(null);
    expect(store.history.size).toBe(entries);
    expect(store.announcement).toBe('No scenario is attached.');
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
    expect(store.doc.id).toBe(doc.id);
    expect(store.etag).toBe('"3"');
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
    const abandoned = { ...sampleDocument().doc, name: 'Abandoned' };
    const final = { ...sampleDocument().doc, name: 'Final' };

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
      document: store.doc.name,
      saveState: store.saveState,
      index: store.history.index,
      entries: store.history.entries.map((entry) => ({
        id: entry.id,
        name: entry.snapshot.name,
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
    const later = { ...sent, name: 'Later' };

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
    expect(store.doc.name).toBe('Later');
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
      document: expect.objectContaining({ name: 'Recovered' }),
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
    expect(store.doc.name).toBe('Recovered');
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
        document: expect.objectContaining({ name: 'Recovered' }),
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
      expect(store.doc.name).toBe(title);
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

    expect(viewed.name).toBe(sampleDocument().doc.name);
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
      `Opened your draft of published diagram ${doc.name}.`,
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

  // Generate is named Import in the UI. The user may still cancel on its
  // warnings, so nothing says it imported anything: GenerateDialog has the
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
});
