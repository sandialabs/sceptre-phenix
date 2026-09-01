import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

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
  createDraft: vi.fn(async (request) => ({
    draft: { id: 'd1', owner: 'alice' },
    document: request.document,
    history: [],
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
  appendSnapshot: vi.fn(async () => ({
    draft: { id: 'd1', owner: 'alice', snapshotId: 's2' },
    history: [{ id: 's1' }, { id: 's2' }],
    cursor: 1,
    etag: '"2"',
  })),
  moveCursor: vi.fn(async () => ({
    draft: { id: 'd1', owner: 'alice' },
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
}));

vi.mock('@/builder/api.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, builderApi: api };
});

vi.mock('@/builder/idb.js', async (importOriginal) => {
  const actual = await importOriginal();

  return {
    ...actual,
    createDraftStore: () => {
      const data = new Map();

      return {
        async put(record) {
          data.set(record.key, JSON.parse(JSON.stringify(record)));
        },
        async get(key) {
          return data.get(key) || null;
        },
        async all() {
          return [...data.values()];
        },
        async remove(key) {
          data.delete(key);
        },
      };
    },
  };
});

import { builderSchemaV1 } from '@/builder/schema.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

let store;

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
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
});

describe('editing commits', () => {
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
      draft: { id: 'd1', owner: 'alice' },
      history: store.serverHistory,
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

    api.appendSnapshot
      .mockResolvedValueOnce({
        draft: { snapshotId: 's2' },
        history: [{ id: 's1' }, { id: 's2' }],
        cursor: 1,
        etag: '"2"',
      })
      .mockResolvedValueOnce({
        draft: { snapshotId: 's3' },
        history: [{ id: 's1' }, { id: 's3' }],
        cursor: 1,
        etag: '"4"',
      });
    api.moveCursor.mockResolvedValueOnce({
      history: [{ id: 's1' }, { id: 's2' }],
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

    expect(api.createDraft).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Recovered' }),
    );
    expect(store.draftId).toBe('d1');
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
  });

  test('a schema failure is reported, not swallowed', async () => {
    api.getSchema.mockRejectedValueOnce(new Error('boom'));

    await store.fetchSchema();

    expect(store.schema.$id).toBe(builderSchemaV1.$id);
    expect(store.schemaSource).toBe('bundled');
    expect(store.schemaError).toMatch(/bundled schema/i);
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

  test('generate rejects an unreadable document without changing the current draft', async () => {
    await withDraft();

    const beforeDocument = store.doc;
    const beforeAutosave = store.autosave;
    const result = await store.generate({ kind: 'topology', name: 'core' });

    expect(result).toBeNull();
    expect(store.error).toBeTruthy();
    expect(store.doc).toBe(beforeDocument);
    expect(store.autosave).toBe(beforeAutosave);
    expect(store.owner).toBe('alice');
    expect(store.draftId).toBe('d1');
  });

  test('deleting a draft passes its ETag', async () => {
    await store.deleteDraft('alice', 'd1', '"1"');

    expect(api.deleteDraft).toHaveBeenCalledWith('alice', 'd1', '"1"');
  });
});

describe('theme', () => {
  test('cycles through system, light and dark', () => {
    const element = { dataset: {}, setAttribute() {} };

    store.setTheme('light', element);
    expect(store.theme).toBe('light');

    store.cycleTheme(element);
    expect(['dark', 'system']).toContain(store.theme);
  });
});
