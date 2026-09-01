// Pinia store orchestrating the beta builder editor.
//
// The store is a thin shell: all document logic lives in the pure modules under
// src/builder, which keeps this file about wiring (selection, history, the
// autosave queue, API calls, announcements) rather than behaviour.

import { defineStore } from 'pinia';
import { markRaw } from 'vue';

import { usePhenixStore } from '@/store.js';

import { builderApi, classifyError, errorMessage } from './api.js';
import { createAutosave, describeState, initialState } from './autosave.js';
import { copySelection, pasteClipboard } from './clipboard.js';
import { DocumentError, parseDocument } from './decode.js';
import { History, DEFAULT_HISTORY_LIMIT } from './history.js';
import { createDraftStore } from './idb.js';
import { applyLayout } from './layout.js';
import { outlineSummary } from './outline.js';
import {
  builderSchemaV1,
  isSchemaBundle,
  normalizeSchemaBundle,
} from './schema.js';
import { validateDocument } from './validate.js';
import {
  addInterface,
  addNetwork,
  addNode,
  connect,
  createDocument,
  documentSummary,
  findNode,
  groupNodes,
  moveNodes,
  removeElements,
  removeInterface,
  removeNetworks,
  renameInterface,
  resizeNode,
  setGrid,
  setParent,
  setDocumentInfo,
  setScenario,
  setViewport,
  ungroup,
  updateEdge,
  updateNetwork,
  updateNode,
} from './model.js';
import {
  DEFAULT_THEME,
  applyTheme,
  nextTheme,
  readStoredTheme,
  resolveTheme,
  storeTheme,
} from './theme.js';

function emptySelection() {
  return { nodes: [], edges: [] };
}

