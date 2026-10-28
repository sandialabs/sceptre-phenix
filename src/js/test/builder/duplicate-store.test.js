// Duplicate draft in the store: what it sends to the server, what it opens,
// and why it fails (duplicateDraft in store.js).

import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

const api = vi.hoisted(() => ({
  createDraft: vi.fn(),
  getDraft: vi.fn(),
  listDrafts: vi.fn(async () => ({ all: [], published: [], damaged: [] })),
}));

vi.mock('@/builder/api.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, builderApi: { ...actual.builderApi, ...api } };
});

vi.mock('@/builder/idb.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, createDraftStore: () => createMemoryStore() };
});

import { duplicateDocument } from '@/builder/duplicate.js';
import { draftCanUpdate } from '@/builder/publish.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';
import { createMemoryStore } from './memory-store.js';

// The sample diagram as a draft imported from topology pump-station and
// saved by the server, which stamped it.
function importedDiagram() {
  const { doc } = sampleDocument();

  return {
    ...doc,
    metadata: {
      ...doc.metadata,
      name: 'Pump station',
      createdBy: 'alice',
      createdAt: '2026-10-01T15:04:05Z',
      updatedBy: 'bob',
      updatedAt: '2026-10-02T09:30:00Z',
    },
    source: {
      kind: 'topology',
      name: 'pump-station',
      importedAt: '2026-10-01T15:04:05Z',
    },
  };
}

function refusal(status, message, code = undefined) {
  return Object.assign(new Error('refused'), {
    response: { status, data: { message, cause: '', code } },
  });
}

// The server's refusal of a new draft when it holds MaxDrafts drafts (see
// builderDraftCreateError in web/builder_drafts.go).
const draftLimit = () =>
  refusal(
    413,
    'the server holds 2000 drafts, the most it can hold. Delete drafts, or run Clean up in the Builder settings',
    'draft.limit',
  );

describe('Duplicate draft', () => {
  let store;
  let served;

  beforeEach(() => {
    vi.clearAllMocks();
    setActivePinia(createPinia());
    store = useBuilderStore();
    served = importedDiagram();
    api.getDraft.mockImplementation(async (id) => ({
      draft: { id, createdBy: 'alice', title: 'Pump station' },
      document: structuredClone(served),
      history: null,
      cursor: 0,
      etag: '"7"',
    }));
    api.createDraft.mockImplementation(async (request) => ({
      draft: { id: 'd9', createdBy: 'erin' },
      document: request.document,
      history: null,
      cursor: 0,
      etag: '"1"',
    }));
  });

  test('makes a new draft of the listed draft, linked to no config, and opens it', async () => {
    api.listDrafts.mockResolvedValueOnce({
      all: [
        { id: 'd1', title: 'Pump station' },
        { id: 'd2', title: 'Pump station (copy)' },
      ],
      published: [],
      damaged: [],
    });

    const created = await store.duplicateDraft({ id: 'd1' });

    expect(created).toBeTruthy();
    expect(api.getDraft).toHaveBeenCalledWith('d1');

    const [sent] = api.createDraft.mock.calls[0];

    expect(sent.title).toBe('Pump station (copy 2)');
    expect(sent.sourceToken).toBe('');
    expect(sent).not.toHaveProperty('forkOf');
    expect(sent.document.metadata.name).toBe('Pump station (copy 2)');
    expect(sent.document.metadata).not.toHaveProperty('createdBy');
    expect(sent.document.metadata).not.toHaveProperty('updatedAt');
    expect(sent.document.metadata.id).not.toBe(served.metadata.id);
    expect(sent.document.source).toEqual({ kind: 'manual' });
    expect(sent.document.nodes).toEqual(served.nodes);

    // The copy is the open draft, with no history and no publication.
    expect(store.draftId).toBe('d9');
    expect(store.doc.metadata.name).toBe('Pump station (copy 2)');
    expect(store.draftRecord.publication).toBeNull();
    expect(store.draftRecord.forked).toBeNull();
    expect(store.canUndo).toBe(false);
    expect(store.announcement).toBe(
      'Duplicated draft Pump station as Pump station (copy 2).',
    );
  });

  test('copies the open draft as it is, without reading it again', async () => {
    const open = {
      ...importedDiagram(),
      metadata: { ...importedDiagram().metadata, name: 'Edited lab' },
    };

    store.setDocument(open);
    await store.duplicateDraft({ id: 'd1', document: store.doc });

    expect(api.getDraft).not.toHaveBeenCalled();
    expect(api.createDraft.mock.calls[0][0].title).toBe('Edited lab (copy)');
  });

  test('says why a draft that cannot be read is not copied, and changes nothing', async () => {
    const before = store.doc;

    api.getDraft.mockRejectedValueOnce(
      refusal(404, 'phenix cannot find draft d1'),
    );

    expect(await store.duplicateDraft({ id: 'd1' })).toBeNull();
    expect(store.error).toMatch(/^Could not read the draft to copy\./);
    expect(api.createDraft).not.toHaveBeenCalled();
    expect(store.doc).toBe(before);
  });

  test('shows the server’s reason when it refuses the new draft', async () => {
    api.createDraft.mockRejectedValueOnce(draftLimit());

    expect(await store.duplicateDraft({ id: 'd1' })).toBeNull();
    expect(store.error).toBe(
      'Could not create the draft. The server holds 2000 drafts, the most it can hold. Delete drafts, or run Clean up in the Builder settings.',
    );
  });

  test('keeps the open draft, its history and selection, when the server refuses the copy', async () => {
    store.setDocument(importedDiagram());
    store.draftId = 'd1';
    store.etag = '"7"';
    store.commit(
      {
        ...store.doc,
        metadata: { ...store.doc.metadata, name: 'Renamed lab' },
      },
      'Renamed diagram',
    );
    store.selection = { ...store.selection, nodeIds: [store.doc.nodes[0].id] };

    const doc = store.doc;
    const history = store.history;
    const selection = store.selection;

    api.createDraft.mockRejectedValueOnce(draftLimit());

    expect(
      await store.duplicateDraft({ id: 'd1', document: store.doc }),
    ).toBeNull();
    expect(store.error).toBe(
      'Could not create the draft. The server holds 2000 drafts, the most it can hold. Delete drafts, or run Clean up in the Builder settings.',
    );
    expect(store.doc).toBe(doc);
    expect(store.draftId).toBe('d1');
    expect(store.etag).toBe('"7"');
    expect(store.history).toBe(history);
    expect(store.history.undoLabel()).toBe('Renamed diagram');
    expect(store.selection).toBe(selection);
  });

  // Publishing the copy asks for a new topology: it may not update the one
  // the original was imported from or published to.
  test('the copy may not update the topology of the draft it copies', () => {
    const doc = importedDiagram();
    const documents = [
      {
        kind: 'Topology',
        target: 'pump-station',
        id: 'doc1',
        draftId: 'd1',
        digest: 'sha256:aaa',
      },
    ];
    const original = {
      id: 'd1',
      source: doc.source,
      sourceToken: 'Topology/pump-station',
      digest: 'sha256:aaa',
      publication: { topologyTarget: 'pump-station', documentId: 'doc1' },
      documents,
    };
    const copy = {
      id: 'd2',
      source: duplicateDocument(doc, 'Copy').source,
      sourceToken: '',
      digest: 'sha256:bbb',
      publication: null,
      documents,
    };

    expect(draftCanUpdate('topology', 'pump-station', original)).toBe(true);
    expect(draftCanUpdate('topology', 'pump-station', copy)).toBe(false);
  });
});