export const useBuilderStore = defineStore('builder', {
  state: () => ({
    doc: createDocument(),
    history: markRaw(new History(createDocument(), DEFAULT_HISTORY_LIMIT)),
    autosave: null,
    saveState: initialState(),
    selection: emptySelection(),
    clipboard: null,
    schema: builderSchemaV1,
    schemaSource: 'bundled',
    schemaError: '',
    owner: '',
    draftId: '',
    etag: null,
    readOnly: false,
    announcement: '',
    theme: DEFAULT_THEME,
    resolvedTheme: 'light',
    drafts: { mine: [], shared: [], published: [] },
    publishing: false,
    publishResult: null,
    queueWork: null,
    documents: [],
    sources: { images: [], topologies: [], scenarios: [], experiments: [] },
    serverHistory: [],
    loading: false,
    error: '',
  }),

  getters: {
    canUndo: (state) => state.history.canUndo(),
    canRedo: (state) => state.history.canRedo(),
    summary: (state) => documentSummary(state.doc),
    issues: (state) => validateDocument(state.doc),
    errors: (state) =>
      validateDocument(state.doc).filter((issue) => issue.level === 'error'),
    hasConflict: (state) => state.saveState.status === 'conflict',
    saveStateText: (state) => describeState(state.saveState),
    statusText: (state) => outlineSummary(state.doc),
    selectedNode(state) {
      return state.selection.nodes.length === 1
        ? findNode(state.doc, state.selection.nodes[0])
        : null;
    },
    selectedEdge(state) {
      return state.selection.edges.length === 1
        ? (state.doc.edges || []).find(
            (edge) => edge.id === state.selection.edges[0],
          )
        : null;
    },
    inspectorSelection(state) {
      if (state.selection.nodes.length === 1) {
        return { type: 'node', id: state.selection.nodes[0] };
      }

      if (state.selection.edges.length === 1) {
        return { type: 'edge', id: state.selection.edges[0] };
      }

      return { type: 'document' };
    },
  },

  actions: {
    announce(message) {
      this.announcement = message;
    },

    /**
     * Records one semantic edit: local history, then the autosave queue. Every
     * call becomes exactly one server snapshot.
     *
     * @param {object} doc next document
     * @param {string} label short description
     */
    commit(doc, label) {
      if (this.readOnly) {
        this.error = 'This draft is read only.';
        this.announce(this.error);

        return null;
      }

      this.doc = doc;

      const entry = this.history.push(doc, label);

      if (label) {
        this.announce(label);
      }

      if (this.autosave && !this.readOnly) {
        this.trackQueue(this.autosave.commit(entry));
      }

      return entry;
    },

    /**
     * Keeps a queue call from becoming fire and forget. The queue reports
     * failures through its own state, so nothing is thrown here, but the
     * promise is always handled and the last one is awaited by saveNow.
     *
     * @param {Promise<object>} promise
     */
    trackQueue(promise) {
      this.queueWork = markRaw(
        Promise.resolve(promise).catch((error) => {
          this.error = this.describeError(error, 'save your changes');

          return this.saveState;
        }),
      );

      return this.queueWork;
    },

    initAutosave({ owner, draftId, etag, entries, cursor, queue }) {
      const phenix = usePhenixStore();

      this.owner = owner;
      this.draftId = draftId;

      if (etag) {
        this.etag = etag;
      }

      this.autosave?.dispose();
      this.autosave = markRaw(
        createAutosave({
          api: builderApi,
          store: createDraftStore(),
          actor: phenix.username || 'anonymous',
          onState: (state) => {
            this.saveState = state;

            if (state.etag) {
              this.etag = state.etag;
            }
          },
          onDraft: (envelope) => {
            if (envelope.etag) {
              this.etag = envelope.etag;
            }

            const history = envelope.history || envelope.draft?.history;
            if (history) {
              this.serverHistory = history;
            }
          },
        }),
      );

      this.autosave.listen();

      // Entries and the queue are deliberately left out unless the caller has
      // them: attach then keeps whatever ordered log this device already holds
      // for the draft, which is what recoverLocalHistory reads back.
      return this.autosave.attach({
        owner,
        draftId,
        etag,
        entries,
        cursor,
        queue,
      });
    },

    newDocument(init = {}) {
      this.autosave?.dispose();
      this.autosave = null;
      this.queueWork = null;

      const doc = createDocument(init);

      this.doc = doc;
      this.history = markRaw(new History(doc, DEFAULT_HISTORY_LIMIT));
      this.selection = emptySelection();
      this.owner = '';
      this.draftId = doc.id;
      this.etag = null;
      this.readOnly = false;
      this.saveState = initialState();

      return doc;
    },

    /**
     * Replaces the document. The payload is decoded strictly: an unsupported or
     * invalid document is rejected with a message instead of being silently
     * repaired.
     *
     * @param {object} payload
     * @param {object} [options] label, resetHistory
     * @returns {object|null} document, or null when rejected
     */
    setDocument(
      payload,
      { label = 'Document loaded', resetHistory = true } = {},
    ) {
      let document;

      try {
        document = parseDocument(payload);
      } catch (error) {
        this.error =
          error instanceof DocumentError
            ? error.message
            : 'The document could not be read.';
        this.announce(this.error);

        return null;
      }

      this.doc = document;

      if (resetHistory) {
        this.history.reset(document, label);
      } else {
        this.commit(document, label);
      }

      this.selection = emptySelection();
      this.announce(label);

      return document;
    },

    async createDraft({ document, title, sourceToken } = {}) {
      const phenix = usePhenixStore();
      const doc = document || this.doc;

      this.error = '';

      try {
        const envelope = await builderApi.createDraft({
          title: title || doc.name || 'Untitled diagram',
          sourceToken,
          document: doc,
        });

        this.owner = envelope.draft?.owner || phenix.username || '';
        this.draftId = envelope.draft?.id || doc.id;
        this.etag = envelope.etag;
        this.serverHistory = envelope.history || [];
        this.readOnly = false;
        this.doc = envelope.document ? parseDocument(envelope.document) : doc;
        this.history = markRaw(new History(this.doc, DEFAULT_HISTORY_LIMIT));

        await this.initAutosave({
          owner: this.owner,
          draftId: this.draftId,
          etag: this.etag,
        });

        this.announce('Draft created.');

        return envelope;
      } catch (error) {
        this.error = this.describeError(error, 'create the draft');

        return null;
      }
    },

    async loadDraft(owner, id) {
      this.loading = true;
      this.error = '';

      try {
        const envelope = await builderApi.getDraft(owner, id);
        const phenix = usePhenixStore();

        this.owner = envelope.draft?.owner || owner;
        this.draftId = envelope.draft?.id || id;
        this.etag = envelope.etag;
        this.serverHistory = envelope.history || [];
        this.readOnly = Boolean(envelope.draft?.readOnly);

        const loaded = this.setDocument(envelope.document, {
          label: 'Draft loaded',
        });

        if (!loaded) {
          return null;
        }

        await this.initAutosave({
          owner: this.owner,
          draftId: this.draftId,
          etag: this.etag,
        });

        await this.recoverLocalHistory();

        if (phenix.username && this.owner !== phenix.username) {
          this.announce('Opened a shared draft.');
        }

        return this.doc;
      } catch (error) {
        this.error = this.describeError(error, 'load the draft');

        return null;
      } finally {
        this.loading = false;
      }
    },

    /**
     * Restores the full ordered local history and pending queue recorded by a
     * previous session on this device.
     */
    async recoverLocalHistory() {
      if (!this.autosave) {
        return false;
      }

      const local = await this.autosave.recover(this.owner, this.draftId);

      if (!local || !local.entries || local.entries.length === 0) {
        return false;
      }

      const pending = (local.queue || []).length;

      this.history.restore(local.entries, local.cursor);
      this.doc = this.history.current();

      await this.autosave.attach({
        owner: this.owner,
        draftId: this.draftId,
        etag: this.etag,
        entries: local.entries,
        cursor: local.cursor,
        queue: local.queue || [],
      });

      if (pending > 0) {
        this.announce(
          `Recovered ${pending} unsaved change${pending === 1 ? '' : 's'} from this device.`,
        );
        await this.autosave.flush();
      } else {
        this.announce('Recovered local edit history from this device.');
      }

      return true;
    },

    /**
     * Tells the server which snapshot in the draft's own undo history is
     * current. This is the draft history cursor; the editor has no presence or
     * live collaboration signal.
     */
    moveHistoryCursor() {
      if (!this.autosave || this.readOnly) {
        return;
      }

      this.trackQueue(this.autosave.moveCursor(this.history.index));
    },

    async saveNow() {
      if (!this.autosave) {
        return this.saveState;
      }

      // Anything already queued is awaited first so callers (publish) see the
      // real end state rather than racing an in-flight commit.
      await this.queueWork;

      return this.autosave.flush();
    },

    async retrySave() {
      if (!this.autosave) {
        return this.saveState;
      }

      return this.autosave.retry();
    },

    /**
     * Resolves an ETag conflict. Local work is never written over the server
     * copy: either the server copy is reloaded, or the local history is saved
     * as a new draft.
     *
     * @param {'reload'|'fork'} choice
     * @param {object} [options] title
     */
    async resolveConflict(choice, options = {}) {
      if (!this.autosave) {
        return null;
      }

      if (choice === 'fork') {
        try {
          const envelope = await this.autosave.forkLocalHistory({
            title:
              options.title || `${this.doc.name || 'Diagram'} (local copy)`,
          });

          this.owner = envelope.draft?.owner || this.owner;
          this.draftId = envelope.draft?.id || this.draftId;
          this.etag = envelope.etag;
          this.announce('Saved your local history as a new draft.');

          return envelope;
        } catch (error) {
          this.error = this.describeError(error, 'save a new draft');

          return null;
        }
      }

      await this.autosave.discardLocal();

      return this.loadDraft(this.owner, this.draftId);
    },

    async deleteDraft(owner, id, etag) {
      try {
        await builderApi.deleteDraft(owner, id, etag);
        this.announce('Draft deleted.');
        await this.fetchDrafts();

        return true;
      } catch (error) {
        this.error = this.describeError(error, 'delete the draft');

        return false;
      }
    },

    /**
     * Loads the server schema. A failure is reported: the editor keeps working
     * with the bundled copy, but the user is told the server copy is not in
     * use, because form fields may differ from what the server will accept.
     */
    async fetchSchema() {
      try {
        const payload = await builderApi.getSchema();

        const usable = isSchemaBundle(payload);

        this.schema = normalizeSchemaBundle(payload);
        this.schemaSource = usable ? 'server' : 'bundled';
        this.schemaError = usable
          ? ''
          : 'The server returned a schema this editor does not recognize. Using the bundled schema; fields may differ from this server.';
      } catch (error) {
        this.schema = builderSchemaV1;
        this.schemaSource = 'bundled';
        this.schemaError = `${this.describeError(error, 'load the server schema')} Using the bundled schema; fields may differ from this server.`;
      }

      return this.schema;
    },

    async fetchDrafts() {
      this.loading = true;

      try {
        this.drafts = await builderApi.listDrafts();
      } catch (error) {
        this.error = this.describeError(error, 'list drafts');
      } finally {
        this.loading = false;
      }

      return this.drafts;
    },

    async fetchDocuments() {
      try {
        this.documents = await builderApi.listDocuments();
      } catch (error) {
        this.error = this.describeError(error, 'list published diagrams');
      }

      return this.documents;
    },

    async openPublishedDocument(id) {
      this.loading = true;
      this.error = '';

      try {
        const document = parseDocument(await builderApi.getDocument(id));

        const created = await this.createDraft({
          document,
          title: document.name,
          sourceToken: `builder-doc/${id}`,
        });

        if (created) {
          this.announce('Opened published diagram.');
        }

        return created ? this.doc : null;
      } catch (error) {
        this.error = this.describeError(error, 'open the published diagram');

        return null;
      } finally {
        this.loading = false;
      }
    },

    async fetchSources() {
      try {
        this.sources = await builderApi.getSources();
      } catch (error) {
        this.error = this.describeError(error, 'load source catalogs');
      }

      return this.sources;
    },

    async fetchHistory() {
      if (!this.owner || !this.draftId) {
        return [];
      }

      try {
        this.serverHistory = await builderApi.listSnapshots(
          this.owner,
          this.draftId,
        );
      } catch (error) {
        this.error = this.describeError(error, 'list snapshots');
      }

      return this.serverHistory;
    },

    async restoreSnapshot(snapshotId) {
      try {
        if (!this.autosave) {
          this.error = 'Open a saved draft before restoring history.';

          return null;
        }

        const saved = await this.saveNow();
        if (saved.status !== 'saved' || saved.pending > 0) {
          this.error = 'Save pending changes before restoring history.';

          return null;
        }

        const cursor = this.serverHistory.findIndex(
          (entry) => entry.id === snapshotId,
        );
        if (cursor < 0) {
          this.error = 'That snapshot is no longer in the draft history.';

          return null;
        }

        const envelope = await builderApi.getSnapshot(
          this.owner,
          this.draftId,
          snapshotId,
        );

        const cursorState = await this.trackQueue(
          this.autosave.moveCursor(cursor),
        );
        if (cursorState.status !== 'saved') {
          return null;
        }

        const document = this.setDocument(envelope.document, {
          label: 'Snapshot restored',
          resetHistory: true,
        });
        this.announce('Snapshot restored');

        return document;
      } catch (error) {
        this.error = this.describeError(error, 'restore the snapshot');

        return null;
      }
    },

    /**
     * Publishes the draft. Publish never carries document bytes: the server
     * reads the snapshot the cursor points at, so the ordered queue has to be
     * confirmed by the server first. A draft with unsent work, a blocked queue
     * or local validation errors is not published.
     *
     * @param {object} intent mode, topology, scenario, experiment
     * @returns {Promise<object|null>} publish result
     */
    async publish(intent) {
      this.publishResult = null;

      if (this.readOnly) {
        this.error = 'This draft is read only.';

        return null;
      }

      if (this.errors.length > 0) {
        this.error = 'Fix the errors in the diagram before publishing.';

        return null;
      }

      if (!this.autosave || !this.owner || !this.draftId) {
        this.error = 'Save this diagram as a draft before publishing it.';

        return null;
      }

      const state = await this.saveNow();

      if (state.status !== 'saved' || state.pending > 0) {
        this.error =
          state.status === 'conflict'
            ? 'This draft changed elsewhere. Resolve the conflict before publishing.'
            : 'Your latest changes are not saved on the server yet, so there is nothing new to publish. Wait for the save to finish or retry it.';
        this.announce(this.error);

        return null;
      }

      this.publishing = true;

      try {
        const { result, etag } = await builderApi.publish(
          this.owner,
          this.draftId,
          intent,
          this.etag,
        );

        if (etag) {
          this.etag = etag;
          await this.autosave?.setETag(etag);
        }

        this.publishResult = result;

        if (result.ok) {
          this.error = '';
          this.announce('Diagram published.');
        } else {
          this.error = result.partial
            ? 'Publish finished with failures. Some configs were written.'
            : 'Publish failed. No configs were written.';
          this.announce(this.error);
        }

        return result;
      } catch (error) {
        if (classifyError(error) === 'conflict') {
          this.error =
            'This draft changed on the server while publishing. Reload it and try again.';
        } else {
          this.error = this.describeError(error, 'publish the diagram');
        }

        return null;
      } finally {
        this.publishing = false;
      }
    },

    async generate(request) {
      try {
        const result = await builderApi.generate(request);
        // Validate before detaching the current draft. A successful generation
        // always starts separately; it must never reuse the prior draft's queue.
        const generated = parseDocument(result.document);

        this.newDocument({ name: generated.name });

        const document = this.setDocument(generated, {
          label: 'Generated diagram loaded',
        });

        if (document && result.warnings.length > 0) {
          this.announce(
            `Generated with ${result.warnings.length} warning${result.warnings.length === 1 ? '' : 's'}.`,
          );
        }

        return document
          ? { document, warnings: result.warnings, source: result.source }
          : null;
      } catch (error) {
        this.error = this.describeError(error, 'generate a diagram');

        return null;
      }
    },

    describeError(error, action) {
      const kind = classifyError(error);
      const detail = errorMessage(kind, error);

      return `Could not ${action}. ${detail}`;
    },

    // --- document editing -------------------------------------------------

    addNode(options) {
      const result = addNode(this.doc, options);

      this.commit(result.doc, `Added ${options.kind || 'device'}`);
      this.selection = { nodes: [result.node.id], edges: [] };

      return result.node;
    },

    updateNode(id, patch, label = 'Updated node') {
      this.commit(updateNode(this.doc, id, patch), label);
    },

    /**
     * Moves one or more nodes as a single history commit, so a multi-node drag
     * or keyboard nudge is one undo step and one server snapshot.
     *
     * @param {{id: string, position: {x: number, y: number}}[]} moves
     */
    moveNodes(moves) {
      if (!moves || moves.length === 0) {
        return;
      }

      this.commit(
        moveNodes(this.doc, moves),
        moves.length === 1 ? 'Moved node' : `Moved ${moves.length} nodes`,
      );
    },

    moveNode(id, position) {
      this.moveNodes([{ id, position }]);
    },

    /**
     * Nudges the current selection by a delta, as one commit.
     *
     * @param {number} dx
     * @param {number} dy
     */
    nudgeSelection(dx, dy) {
      const moves = this.selection.nodes
        .map((id) => findNode(this.doc, id))
        .filter(Boolean)
        .map((node) => ({
          id: node.id,
          position: { x: node.position.x + dx, y: node.position.y + dy },
        }));

      this.moveNodes(moves);
    },

    resizeNode(id, size) {
      this.commit(resizeNode(this.doc, id, size), 'Resized node');
    },

    setParent(id, parentId) {
      this.commit(
        setParent(this.doc, id, parentId),
        parentId ? 'Added node to group' : 'Removed node from group',
      );
    },

    addNetwork(init) {
      const result = addNetwork(this.doc, init);

      this.commit(result.doc, `Added network ${result.network.name}`);

      return result.network;
    },

    updateNetwork(id, patch) {
      this.commit(updateNetwork(this.doc, id, patch), 'Updated network');
    },

    removeNetwork(id) {
      this.commit(removeNetworks(this.doc, [id]), 'Removed network');
    },

    addInterface(nodeId, init) {
      const result = addInterface(this.doc, nodeId, init);

      if (result.handle) {
        this.commit(result.doc, `Added interface ${result.handle.name}`);
      }

      return result.handle;
    },

    removeInterface(nodeId, handleId) {
      this.commit(
        removeInterface(this.doc, nodeId, handleId),
        'Removed interface',
      );
    },

    renameInterface(nodeId, handleId, name) {
      this.commit(
        renameInterface(this.doc, nodeId, handleId, name),
        `Renamed interface to ${name}`,
      );
    },

    connect(connection) {
      const result = connect(this.doc, connection);

      if (result.error) {
        this.announce(result.error);

        return { error: result.error };
      }

      this.commit(result.doc, 'Connected nodes');

      return { edge: result.edge };
    },

    updateEdge(id, patch) {
      this.commit(updateEdge(this.doc, id, patch), 'Updated connection');
    },

    removeSelection() {
      if (!this.selection.nodes.length && !this.selection.edges.length) {
        return;
      }

      this.commit(
        removeElements(this.doc, this.selection),
        'Deleted selection',
      );
      this.selection = emptySelection();
    },

    remove(selection) {
      this.commit(removeElements(this.doc, selection), 'Deleted selection');
      this.selection = emptySelection();
    },

    group() {
      const result = groupNodes(this.doc, this.selection.nodes);

      if (!result.group) {
        this.announce('Select at least one node to group.');

        return null;
      }

      this.commit(result.doc, 'Grouped selection');
      this.selection = { nodes: [result.group.id], edges: [] };

      return result.group;
    },

    ungroup(groupId) {
      const id = groupId || this.selection.nodes[0];

      if (!id) {
        return;
      }

      this.commit(ungroup(this.doc, id), 'Ungrouped nodes');
      this.selection = emptySelection();
    },

    layout(options) {
      this.commit(applyLayout(this.doc, options), 'Applied automatic layout');
    },

    setInfo(patch) {
      this.commit(setDocumentInfo(this.doc, patch), 'Renamed diagram');
    },

    setViewport(viewport) {
      this.doc = setViewport(this.doc, viewport);
    },

    setGrid(patch) {
      this.commit(setGrid(this.doc, patch), 'Changed grid');
    },

    setScenario(scenario) {
      this.commit(
        setScenario(this.doc, scenario),
        scenario ? 'Attached scenario' : 'Removed scenario',
      );
    },

    copy() {
      this.clipboard = copySelection(this.doc, this.selection);
      this.announce(
        `Copied ${this.clipboard.nodes.length} nodes and ${this.clipboard.edges.length} connections.`,
      );

      return this.clipboard;
    },

    paste() {
      if (!this.clipboard) {
        this.announce('Clipboard is empty.');

        return [];
      }

      const result = pasteClipboard(this.doc, this.clipboard);

      this.commit(result.doc, `Pasted ${result.nodeIds.length} nodes`);
      this.selection = { nodes: result.nodeIds, edges: [] };

      return result.nodeIds;
    },

    duplicate() {
      this.copy();

      return this.paste();
    },

    undo() {
      if (!this.history.canUndo()) {
        this.announce('Nothing to undo.');

        return;
      }

      const label = this.history.undoLabel();

      this.doc = this.history.undo();
      this.selection = emptySelection();
      this.announce(`Undid ${label}.`);
      this.moveHistoryCursor();
    },

    redo() {
      if (!this.history.canRedo()) {
        this.announce('Nothing to redo.');

        return;
      }

      const label = this.history.redoLabel();

      this.doc = this.history.redo();
      this.selection = emptySelection();
      this.announce(`Redid ${label}.`);
      this.moveHistoryCursor();
    },

    select(selection) {
      this.selection = {
        nodes: selection?.nodes || [],
        edges: selection?.edges || [],
      };
    },

    selectAll() {
      this.selection = {
        nodes: (this.doc.nodes || []).map((node) => node.id),
        edges: (this.doc.edges || []).map((edge) => edge.id),
      };
      this.announce('Selected everything.');
    },

    clearSelection() {
      this.selection = emptySelection();
    },

    // --- theme ------------------------------------------------------------

    initTheme(element) {
      const storage = typeof localStorage !== 'undefined' ? localStorage : null;
      const mm =
        typeof window !== 'undefined' && window.matchMedia
          ? window.matchMedia.bind(window)
          : undefined;

      this.theme = readStoredTheme(storage);
      this.resolvedTheme = applyTheme(element, this.theme, mm);

      return this.resolvedTheme;
    },

    setTheme(theme, element) {
      const storage = typeof localStorage !== 'undefined' ? localStorage : null;
      const mm =
        typeof window !== 'undefined' && window.matchMedia
          ? window.matchMedia.bind(window)
          : undefined;

      this.theme = storeTheme(theme, storage);
      this.resolvedTheme = element
        ? applyTheme(element, this.theme, mm)
        : resolveTheme(this.theme, mm);
      this.announce(`Theme set to ${this.theme}.`);

      return this.resolvedTheme;
    },

    cycleTheme(element) {
      return this.setTheme(nextTheme(this.theme), element);
    },
  },
});
